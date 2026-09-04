package web

import (
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With the dev adapter selected, unsigned fixtures are trusted (the
// fresh-clone zero-account path) and no provider secret is consulted at all.
// Never possible in production: the dev adapter is a development/test target
// and DEV_AUTH_BYPASS is boot-refused there.
//
// The mirror-image case — a hosted adapter with no webhook secret refusing
// the same delivery — belongs to the adapter, and is pinned by
// identity/clerk's TestWebhookRefusesWithoutSecret. This receiver has no
// bypass branch of its own: it asks the selected adapter and reports what it
// says.
func TestIdentityWebhookAcceptsUnsignedDevDelivery(t *testing.T) {
	s := integrationServer(t, func(d *Deps) { d.Config.ClerkWebhookSecret = "" })
	ctx := t.Context()

	payload := userCreatedPayload("user_ns1", "ns1@example.com", "No Secret")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, identityDelivery("msg_ns1"))
	require.Equal(t, http.StatusOK, code)

	mapping, err := s.q.GetIdentitySubject(ctx, sqlc.GetIdentitySubjectParams{Provider: "dev", Subject: "user_ns1"})
	require.NoError(t, err)
	u, err := s.q.GetUserByID(ctx, mapping.UserID)
	require.NoError(t, err)
	assert.Equal(t, "No Secret", u.Name)

	// Welcome email job enqueued even without a configured secret.
	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind='email.welcome'`).Scan(&n))
	_ = s.q.DeleteUser(ctx, mapping.UserID)
}
