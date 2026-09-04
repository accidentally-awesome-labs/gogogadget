package identitydev

import (
	"context"
	"testing"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/stretchr/testify/assert"
)

// FuzzVerifier exercises the e2e token parser with arbitrary input.
// Invariants: Verify never panics; failures are always ErrInvalidToken; on
// success the returned claims round-trip exactly the token parts (and OrgSlug
// mirrors the org subject, per the dev adapter contract).
func FuzzVerifier(f *testing.F) {
	// Seeds from TestVerifierParsesE2ETokens (valid + rejection cases).
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

	v := Verifier{}
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, token string) {
		claims, err := v.Verify(ctx, token)
		if err != nil {
			assert.ErrorIs(t, err, identity.ErrInvalidToken, "token %q", token)
			return
		}
		// Success: claims must round-trip the token parts exactly.
		assert.Equal(t, "e2e:"+claims.UserSubject+":"+claims.OrgSubject+":"+claims.OrgRole, token,
			"claims do not round-trip token %q", token)
		assert.NotEmpty(t, claims.UserSubject, "empty userID accepted for token %q", token)
		assert.Equal(t, claims.OrgSubject, claims.OrgSlug, "OrgSlug must mirror OrgID for token %q", token)
	})
}
