// Package contract is the provider-neutral identity contract. It lives beside
// the seam and is imported by every adapter's own test package, so the local
// adapter and each hosted adapter are held to one identical table instead of
// a vendor-shaped table inside the seam.
package contract

import (
	"context"
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// VerifierHarness adapts an implementation-specific token minter to the
// implementation-agnostic contract below. Mint encodes the requested claims
// into a token the verifier accepts and returns the claims the contract
// should expect back: implementations model fields differently (the dev
// adapter derives OrgSlug from the org subject, Clerk strips its `org:` role
// prefix on the wire).
type VerifierHarness struct {
	Verifier identity.Verifier
	Mint     func(t *testing.T, c identity.ProviderClaims) (token string, want identity.ProviderClaims)
	// MintExpired is nil for implementations with no expiry concept.
	MintExpired func(t *testing.T) string
	// MintWrongKey is nil for implementations with no signing-key concept.
	MintWrongKey func(t *testing.T) string
	// ExtraInvalidTokens are implementation-specific rejections on top of the
	// universal ones every adapter must refuse.
	ExtraInvalidTokens []string
}

// RunVerifier is the Verifier contract: every implementation must satisfy it.
// It asserts behavior only — valid tokens round-trip their claims, everything
// else errors with nil claims.
func RunVerifier(t *testing.T, harness func(t *testing.T) VerifierHarness) {
	t.Helper()
	h := harness(t)
	require.NotNil(t, h.Verifier, "harness must supply a verifier")
	require.NotNil(t, h.Mint, "harness must supply a minter")
	ctx := context.Background()

	t.Run("valid token with org round-trips claims", func(t *testing.T) {
		token, want := h.Mint(t, identity.ProviderClaims{
			UserSubject: "user_contract",
			OrgSubject:  "org_contract",
			OrgRole:     "org:admin",
			OrgSlug:     "contract-org",
		})
		got, err := h.Verifier.Verify(ctx, token)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, want, *got)
	})

	t.Run("valid token without org round-trips claims", func(t *testing.T) {
		token, want := h.Mint(t, identity.ProviderClaims{UserSubject: "user_noorg"})
		got, err := h.Verifier.Verify(ctx, token)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, want, *got)
	})

	t.Run("invalid tokens error", func(t *testing.T) {
		for _, tok := range append([]string{"", "not-a-token", "a.b.c"}, h.ExtraInvalidTokens...) {
			got, err := h.Verifier.Verify(ctx, tok)
			assert.Error(t, err, "token %q", tok)
			assert.Nil(t, got, "token %q", tok)
		}
	})

	if h.MintExpired != nil {
		t.Run("expired token errors", func(t *testing.T) {
			got, err := h.Verifier.Verify(ctx, h.MintExpired(t))
			assert.Error(t, err)
			assert.Nil(t, got)
		})
	}

	if h.MintWrongKey != nil {
		t.Run("token signed by unknown key errors", func(t *testing.T) {
			got, err := h.Verifier.Verify(ctx, h.MintWrongKey(t))
			assert.Error(t, err)
			assert.Nil(t, got)
		})
	}
}

// WebhookHarness adapts one adapter's wire format to the neutral webhook
// contract. Deliver encodes the requested neutral event as this adapter's own
// signed delivery and returns the event the contract expects back.
type WebhookHarness struct {
	Webhook identity.Webhook
	Deliver func(t *testing.T, want identity.Event) (payload []byte, headers http.Header)
	// Tamper returns a delivery the adapter must refuse. It is nil for
	// adapters that perform no signature verification (the dev simulator).
	Tamper func(t *testing.T) (payload []byte, headers http.Header)
}

// WebhookEvents is the neutral event vocabulary the receiver dispatches on.
// Every adapter must be able to deliver all of it.
func WebhookEvents() []identity.Event {
	return []identity.Event{
		{ID: "msg_user_created", Type: "user.created", User: &identity.UserEvent{
			Subject: "user_contract", Email: "contract@example.com", Name: "Ada Lovelace", AvatarURL: "https://img.example.com/a.png",
		}},
		{ID: "msg_user_deleted", Type: "user.deleted", User: &identity.UserEvent{Subject: "user_contract"}},
		{ID: "msg_org_created", Type: "organization.created", Organization: &identity.OrganizationEvent{
			Subject: "org_contract", Name: "Contract Org", Slug: "contract-org", ImageURL: "https://img.example.com/o.png",
		}},
		{ID: "msg_membership_created", Type: "organizationMembership.created", Membership: &identity.MembershipEvent{
			OrganizationSubject: "org_contract", UserSubject: "user_contract", Role: "org:admin",
		}},
	}
}

// RunWebhook is the Webhook contract: an adapter must round-trip every event
// in the neutral vocabulary, stamp its own provider name, and refuse a
// delivery it cannot authenticate.
func RunWebhook(t *testing.T, provider string, harness func(t *testing.T) WebhookHarness) {
	t.Helper()
	h := harness(t)
	require.NotNil(t, h.Webhook, "harness must supply a webhook")
	require.NotNil(t, h.Deliver, "harness must supply a delivery encoder")
	ctx := context.Background()

	for _, want := range WebhookEvents() {
		t.Run(want.Type, func(t *testing.T) {
			payload, headers := h.Deliver(t, want)
			got, err := h.Webhook.Verify(ctx, payload, headers)
			require.NoError(t, err)
			want.Provider = provider
			assert.Equal(t, want, got)
		})
	}

	t.Run("malformed payload errors", func(t *testing.T) {
		_, headers := h.Deliver(t, WebhookEvents()[0])
		_, err := h.Webhook.Verify(ctx, []byte("{"), headers)
		assert.Error(t, err)
	})

	if h.Tamper != nil {
		t.Run("unauthenticated delivery errors", func(t *testing.T) {
			payload, headers := h.Tamper(t)
			_, err := h.Webhook.Verify(ctx, payload, headers)
			assert.Error(t, err)
		})
	}
}

// RunNavigator is the Navigator contract: every URL is absolute against the
// adapter's configured base and a return target survives login and signup.
func RunNavigator(t *testing.T, n identity.Navigator, baseURL string) {
	t.Helper()
	const returnTo = "https://app.example.com/?after-auth=1"
	for name, got := range map[string]string{
		"LoginURL":   n.LoginURL(returnTo),
		"SignupURL":  n.SignupURL(returnTo),
		"AccountURL": n.AccountURL(),
	} {
		assert.True(t, len(got) >= len(baseURL) && got[:len(baseURL)] == baseURL,
			"%s must be absolute against %q, got %q", name, baseURL, got)
	}
	assert.Contains(t, n.LoginURL(returnTo), returnTo, "LoginURL must carry the return target")
	assert.Contains(t, n.SignupURL(returnTo), returnTo, "SignupURL must carry the return target")
}
