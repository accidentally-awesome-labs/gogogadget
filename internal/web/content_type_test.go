package web

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The extensibility claim, proved end to end over HTTP: a content type that
// exists nowhere in the production tree gets an admin list entry, an editor
// with its declared field, validation, a public index and detail page, and
// sitemap membership — with no migration, no table, no handler and no
// template written for it.
//
// Label keys are borrowed from the built-in types so this test needs no
// catalog entries; that a REAL new type needs them is asserted by
// TestContentTypeKeysExistInCatalogs.
func guideType() content.Type {
	return content.Type{
		Kind: "guide", LabelKey: "content.type.post", PluralKey: "content.type.posts",
		Path: "/guides", Mode: content.ModePages, Slug: content.SlugFromTitle, Sitemap: true,
		Fields: []content.Field{{Key: "level", LabelKey: "content.field.author",
			Kind: content.FieldSelect, Required: true, Options: []string{"intro", "advanced"}}},
	}
}

func guideServer(t *testing.T, mutate func(*content.Type)) (*Server, *http.Cookie) {
	t.Helper()
	guide := guideType()
	if mutate != nil {
		mutate(&guide)
	}
	s := integrationServer(t, func(d *Deps) {
		d.ContentTypes = append(content.DefaultTypes(), guide)
	})
	return s, staffUser(t, s, "user_guide", "org_guide", identity.RoleAdmin)
}

func TestNewContentTypeGetsAdminSurface(t *testing.T) {
	s, admin := guideServer(t, nil)

	code, _, body := serve(t, s, "GET", "/admin/content", nil, nil, admin)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="content-filter-guide"`)
	assert.Contains(t, body, `data-testid="content-new-guide"`)

	code, _, body = serve(t, s, "GET", "/admin/content/new?kind=guide", nil, nil, admin)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="content-meta-level"`)
	assert.Contains(t, body, `value="intro"`)
	assert.Contains(t, body, `value="advanced"`)
}

func TestNewContentTypeValidatesItsDeclaredField(t *testing.T) {
	s, admin := guideServer(t, nil)

	code, _, body := postForm(t, s, "/admin/content", url.Values{
		"kind": {"guide"}, "title": {"Bad level"}, "slug": {"bad-level"},
		"body_md": {"x"}, "meta_level": {"nonsense"},
	}, admin)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, `data-testid="form-error"`)

	_, err := s.q.GetEntryByKindSlugLocale(t.Context(), sqlc.GetEntryByKindSlugLocaleParams{
		Kind: "guide", Slug: "bad-level", Locale: "",
	})
	assert.Error(t, err, "a rejected submission must not reach the table")
}

func TestNewContentTypePublishesThroughGenericTemplates(t *testing.T) {
	s, admin := guideServer(t, nil)

	code, _, _ := postForm(t, s, "/admin/content", url.Values{
		"kind": {"guide"}, "title": {"Deploying to Fly"},
		"body_md": {"Run **flyctl**."}, "meta_level": {"advanced"},
	}, admin)
	require.Equal(t, http.StatusOK, code)

	entry, err := s.q.GetEntryByKindSlugLocale(t.Context(), sqlc.GetEntryByKindSlugLocaleParams{
		Kind: "guide", Slug: "deploying-to-fly", Locale: "",
	})
	require.NoError(t, err, "the slug came from the type's own SlugFunc")
	t.Cleanup(func() { _ = s.q.DeleteEntry(t.Context(), entry.ID) })
	assert.JSONEq(t, `{"level":"advanced"}`, string(entry.Meta), "the declared field round-trips through meta")

	// Unpublished: the index exists, the entry does not.
	code, _, index := serve(t, s, "GET", "/guides", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, index, "Deploying to Fly")

	code, _, _ = postForm(t, s, fmt.Sprintf("/admin/content/%d/publish", entry.ID), url.Values{}, admin)
	require.Equal(t, http.StatusOK, code)

	code, _, index = serve(t, s, "GET", "/guides", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, index, "Deploying to Fly")
	assert.Contains(t, index, `data-testid="content-index-entry"`)

	code, _, detail := serve(t, s, "GET", "/guides/deploying-to-fly", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, detail, `data-testid="content-detail"`)
	assert.Contains(t, detail, "<strong>flyctl</strong>")
	// The generic public layout brings canonical and hreflang with it.
	assert.Contains(t, detail, `<link rel="canonical" href="http://localhost:18080/guides/deploying-to-fly"`)
	assert.Contains(t, detail, `hreflang="es"`)

	_, _, sitemap := serve(t, s, "GET", "/sitemap.xml", nil, nil)
	assert.Contains(t, sitemap, "<loc>http://localhost:18080/guides/deploying-to-fly</loc>")
	_, _, feed := serve(t, s, "GET", "/rss.xml", nil, nil)
	assert.NotContains(t, feed, "Deploying to Fly", "the type declares Feed: false")
}

// Path "" is the in-app-copy mode: admin CRUD and programmatic reads with no
// public route whatsoever.
func TestPathlessContentTypeHasNoPublicRoute(t *testing.T) {
	s, admin := guideServer(t, func(g *content.Type) { g.Path = "" })

	entries := seedEntries(t, s, sqlc.CreateEntryParams{
		Kind: "guide", Slug: "in-app-copy", Title: "In-app copy",
		BodyMd: "x", BodyHtml: "<p>x</p>", Meta: []byte(`{"level":"intro"}`), Status: "draft",
	})

	code, _, _ := serve(t, s, "GET", "/guides", nil, nil)
	assert.Equal(t, http.StatusNotFound, code)
	code, _, _ = serve(t, s, "GET", "/guides/in-app-copy", nil, nil)
	assert.Equal(t, http.StatusNotFound, code)

	code, _, body := serve(t, s, "GET", "/admin/content", nil, nil, admin)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "In-app copy", "it is still managed like any other type")

	code, _, _ = serve(t, s, "GET", fmt.Sprintf("/admin/content/%d", entries[0].ID), nil, nil, admin)
	assert.Equal(t, http.StatusOK, code)
	// There is no public rendering to preview.
	code, _, _ = serve(t, s, "GET", fmt.Sprintf("/admin/content/%d/preview", entries[0].ID), nil, nil, admin)
	assert.Equal(t, http.StatusNotFound, code)
}

// An unknown kind must 404 rather than render an empty editor that saves
// nothing.
func TestUnknownKindIsNotFound(t *testing.T) {
	s, admin := contentAdmin(t, "kind404")
	code, _, _ := serve(t, s, "GET", "/admin/content?kind=wombat", nil, nil, admin)
	assert.Equal(t, http.StatusNotFound, code)
	code, _, _ = serve(t, s, "GET", "/admin/content/new?kind=wombat", nil, nil, admin)
	assert.Equal(t, http.StatusNotFound, code)
}

// An invalid declaration must not take the server down: NewServer falls back
// to the built-in types and logs. cmd/server validates first and exits.
func TestInvalidTypeDeclarationFallsBackToDefaults(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.ContentTypes = []content.Type{{Kind: "Bad Kind", LabelKey: "l", PluralKey: "p"}}
	})
	assert.Len(t, s.types.All(), 2, "the built-in types")
	code, _, _ := serve(t, s, "GET", "/blog", nil, nil)
	assert.Equal(t, http.StatusOK, code)
}
