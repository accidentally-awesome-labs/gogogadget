package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contentAdmin builds a server with a full admin, ready to drive the CMS
// entirely over HTTP.
func contentAdmin(t *testing.T, id string) (*Server, *http.Cookie) {
	t.Helper()
	s := integrationServer(t, nil)
	return s, staffUser(t, s, "user_"+id, "org_"+id, identity.RoleAdmin)
}

// A draft is invisible everywhere a reader looks, and publishing shows up on
// the very NEXT request — a missing cache invalidation would surface here as
// a 30-second delay.
func TestDraftIsInvisibleUntilPublished(t *testing.T) {
	s, admin := contentAdmin(t, "cms1")
	entries := seedEntries(t, s, sqlc.CreateEntryParams{
		Kind: "post", Slug: "cms-draft", Title: "CMS Draft",
		BodyMd: "hidden", BodyHtml: "<p>hidden</p>", Status: "draft",
	})
	entry := entries[0]

	_, _, index := serve(t, s, "GET", "/blog", nil, nil)
	assert.NotContains(t, index, "CMS Draft")
	_, _, feed := serve(t, s, "GET", "/rss.xml", nil, nil)
	assert.NotContains(t, feed, "CMS Draft")
	code, _, _ := serve(t, s, "GET", "/blog/cms-draft", nil, nil)
	assert.Equal(t, http.StatusNotFound, code)

	code, _, _ = postForm(t, s, fmt.Sprintf("/admin/content/%d/publish", entry.ID), url.Values{}, admin)
	require.Equal(t, http.StatusOK, code)

	_, _, index = serve(t, s, "GET", "/blog", nil, nil)
	assert.Contains(t, index, "CMS Draft", "publishing must be visible on the next request, not the next TTL")
	code, _, _ = serve(t, s, "GET", "/blog/cms-draft", nil, nil)
	assert.Equal(t, http.StatusOK, code)
}

// A future published_at IS the scheduled state, and a backdated one is
// simply live: no worker moves an entry between them.
func TestScheduledEntryIsHiddenUntilItsTime(t *testing.T) {
	s, _ := contentAdmin(t, "cms2")
	seedEntries(t, s,
		sqlc.CreateEntryParams{
			Kind: "post", Slug: "cms-future", Title: "CMS Future",
			BodyMd: "later", BodyHtml: "<p>later</p>", Status: "published",
			PublishedAt: publishedAt(time.Now().Add(time.Hour)),
		},
		sqlc.CreateEntryParams{
			Kind: "post", Slug: "cms-past", Title: "CMS Past",
			BodyMd: "now", BodyHtml: "<p>now</p>", Status: "published",
			PublishedAt: publishedAt(time.Now().Add(-time.Hour)),
		},
	)

	_, _, index := serve(t, s, "GET", "/blog", nil, nil)
	assert.NotContains(t, index, "CMS Future")
	assert.Contains(t, index, "CMS Past")
	code, _, _ := serve(t, s, "GET", "/blog/cms-future", nil, nil)
	assert.Equal(t, http.StatusNotFound, code)
}

// An expired entry leaves every public surface at once, while staying listed
// and editable in the admin — retiring content must never lose it.
func TestExpiredEntryLeavesPublicSurfacesButStaysEditable(t *testing.T) {
	s, admin := contentAdmin(t, "cms3")
	entries := seedEntries(t, s, sqlc.CreateEntryParams{
		Kind: "post", Slug: "cms-expired", Title: "CMS Expired",
		BodyMd: "gone", BodyHtml: "<p>gone</p>", Status: "published",
		PublishedAt: publishedAt(time.Now().Add(-2 * time.Hour)),
		UnpublishAt: publishedAt(time.Now().Add(-time.Hour)),
	})

	_, _, index := serve(t, s, "GET", "/blog", nil, nil)
	assert.NotContains(t, index, "CMS Expired")
	_, _, feed := serve(t, s, "GET", "/rss.xml", nil, nil)
	assert.NotContains(t, feed, "CMS Expired")
	_, _, sitemap := serve(t, s, "GET", "/sitemap.xml", nil, nil)
	assert.NotContains(t, sitemap, "/blog/cms-expired")
	code, _, _ := serve(t, s, "GET", "/blog/cms-expired", nil, nil)
	assert.Equal(t, http.StatusNotFound, code)

	code, _, adminBody := serve(t, s, "GET", "/admin/content", nil, nil, admin)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, adminBody, "CMS Expired")
	assert.Contains(t, adminBody, "Expired", "the badge tells the editor why it is gone")

	code, _, _ = serve(t, s, "GET", fmt.Sprintf("/admin/content/%d", entries[0].ID), nil, nil, admin)
	assert.Equal(t, http.StatusOK, code)
}

// templ escapes; goldmark runs without WithUnsafe. Between them, a body is
// text no matter what an author pastes into it.
func TestPublishedBodyEscapesMarkup(t *testing.T) {
	s, admin := contentAdmin(t, "cms4")
	code, _, _ := postForm(t, s, "/admin/content", url.Values{
		"kind":    {"post"},
		"title":   {"CMS XSS"},
		"slug":    {"cms-xss"},
		"body_md": {"Before\n\n<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\nAfter"},
	}, admin)
	require.Equal(t, http.StatusOK, code)

	entry, err := s.q.GetEntryByKindSlugLocale(t.Context(), sqlc.GetEntryByKindSlugLocaleParams{
		Kind: "post", Slug: "cms-xss", Locale: "",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.q.DeleteEntry(t.Context(), entry.ID) })

	code, _, _ = postForm(t, s, fmt.Sprintf("/admin/content/%d/publish", entry.ID), url.Values{}, admin)
	require.Equal(t, http.StatusOK, code)

	_, _, body := serve(t, s, "GET", "/blog/cms-xss", nil, nil)
	assert.NotContains(t, body, "<script>alert(1)</script>")
	assert.NotContains(t, body, "onerror=alert(1)")
	assert.Contains(t, body, "raw HTML omitted", "goldmark drops raw HTML instead of trusting it")
}

// The unique index is per locale, so the message has to say so rather than
// leaking a constraint name through the 500 page.
func TestDuplicateSlugIsRejectedWithTheForm(t *testing.T) {
	s, admin := contentAdmin(t, "cms5")
	seedEntries(t, s, sqlc.CreateEntryParams{
		Kind: "post", Slug: "cms-taken", Title: "Taken",
		BodyMd: "x", BodyHtml: "<p>x</p>", Status: "draft",
	})

	code, _, body := postForm(t, s, "/admin/content", url.Values{
		"kind": {"post"}, "title": {"Also taken"}, "slug": {"cms-taken"}, "body_md": {"y"},
	}, admin)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "already used")
	assert.Contains(t, body, `value="Also taken"`, "the rejected submission stays on screen")
}

// The expiry check has a database CHECK behind it; this is the message a
// human gets instead of a constraint violation.
func TestExpiryBeforePublishIsRejected(t *testing.T) {
	s, admin := contentAdmin(t, "cms6")
	code, _, body := postForm(t, s, "/admin/content", url.Values{
		"kind": {"post"}, "title": {"Backwards"}, "slug": {"cms-backwards"}, "body_md": {"x"},
		"published_at": {"2026-05-05T10:00"}, "unpublish_at": {"2026-05-04T10:00"},
	}, admin)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "must be after the publish date")
	assert.NotContains(t, body, "Something went wrong", "a validation failure is not a 500")
}

// Unpublishing keeps the row and its date; the public surfaces drop it.
func TestUnpublishRemovesFromPublicSurfaces(t *testing.T) {
	s, admin := contentAdmin(t, "cms7")
	entries := seedEntries(t, s, sqlc.CreateEntryParams{
		Kind: "post", Slug: "cms-retract", Title: "CMS Retract",
		BodyMd: "x", BodyHtml: "<p>x</p>", Status: "published",
		PublishedAt: publishedAt(time.Now().Add(-time.Hour)),
	})

	_, _, index := serve(t, s, "GET", "/blog", nil, nil)
	require.Contains(t, index, "CMS Retract")

	code, _, _ := postForm(t, s, fmt.Sprintf("/admin/content/%d/unpublish", entries[0].ID), url.Values{}, admin)
	require.Equal(t, http.StatusOK, code)

	_, _, index = serve(t, s, "GET", "/blog", nil, nil)
	assert.NotContains(t, index, "CMS Retract")
	_, _, sitemap := serve(t, s, "GET", "/sitemap.xml", nil, nil)
	assert.NotContains(t, sitemap, "/blog/cms-retract")

	stored, err := s.q.GetEntry(t.Context(), entries[0].ID)
	require.NoError(t, err)
	assert.True(t, stored.PublishedAt.Valid, "the date survives so re-publishing restores it")
}

func TestDeleteRemovesTheEntry(t *testing.T) {
	s, admin := contentAdmin(t, "cms8")
	entries := seedEntries(t, s, sqlc.CreateEntryParams{
		Kind: "post", Slug: "cms-doomed", Title: "CMS Doomed",
		BodyMd: "x", BodyHtml: "<p>x</p>", Status: "published",
		PublishedAt: publishedAt(time.Now().Add(-time.Hour)),
	})

	code, _, _ := postForm(t, s, fmt.Sprintf("/admin/content/%d/delete", entries[0].ID), url.Values{}, admin)
	require.Equal(t, http.StatusOK, code)

	_, err := s.q.GetEntry(t.Context(), entries[0].ID)
	require.Error(t, err)
	_, _, index := serve(t, s, "GET", "/blog", nil, nil)
	assert.NotContains(t, index, "CMS Doomed")
}

// Restoring an older revision brings the old text back AND snapshots the
// state it replaced, so a restore is itself undoable.
func TestRevisionRestoreRoundTrip(t *testing.T) {
	s, admin := contentAdmin(t, "cms9")
	code, _, _ := postForm(t, s, "/admin/content", url.Values{
		"kind": {"post"}, "title": {"First title"}, "slug": {"cms-revisions"}, "body_md": {"first body"},
	}, admin)
	require.Equal(t, http.StatusOK, code)

	entry, err := s.q.GetEntryByKindSlugLocale(t.Context(), sqlc.GetEntryByKindSlugLocaleParams{
		Kind: "post", Slug: "cms-revisions", Locale: "",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.q.DeleteEntry(t.Context(), entry.ID) })

	code, _, _ = postForm(t, s, fmt.Sprintf("/admin/content/%d", entry.ID), url.Values{
		"kind": {"post"}, "title": {"Second title"}, "slug": {"cms-revisions"}, "body_md": {"second body"},
	}, admin)
	require.Equal(t, http.StatusOK, code)

	revisions, err := s.q.ListRevisionsByEntry(t.Context(), sqlc.ListRevisionsByEntryParams{
		EntryID: entry.ID, Lim: 10,
	})
	require.NoError(t, err)
	require.Len(t, revisions, 2, "each save snapshots the state it saved")
	oldest := revisions[len(revisions)-1]
	require.Equal(t, "First title", oldest.Title)

	code, _, _ = postForm(t, s,
		fmt.Sprintf("/admin/content/%d/revisions/%d/restore", entry.ID, oldest.ID), url.Values{}, admin)
	require.Equal(t, http.StatusOK, code)

	restored, err := s.q.GetEntry(t.Context(), entry.ID)
	require.NoError(t, err)
	assert.Equal(t, "First title", restored.Title)
	assert.Equal(t, "first body", restored.BodyMd)
	assert.Contains(t, restored.BodyHtml, "first body", "the HTML is re-rendered, not left stale")

	after, err := s.q.ListRevisionsByEntry(t.Context(), sqlc.ListRevisionsByEntryParams{
		EntryID: entry.ID, Lim: 10,
	})
	require.NoError(t, err)
	assert.Len(t, after, 3, "restoring is a save, so it leaves its own snapshot")
}

// A revision id from a different entry must miss rather than cross-load.
func TestRevisionRestoreRejectsForeignRevision(t *testing.T) {
	s, admin := contentAdmin(t, "cms10")
	entries := seedEntries(t, s,
		sqlc.CreateEntryParams{Kind: "post", Slug: "cms-owner", Title: "Owner",
			BodyMd: "x", BodyHtml: "<p>x</p>", Status: "draft"},
		sqlc.CreateEntryParams{Kind: "post", Slug: "cms-other", Title: "Other",
			BodyMd: "y", BodyHtml: "<p>y</p>", Status: "draft"},
	)
	rev, err := s.q.InsertRevision(t.Context(), sqlc.InsertRevisionParams{
		EntryID: entries[1].ID, Title: "Other", BodyMd: "y", Meta: []byte("{}"),
	})
	require.NoError(t, err)

	code, _, _ := postForm(t, s,
		fmt.Sprintf("/admin/content/%d/revisions/%d/restore", entries[0].ID, rev.ID), url.Values{}, admin)
	assert.Equal(t, http.StatusNotFound, code)
}

// Mutations are audited: the platform log is how an operator answers "who
// changed the pricing page".
func TestContentMutationsAreAudited(t *testing.T) {
	s, admin := contentAdmin(t, "cms11")
	code, _, _ := postForm(t, s, "/admin/content", url.Values{
		"kind": {"post"}, "title": {"Audited"}, "slug": {"cms-audited"}, "body_md": {"x"},
	}, admin)
	require.Equal(t, http.StatusOK, code)

	entry, err := s.q.GetEntryByKindSlugLocale(t.Context(), sqlc.GetEntryByKindSlugLocaleParams{
		Kind: "post", Slug: "cms-audited", Locale: "",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.q.DeleteEntry(t.Context(), entry.ID) })

	rows, err := s.q.ListAuditAll(t.Context(), sqlc.ListAuditAllParams{Filter: "content.created", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	assert.Contains(t, string(rows[0].Metadata), "cms-audited")
}

// The live preview is a bare fragment: swapping a layout into a pane inside
// an already-rendered page would nest two documents.
func TestPreviewReturnsFragmentOnly(t *testing.T) {
	s, admin := contentAdmin(t, "cms12")
	code, _, body := postForm(t, s, "/admin/content/preview", url.Values{
		"kind": {"post"}, "body_md": {"# Preview heading"},
	}, admin)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "<h1>Preview heading</h1>")
	assert.NotContains(t, body, "<html")
	assert.NotContains(t, strings.ToLower(body), "<!doctype")
}

// The preview page renders the type's REAL public view, so an editor cannot
// approve something different from what publishes.
func TestPreviewPageRendersThePublicView(t *testing.T) {
	s, admin := contentAdmin(t, "cms13")
	entries := seedEntries(t, s, sqlc.CreateEntryParams{
		Kind: "post", Slug: "cms-preview", Title: "CMS Preview",
		Summary: "Not published yet", BodyMd: "draft body", BodyHtml: "<p>draft body</p>",
		Status: "draft",
	})

	code, _, body := serve(t, s, "GET", fmt.Sprintf("/admin/content/%d/preview", entries[0].ID), nil, nil, admin)
	require.Equal(t, http.StatusOK, code, "a draft previews regardless of status or dates")
	assert.Contains(t, body, "CMS Preview")
	assert.Contains(t, body, "draft body")
	assert.Contains(t, body, "BlogPosting", "the post type's own page metadata comes with it")
}
