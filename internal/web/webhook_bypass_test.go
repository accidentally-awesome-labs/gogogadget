package web

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// In bypass mode with no webhook secret configured, unsigned fixtures are
// trusted (the fresh-clone zero-account path). Never possible in production —
// DEV_AUTH_BYPASS is boot-refused there.
func TestClerkWebhookBypassWithoutSecret(t *testing.T) {
	s := integrationServer(t, func(d *Deps) { d.Config.ClerkWebhookSecret = "" })
	ctx := t.Context()

	payload := userCreatedPayload("user_ns1", "em_1", "ns1@example.com", "No", "Secret")
	h := http.Header{}
	h.Set("svix-id", "msg_ns1")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, h)
	require.Equal(t, http.StatusOK, code)

	u, err := s.q.GetUserByID(ctx, "user_ns1")
	require.NoError(t, err)
	assert.Equal(t, "No Secret", u.Name)

	// Welcome email job enqueued even without a configured secret.
	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind='email.welcome'`).Scan(&n))
	assert.Equal(t, 1, n)

	_ = s.q.DeleteUser(ctx, "user_ns1")
}

// Without bypass and without a secret, the endpoint refuses.
func TestClerkWebhookUnconfiguredRefuses(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.Config.ClerkWebhookSecret = ""
		d.Config.DevAuthBypass = false
		d.Config.ClerkSecretKey = "sk_test_x"
	})
	payload := userCreatedPayload("user_ns2", "em_1", "ns2@example.com", "No", "Secret")
	h := http.Header{}
	h.Set("svix-id", "msg_ns2")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, h)
	assert.Equal(t, http.StatusServiceUnavailable, code)
}
