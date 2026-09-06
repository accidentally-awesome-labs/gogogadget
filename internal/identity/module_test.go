package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewModuleRequiresEveryCapability pins the seam's only behavior: it never
// selects or substitutes a provider, so a missing capability is a refusal
// rather than a silent no-op implementation.
//
// The capability set is the seam's own doubles (mock.go). This file used to
// define five private stubs for it, while every consuming module's test
// payload reached for an ADAPTER instead — the doubles are exported so that
// stops being the easier path.
func TestNewModuleRequiresEveryCapability(t *testing.T) {
	full := Deps{
		Verifier:  MockVerifier{},
		Fetcher:   MockUserFetcher{},
		Deleter:   &MockDeleter{},
		Navigator: MockNavigator{BaseURL: "http://localhost:18080"},
		Webhook:   MockWebhook{},
	}
	if _, err := NewModule(context.Background(), full); err != nil {
		t.Fatalf("complete capability set refused: %v", err)
	}
	for name, drop := range map[string]func(*Deps){
		"verifier":  func(d *Deps) { d.Verifier = nil },
		"fetcher":   func(d *Deps) { d.Fetcher = nil },
		"deleter":   func(d *Deps) { d.Deleter = nil },
		"navigator": func(d *Deps) { d.Navigator = nil },
		"webhook":   func(d *Deps) { d.Webhook = nil },
	} {
		t.Run("missing "+name, func(t *testing.T) {
			d := full
			drop(&d)
			if _, err := NewModule(context.Background(), d); err == nil {
				t.Fatalf("missing %s accepted", name)
			}
		})
	}
}

// The double verifies exactly what it minted, and the token grammar is
// written in one place: a consumer asks MintSession, the same
// SyntheticSessionMinter port the zero-account dev surface asks.
func TestMockVerifierRoundTripsItsOwnSessions(t *testing.T) {
	ctx := context.Background()
	v := MockVerifier{}

	token, err := v.MintSession("user_demo", "org_demo", "org:admin")
	require.NoError(t, err)
	claims, err := v.Verify(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, MockProvider, claims.Provider)
	assert.Equal(t, "user_demo", claims.UserSubject)
	assert.Equal(t, "org_demo", claims.OrgSubject)
	assert.Equal(t, "org:admin", claims.OrgRole, "the role is the remainder, so it may contain a colon")
	assert.Equal(t, "org_demo", claims.OrgSlug)

	// No active organization.
	token, err = v.MintSession("user_noorg", "", "")
	require.NoError(t, err)
	claims, err = v.Verify(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "user_noorg", claims.UserSubject)
	assert.Empty(t, claims.OrgSubject)

	// Refusals: a subject may not carry the separator, and nothing this
	// double did not mint verifies.
	_, err = v.MintSession("", "org_demo", "org:admin")
	assert.ErrorIs(t, err, ErrInvalidToken)
	_, err = v.MintSession("user:evil", "org_demo", "org:admin")
	assert.ErrorIs(t, err, ErrInvalidToken)
	for _, token := range []string{"", "nope", "mock:", "mock::org:r", "e2e:user_demo:org_demo:org:admin", "mock:u:o"} {
		_, err := v.Verify(ctx, token)
		assert.ErrorIs(t, err, ErrInvalidToken, "token %q", token)
	}

	// The injectable refusal, for a consumer driving the invalid-session path.
	sentinel := errors.New("provider unreachable")
	_, err = MockVerifier{Err: sentinel}.Verify(ctx, token)
	assert.ErrorIs(t, err, sentinel)
}

// A hosted adapter cannot mint, and that has to be a distinct type: whether
// MintSession exists is a method-set property, so no field could turn it off
// and a consumer's type assertion would still succeed.
func TestMockHostedVerifierOffersNoSyntheticSessions(t *testing.T) {
	var hosted Verifier = MockHostedVerifier{}
	_, minting := hosted.(SyntheticSessionMinter)
	assert.False(t, minting)

	_, err := hosted.Verify(context.Background(), "anything")
	assert.ErrorIs(t, err, ErrInvalidToken)

	answering := MockHostedVerifier{Claims: &ProviderClaims{Provider: "hosted", UserSubject: "sub_1"}}
	claims, err := answering.Verify(context.Background(), "anything")
	require.NoError(t, err)
	assert.Equal(t, "sub_1", claims.UserSubject)
}

// The navigator's two defaults are load-bearing for consumers: the zero
// value refuses everything, and a configured base still refuses the three
// self-service destinations the way every zero-account adapter does.
func TestMockNavigatorDefaultsRefuse(t *testing.T) {
	empty := MockNavigator{}
	for name, destination := range map[string]func(string) (string, error){
		"login": empty.LoginURL, "signup": empty.SignupURL, "logout": empty.LogoutURL,
		"account": empty.AccountURL, "organization": empty.OrganizationURL,
		"create-organization": empty.CreateOrganizationURL,
	} {
		url, err := destination("http://localhost:18080/app")
		assert.ErrorIs(t, err, ErrNoDestination, name)
		assert.Empty(t, url, name)
	}

	local := MockNavigator{BaseURL: "http://localhost:18080/"}
	url, err := local.LoginURL("http://localhost:18080/?after-auth=1")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:18080/mock/login?return_to=http%3A%2F%2Flocalhost%3A18080%2F%3Fafter-auth%3D1", url)
	url, err = local.LoginURL("")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:18080/mock/login", url, "no return target, no parameter")
	for name, destination := range map[string]func(string) (string, error){
		"account": local.AccountURL, "organization": local.OrganizationURL,
		"create-organization": local.CreateOrganizationURL,
	} {
		_, err := destination("")
		assert.ErrorIs(t, err, ErrNoDestination, name)
	}

	portal := MockNavigator{BaseURL: "https://accounts.example.test", SelfService: true}
	url, err = portal.AccountURL("")
	require.NoError(t, err)
	assert.Equal(t, "https://accounts.example.test/mock/account", url)
}

// The webhook double owns both directions, so a consumer's fixture is a
// typed event rather than an adapter's JSON.
func TestMockWebhookRoundTripsNeutralEvents(t *testing.T) {
	ctx := context.Background()
	for name, event := range map[string]Event{
		"user": {Type: "user.created", User: &UserEvent{Subject: "user_1", Email: "u@example.test", Name: "U"}},
		"organization": {Type: "organization.updated",
			Organization: &OrganizationEvent{Subject: "org_1", Name: "Org", Slug: "org"}},
		"membership": {Type: "organizationMembership.created",
			Membership: &MembershipEvent{OrganizationSubject: "org_1", UserSubject: "user_1", Role: "org:admin"}},
	} {
		t.Run(name, func(t *testing.T) {
			payload, headers, err := MockDelivery("msg_1", event)
			require.NoError(t, err)
			got, err := MockWebhook{}.Verify(ctx, payload, headers)
			require.NoError(t, err)
			assert.Equal(t, "msg_1", got.ID, "the delivery id is the receiver's idempotency key")
			assert.Equal(t, MockProvider, got.Provider)
			assert.Equal(t, event.Type, got.Type)
			assert.Equal(t, event.User, got.User)
			assert.Equal(t, event.Organization, got.Organization)
			assert.Equal(t, event.Membership, got.Membership)
		})
	}

	// An empty delivery id yields no header, which is how a consumer drives
	// the receiver's "no idempotency key" refusal.
	_, headers, err := MockDelivery("", Event{Type: "user.created", User: &UserEvent{Subject: "user_1"}})
	require.NoError(t, err)
	assert.Empty(t, headers.Get(MockDeliveryIDHeader))

	// Incomplete events refuse, the way every adapter does.
	for name, event := range map[string]Event{
		"no type":         {},
		"user no subject": {Type: "user.created"},
		"org no subject":  {Type: "organization.created"},
		"membership no user": {Type: "organizationMembership.created",
			Membership: &MembershipEvent{OrganizationSubject: "org_1"}},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			payload, headers, err := MockDelivery("msg_bad", event)
			require.NoError(t, err)
			_, err = MockWebhook{}.Verify(ctx, payload, headers)
			require.Error(t, err)
		})
	}
}

// The fetcher derives a profile so a consumer needs no upstream account, and
// the deleter records so a consumer can prove the account was reached.
func TestMockFetcherAndDeleter(t *testing.T) {
	ctx := context.Background()
	profile, err := MockUserFetcher{}.Fetch(ctx, "user_demo")
	require.NoError(t, err)
	assert.Equal(t, "user_demo@"+MockEmailDomain, profile.Email)
	assert.Equal(t, "user_demo", profile.Name)

	override := MockUserFetcher{Profiles: map[string]UserProfile{"user_demo": {Email: "real@example.test"}}}
	profile, err = override.Fetch(ctx, "user_demo")
	require.NoError(t, err)
	assert.Equal(t, "real@example.test", profile.Email)

	_, err = MockUserFetcher{}.Fetch(ctx, "")
	require.Error(t, err)

	deleter := &MockDeleter{}
	require.NoError(t, deleter.DeleteUser(ctx, "user_demo"))
	assert.Equal(t, []string{"user_demo"}, deleter.Deleted())

	sentinel := errors.New("upstream refused")
	failing := &MockDeleter{Err: sentinel}
	assert.ErrorIs(t, failing.DeleteUser(ctx, "user_demo"), sentinel)
	assert.Equal(t, []string{"user_demo"}, failing.Deleted(),
		"a call that failed still happened, and a consumer asserting the attempt needs to see it")
}
