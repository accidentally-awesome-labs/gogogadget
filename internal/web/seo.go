package web

import (
	"net/http"
	"net/url"

	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// Search-engine metadata.
//
// The app serves one path per page and picks the language from ?lang= (then a
// cookie, then Accept-Language). Left alone, that makes /, /?lang=en and
// /?lang=es three URLs with the same content — the textbook duplicate-content
// shape, introduced the day the locale switcher shipped.
//
// The fix is the standard pair:
//
//   - a SELF-referential canonical that keeps the language parameter and
//     drops everything else, so each language version is one indexable URL
//     and tracking or pagination junk cannot fork it;
//   - reciprocal hreflang alternates naming every language version plus
//     x-default, so a crawler knows they are translations rather than
//     duplicates.
//
// Cookie-negotiated content deliberately does NOT get its own URL: a crawler
// has no cookie, so it would only ever see the default.

// canonicalFor returns the absolute canonical URL for this request.
func (s *Server) canonicalFor(r *http.Request) string {
	u := s.cfg.AppURL + r.URL.Path
	// Only a supported, non-default language survives into the canonical.
	if lang := r.URL.Query().Get("lang"); lang != "" {
		if tag, ok := i18n.ParseSupported(lang); ok && tag != i18n.Locales[0].Tag {
			u += "?lang=" + url.QueryEscape(tag.String())
		}
	}
	return u
}

// alternatesFor lists every language version of this path plus x-default.
// The default locale owns the bare path, which is also x-default: it is what
// a crawler with no language preference should be sent to.
func (s *Server) alternatesFor(r *http.Request) []templates.Alternate {
	base := s.cfg.AppURL + r.URL.Path
	out := make([]templates.Alternate, 0, len(i18n.Locales)+1)
	for i, l := range i18n.Locales {
		href := base
		if i > 0 {
			href = base + "?lang=" + l.Code
		}
		out = append(out, templates.Alternate{Lang: l.Code, Href: href})
	}
	return append(out, templates.Alternate{Lang: "x-default", Href: base})
}

// --- JSON-LD -----------------------------------------------------------
//
// These return plain Go values; templ.JSONScriptElement marshals them into
// the script element, escaping <, > and & — so a title containing
// "</script>" cannot break out of the data block.

const orgName = "GoGoGadget"

// siteJSONLD describes the site itself: an Organization plus a WebSite whose
// SearchAction points at the docs search that actually exists.
func (s *Server) siteJSONLD() any {
	return []any{
		map[string]any{
			"@context": "https://schema.org",
			"@type":    "Organization",
			"name":     orgName,
			"url":      s.cfg.AppURL,
			"logo":     s.cfg.AppURL + "/static/og.png",
		},
		map[string]any{
			"@context": "https://schema.org",
			"@type":    "WebSite",
			"name":     orgName,
			"url":      s.cfg.AppURL,
			"potentialAction": map[string]any{
				"@type":       "SearchAction",
				"target":      s.cfg.AppURL + "/docs/search?q={search_term_string}",
				"query-input": "required name=search_term_string",
			},
		},
	}
}

// postJSONLD describes one blog post.
func (s *Server) postJSONLD(p content.Post) any {
	return map[string]any{
		"@context":      "https://schema.org",
		"@type":         "BlogPosting",
		"headline":      p.Title,
		"description":   p.Description,
		"datePublished": p.Date.UTC().Format("2006-01-02"),
		"url":           s.cfg.AppURL + "/blog/" + p.Slug,
		"image":         s.cfg.AppURL + "/static/og.png",
		"author":        map[string]any{"@type": "Person", "name": p.Author},
		"publisher": map[string]any{
			"@type": "Organization", "name": orgName,
			"logo": map[string]any{"@type": "ImageObject", "url": s.cfg.AppURL + "/static/og.png"},
		},
	}
}
