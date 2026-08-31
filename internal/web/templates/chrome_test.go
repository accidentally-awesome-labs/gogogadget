package templates

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedClock is a render clock a long way from the wall clock, so a test that
// passes by accident on the current year fails here.
func fixedClock() time.Time {
	return time.Date(2031, time.March, 4, 12, 0, 0, 0, time.UTC)
}

// Rebranding must be a value edit, not a grep. BrandName is read at render
// time by every surface that spells the product name — document title, OG
// title, RSS autodiscovery, logo, footer copyright and the email header — so
// overriding the var has to reach all of them. Assert that with a temporary
// override rather than trusting six separate templates.
func TestBrandNameReachesEverySurface(t *testing.T) {
	original := BrandName
	BrandName = "Acme Ltd"
	t.Cleanup(func() { BrandName = original })

	page := Page{
		Title:     "Pricing",
		AppURL:    "https://example.test",
		Path:      "/pricing",
		Canonical: "https://example.test/pricing",
		Now:       fixedClock,
	}
	shell := renderComponent(t, PublicLayout(page, NotFound()))

	for _, want := range []string{
		"<title>Pricing — Acme Ltd</title>",
		`content="Pricing — Acme Ltd"`,
		`title="Acme Ltd Blog"`,
		">Acme Ltd</a>",    // logo
		"© 2031 Acme Ltd.", // footer copyright, year from the render clock
	} {
		assert.Contains(t, shell, want)
	}
	assert.NotContains(t, shell, original, "no chrome surface may hardcode the old name")

	// Email chrome follows too. Its BODY does not: the product name also
	// appears inside translated prose (email.footer, email.*.subject,
	// layout.analytics_consent_body), which belongs to the catalogs — word
	// order around a brand differs per language, so it is not a template
	// variable. /docs/frontend's rebrand recipe says to grep the catalogs.
	email := renderComponent(t, WelcomeEmailHTML("https://example.test", "Ada"))
	assert.Contains(t, email, "⚡ Acme Ltd", "a rebrand must reach the email header")
}

// The footer year comes from the render clock so a frozen TEST_NOW keeps
// visual baselines stable and a real deploy never ships a stale year.
func TestFooterYearComesFromTheRenderClock(t *testing.T) {
	page := Page{AppURL: "https://example.test", Path: "/", Now: fixedClock}
	shell := renderComponent(t, PublicLayout(page, NotFound()))
	assert.Contains(t, shell, "2031")
}

// Chrome navigation is data, so a fork can reorder or replace it without
// touching markup. Prove the shell really iterates the vars.
func TestShellRendersChromeConfig(t *testing.T) {
	originalPublic, originalApp := PublicNav, AppNav
	PublicNav = []NavItem{{LabelKey: "nav.pricing", Href: "/only-link"}}
	AppNav = []NavItem{{LabelKey: "sidebar.dashboard", Href: "/app/only", Match: "/app/only"}}
	t.Cleanup(func() { PublicNav, AppNav = originalPublic, originalApp })

	nav := renderComponent(t, Nav(Page{Path: "/"}))
	assert.Contains(t, nav, `href="/only-link"`)
	assert.NotContains(t, nav, `href="/changelog"`, "the public nav must come from PublicNav alone")

	side := renderComponent(t, Sidebar(Page{Path: "/app/only"}))
	assert.Contains(t, side, `href="/app/only"`)
	assert.Contains(t, side, `data-nav-match="/app/only"`)
	assert.Contains(t, side, `aria-current="page"`)
	assert.NotContains(t, side, `href="/app/projects"`, "the app nav must come from AppNav alone")
}

func TestNavRendersOnlyActiveShellSlotsForEnvironment(t *testing.T) {
	originalRegistry, originalActive := ShellSlotsRegistry, ShellSlotActive
	ShellSlotsRegistry = map[string][]string{"head": {"inactive", "active"}}
	ShellSlotActive = map[string]func(string) bool{
		"inactive": func(string) bool { return false },
		"active":   func(env string) bool { return env == "production" },
	}
	t.Cleanup(func() {
		ShellSlotsRegistry, ShellSlotActive = originalRegistry, originalActive
	})
	ctx := WithProviderEnvironment(t.Context(), "production")
	var output strings.Builder
	require.NoError(t, Nav(Page{Path: "/"}).Render(ctx, &output))
	assert.NotContains(t, output.String(), `data-shell-slot="inactive"`)
	assert.Contains(t, output.String(), `data-shell-slot="active"`)
}

// MatchPath is the prefix navCurrent compares against; an item with no
// explicit Match falls back to its own Href.
func TestNavItemMatchPath(t *testing.T) {
	assert.Equal(t, "/pricing", NavItem{Href: "/pricing"}.MatchPath())
	assert.Equal(t, "/app/settings", NavItem{Href: "/app/settings/account", Match: "/app/settings"}.MatchPath())
}

// Every email style string must be a real declaration list — a typo here
// silently drops inline colour in mail clients, which strip <style>.
func TestEmailStyleTokensAreDeclarations(t *testing.T) {
	for name, value := range map[string]string{
		"Page": emailStyle.Page, "Card": emailStyle.Card, "Brand": emailStyle.Brand,
		"Footer": emailStyle.Footer, "Link": emailStyle.Link, "DigestRow": emailStyle.DigestRow,
		"DigestLink": emailStyle.DigestLink, "DigestBody": emailStyle.DigestBody,
		"DigestMeta": emailStyle.DigestMeta, "Manage": emailStyle.Manage,
		"MutedLink": emailStyle.MutedLink,
	} {
		require.NotEmpty(t, value, "emailStyle.%s", name)
		assert.True(t, strings.Contains(value, ":") && strings.HasSuffix(value, ";"),
			"emailStyle.%s must be a css declaration list ending in ';', got %q", name, value)
	}
}
