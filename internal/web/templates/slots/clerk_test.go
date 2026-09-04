package slots_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// The package under test is imported by the generated shell-slot registry in
// package templates, so these assertions live in the external test package:
// slots_test may reach templates, templates may reach slots, and neither
// direction is a cycle.

func clerkAppShell(t *testing.T, environment string, values map[string]string) string {
	t.Helper()
	ctx := templates.WithProviderEnvironment(t.Context(), environment)
	ctx = templates.WithConfigLookup(ctx, func(key string) string { return values[key] })
	ctx = identity.WithUser(ctx, &sqlc.User{UserID: "usr_1", Name: "Uma Nakamura"})
	ctx = identity.WithOrg(ctx, &sqlc.Org{OrgID: "org_1", Name: "Acme Rockets"})

	page := templates.Page{
		Title:  "Dashboard",
		Layout: templates.LayoutApp,
		Path:   "/app",
		User:   &sqlc.User{UserID: "usr_1", Name: "Uma Nakamura"},
		Org:    &sqlc.Org{OrgID: "org_1", Name: "Acme Rockets"},
	}
	var out strings.Builder
	require.NoError(t, templates.AppLayout(page, templates.NotFound()).Render(ctx, &out))
	return out.String()
}

// A Clerk-configured project must still load clerk-js with the same
// attributes: it owns the ~60s __session JWT refresh, so a missing or
// differently-shaped tag expires authentication a minute after login. This
// asserts the rendered document, not the Go shape — the shape moved, the HTML
// must not have.
func TestClerkConfiguredShellRendersLoaderAndLiveMounts(t *testing.T) {
	body := clerkAppShell(t, "production", map[string]string{
		"CLERK_PUBLISHABLE_KEY": "pk_test_fixture",
	})

	for _, want := range []string{
		`<meta name="clerk-publishable-key" content="pk_test_fixture">`,
		`<script defer src="/static/vendor/clerk.browser.js" data-clerk-publishable-key="pk_test_fixture"></script>`,
		`<div id="org-switcher" class="clerk-org-slot min-h-8" data-clerk-placeholder="Acme Rockets"></div>`,
		`<div id="user-button" class="clerk-user-slot min-h-8 min-w-8" data-clerk-placeholder="U"></div>`,
	} {
		assert.Contains(t, body, want)
	}

	// The loader belongs in <head>, where a provider's meta tag is readable
	// before Alpine boots. It used to render inside <nav>.
	head, _, found := strings.Cut(body, "</head>")
	require.True(t, found)
	assert.Contains(t, head, `name="clerk-publishable-key"`)
	assert.Contains(t, head, "clerk.browser.js")

	// The shell's neutral containers must not also be in the document: two
	// elements with one id is not a fallback.
	assert.NotContains(t, body, "data-shell-placeholder")
	assert.Equal(t, 1, strings.Count(body, `id="org-switcher"`))
	assert.Equal(t, 1, strings.Count(body, `id="user-button"`))
}

// The same project in an environment that selects another identity adapter
// gets the shell's own containers and no trace of this one.
func TestClerkDeselectedEnvironmentRendersNeutralShell(t *testing.T) {
	body := clerkAppShell(t, "development", map[string]string{
		"CLERK_PUBLISHABLE_KEY": "pk_test_fixture",
	})

	assert.Contains(t, body, `<div id="org-switcher" class="min-h-8" data-shell-placeholder="Acme Rockets"></div>`)
	assert.Contains(t, body, `<div id="user-button" class="min-h-8 min-w-8" data-shell-placeholder="U"></div>`)
	assert.NotContains(t, strings.ToLower(body), "clerk")
}

// Selecting Clerk without a publishable key renders the mounts (the ids are
// what app.js looks up, and this adapter's stylesheet labels the empty box)
// but not the loader: clerk.browser.js without a key throws and mounts
// nothing.
func TestClerkSelectedWithoutKeyRendersMountsButNoLoader(t *testing.T) {
	body := clerkAppShell(t, "production", nil)

	assert.Contains(t, body, `<div id="org-switcher" class="clerk-org-slot min-h-8"`)
	assert.NotContains(t, body, "clerk.browser.js")
	assert.NotContains(t, body, "clerk-publishable-key")
}
