// Package identitydev implements the deterministic, zero-account identity
// adapter: synthetic `e2e:` session tokens, derived profiles, in-app portal
// URLs, and an unsigned webhook envelope. It imports no provider SDK, so a
// project that never selects a hosted identity adapter compiles none.
package identitydev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// Provider is the value stamped on every claim and event this adapter
// produces. The identity mapping tables store it exactly as a hosted
// provider's key, so a dev-seeded database is never mistaken for a live one.
const Provider = "dev"

type Deps struct{ Config *config.Config }

type Module struct {
	Verifier  identity.Verifier
	Fetcher   identity.UserFetcher
	Deleter   identity.Deleter
	Navigator identity.Navigator
	Webhook   identity.Webhook
}

func NewModule(ctx context.Context, _ apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("identity dev: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{
		Verifier:  Verifier{},
		Fetcher:   UserFetcher{},
		Deleter:   Deleter{},
		Navigator: Navigator{BaseURL: d.Config.AppURL, Bypass: d.Config.DevAuthBypass},
		Webhook:   Webhook{},
	}, nil
}

// Verifier accepts the synthetic `e2e:<userSubject>:<orgSubject>:<role>`
// token the dev login and the e2e harness mint.
type Verifier struct{}

func (Verifier) Verify(_ context.Context, token string) (*identity.ProviderClaims, error) {
	parts := strings.SplitN(token, ":", 4)
	if len(parts) != 4 || parts[0] != "e2e" || parts[1] == "" {
		return nil, fmt.Errorf("%w: want e2e:<userID>:<orgID>:<role>", identity.ErrInvalidToken)
	}
	return &identity.ProviderClaims{Provider: Provider, UserSubject: parts[1], OrgSubject: parts[2], OrgRole: parts[3], OrgSlug: parts[2]}, nil
}

// MintSession is the inverse of Verify: it is the only place the synthetic
// token's shape is written, so the dev login never spells `e2e:` itself.
func (Verifier) MintSession(userSubject, orgSubject, role string) (string, error) {
	if userSubject == "" {
		return "", fmt.Errorf("%w: a synthetic session needs a user subject", identity.ErrInvalidToken)
	}
	// Verify splits on the first three colons, so the role is the remainder
	// and may legitimately contain one ("org:admin"); the two subjects may not.
	if strings.ContainsAny(userSubject+orgSubject, ":") {
		return "", fmt.Errorf("%w: a synthetic session subject may not contain ':'", identity.ErrInvalidToken)
	}
	return "e2e:" + userSubject + ":" + orgSubject + ":" + role, nil
}

func (Verifier) VerifySubject(_ context.Context, subject string) (*identity.ProviderClaims, error) {
	if subject == "" {
		return nil, identity.ErrInvalidToken
	}
	return &identity.ProviderClaims{Provider: Provider, UserSubject: subject}, nil
}

func (Verifier) VerifyOrganizationSubject(_ context.Context, subject string) (*identity.ProviderClaims, error) {
	if subject == "" {
		return nil, identity.ErrInvalidToken
	}
	return &identity.ProviderClaims{Provider: Provider, OrgSubject: subject}, nil
}

// UserFetcher derives a profile from the subject itself, so a fresh clone has
// mirror rows without any upstream account.
type UserFetcher struct{}

func (UserFetcher) Fetch(_ context.Context, userSubject string) (identity.UserProfile, error) {
	if !strings.HasPrefix(userSubject, "user_") {
		return identity.UserProfile{}, fmt.Errorf("dev fetcher: unexpected user subject %q", userSubject)
	}
	return identity.UserProfile{Email: userSubject + "@gogogadget.dev", Name: userSubject}, nil
}

// Deleter has no upstream account to delete.
type Deleter struct{}

func (Deleter) DeleteUser(context.Context, string) error { return nil }

// Navigator maps each destination onto this adapter's own local surface.
//
// There is no hosted portal here, and pretending otherwise is what the old
// implementation did: it returned `<app>/login`, `<app>/signup` and
// `<app>/account`, of which the first two are the very handlers that ask this
// port (an infinite redirect) and the third is a route this application has
// never served. Each destination below either names a route that exists in
// the configuration this adapter runs in, or refuses.
type Navigator struct {
	BaseURL string
	// Bypass mirrors DEV_AUTH_BYPASS. The dev session routes are registered
	// only while it is on — and it is the only condition under which this
	// adapter can mint a session at all — so it decides whether this adapter
	// has a sign-in surface to offer.
	Bypass bool
}

// LoginURL and SignupURL are both the dev login. Minting a session for a
// subject IS account creation here: the fetcher derives the profile from the
// subject, so there is no second page a new visitor would need.
func (n Navigator) LoginURL(returnTo string) (string, error) {
	return n.session("/dev/login", returnTo)
}
func (n Navigator) SignupURL(returnTo string) (string, error) {
	return n.session("/dev/login", returnTo)
}

// LogoutURL is the home page. The synthetic session is one cookie belonging to
// this application, so sign-out is complete once the caller has cleared it and
// there is nothing upstream left to visit.
func (n Navigator) LogoutURL(string) (string, error) {
	if n.BaseURL == "" {
		return "", fmt.Errorf("identity dev: APP_URL is required: %w", identity.ErrNoDestination)
	}
	return n.BaseURL + "/", nil
}

// AccountURL and OrganizationURL refuse. This adapter manages nothing: the
// profile is derived from the subject and the organization lives in this
// application's own tables, so the settings page a caller renders the link
// *on* is already the whole surface. A link back to the page you are reading,
// captioned "managed by your identity provider", is worse than no link.
func (n Navigator) AccountURL(string) (string, error) {
	return "", fmt.Errorf("identity dev: a synthetic profile has no account page: %w",
		identity.ErrNoDestination)
}
func (n Navigator) OrganizationURL(string) (string, error) {
	return "", fmt.Errorf("identity dev: organizations live in this application's own tables: %w",
		identity.ErrNoDestination)
}

// CreateOrganizationURL refuses because this application serves no
// org-creation route, in any environment: organizations arrive through the
// identity webhook or the seed. Answering with some other real page — the dev
// login, the org settings tab — would send a visitor somewhere that cannot do
// what they were sent to do, which is the failure this port was reshaped to
// stop. The caller renders a named diagnostic instead.
func (n Navigator) CreateOrganizationURL(string) (string, error) {
	return "", fmt.Errorf("identity dev: this application serves no org-creation page: %w",
		identity.ErrNoDestination)
}

// session builds a dev session URL, refusing when the route is not registered.
func (n Navigator) session(path, returnTo string) (string, error) {
	if n.BaseURL == "" {
		return "", fmt.Errorf("identity dev: APP_URL is required to reach %s: %w",
			path, identity.ErrNoDestination)
	}
	if !n.Bypass {
		return "", fmt.Errorf("identity dev: %s is registered only while DEV_AUTH_BYPASS is on: %w",
			path, identity.ErrNoDestination)
	}
	if returnTo == "" {
		return n.BaseURL + path, nil
	}
	return n.BaseURL + path + "?return_to=" + url.QueryEscape(returnTo), nil
}

// messageIDHeader is this adapter's delivery id. It is deliberately not a
// hosted provider's header family: an unsigned local envelope has no
// signature scheme to borrow one from.
const messageIDHeader = "id"

// Webhook parses this adapter's own flat, unsigned delivery envelope:
//
//	{"type":"user.created","data":{"id":"user_x","email":"…","name":"…"}}
//
// There is no signature to verify — the endpoint is only reachable with the
// dev bypass on, and DEV_AUTH_BYPASS is boot-refused in production.
type Webhook struct{}

type webhookEnvelope struct {
	Type string `json:"type"`
	Data struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
		Slug      string `json:"slug"`
		ImageURL  string `json:"image_url"`
		OrgID     string `json:"organization_id"`
		UserID    string `json:"user_id"`
		Role      string `json:"role"`
	} `json:"data"`
}

func (Webhook) Verify(_ context.Context, payload []byte, headers http.Header) (identity.Event, error) {
	var raw webhookEnvelope
	if err := json.Unmarshal(payload, &raw); err != nil {
		return identity.Event{}, err
	}
	if raw.Type == "" {
		return identity.Event{}, fmt.Errorf("identity dev: event type is required")
	}
	out := identity.Event{ID: headers.Get(messageIDHeader), Provider: Provider, Type: raw.Type}
	d := raw.Data
	switch raw.Type {
	case "user.created", "user.updated", "user.deleted":
		if d.ID == "" {
			return identity.Event{}, fmt.Errorf("identity dev: %s data: missing id", raw.Type)
		}
		out.User = &identity.UserEvent{Subject: d.ID, Email: d.Email, Name: d.Name, AvatarURL: d.AvatarURL}
	case "organization.created", "organization.updated", "organization.deleted":
		if d.ID == "" {
			return identity.Event{}, fmt.Errorf("identity dev: %s data: missing id", raw.Type)
		}
		out.Organization = &identity.OrganizationEvent{Subject: d.ID, Name: d.Name, Slug: d.Slug, ImageURL: d.ImageURL}
	case "organizationMembership.created", "organizationMembership.updated", "organizationMembership.deleted":
		if d.OrgID == "" || d.UserID == "" {
			return identity.Event{}, fmt.Errorf("identity dev: %s data: missing organization or user id", raw.Type)
		}
		out.Membership = &identity.MembershipEvent{OrganizationSubject: d.OrgID, UserSubject: d.UserID, Role: d.Role}
	}
	return out, nil
}

var (
	_ identity.SubjectVerifier             = Verifier{}
	_ identity.OrganizationSubjectVerifier = Verifier{}
	_ identity.UserFetcher                 = UserFetcher{}
	_ identity.Deleter                     = Deleter{}
	_ identity.Navigator                   = Navigator{}
	_ identity.Webhook                     = Webhook{}
)
