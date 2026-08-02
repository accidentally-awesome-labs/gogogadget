package identity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFakeVerifierParsesE2ETokens(t *testing.T) {
	v := FakeVerifier{}
	ctx := context.Background()

	claims, err := v.Verify(ctx, "e2e:user_free:org_free:org:member")
	require.NoError(t, err)
	assert.Equal(t, "user_free", claims.UserID)
	assert.Equal(t, "org_free", claims.OrgID)
	assert.Equal(t, "org:member", claims.OrgRole)

	// Empty org = no active organization.
	claims, err = v.Verify(ctx, "e2e:user_noorg::")
	require.NoError(t, err)
	assert.Equal(t, "user_noorg", claims.UserID)
	assert.Equal(t, "", claims.OrgID)
	assert.Equal(t, "", claims.OrgRole)

	// Rejections.
	for _, tok := range []string{"", "nope", "e2e:", "e2e::org:r", "basic:user:org:role", "e2e:u:o"} {
		_, err := v.Verify(ctx, tok)
		assert.ErrorIs(t, err, ErrInvalidToken, "token %q", tok)
	}
}

func TestDisplayName(t *testing.T) {
	first, last := "Ada", "Lovelace"
	assert.Equal(t, "Ada Lovelace", DisplayName(&first, &last, "ada@example.com"))
	assert.Equal(t, "Ada", DisplayName(&first, nil, "ada@example.com"))
	assert.Equal(t, "ada", DisplayName(nil, nil, "ada@example.com"))
}
