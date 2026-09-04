package identitydev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/identity"
	identitycontract "github.com/gogogadget/gogogadget/internal/identity/contract"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifierContract runs the shared identity contract against the dev
// adapter. The hosted adapters run the identical table.
func TestVerifierContract(t *testing.T) {
	identitycontract.RunVerifier(t, func(t *testing.T) identitycontract.VerifierHarness {
		t.Helper()
		return identitycontract.VerifierHarness{
			Verifier: Verifier{},
			Mint: func(t *testing.T, c identity.ProviderClaims) (string, identity.ProviderClaims) {
				t.Helper()
				token := fmt.Sprintf("e2e:%s:%s:%s", c.UserSubject, c.OrgSubject, c.OrgRole)
				// The dev adapter models OrgSlug as the org subject.
				return token, identity.ProviderClaims{
					Provider:    Provider,
					UserSubject: c.UserSubject,
					OrgSubject:  c.OrgSubject,
					OrgRole:     c.OrgRole,
					OrgSlug:     c.OrgSubject,
				}
			},
			ExtraInvalidTokens: []string{"e2e:", "e2e::org:r", "basic:user:org:role", "e2e:u:o"},
		}
	})
}

// TestWebhookContract proves the dev envelope round-trips the whole neutral
// event vocabulary the receiver dispatches on.
func TestWebhookContract(t *testing.T) {
	identitycontract.RunWebhook(t, Provider, func(t *testing.T) identitycontract.WebhookHarness {
		t.Helper()
		return identitycontract.WebhookHarness{
			Webhook: Webhook{},
			Deliver: func(t *testing.T, want identity.Event) ([]byte, http.Header) {
				t.Helper()
				return delivery(t, want), headers(want.ID)
			},
		}
	})
}

func TestNavigatorContract(t *testing.T) {
	identitycontract.RunNavigator(t, Navigator{BaseURL: "http://localhost:18080"}, "http://localhost:18080")
}

func TestVerifierParsesE2ETokens(t *testing.T) {
	v := Verifier{}
	ctx := context.Background()

	claims, err := v.Verify(ctx, "e2e:user_free:org_free:org:member")
	require.NoError(t, err)
	assert.Equal(t, "user_free", claims.UserSubject)
	assert.Equal(t, "org_free", claims.OrgSubject)
	assert.Equal(t, "org:member", claims.OrgRole)

	// Empty org = no active organization.
	claims, err = v.Verify(ctx, "e2e:user_noorg::")
	require.NoError(t, err)
	assert.Equal(t, "user_noorg", claims.UserSubject)
	assert.Equal(t, "", claims.OrgSubject)
	assert.Equal(t, "", claims.OrgRole)

	// Rejections.
	for _, tok := range []string{"", "nope", "e2e:", "e2e::org:r", "basic:user:org:role", "e2e:u:o"} {
		_, err := v.Verify(ctx, tok)
		assert.ErrorIs(t, err, identity.ErrInvalidToken, "token %q", tok)
	}
}

func TestFetcherRefusesForeignSubject(t *testing.T) {
	_, err := UserFetcher{}.Fetch(context.Background(), "sub_not_ours")
	require.Error(t, err)
	profile, err := UserFetcher{}.Fetch(context.Background(), "user_demo")
	require.NoError(t, err)
	assert.Equal(t, "user_demo@gogogadget.dev", profile.Email)
}

func TestWebhookRefusesIncompletePayloads(t *testing.T) {
	for name, payload := range map[string]string{
		"no type":          `{"data":{"id":"user_x"}}`,
		"user no id":       `{"type":"user.created","data":{}}`,
		"org no id":        `{"type":"organization.created","data":{}}`,
		"membership no id": `{"type":"organizationMembership.created","data":{"role":"org:admin"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Webhook{}.Verify(context.Background(), []byte(payload), headers("msg_x"))
			require.Error(t, err)
		})
	}
}

func TestModuleRequiresConfig(t *testing.T) {
	_, err := NewModule(context.Background(), nil, Deps{})
	require.Error(t, err)
}

// headers builds the delivery headers this adapter reads.
func headers(msgID string) http.Header {
	h := http.Header{}
	h.Set(messageIDHeader, msgID)
	return h
}

// delivery encodes a neutral event as this adapter's wire envelope.
func delivery(t *testing.T, evt identity.Event) []byte {
	t.Helper()
	data := map[string]string{}
	switch {
	case evt.User != nil:
		data["id"] = evt.User.Subject
		data["email"] = evt.User.Email
		data["name"] = evt.User.Name
		data["avatar_url"] = evt.User.AvatarURL
	case evt.Organization != nil:
		data["id"] = evt.Organization.Subject
		data["name"] = evt.Organization.Name
		data["slug"] = evt.Organization.Slug
		data["image_url"] = evt.Organization.ImageURL
	case evt.Membership != nil:
		data["organization_id"] = evt.Membership.OrganizationSubject
		data["user_id"] = evt.Membership.UserSubject
		data["role"] = evt.Membership.Role
	}
	out, err := json.Marshal(map[string]any{"type": evt.Type, "data": data})
	require.NoError(t, err)
	return out
}
