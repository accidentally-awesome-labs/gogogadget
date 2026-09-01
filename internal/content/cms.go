package content

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gogogadget/gogogadget/internal/cache"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

// liveCap bounds the cached (and therefore index, feed and sitemap) set per
// (kind, locale). A corpus larger than this needs pagination, not a bigger map.
const liveCap = 500

// cmsTTL is how long a loaded snapshot is served without re-reading. Because
// visibility is a SQL predicate over now(), a scheduled entry appears — and an
// expired one disappears — within one TTL of its moment. Admin mutations call
// Invalidate, so an editor never waits.
const cmsTTL = 30 * time.Second

// Entry is one published item of any type, ready to render.
type Entry struct {
	ID          int64
	Kind        string
	Slug        string
	Locale      string // "" = serves every language
	Title       string
	Summary     string
	Body        string // rendered HTML (goldmark GFM; raw HTML escaped)
	PublishedAt time.Time
	Meta        map[string]string
	// Anchor is the element id and URL fragment for ModeSinglePage types:
	// "<kind>-<slug>". Prefixed because a bare "2026-08-19" is a legal HTML id
	// but an illegal CSS selector — an id cannot start with a digit.
	Anchor string
}

// Field returns a declared meta value, or "" when absent.
func (e Entry) Field(key string) string { return e.Meta[key] }

// AsPost projects an entry onto the blog view model. Draft is always false:
// only live entries reach this path.
func (e Entry) AsPost() Post {
	return Post{
		Slug:        e.Slug,
		Title:       e.Title,
		Description: e.Summary,
		Date:        e.PublishedAt,
		Author:      e.Field("author"),
		Body:        e.Body,
	}
}

// AsRelease projects an entry onto the changelog view model.
func (e Entry) AsRelease() Release {
	return Release{
		Slug:    e.Slug,
		Anchor:  e.Anchor,
		Title:   e.Title,
		Date:    e.PublishedAt,
		Summary: e.Summary,
		Body:    e.Body,
	}
}

// EntryFrom maps a database row onto the render model.
func EntryFrom(row sqlc.ContentEntry) Entry {
	e := Entry{
		ID:      row.ID,
		Kind:    row.Kind,
		Slug:    row.Slug,
		Locale:  row.Locale,
		Title:   row.Title,
		Summary: row.Summary,
		Body:    row.BodyHtml,
		Anchor:  row.Kind + "-" + row.Slug,
	}
	if row.PublishedAt.Valid {
		e.PublishedAt = row.PublishedAt.Time
	}
	if len(row.Meta) > 0 {
		var meta map[string]string
		if err := json.Unmarshal(row.Meta, &meta); err == nil {
			e.Meta = meta
		}
	}
	return e
}

type kindSnapshot struct {
	entries []Entry
	expires time.Time
}

// CMS is the database-backed read path for every registered type: published
// entries mapped to view models, cached per (kind, locale) for 30s and
type CMS struct {
	q      *sqlc.Queries
	reg    *Registry
	store  cache.Store
	report func(context.Context, error)
	mu     sync.Mutex
	cache  map[string]kindSnapshot // local stale-if-error fallback
}

func NewCMS(q *sqlc.Queries, reg *Registry) *CMS {
	return NewCMSWithCache(q, reg, nil, nil)
}

func NewCMSWithCache(q *sqlc.Queries, reg *Registry, store cache.Store, report func(context.Context, error)) *CMS {
	return &CMS{q: q, reg: reg, store: store, report: report, cache: map[string]kindSnapshot{}}
}

// List returns every live entry of a kind, newest first, for a locale. An
// unregistered kind is an error: a typo must fail loudly, not render nothing.
func (c *CMS) List(ctx context.Context, kind, locale string) ([]Entry, error) {
	if _, ok := c.reg.Get(kind); !ok {
		return nil, fmt.Errorf("content: unregistered kind %q", kind)
	}
	key := kind + "|" + locale
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, cached := c.cache[key]
	if cached && time.Now().Before(snap.expires) {
		return snap.entries, nil
	}
	if c.store != nil {
		if raw, ok, err := c.store.Get(ctx, "content:"+key); err == nil && ok {
			var entries []Entry
			if json.Unmarshal(raw, &entries) == nil {
				snap = kindSnapshot{entries: entries, expires: time.Now().Add(cmsTTL)}
				c.cache[key] = snap
				return entries, nil
			}
		} else if err != nil && c.report != nil {
			c.report(ctx, fmt.Errorf("content cache read: %w", err))
		}
	}
	rows, err := c.q.ListLiveEntries(ctx, sqlc.ListLiveEntriesParams{
		Kind: kind, Locale: locale, Lim: liveCap,
	})
	if err != nil {
		if cached {
			return snap.entries, nil
		}
		return nil, err
	}
	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, EntryFrom(row))
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].PublishedAt.After(entries[j].PublishedAt)
	})
	c.cache[key] = kindSnapshot{entries: entries, expires: time.Now().Add(cmsTTL)}
	if c.store != nil {
		if raw, marshalErr := json.Marshal(entries); marshalErr == nil {
			if err := c.store.Set(ctx, "content:"+key, raw, cmsTTL); err != nil && c.report != nil {
				c.report(ctx, fmt.Errorf("content cache write: %w", err))
			}
		}
	}
	return entries, nil
}

// BySlug serves one entry out of the cached slice; a miss returns (nil, nil)
// so the handler can 404 without a second query.
func (c *CMS) BySlug(ctx context.Context, kind, slug, locale string) (*Entry, error) {
	entries, err := c.List(ctx, kind, locale)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Slug == slug {
			e := entries[i]
			return &e, nil
		}
	}
	return nil, nil
}

// Latest returns the newest live entry of a kind, or nil when there is none.
func (c *CMS) Latest(ctx context.Context, kind, locale string) (*Entry, error) {
	entries, err := c.List(ctx, kind, locale)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	e := entries[0]
	return &e, nil
}

// Invalidate forces the next read of every pair to re-query. Snapshots are
// EXPIRED rather than dropped: a mutation can change any locale's resolution
// for a slug, so all of them must be re-read — but keeping the last good
// slice means a database hiccup during that re-read degrades to slightly
// stale content instead of a blank page (invalidateAnnouncementCache does
// the same with its single row).
func (c *CMS) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, snap := range c.cache {
		snap.expires = time.Time{}
		c.cache[key] = snap
	}
}
