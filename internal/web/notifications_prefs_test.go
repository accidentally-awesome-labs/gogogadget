package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationPrefsPageAndSave(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_np", "org_np", "org:admin")
	cookie := sessionCookie("user_np", "org_np", "org:admin")

	code, _, body := serve(t, s, "GET", "/app/settings/notifications", nil, nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="notification-prefs-form"`)
	for _, k := range notify.Kinds {
		assert.Contains(t, body, `data-testid="pref-`+k+`"`, "row per kind")
	}

	// Uncheck welcome (absent field), keep payment_failed on (present field).
	form := url.Values{"kind_payment_failed": []string{"on"}}
	code, _, _ = postForm(t, s, "/app/settings/notifications", form, cookie)
	assert.Equal(t, http.StatusOK, code)

	muted, err := s.q.GetNotificationPreference(t.Context(), sqlc.GetNotificationPreferenceParams{ClerkUserID: "user_np", Kind: "welcome"})
	require.NoError(t, err)
	assert.False(t, muted.InApp)
	on, err := s.q.GetNotificationPreference(t.Context(), sqlc.GetNotificationPreferenceParams{ClerkUserID: "user_np", Kind: "payment_failed"})
	require.NoError(t, err)
	assert.True(t, on.InApp)

	// The muted kind blocks notify.Send; the on kind still lands.
	notify.Send(t.Context(), s.q, "org_np", "user_np", "welcome", "muted", "", "")
	notify.Send(t.Context(), s.q, "org_np", "user_np", "payment_failed", "sent", "", "")
	n, err := s.q.CountNotificationsByUser(t.Context(), sqlc.CountNotificationsByUserParams{ClerkOrgID: "org_np", ClerkUserID: "user_np"})
	require.NoError(t, err)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "only the unmuted kind wrote a row")

	// Re-render reflects the saved state (unchecked box).
	code, _, body = serve(t, s, "GET", "/app/settings/notifications", nil, nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="pref-welcome"`)
}
