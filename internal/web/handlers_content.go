package web

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// The public content surface is generated from the content-type registry: one
// index route per type with a Path, plus a detail route for ModePages types.
// A type with Path "" is admin-managed only and gets no public route at all.

// contentView lets a type keep bespoke public markup. Absent → generic.
type contentView struct {
	index  func(t content.Type, entries []content.Entry) templ.Component
	detail func(t content.Type, e content.Entry) templ.Component
	page   func(t content.Type, e *content.Entry) Page // e nil on the index
}

// contentViews registers the two built-in overrides, preserving the blog and
// changelog markup byte-for-byte. Every other type renders generically.
func (s *Server) contentViews() map[string]contentView {
	return map[string]contentView{
		"post": {
			index: func(_ content.Type, entries []content.Entry) templ.Component {
				return templates.BlogIndex(postsOf(entries))
			},
			detail: func(_ content.Type, e content.Entry) templ.Component {
				return templates.BlogPost(e.AsPost())
			},
			page: func(_ content.Type, e *content.Entry) Page {
				if e == nil {
					return Page{
						Title:       "Blog",
						Description: "Product and engineering updates from the GoGoGadget team.",
						Layout:      templates.LayoutPublic,
					}
				}
				return Page{
					Title:       e.Title,
					Description: e.Summary,
					Layout:      templates.LayoutPublic,
					JSONLD:      s.postJSONLD(e.AsPost()),
				}
			},
		},
		"release": {
			index: func(_ content.Type, entries []content.Entry) templ.Component {
				return templates.ChangelogPage(releasesOf(entries))
			},
			page: func(_ content.Type, _ *content.Entry) Page {
				return Page{
					Title:       "Changelog",
					Description: "Everything that shipped in GoGoGadget, newest first.",
					Layout:      templates.LayoutPublic,
				}
			},
		},
	}
}

func postsOf(entries []content.Entry) []content.Post {
	out := make([]content.Post, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.AsPost())
	}
	return out
}

func releasesOf(entries []content.Entry) []content.Release {
	out := make([]content.Release, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.AsRelease())
	}
	return out
}

// requestLocale is the code the CMS resolves variants against ("en"/"es").
func requestLocale(r *http.Request) string { return i18n.Tag(r.Context()).String() }

// handleContentIndex serves a type's index: a listing for ModePages, every
// entry on one anchored page for ModeSinglePage.
func (s *Server) handleContentIndex(t content.Type) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := s.cms.List(r.Context(), t.Kind, requestLocale(r))
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		view := s.contentViews()[t.Kind]
		page := Page{Title: i18n.T(r.Context(), t.PluralKey), Layout: templates.LayoutPublic}
		if view.page != nil {
			page = view.page(t, nil)
		}
		component := s.contentIndexComponent(t, view, entries)
		s.Render(w, r, page, component)
	}
}

func (s *Server) contentIndexComponent(t content.Type, view contentView, entries []content.Entry) templ.Component {
	if view.index != nil {
		return view.index(t, entries)
	}
	if t.Mode == content.ModeSinglePage {
		return templates.ContentSinglePage(t, entries)
	}
	return templates.ContentIndex(t, entries)
}

// handleContentDetail serves one entry of a ModePages type.
func (s *Server) handleContentDetail(t content.Type) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entry, err := s.cms.BySlug(r.Context(), t.Kind, r.PathValue("slug"), requestLocale(r))
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		if entry == nil {
			s.handleNotFound(w, r)
			return
		}
		view := s.contentViews()[t.Kind]
		page := Page{Title: entry.Title, Description: entry.Summary, Layout: templates.LayoutPublic}
		if view.page != nil {
			page = view.page(t, entry)
		}
		component := templ.Component(templates.ContentDetail(t, *entry))
		if view.detail != nil {
			component = view.detail(t, *entry)
		}
		s.Render(w, r, page, component)
	}
}

// GET /rss.xml — every Feed: true type, merged newest-first.
//
// Resolved at the default locale so the feed is one canonical set of items
// rather than something that shifts with a reader's cookie.
func (s *Server) handleRSS(w http.ResponseWriter, r *http.Request) {
	locale := i18n.Locales[0].Code
	var items []content.FeedItem
	for _, t := range s.types.All() {
		if !t.Feed || t.Path == "" {
			continue
		}
		entries, err := s.cms.List(r.Context(), t.Kind, locale)
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		for _, e := range entries {
			items = append(items, content.FeedItem{
				Title:       e.Title,
				Link:        s.cfg.AppURL + contentLink(t, e),
				Description: e.Summary,
				Date:        e.PublishedAt,
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Date.After(items[j].Date) })

	feed, err := content.RSS(s.cfg.AppURL, "GoGoGadget Blog", "Product and engineering updates",
		s.cfg.AppURL+"/blog", items)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	fmt.Fprint(w, feed)
}

// contentLink is an entry's public path: its own URL for ModePages, the
// index anchor for ModeSinglePage.
func contentLink(t content.Type, e content.Entry) string {
	if t.Mode == content.ModeSinglePage {
		return t.Path + "#" + e.Anchor
	}
	return t.Path + "/" + e.Slug
}

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", s.cfg.AppURL)
}

// sitemapEntry is one <url> line, assembled BEFORE anything is written: the
// handler sets an XML content type and emits a prologue, so an error
// discovered mid-write would leave a truncated document behind it.
type sitemapEntry struct {
	loc     string
	lastmod string // "" → omitted
}

// handleSitemap lists every indexable URL. lastmod is emitted ONLY where a
// real publication date exists: a fabricated timestamp on the marketing and
// docs pages (build time, "today") teaches crawlers to distrust the field,
// which is worse than omitting it.
//
// Content is resolved at the default locale so each page appears once and
// language versions stay expressed through hreflang, not duplicate <loc>s.
func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	urls := []sitemapEntry{{loc: "/"}, {loc: "/pricing"}, {loc: "/terms"}, {loc: "/privacy"}, {loc: "/docs"}}
	locale := i18n.Locales[0].Code
	for _, t := range s.types.All() {
		if !t.Sitemap || t.Path == "" {
			continue
		}
		entries, err := s.cms.List(r.Context(), t.Kind, locale)
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		index := sitemapEntry{loc: t.Path}
		if t.Mode == content.ModeSinglePage {
			// One page whose modification date is its newest entry.
			if len(entries) > 0 {
				index.lastmod = entries[0].PublishedAt.UTC().Format("2006-01-02")
			}
			urls = append(urls, index)
			continue
		}
		urls = append(urls, index)
		for _, e := range entries {
			urls = append(urls, sitemapEntry{
				loc:     t.Path + "/" + e.Slug,
				lastmod: e.PublishedAt.UTC().Format("2006-01-02"),
			})
		}
	}
	for _, d := range s.docs.Pages {
		urls = append(urls, sitemapEntry{loc: "/docs/" + d.Slug})
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`+"\n")
	fmt.Fprint(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, u := range urls {
		if u.lastmod == "" {
			fmt.Fprintf(w, "<url><loc>%s%s</loc></url>", s.cfg.AppURL, u.loc)
			continue
		}
		fmt.Fprintf(w, "<url><loc>%s%s</loc><lastmod>%s</lastmod></url>", s.cfg.AppURL, u.loc, u.lastmod)
	}
	fmt.Fprint(w, `</urlset>`)
}

// GET /media/{id}/{filename} — a content image, rendered inline. The filename
// segment is cosmetic (it gives the URL a readable tail) and is ignored: the
// id is the identity. Rows are immutable and keys are random, so the response
// is safely immutable-cacheable.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		s.handleNotFound(w, r)
		return
	}
	m, err := s.q.GetMedia(r.Context(), id)
	if err != nil {
		s.handleNotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if err := s.store.ServeInline(r.Context(), w, m.StorageKey, m.ContentType); err != nil {
		s.renderError(w, r, err.Error())
	}
}
