package web

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnnouncementCRUDAndBanner(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_an", "org_an")
	seedMembership(t, s, "user_an2", "org_an2", "org:member")
	adminCookie := sessionCookie("user_an", "org_an", "org:admin")
	userCookie := sessionCookie("user_an2", "org_an2", "org:member")

	// Create (invalid → 422 with error text).
	form := url.Values{"kind": []string{"info"}, "message": []string{""}}
	code, _, body := postForm(t, s, "/admin/announcements", form, adminCookie)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "alert-danger")

	// Create (valid → inactive row).
	form = url.Values{"kind": []string{"info"}, "message": []string{"Scheduled maintenance"}, "url": []string{""}}
	code, _, _ = postForm(t, s, "/admin/announcements", form, adminCookie)
	assert.Equal(t, http.StatusOK, code)
	items, err := s.q.ListAnnouncements(t.Context())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.False(t, items[0].Active, "created rows land inactive")
	id := items[0].ID

	// No banner while inactive.
	code, _, body = serve(t, s, "GET", "/app", nil, nil, userCookie)
	assert.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, `data-testid="announcement-banner"`)

	// Activate → banner appears IMMEDIATELY on the next render (cache invalidation).
	code, _, _ = postForm(t, s, "/admin/announcements/"+strconv.FormatInt(id, 10)+"/activate", nil, adminCookie)
	assert.Equal(t, http.StatusOK, code)
	code, _, body = serve(t, s, "GET", "/app", nil, nil, userCookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="announcement-banner"`)
	assert.Contains(t, body, "Scheduled maintenance")

	// One-active: a second announcement displaces the first.
	form = url.Values{"kind": []string{"critical"}, "message": []string{"Second banner"}, "url": []string{""}}
	_, _, _ = postForm(t, s, "/admin/announcements", form, adminCookie)
	items, err = s.q.ListAnnouncements(t.Context())
	require.NoError(t, err)
	require.Len(t, items, 2)
	var second int64
	for _, a := range items {
		if a.Message == "Second banner" {
			second = a.ID
		}
	}
	_, _, _ = postForm(t, s, "/admin/announcements/"+strconv.FormatInt(second, 10)+"/activate", nil, adminCookie)
	active, err := s.q.GetActiveAnnouncement(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "Second banner", active.Message, "activating one deactivates the rest")
	code, _, body = serve(t, s, "GET", "/app", nil, nil, userCookie)
	assert.Contains(t, body, "Second banner")

	// Deactivate → banner gone.
	_, _, _ = postForm(t, s, "/admin/announcements/"+strconv.FormatInt(second, 10)+"/deactivate", nil, adminCookie)
	code, _, body = serve(t, s, "GET", "/app", nil, nil, userCookie)
	assert.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, `data-testid="announcement-banner"`, "deactivated banner leaves the shell")

	// Delete.
	_, _, _ = postForm(t, s, "/admin/announcements/"+strconv.FormatInt(id, 10)+"/delete", nil, adminCookie)
	items, err = s.q.ListAnnouncements(t.Context())
	require.NoError(t, err)
	assert.Len(t, items, 1)

	// Non-admin cannot reach the CRUD.
	code, _, _ = serve(t, s, "GET", "/admin/announcements", nil, nil, userCookie)
	assert.Equal(t, http.StatusForbidden, code)
}

// The kind cell used to be written as class="badge { announcementBadgeClass(…) }",
// which templ does not interpolate inside a quoted attribute: every row shipped
// the literal expression as a class name and rendered unstyled. Assert the
// resolved component class per kind.
func TestAnnouncementKindBadgeIsStyled(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_akb", "org_akb")
	cookie := sessionCookie("user_akb", "org_akb", "org:admin")

	for kind, want := range map[string]string{
		"info":     "badge badge-info",
		"warning":  "badge badge-warn",
		"critical": "badge badge-danger",
	} {
		form := url.Values{"kind": {kind}, "message": {"Notice " + kind}, "url": {""}}
		code, _, _ := postForm(t, s, "/admin/announcements", form, cookie)
		require.Equal(t, http.StatusOK, code)

		code, _, body := serve(t, s, "GET", "/admin/announcements", nil, nil, cookie)
		require.Equal(t, http.StatusOK, code)
		assert.Contains(t, body, `class="`+want+`"`, "kind %q must render its component class", kind)
		assert.NotContains(t, body, "announcementBadgeClass",
			"a templ expression must never reach the browser as a class name")
	}
}
