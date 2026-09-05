package csp_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gogogadget/gogogadget/internal/identity/clerk/csp"
)

// The three sources clerk-js needs, and the exact strings the seam used to
// hardcode. This is the unit; the composed header is asserted through the real
// middleware in internal/web.
func TestSourcesCoverTheThreeDirectivesClerkNeeds(t *testing.T) {
	sources := csp.Sources(map[string]string{
		"CLERK_FRONTEND_API_URL": "https://clerk.example.com",
	})

	assert.Equal(t, []string{"https://clerk.example.com"}, sources["connect-src"])
	assert.Equal(t, []string{"https://img.clerk.com"}, sources["img-src"])
	assert.Equal(t, []string{"blob:"}, sources["worker-src"])
	assert.Len(t, sources, 3, "a contribution that grows past its granted directives is a plan refusal")
}

// The development shape: Clerk's own wildcard frontend API, which is why one
// leading wildcard label is legitimate in the source grammar.
func TestSourcesAcceptTheDevelopmentWildcardOrigin(t *testing.T) {
	sources := csp.Sources(map[string]string{
		"CLERK_FRONTEND_API_URL": "https://*.clerk.accounts.dev",
	})

	assert.Equal(t, []string{"https://*.clerk.accounts.dev"}, sources["connect-src"])
}

// Selected but unconfigured contributes no connect-src at all rather than an
// empty source: a stray space in a directive is a policy nobody wrote, and
// selection is the gate anyway.
func TestSourcesOmitConnectWithoutAFrontendAPI(t *testing.T) {
	sources := csp.Sources(nil)

	_, present := sources["connect-src"]
	assert.False(t, present)
	assert.Equal(t, []string{"https://img.clerk.com"}, sources["img-src"])
	assert.Equal(t, []string{"blob:"}, sources["worker-src"])
}
