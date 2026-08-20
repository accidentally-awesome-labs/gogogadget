package templates

import (
	"context"
	"github.com/a-h/templ"
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
	// Canonical is the absolute self-referential URL for this page, and
	// Alternates are its language versions (see internal/web/seo.go).
	// Empty on authed pages — /app and /admin are noindex by nature.
	Canonical  string
	Alternates []Alternate
	// JSONLD is schema.org data for this page (a map or slice of maps), or
	// nil for none. Rendered through templ.JSONScriptElement, which marshals
	// and escapes it for script context — the page never hand-builds JSON.
	JSONLD any
	// Theme is the resolved appearance: "dark" renders the class on <html>
	// server-side so a fresh device never flashes light before app.js runs.
	// "system" and "light" render nothing — only the browser knows the OS
	// setting, and light is the stylesheet's default.
	Theme  string
	AppURL string

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

	// Announcement is the active platform banner (nil = none). Set by the
	// render path from the server's 30s cache; layouts tolerate nil.
	Announcement *sqlc.Announcement

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

// HTMLAttrs carries the resolved theme onto <html>. A spread rather than
// class={…} because templ renders an empty class="" for the light case, and
// every page in the app would carry that noise; an empty map renders nothing.
func (p Page) HTMLAttrs() templ.Attributes {
	if p.Theme == "dark" {
		return templ.Attributes{"class": "dark"}
	}
	return templ.Attributes{}
}

type ctxAdminWrite struct{}

// WithAdminWrite carries "this viewer may change platform state" into admin
// templates. A context value rather than a field on five different data
// structs: the capability belongs to the request, not to the table being
// rendered, and a new admin page inherits the gate instead of having to
// remember a bool.
func WithAdminWrite(ctx context.Context, on bool) context.Context {
	return context.WithValue(ctx, ctxAdminWrite{}, on)
}

// AdminWrite reports the viewer's capability. Default FALSE — an unset
// context renders read-only, so forgetting to set it hides controls rather
// than offering ones that 403.
func AdminWrite(ctx context.Context) bool {
	on, _ := ctx.Value(ctxAdminWrite{}).(bool)
	return on
}

// Alternate is one hreflang link: a language code (or "x-default") and the
// URL that serves that version.
type Alternate struct {
	Lang string
	Href string
}

// LDScript renders a schema.org data block.
//
// Built through templ.JSONScript rather than a JSONScriptElement literal:
// the element's Nonce field is a func that only the constructor populates,
// so a literal panics on render. templ marshals and escapes the data, which
// is why page content cannot close the element.
func LDScript(data any) templ.Component {
	el := templ.JSONScript("structured-data", data)
	el.Type = "application/ld+json"
	return el
}
