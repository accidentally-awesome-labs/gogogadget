package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The shipped markdown under content/blog and content/changelog is the SEED
// CORPUS, not a runtime read path: cmd/seed imports it into content_entries
// once, and every request after that reads the database.

// ParsePosts reads content/blog into the blog view model, newest first.
// Drafts are skipped: a draft in the seed corpus is a file you have not
// finished, and the database has its own draft state.
func ParsePosts(fsys fs.FS) ([]Post, error) {
	entries, err := fs.ReadDir(fsys, "blog")
	if err != nil {
		return nil, err
	}
	var posts []Post
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(fsys, "blog/"+e.Name())
		if err != nil {
			return nil, err
		}
		var fm blogFrontmatter
		body, err := splitFrontmatter(raw, &fm)
		if err != nil {
			return nil, fmt.Errorf("blog/%s: %w", e.Name(), err)
		}
		if fm.Draft {
			continue
		}
		date, err := time.Parse("2006-01-02", fm.Date)
		if err != nil {
			return nil, fmt.Errorf("blog/%s: date %q: %w", e.Name(), fm.Date, err)
		}
		html, err := Render(body)
		if err != nil {
			return nil, fmt.Errorf("blog/%s: %w", e.Name(), err)
		}
		posts = append(posts, Post{
			Slug:        strings.TrimSuffix(e.Name(), ".md"),
			Title:       fm.Title,
			Description: fm.Description,
			Date:        date,
			Author:      fm.Author,
			Body:        html,
		})
	}
	sort.Slice(posts, func(i, j int) bool { return posts[i].Date.After(posts[j].Date) })
	return posts, nil
}

// ParseReleases reads content/changelog into the changelog view model,
// newest first.
func ParseReleases(fsys fs.FS) ([]Release, error) {
	entries, err := fs.ReadDir(fsys, "changelog")
	if err != nil {
		return nil, err
	}
	var releases []Release
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(fsys, "changelog/"+e.Name())
		if err != nil {
			return nil, err
		}
		var fm releaseFrontmatter
		body, err := splitFrontmatter(raw, &fm)
		if err != nil {
			return nil, fmt.Errorf("changelog/%s: %w", e.Name(), err)
		}
		date, err := time.Parse("2006-01-02", fm.Date)
		if err != nil {
			return nil, fmt.Errorf("changelog/%s: date %q: %w", e.Name(), fm.Date, err)
		}
		if fm.Title == "" {
			return nil, fmt.Errorf("changelog/%s: title is required", e.Name())
		}
		html, err := Render(body)
		if err != nil {
			return nil, fmt.Errorf("changelog/%s: %w", e.Name(), err)
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		releases = append(releases, Release{
			Slug:    slug,
			Anchor:  "release-" + slug,
			Title:   fm.Title,
			Date:    date,
			Summary: fm.Summary,
			Body:    html,
		})
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].Date.After(releases[j].Date) })
	return releases, nil
}

// Import loads content/blog and content/changelog into content_entries. It is
// idempotent by (kind, slug) and never overwrites an existing row, so
// re-seeding a database an operator has since edited cannot clobber their
// work. Imported entries are published at their frontmatter date and carry
// locale "" — the shipped markdown serves every language.
func Import(ctx context.Context, q *sqlc.Queries, fsys fs.FS) (posts, releases int, err error) {
	parsedPosts, err := ParsePosts(fsys)
	if err != nil {
		return 0, 0, err
	}
	for _, p := range parsedPosts {
		meta, err := json.Marshal(map[string]string{"author": p.Author})
		if err != nil {
			return posts, releases, err
		}
		md, err := markdownSource(fsys, "blog", p.Slug)
		if err != nil {
			return posts, releases, err
		}
		created, err := importEntry(ctx, q, sqlc.CreateEntryParams{
			Kind: "post", Slug: p.Slug, Locale: "",
			Title: p.Title, Summary: p.Description,
			BodyMd: md, BodyHtml: p.Body, Meta: meta,
			Status:      "published",
			PublishedAt: pgtype.Timestamptz{Time: p.Date, Valid: true},
		})
		if err != nil {
			return posts, releases, fmt.Errorf("import post %s: %w", p.Slug, err)
		}
		if created {
			posts++
		}
	}

	parsedReleases, err := ParseReleases(fsys)
	if err != nil {
		return posts, releases, err
	}
	for _, r := range parsedReleases {
		md, err := markdownSource(fsys, "changelog", r.Slug)
		if err != nil {
			return posts, releases, err
		}
		created, err := importEntry(ctx, q, sqlc.CreateEntryParams{
			Kind: "release", Slug: r.Slug, Locale: "",
			Title: r.Title, Summary: r.Summary,
			BodyMd: md, BodyHtml: r.Body, Meta: []byte("{}"),
			Status:      "published",
			PublishedAt: pgtype.Timestamptz{Time: r.Date, Valid: true},
		})
		if err != nil {
			return posts, releases, fmt.Errorf("import release %s: %w", r.Slug, err)
		}
		if created {
			releases++
		}
	}
	return posts, releases, nil
}

// importEntry inserts one entry unless (kind, slug, locale) already exists.
func importEntry(ctx context.Context, q *sqlc.Queries, arg sqlc.CreateEntryParams) (bool, error) {
	_, err := q.GetEntryByKindSlugLocale(ctx, sqlc.GetEntryByKindSlugLocaleParams{
		Kind: arg.Kind, Slug: arg.Slug, Locale: arg.Locale,
	})
	switch {
	case err == nil:
		return false, nil // already imported (or edited since) — never clobber
	case !errors.Is(err, pgx.ErrNoRows):
		return false, err
	}
	if _, err := q.CreateEntry(ctx, arg); err != nil {
		return false, err
	}
	return true, nil
}

// markdownSource re-reads a file's body so the stored body_md is the real
// source an editor will see, not a re-serialization of the rendered HTML.
func markdownSource(fsys fs.FS, dir, slug string) (string, error) {
	raw, err := fs.ReadFile(fsys, dir+"/"+slug+".md")
	if err != nil {
		return "", err
	}
	var discard struct{}
	body, err := splitFrontmatter(raw, &discard)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
