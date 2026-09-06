package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// This file is the identity seam's own test double, following
// billing.MockClient, observability.NoopReporter, analytics.NoopCapturer,
// realtime.NewMemory and search.NewMemory: a seam ships the stand-in its
// consumers test against, so a payload never names an adapter package.
//
// Why it cannot be an adapter. `requires` names modules, but an adapter is a
// per-environment provider SELECTION. A test payload that constructs
// identitydev.Verifier{} compiles only while providers.identity.test is
// identity-dev, and that constraint is expressible nowhere in the manifest
// vocabulary — so the pin is not merely undeclared, it is undeclarable. These
// doubles leave with the seam every consumer already declares.

// MockProvider is the provider key every double here stamps on claims and
// events. It is deliberately not any adapter's key: the identity mapping
// tables store the provider verbatim, so a row a double wrote must never be
// mistaken for one a real adapter produced.
const MockProvider = "mock"

// mockSessionPrefix is this double's session-token grammar, and the ONLY
// place it is written. Callers mint through MockVerifier.MintSession — the
// same SyntheticSessionMinter port the zero-account dev surface uses — so no
// payload spells a token shape it does not own. Hand-writing one is the
// defect production already removed from internal/web/workflow_dev_session.go:
// a provider's token shape written into neutral code compiles against any
// adapter and produces a cookie nothing can verify.
const mockSessionPrefix = "mock:"

// MockVerifier verifies exactly the tokens it minted. The zero value works.
type MockVerifier struct {
	// Provider overrides the stamped provider key; empty means MockProvider.
	Provider string
	// Err, when non-nil, is what every verification returns, so a payload
	// can drive the refusal path without inventing a malformed token.
	Err error
}

func (v MockVerifier) provider() string {
	if v.Provider != "" {
		return v.Provider
	}
	return MockProvider
}

// MintSession writes the double's synthetic session token.
func (v MockVerifier) MintSession(userSubject, orgSubject, role string) (string, error) {
	if userSubject == "" {
		return "", fmt.Errorf("%w: a synthetic session needs a user subject", ErrInvalidToken)
	}
	// Verify splits on the first three separators, so the role is the
	// remainder and may legitimately contain one ("org:admin"); the two
	// subjects may not.
	if strings.ContainsAny(userSubject+orgSubject, ":") {
		return "", fmt.Errorf("%w: a synthetic session subject may not contain ':'", ErrInvalidToken)
	}
	return mockSessionPrefix + userSubject + ":" + orgSubject + ":" + role, nil
}

func (v MockVerifier) Verify(_ context.Context, token string) (*ProviderClaims, error) {
	if v.Err != nil {
		return nil, v.Err
	}
	parts := strings.SplitN(token, ":", 4)
	if len(parts) != 4 || parts[0]+":" != mockSessionPrefix || parts[1] == "" {
		return nil, fmt.Errorf("%w: not a token this double minted", ErrInvalidToken)
	}
	return &ProviderClaims{Provider: v.provider(), UserSubject: parts[1], OrgSubject: parts[2], OrgRole: parts[3], OrgSlug: parts[2]}, nil
}

func (v MockVerifier) VerifySubject(_ context.Context, subject string) (*ProviderClaims, error) {
	if v.Err != nil {
		return nil, v.Err
	}
	if subject == "" {
		return nil, ErrInvalidToken
	}
	return &ProviderClaims{Provider: v.provider(), UserSubject: subject}, nil
}

func (v MockVerifier) VerifyOrganizationSubject(_ context.Context, subject string) (*ProviderClaims, error) {
	if v.Err != nil {
		return nil, v.Err
	}
	if subject == "" {
		return nil, ErrInvalidToken
	}
	return &ProviderClaims{Provider: v.provider(), OrgSubject: subject}, nil
}

// MockHostedVerifier stands for any hosted adapter: it verifies the tokens it
// issued and knows nothing about synthetic sessions. It exists as a separate
// type because MintSession is a method-set property — a field could not turn
// it off — and a payload that needs "the selected adapter cannot mint" needs
// a value that genuinely lacks the method.
type MockHostedVerifier struct {
	// Claims, when non-nil, is what Verify returns for any token. Nil means
	// every token is refused, which is what a hosted adapter does with a
	// token it never issued.
	Claims *ProviderClaims
}

func (v MockHostedVerifier) Verify(context.Context, string) (*ProviderClaims, error) {
	if v.Claims == nil {
		return nil, ErrInvalidToken
	}
	claims := *v.Claims
	return &claims, nil
}

// MockUserFetcher derives a profile from the subject, so a payload needs no
// upstream account and no fixture table.
type MockUserFetcher struct {
	// Err, when non-nil, is returned instead of a profile.
	Err error
	// Profiles overrides the derivation for named subjects.
	Profiles map[string]UserProfile
}

// MockEmailDomain is the domain MockUserFetcher derives addresses under. It
// is a reserved-for-testing TLD, and it is deliberately not the dev
// adapter's: a payload that asserts on a derived address says whose
// derivation it means.
const MockEmailDomain = "mock.test"

func (f MockUserFetcher) Fetch(_ context.Context, userSubject string) (UserProfile, error) {
	if f.Err != nil {
		return UserProfile{}, f.Err
	}
	if userSubject == "" {
		return UserProfile{}, fmt.Errorf("mock fetcher: a user subject is required")
	}
	if profile, ok := f.Profiles[userSubject]; ok {
		return profile, nil
	}
	return UserProfile{Email: userSubject + "@" + MockEmailDomain, Name: userSubject}, nil
}

// MockDeleter records the subjects it was asked to delete, so a payload can
// assert the upstream account was really reached, and takes an injectable
// error for the failure path.
type MockDeleter struct {
	Err error

	mu      sync.Mutex
	deleted []string
}

func (d *MockDeleter) DeleteUser(_ context.Context, userSubject string) error {
	d.mu.Lock()
	d.deleted = append(d.deleted, userSubject)
	d.mu.Unlock()
	return d.Err
}

// Deleted returns the subjects passed to DeleteUser, in order.
func (d *MockDeleter) Deleted() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.deleted...)
}

// MockNavigator answers a destination with a path under BaseURL, or refuses.
//
// Two defaults are load-bearing, because "this adapter publishes no page for
// that" is a value callers must handle (see ErrNoDestination) and the double
// used to test that path must be as easy to build as the one that answers:
//
//   - The ZERO VALUE refuses EVERY destination. It stands for an adapter
//     with no surface at all — an unconfigured base, or a sign-in route that
//     is not registered.
//   - With a BaseURL, sign-in, sign-up and sign-out answer while the three
//     self-service destinations still refuse. That is the shape of every
//     zero-account adapter: the profile is derived and the organization
//     lives in this application's own tables, so there is no upstream page
//     to send a visitor to, and a link back to the page they are reading is
//     worse than none. SelfService: true makes those three answer too.
type MockNavigator struct {
	// BaseURL is the origin every answered destination hangs off. Empty
	// means this double publishes no pages at all and refuses.
	BaseURL string
	// SelfService makes the account, organization and org-creation pages
	// answer, the way a hosted portal does.
	SelfService bool
}

func (n MockNavigator) LoginURL(returnTo string) (string, error) {
	return n.destination("/mock/login", returnTo)
}
func (n MockNavigator) SignupURL(returnTo string) (string, error) {
	return n.destination("/mock/signup", returnTo)
}
func (n MockNavigator) LogoutURL(returnTo string) (string, error) {
	return n.destination("/mock/logout", returnTo)
}
func (n MockNavigator) AccountURL(returnTo string) (string, error) {
	if !n.SelfService {
		return "", fmt.Errorf("%w: this double manages no account page", ErrNoDestination)
	}
	return n.destination("/mock/account", returnTo)
}
func (n MockNavigator) OrganizationURL(returnTo string) (string, error) {
	if !n.SelfService {
		return "", fmt.Errorf("%w: this double manages no organization page", ErrNoDestination)
	}
	return n.destination("/mock/organization", returnTo)
}
func (n MockNavigator) CreateOrganizationURL(returnTo string) (string, error) {
	if !n.SelfService {
		return "", fmt.Errorf("%w: this double founds no organizations", ErrNoDestination)
	}
	return n.destination("/mock/organization/new", returnTo)
}

func (n MockNavigator) destination(path, returnTo string) (string, error) {
	if n.BaseURL == "" {
		return "", fmt.Errorf("%w: this double was built with no base URL", ErrNoDestination)
	}
	target := strings.TrimRight(n.BaseURL, "/") + path
	if returnTo != "" {
		target += "?return_to=" + url.QueryEscape(returnTo)
	}
	return target, nil
}

// MockDeliveryIDHeader carries the delivery id of a MockWebhook delivery. It
// is this double's own header, not a hosted provider's signature family: an
// unsigned local envelope has no signature scheme to borrow one from.
const MockDeliveryIDHeader = "Ggg-Mock-Delivery"

// MockWebhook parses a delivery whose body is the neutral identity.Event
// itself, encoded by MockDelivery.
//
// The double owns BOTH directions on purpose. An adapter's webhook fixtures
// are otherwise hand-written JSON in the shape of whichever adapter the
// harness selected — a coupling no import-based check can see, because the
// payload imports nothing. Building the fixture from a typed Event removes
// the wire format from every consumer.
type MockWebhook struct {
	// Provider overrides the stamped provider key; empty means MockProvider.
	Provider string
	// Err, when non-nil, is what every delivery returns, standing in for a
	// signature an adapter refuses.
	Err error
}

// MockDelivery encodes one event as a delivery this double accepts. The
// returned header carries the delivery id the receiver records for
// idempotency; an empty id yields no header, which is how a payload drives
// the "no idempotency key" refusal.
func MockDelivery(deliveryID string, event Event) ([]byte, http.Header, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, nil, fmt.Errorf("mock identity delivery: %w", err)
	}
	headers := http.Header{}
	if deliveryID != "" {
		headers.Set(MockDeliveryIDHeader, deliveryID)
	}
	return payload, headers, nil
}

func (h MockWebhook) Verify(_ context.Context, payload []byte, headers http.Header) (Event, error) {
	if h.Err != nil {
		return Event{}, h.Err
	}
	var event Event
	if err := json.Unmarshal(payload, &event); err != nil {
		return Event{}, fmt.Errorf("mock identity webhook: %w", err)
	}
	if event.Type == "" {
		return Event{}, fmt.Errorf("mock identity webhook: event type is required")
	}
	// The same completeness check every adapter performs, so a payload that
	// wants a refusal gets one from an incomplete event rather than from
	// malformed bytes.
	switch {
	case strings.HasPrefix(event.Type, "user."):
		if event.User == nil || event.User.Subject == "" {
			return Event{}, fmt.Errorf("mock identity webhook: %s needs a user subject", event.Type)
		}
	case strings.HasPrefix(event.Type, "organizationMembership."):
		if event.Membership == nil || event.Membership.OrganizationSubject == "" || event.Membership.UserSubject == "" {
			return Event{}, fmt.Errorf("mock identity webhook: %s needs an organization and a user subject", event.Type)
		}
	case strings.HasPrefix(event.Type, "organization."):
		if event.Organization == nil || event.Organization.Subject == "" {
			return Event{}, fmt.Errorf("mock identity webhook: %s needs an organization subject", event.Type)
		}
	}
	event.ID = headers.Get(MockDeliveryIDHeader)
	event.Provider = MockProvider
	if h.Provider != "" {
		event.Provider = h.Provider
	}
	return event, nil
}

var (
	_ Verifier                    = MockVerifier{}
	_ SyntheticSessionMinter      = MockVerifier{}
	_ SubjectVerifier             = MockVerifier{}
	_ OrganizationSubjectVerifier = MockVerifier{}
	_ Verifier                    = MockHostedVerifier{}
	_ UserFetcher                 = MockUserFetcher{}
	_ Deleter                     = (*MockDeleter)(nil)
	_ Navigator                   = MockNavigator{}
	_ Webhook                     = MockWebhook{}
)
