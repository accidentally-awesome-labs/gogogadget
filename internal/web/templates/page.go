package templates

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// Layout names for Page.Layout.
const (
	LayoutPublic = "public"
	LayoutApp    = "app"
	LayoutAdmin  = "admin"
	LayoutDocs   = "docs"
)

// Navigation contract, shared by every nav link and by the server's Navigate
// helper (web.ContentTarget / web.NavSwap alias these).
//
// NavTarget is the only element a navigation may swap: the shell around it
// hosts clerk-js's mounted widgets and their body-level dropdown portals, which
// do not survive being replaced. Every page reachable by a boosted link must
// therefore render identical chrome around it — see publicShell.
//
// NavSwap replaces that box, lets the browser's View Transitions API cross-fade
// the change, and brings the top of the new content into view the way a real
// page load would. Transitions are opted in per-swap rather than via
// htmx.config.transitions so in-page updates stay instant. The scroll is
// explicit because htmx only defaults boosted *forms* to show:top — a boosted
// link otherwise keeps the previous scroll offset and lands you mid-page.
const (
	NavTarget = "#content"
	NavSwap   = "outerHTML transition:true show:top"
)

// Page is the per-request view model every layout receives. Fields are added
// as capabilities land (identity adds User/Org, billing adds Plan) — layouts
// tolerate zero values.
type Page struct {
	Title       string
	Description string
	Path        string
	Layout      string
	CSRFToken   string
	AppURL      string

	PostHogKey          string
	ClerkPublishableKey string
	ClerkFrontendAPIURL string

	// Identity/billing context (populated by the render path from ctx).
	User *sqlc.User
	Org  *sqlc.Org
	Plan billing.Plan
	Sub  *sqlc.Subscription

	// Impersonator is non-nil while an admin "views as" this user (banner).
	Impersonator *identity.Impersonator

	// Docs navigation context (set by the docs handlers).
	Docs    *content.Docs
	DocSlug string

	// Now is the render clock (frozen under APP_ENV=test via TEST_NOW) so
	// rendered dates never rot visual baselines.
	Now func() time.Time
}

// NowOrDefault guards against nil Now in tests that construct Page directly.
func (p Page) NowOrDefault() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func avatarInitial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return string(unicode.ToUpper(r))
	}
	return "?"
}

type ctxWebhooksEnabled struct{}

// WithWebhooksEnabled carries the webhooks feature gate into templates
// (SettingsTabs hides the tab when off). Set by the settings handlers.
func WithWebhooksEnabled(ctx context.Context, on bool) context.Context {
	return context.WithValue(ctx, ctxWebhooksEnabled{}, on)
}

// WebhooksEnabled reads the gate from ctx; default true (present = shown).
func WebhooksEnabled(ctx context.Context) bool {
	on, ok := ctx.Value(ctxWebhooksEnabled{}).(bool)
	return !ok || on
}

func clerkOrgPlaceholder(org *sqlc.Org) string {
	if org == nil {
		return "Organization"
	}
	return org.Name
}

func clerkUserPlaceholder(user *sqlc.User) string {
	if user == nil {
		return "?"
	}
	return avatarInitial(user.Name)
}
