package slots_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gogogadget/gogogadget/internal/web/templates"
)

func posthogPublicShell(t *testing.T, environment string, values map[string]string) string {
	t.Helper()
	ctx := templates.WithProviderEnvironment(t.Context(), environment)
	ctx = templates.WithConfigLookup(ctx, func(key string) string { return values[key] })
	page := templates.Page{Title: "Home", Layout: templates.LayoutPublic, Path: "/"}
	var out strings.Builder
	require.NoError(t, templates.PublicLayout(page, templates.NotFound()).Render(ctx, &out))
	return out.String()
}

// The project API key reaches the browser by design: it is a write-only ingest
// key and autocapture cannot work without it. array.js is proxied through
// /ingest so the CSP stays script-src 'self'.
func TestPostHogConfiguredShellRendersLoaderAndConsent(t *testing.T) {
	body := posthogPublicShell(t, "production", map[string]string{
		"POSTHOG_API_KEY": "phc_fixture",
	})

	head, rest, found := strings.Cut(body, "</head>")
	require.True(t, found)
	assert.Contains(t, head, `<meta name="ph-key" content="phc_fixture">`)
	assert.Contains(t, head, `<script defer src="/ingest/static/array.js"></script>`)
	assert.Contains(t, head, `<script defer src="/static/analytics.js"></script>`)

	// The consent dialog is body-level and outside #content, so a boosted
	// navigation cannot remount it and lose its Alpine state.
	assert.Contains(t, rest, `x-data="phConsent"`)
	assert.Contains(t, rest, "Analytics consent")
}

func TestPostHogDeselectedEnvironmentRendersNothing(t *testing.T) {
	body := posthogPublicShell(t, "development", map[string]string{
		"POSTHOG_API_KEY": "phc_fixture",
	})

	assert.NotContains(t, strings.ToLower(body), "posthog")
	assert.NotContains(t, body, "ph-key")
	assert.NotContains(t, body, "/ingest/")
}

func TestPostHogSelectedWithoutKeyRendersNothing(t *testing.T) {
	body := posthogPublicShell(t, "production", nil)

	assert.NotContains(t, body, "ph-key")
	assert.NotContains(t, body, `x-data="phConsent"`)
}
