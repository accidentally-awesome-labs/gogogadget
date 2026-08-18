package identity

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verifierHarness adapts an impl-specific token minter to the
// impl-agnostic contract below. mint encodes the requested Claims into a
// token the verifier accepts and returns the Claims the contract should
// expect back (impls model fields differently: FakeVerifier derives
// OrgSlug from OrgID, Clerk prefixes org roles).
type verifierHarness struct {
	verifier Verifier
	mint     func(t *testing.T, c Claims) (token string, want Claims)
	// mintExpired is nil for impls with no expiry concept.
	mintExpired func(t *testing.T) string
	// mintWrongKey is nil for impls with no signing-key concept.
	mintWrongKey func(t *testing.T) string
}

// runVerifierContract is the Verifier seam contract: every implementation
// must satisfy it. It asserts behavior only — valid tokens round-trip
// their claims, everything else errors with nil claims.
func runVerifierContract(t *testing.T, harness func(t *testing.T) verifierHarness) {
	t.Helper()
	h := harness(t)
	ctx := context.Background()

	t.Run("valid token with org round-trips claims", func(t *testing.T) {
		token, want := h.mint(t, Claims{
			UserID:  "user_contract",
			OrgID:   "org_contract",
			OrgRole: "org:admin",
			OrgSlug: "contract-org",
		})
		got, err := h.verifier.Verify(ctx, token)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, want, *got)
	})

	t.Run("valid token without org round-trips claims", func(t *testing.T) {
		token, want := h.mint(t, Claims{UserID: "user_noorg"})
		got, err := h.verifier.Verify(ctx, token)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, want, *got)
	})

	t.Run("invalid tokens error", func(t *testing.T) {
		for _, tok := range []string{"", "not-a-token", "a:b:c:d:e", "e2e:"} {
			got, err := h.verifier.Verify(ctx, tok)
			assert.Error(t, err, "token %q", tok)
			assert.Nil(t, got, "token %q", tok)
		}
	})

	if h.mintExpired != nil {
		t.Run("expired token errors", func(t *testing.T) {
			got, err := h.verifier.Verify(ctx, h.mintExpired(t))
			assert.Error(t, err)
			assert.Nil(t, got)
		})
	}

	if h.mintWrongKey != nil {
		t.Run("token signed by unknown key errors", func(t *testing.T) {
			got, err := h.verifier.Verify(ctx, h.mintWrongKey(t))
			assert.Error(t, err)
			assert.Nil(t, got)
		})
	}
}

// fakeVerifierHarness runs the contract against FakeVerifier.
func fakeVerifierHarness(t *testing.T) verifierHarness {
	t.Helper()
	return verifierHarness{
		verifier: FakeVerifier{},
		mint: func(t *testing.T, c Claims) (string, Claims) {
			t.Helper()
			token := fmt.Sprintf("e2e:%s:%s:%s", c.UserID, c.OrgID, c.OrgRole)
			// FakeVerifier models OrgSlug as the org ID.
			return token, Claims{UserID: c.UserID, OrgID: c.OrgID, OrgRole: c.OrgRole, OrgSlug: c.OrgID}
		},
	}
}

// clerkVerifierHarness runs the contract against ClerkVerifier with a
// locally generated RSA key and an httptest JWKS server (the SDK's
// BackendConfig.URL makes the JWKS endpoint injectable).
func clerkVerifierHarness(t *testing.T) verifierHarness {
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
	v := &ClerkVerifier{jwksClient: jwks.NewClient(&clerk.ClientConfig{
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

	clerkClaims := func(c Claims, expiresAt time.Time) map[string]any {
		now := time.Now()
		m := map[string]any{
			// Non-satellite verification requires a clerk issuer.
			"iss": "https://clerk.contract.test",
			"sub": c.UserID,
			"iat": now.Add(-time.Minute).Unix(),
			"nbf": now.Add(-time.Minute).Unix(),
			"exp": expiresAt.Unix(),
		}
		if c.OrgID != "" {
			// v2 session claims carry the active organization.
			m["v"] = 2
			m["o"] = map[string]any{
				"id":  c.OrgID,
				"slg": c.OrgSlug,
				"rol": strings.TrimPrefix(c.OrgRole, "org:"),
			}
		}
		return m
	}

	return verifierHarness{
		verifier: v,
		mint: func(t *testing.T, c Claims) (string, Claims) {
			t.Helper()
			return sign(t, key, clerkClaims(c, time.Now().Add(time.Hour))), c
		},
		mintExpired: func(t *testing.T) string {
			t.Helper()
			// Beyond the verifier's 10s leeway.
			return sign(t, key, clerkClaims(Claims{UserID: "user_expired"}, time.Now().Add(-time.Hour)))
		},
		mintWrongKey: func(t *testing.T) string {
			t.Helper()
			other, err := rsa.GenerateKey(rand.Reader, 2048)
			require.NoError(t, err)
			return sign(t, other, clerkClaims(Claims{UserID: "user_forged"}, time.Now().Add(time.Hour)))
		},
	}
}
