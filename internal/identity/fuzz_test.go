package identity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// FuzzFakeVerifier exercises the e2e token parser with arbitrary input.
// Invariants: Verify never panics; failures are always ErrInvalidToken; on
// success the returned claims round-trip exactly the token parts (and OrgSlug
// mirrors OrgID, per the FakeVerifier contract).
func FuzzFakeVerifier(f *testing.F) {
	// Seeds from TestFakeVerifierParsesE2ETokens (valid + rejection cases).
	for _, tok := range []string{
		"e2e:user_free:org_free:org:member",
		"e2e:user_noorg::",
		"e2e:u:o:org:admin",
		"",
		"nope",
		"e2e:",
		"e2e::org:r",
		"basic:user:org:role",
		"e2e:u:o",
		"e2e:u:o:r:extra:colons",
		"e2e:\x00:nul:\x00",
		"e2e:ünïcode:org:member",
	} {
		f.Add(tok)
	}

	v := FakeVerifier{}
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, token string) {
		claims, err := v.Verify(ctx, token)
		if err != nil {
			assert.ErrorIs(t, err, ErrInvalidToken, "token %q", token)
			return
		}
		// Success: claims must round-trip the token parts exactly.
		assert.Equal(t, "e2e:"+claims.UserID+":"+claims.OrgID+":"+claims.OrgRole, token,
			"claims do not round-trip token %q", token)
		assert.NotEmpty(t, claims.UserID, "empty userID accepted for token %q", token)
		assert.Equal(t, claims.OrgID, claims.OrgSlug, "OrgSlug must mirror OrgID for token %q", token)
	})
}
