package clerk

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	svix "github.com/svix/svix-webhooks/go"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/identity"
	identitycontract "github.com/gogogadget/gogogadget/internal/identity/contract"
	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifierContract runs the shared identity contract against the Clerk
// verifier with a locally generated RSA key and an httptest JWKS server (the
// SDK's BackendConfig.URL makes the JWKS endpoint injectable). The dev
// adapter runs the identical table.
func TestVerifierContract(t *testing.T) {
	identitycontract.RunVerifier(t, clerkVerifierHarness)
}

func clerkVerifierHarness(t *testing.T) identitycontract.VerifierHarness {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	const kid = "contract-kid"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/jwks", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"keys":[{"use":"sig","kty":"RSA","kid":%q,"alg":"RS256","n":%q,"e":%q}]}`,
			kid,
			base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		)
	}))
	t.Cleanup(ts.Close)

	secret := "sk_test_contract"
	v := &Verifier{jwksClient: jwks.NewClient(&clerk.ClientConfig{
		BackendConfig: clerk.BackendConfig{Key: &secret, URL: &ts.URL, HTTPClient: ts.Client()},
	})}

	sign := func(t *testing.T, signingKey *rsa.PrivateKey, claims map[string]any) string {
		t.Helper()
		header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
		require.NoError(t, err)
		payload, err := json.Marshal(claims)
		require.NoError(t, err)
		input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
		digest := sha256.Sum256([]byte(input))
		sig, err := rsa.SignPKCS1v15(rand.Reader, signingKey, crypto.SHA256, digest[:])
		require.NoError(t, err)
		return input + "." + base64.RawURLEncoding.EncodeToString(sig)
	}

	sessionClaims := func(c identity.ProviderClaims, expiresAt time.Time) map[string]any {
		now := time.Now()
		m := map[string]any{
			// Non-satellite verification requires a clerk issuer.
			"iss": "https://clerk.contract.test",
			"sub": c.UserSubject,
			"iat": now.Add(-time.Minute).Unix(),
			"nbf": now.Add(-time.Minute).Unix(),
			"exp": expiresAt.Unix(),
		}
		if c.OrgSubject != "" {
			// v2 session claims carry the active organization.
			m["v"] = 2
			m["o"] = map[string]any{
				"id":  c.OrgSubject,
				"slg": c.OrgSlug,
				"rol": strings.TrimPrefix(c.OrgRole, "org:"),
			}
		}
		return m
	}

	return identitycontract.VerifierHarness{
		Verifier: v,
		Mint: func(t *testing.T, c identity.ProviderClaims) (string, identity.ProviderClaims) {
			t.Helper()
			want := c
			want.Provider = Provider
			return sign(t, key, sessionClaims(c, time.Now().Add(time.Hour))), want
		},
		MintExpired: func(t *testing.T) string {
			t.Helper()
			// Beyond the verifier's 10s leeway.
			return sign(t, key, sessionClaims(identity.ProviderClaims{UserSubject: "user_expired"}, time.Now().Add(-time.Hour)))
		},
		MintWrongKey: func(t *testing.T) string {
			t.Helper()
			other, err := rsa.GenerateKey(rand.Reader, 2048)
			require.NoError(t, err)
			return sign(t, other, sessionClaims(identity.ProviderClaims{UserSubject: "user_forged"}, time.Now().Add(time.Hour)))
		},
	}
}

// contractWebhookSecret is a fixture secret in the format Clerk's dashboard
// hands out.
var contractWebhookSecret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("gogogadget-test-secret-32b!"))

// TestWebhookContract proves a signed Clerk delivery round-trips the whole
// neutral event vocabulary the receiver dispatches on, and that an
// unauthenticated delivery is refused.
func TestWebhookContract(t *testing.T) {
	identitycontract.RunWebhook(t, Provider, func(t *testing.T) identitycontract.WebhookHarness {
		t.Helper()
		return identitycontract.WebhookHarness{
			Webhook: Webhook{Secret: contractWebhookSecret},
			Deliver: func(t *testing.T, want identity.Event) ([]byte, http.Header) {
				t.Helper()
				payload := clerkPayload(t, want)
				return payload, signSvix(t, contractWebhookSecret, want.ID, payload)
			},
			Tamper: func(t *testing.T) ([]byte, http.Header) {
				t.Helper()
				payload := clerkPayload(t, identitycontract.WebhookEvents()[0])
				otherSecret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("a-completely-different-secret!!"))
				return payload, signSvix(t, otherSecret, "msg_tampered", payload)
			},
		}
	})
}

func TestNavigatorContract(t *testing.T) {
	identitycontract.RunNavigator(t, Navigator{BaseURL: "https://accounts.example.test"}, "https://accounts.example.test")
}

// TestWebhookRefusesWithoutSecret pins the unconfigured refusal: a hosted
// adapter with no webhook secret must never accept an unsigned delivery.
func TestWebhookRefusesWithoutSecret(t *testing.T) {
	payload := clerkPayload(t, identitycontract.WebhookEvents()[0])
	_, err := Webhook{}.Verify(context.Background(), payload, http.Header{"Svix-Id": []string{"msg_ns"}})
	require.Error(t, err)
}

// TestWebhookRefusesUnsignedDelivery pins that a configured adapter refuses a
// delivery that carries only the message id.
func TestWebhookRefusesUnsignedDelivery(t *testing.T) {
	payload := clerkPayload(t, identitycontract.WebhookEvents()[0])
	_, err := Webhook{Secret: contractWebhookSecret}.Verify(context.Background(), payload, http.Header{"Svix-Id": []string{"msg_ns"}})
	require.Error(t, err)
}

// TestWebhookIgnoresUnknownEventTypes pins that an event type outside the
// neutral vocabulary parses to a bare envelope rather than an error, so a new
// Clerk event never wedges the endpoint.
func TestWebhookIgnoresUnknownEventTypes(t *testing.T) {
	payload := []byte(`{"type":"session.created","data":{"id":"sess_x"}}`)
	evt, err := Webhook{Secret: contractWebhookSecret}.Verify(context.Background(), payload,
		signSvix(t, contractWebhookSecret, "msg_unknown", payload))
	require.NoError(t, err)
	assert.Equal(t, identity.Event{ID: "msg_unknown", Provider: Provider, Type: "session.created"}, evt)
}

// TestWebhookParsesClerkPrimaryEmailSelection pins the payload details the
// receiver never sees: the primary address wins, and a missing primary falls
// back to the first address.
func TestWebhookParsesClerkPrimaryEmailSelection(t *testing.T) {
	for name, tc := range map[string]struct {
		payload string
		email   string
		wantErr bool
	}{
		"primary wins": {payload: `{"type":"user.created","data":{"id":"user_p",
			"email_addresses":[{"id":"em_1","email_address":"first@example.com"},{"id":"em_2","email_address":"primary@example.com"}],
			"primary_email_address_id":"em_2"}}`, email: "primary@example.com"},
		"no primary falls back to first": {payload: `{"type":"user.created","data":{"id":"user_f",
			"email_addresses":[{"id":"em_1","email_address":"first@example.com"}]}}`, email: "first@example.com"},
		"missing id refuses": {payload: `{"type":"user.created","data":{}}`, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			evt, err := Webhook{Secret: contractWebhookSecret}.Verify(context.Background(), []byte(tc.payload),
				signSvix(t, contractWebhookSecret, "msg_"+name, []byte(tc.payload)))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, evt.User)
			assert.Equal(t, tc.email, evt.User.Email)
		})
	}
}

func TestDisplayName(t *testing.T) {
	first, last := "Ada", "Lovelace"
	assert.Equal(t, "Ada Lovelace", DisplayName(&first, &last, "ada@example.com"))
	assert.Equal(t, "Ada", DisplayName(&first, nil, "ada@example.com"))
	assert.Equal(t, "ada", DisplayName(nil, nil, "ada@example.com"))
}

func TestModuleRefusesMissingCredentials(t *testing.T) {
	_, err := NewModule(context.Background(), nil, Deps{})
	require.Error(t, err, "config is required")
	_, err = NewModule(context.Background(), nil, Deps{Config: &config.Config{}})
	require.Error(t, err, "CLERK_SECRET_KEY is required")
}

// clerkPayload encodes a neutral event in Clerk's own wire format.
func clerkPayload(t *testing.T, evt identity.Event) []byte {
	t.Helper()
	var data any
	switch {
	case evt.Type == "user.deleted":
		data = map[string]any{"id": evt.User.Subject}
	case evt.User != nil:
		data = map[string]any{
			"id":                       evt.User.Subject,
			"email_addresses":          []any{map[string]any{"id": "em_1", "email_address": evt.User.Email}},
			"primary_email_address_id": "em_1",
			"first_name":               firstName(evt.User.Name),
			"last_name":                lastName(evt.User.Name),
			"image_url":                evt.User.AvatarURL,
		}
	case evt.Organization != nil:
		data = map[string]any{
			"id": evt.Organization.Subject, "name": evt.Organization.Name,
			"slug": evt.Organization.Slug, "image_url": evt.Organization.ImageURL,
		}
	case evt.Membership != nil:
		data = map[string]any{
			"organization":     map[string]any{"id": evt.Membership.OrganizationSubject},
			"public_user_data": map[string]any{"user_id": evt.Membership.UserSubject},
			"role":             evt.Membership.Role,
		}
	}
	out, err := json.Marshal(map[string]any{"type": evt.Type, "data": data})
	require.NoError(t, err)
	return out
}

func firstName(name string) string {
	if i := strings.Index(name, " "); i > 0 {
		return name[:i]
	}
	return name
}

func lastName(name string) string {
	if i := strings.Index(name, " "); i > 0 {
		return name[i+1:]
	}
	return ""
}

// signSvix emits the exact headers a Clerk (Svix) delivery carries.
func signSvix(t *testing.T, secret, msgID string, payload []byte) http.Header {
	t.Helper()
	wh, err := svix.NewWebhook(secret)
	require.NoError(t, err)
	now := time.Now()
	sig, err := wh.Sign(msgID, now, payload)
	require.NoError(t, err)
	h := http.Header{}
	h.Set("svix-id", msgID)
	h.Set("svix-timestamp", fmt.Sprint(now.Unix()))
	h.Set("svix-signature", sig)
	return h
}

// TestFrontendAPIURLIsDerivedAtConfigLoad pins the half of this adapter's
// contract that is not a Go interface: CLERK_FRONTEND_API_URL is what CSP
// connect-src allows, and clerk-js cannot refresh the session JWT without it,
// so it must be resolved by the time internal/web reads it — not by the web
// module, and not by hand-written code in the config seam that would outlive
// removing this adapter.
//
// The read asserted here is Value, because that is the read a module which
// does not declare the key is allowed to make; the typed field is checked
// alongside it so the generated parse and the generated derivation cannot
// disagree.
func TestFrontendAPIURLIsDerivedAtConfigLoad(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "production derives from APP_URL",
			env: map[string]string{
				"APP_ENV": "production", "APP_URL": "https://app.example.com",
				"DATABASE_URL": "postgres://x", "NEON_API_KEY": "x", "RESEND_API_KEY": "x",
				"STORAGE_R2_ACCESS_KEY_ID": "x", "STORAGE_R2_ACCOUNT_ID": "x",
				"STORAGE_R2_BUCKET": "x", "STORAGE_R2_SECRET_ACCESS_KEY": "x",
				"CLERK_PORTAL_URL": "https://accounts.example.com", "CLERK_PUBLISHABLE_KEY": "pk",
				"CLERK_SECRET_KEY": "sk", "CLERK_WEBHOOK_SECRET": "whsec",
			},
			want: "https://clerk.app.example.com",
		},
		{
			name: "production normalises a dashboard-copied APP_URL",
			env: map[string]string{
				// A trailing slash and mixed case are what an operator
				// actually pastes. Both used to survive into the CSP source,
				// where the grammar refuses a path and a host is
				// case-insensitive anyway — and a refused source is dropped,
				// which blocks the ~60s __session refresh in production only.
				"APP_ENV": "production", "APP_URL": "https://App.Example.com/",
				"DATABASE_URL": "postgres://x", "NEON_API_KEY": "x", "RESEND_API_KEY": "x",
				"STORAGE_R2_ACCESS_KEY_ID": "x", "STORAGE_R2_ACCOUNT_ID": "x",
				"STORAGE_R2_BUCKET": "x", "STORAGE_R2_SECRET_ACCESS_KEY": "x",
				"CLERK_PORTAL_URL": "https://accounts.example.com", "CLERK_PUBLISHABLE_KEY": "pk",
				"CLERK_SECRET_KEY": "sk", "CLERK_WEBHOOK_SECRET": "whsec",
			},
			want: "https://clerk.app.example.com",
		},
		{
			name: "development uses the shared wildcard host",
			env:  map[string]string{"APP_ENV": "development", "APP_URL": "http://localhost:8080"},
			want: "https://*.clerk.accounts.dev",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.LoadFrom(func(key string) string { return tc.env[key] })
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Value("CLERK_FRONTEND_API_URL"))
			assert.Equal(t, tc.want, cfg.ClerkFrontendAPIURL)
			// This value IS a CSP source, so asserting the string is only half
			// the contract: it has to satisfy the grammar that decides whether
			// it reaches the header at all. Asserting two happy-path strings
			// is what let a trailing slash through.
			assert.NoError(t, modkit.ValidateCSPSource(cfg.Value("CLERK_FRONTEND_API_URL")),
				"the derived frontend API origin must be a contributable CSP source")
		})
	}

	// An explicit value is never overwritten: the derivation only fills.
	cfg, err := config.LoadFrom(func(key string) string {
		return map[string]string{
			"APP_ENV": "development", "APP_URL": "http://localhost:8080",
			"CLERK_FRONTEND_API_URL": "https://clerk.chosen.test",
		}[key]
	})
	require.NoError(t, err)
	assert.Equal(t, "https://clerk.chosen.test", cfg.Value("CLERK_FRONTEND_API_URL"))

	// An explicit value bypasses the derivation entirely, so the declaration
	// carries trim_slash for the same reason CLERK_PORTAL_URL does.
	cfg, err = config.LoadFrom(func(key string) string {
		return map[string]string{
			"APP_ENV": "development", "APP_URL": "http://localhost:8080",
			"CLERK_FRONTEND_API_URL": "https://clerk.chosen.test/",
		}[key]
	})
	require.NoError(t, err)
	assert.Equal(t, "https://clerk.chosen.test", cfg.Value("CLERK_FRONTEND_API_URL"))
	assert.NoError(t, modkit.ValidateCSPSource(cfg.Value("CLERK_FRONTEND_API_URL")))
}
