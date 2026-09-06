package identitydev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/config"
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
	identitycontract.RunNavigator(t,
		Navigator{BaseURL: "http://localhost:18080", Bypass: true}, "http://localhost:18080")
}

// The destinations this adapter serves, and the ones it refuses, spelled out
// where a reader can see the whole map at once. Every refusal is
// identity.ErrNoDestination with an empty URL: the old implementation
// answered `<app>/login`, `<app>/signup` and `<app>/account`, of which the
// first two redirect straight back into the handler that asked and the third
// is a route this application has never served.
func TestNavigatorDestinations(t *testing.T) {
	n := Navigator{BaseURL: "http://localhost:18080", Bypass: true}

	for name, got := range map[string]func(string) (string, error){
		"LoginURL":  n.LoginURL,
		"SignupURL": n.SignupURL,
	} {
		url, err := got("http://localhost:18080/app?x=a b")
		require.NoError(t, err, name)
		assert.Equal(t, "http://localhost:18080/dev/login?return_to=http%3A%2F%2Flocalhost%3A18080%2Fapp%3Fx%3Da+b",
			url, name)
	}

	logout, err := n.LogoutURL("http://localhost:18080/")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:18080/", logout)

	for name, refuses := range map[string]func(string) (string, error){
		"AccountURL":            n.AccountURL,
		"OrganizationURL":       n.OrganizationURL,
		"CreateOrganizationURL": n.CreateOrganizationURL,
	} {
		url, err := refuses("http://localhost:18080/app")
		assert.ErrorIs(t, err, identity.ErrNoDestination, name)
		assert.Empty(t, url, name)
	}
}

// With the bypass off, /dev/login is not registered, so this adapter has no
// sign-in surface and says so rather than naming a route that 404s.
func TestNavigatorRefusesSignInWithoutTheBypass(t *testing.T) {
	n := Navigator{BaseURL: "http://localhost:18080"}
	for _, destination := range []func(string) (string, error){n.LoginURL, n.SignupURL} {
		url, err := destination("http://localhost:18080/app")
		assert.ErrorIs(t, err, identity.ErrNoDestination)
		assert.Empty(t, url)
	}
}

// The module wires the bypass through from configuration, so the adapter's
// answer tracks the key that gates its route rather than a second copy of it.
func TestModuleNavigatorTracksTheBypass(t *testing.T) {
	m, err := NewModule(context.Background(), nil,
		Deps{Config: &config.Config{AppURL: "http://localhost:18080", DevAuthBypass: true}})
	require.NoError(t, err)
	url, err := m.Navigator.LoginURL("")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:18080/dev/login", url)

	m, err = NewModule(context.Background(), nil,
		Deps{Config: &config.Config{AppURL: "http://localhost:18080"}})
	require.NoError(t, err)
	_, err = m.Navigator.LoginURL("")
	assert.ErrorIs(t, err, identity.ErrNoDestination)
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
