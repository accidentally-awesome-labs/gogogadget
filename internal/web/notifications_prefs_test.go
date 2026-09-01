package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/gogogadget/gogogadget/internal/web/templates"
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

	muted, err := s.q.GetNotificationPreference(t.Context(), sqlc.GetNotificationPreferenceParams{UserID: "user_np", Kind: "welcome"})
	require.NoError(t, err)
	assert.False(t, muted.InApp)
	on, err := s.q.GetNotificationPreference(t.Context(), sqlc.GetNotificationPreferenceParams{UserID: "user_np", Kind: "payment_failed"})
	require.NoError(t, err)
	assert.True(t, on.InApp)

	// The muted kind blocks notify.Send; the on kind still lands.
	notify.Send(t.Context(), s.q, "org_np", "user_np", "welcome", "muted", "", "")
	notify.Send(t.Context(), s.q, "org_np", "user_np", "payment_failed", "sent", "", "")
	n, err := s.q.CountNotificationsByUser(t.Context(), sqlc.CountNotificationsByUserParams{OrgID: "org_np", UserID: "user_np"})
	require.NoError(t, err)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "only the unmuted kind wrote a row")

	// Re-render reflects the saved state (unchecked box).
	code, _, body = serve(t, s, "GET", "/app/settings/notifications", nil, nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="pref-welcome"`)
}

func TestDigestFrequencyPersists(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_dig", "org_dig_w", "org:admin")
	cookie := sessionCookie("user_dig", "org_dig_w", "org:admin")

	code, _, body := serve(t, s, "GET", "/app/settings/notifications", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="digest-frequency"`)
	assert.Contains(t, body, `value="weekly" selected`, "the schema default is reflected in the form")

	code, _, _ = postForm(t, s, "/app/settings/notifications",
		url.Values{"digest_frequency": {"daily"}}, cookie)
	require.Equal(t, http.StatusOK, code)
	u, err := s.q.GetUserByID(t.Context(), "user_dig")
	require.NoError(t, err)
	assert.Equal(t, "daily", u.DigestFrequency)

	code, _, body = serve(t, s, "GET", "/app/settings/notifications", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `value="daily" selected`, "the saved cadence comes back selected")
}

// A hand-crafted POST must not reach the CHECK constraint and 500.
func TestDigestFrequencyRejectsUnknownValue(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_digx", "org_dig_x", "org:admin")
	cookie := sessionCookie("user_digx", "org_dig_x", "org:admin")

	code, _, _ := postForm(t, s, "/app/settings/notifications",
		url.Values{"digest_frequency": {"hourly'; DROP TABLE users; --"}}, cookie)
	assert.Equal(t, http.StatusOK, code, "an unknown cadence is ignored, not a 500")

	u, err := s.q.GetUserByID(t.Context(), "user_digx")
	require.NoError(t, err)
	assert.Equal(t, "weekly", u.DigestFrequency, "the stored value is untouched")
}

// The select is spelled out in the template (computed i18n keys are invisible
// to the catalog guard), so a test ties that markup back to the canonical list.
func TestDigestFrequencyOptions(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_digo", "org_dig_o", "org:admin")

	_, _, body := serve(t, s, "GET", "/app/settings/notifications", nil, nil,
		sessionCookie("user_digo", "org_dig_o", "org:admin"))
	for _, f := range templates.DigestFrequencies {
		assert.Contains(t, body, `value="`+f+`"`, "cadence %q is offered by the server but missing from the form", f)
	}
}
