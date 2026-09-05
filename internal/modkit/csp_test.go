package modkit

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The allowlist grammar is the constraint the whole mechanism turns on: a
// contribution may ADD sources and may never weaken the policy. Everything
// below that is refused is refused by not being in the grammar, which is why
// the grammar is an allowlist rather than a denylist of the attacks known
// today.
func TestValidateCSPSourceAllowlist(t *testing.T) {
	for _, source := range []string{
		"https://clerk.example.com",
		"https://img.clerk.com",
		"https://*.clerk.accounts.dev",
		"https://vendor.example.com:8443",
		"blob:",
		"data:",
	} {
		assert.NoErrorf(t, ValidateCSPSource(source), "source %q", source)
	}

	for source, because := range map[string]string{
		"'unsafe-inline'":            "keyword source",
		"'unsafe-eval'":              "keyword source",
		"'self'":                     "keyword source",
		"*":                          "wildcard host",
		"*.example.com":              "wildcard host",
		"https://*.*.example.com":    "not a contributable source",
		"http://plain.example.com":   "plaintext http",
		"https://ok.example.com/api": "not a contributable source",
		"javascript:":                "not a contributable source",
		"":                           "empty source",
		"https://a.example.com https://b.example.com": "separator",
	} {
		err := ValidateCSPSource(source)
		require.Errorf(t, err, "source %q must be refused", source)
		assert.Containsf(t, err.Error(), because, "refusal for %q should explain: %v", source, err)
	}
}

// The manifest grant is the blast radius: reading it has to tell you which
// directives a contribution can touch, so the closed set excludes the three
// that are the framework's posture rather than a list anybody extends.
func TestValidateCSPContributionsRefuseUncontributableDirectives(t *testing.T) {
	contribution := func(directives ...CSPDirective) []CSPContribution {
		return []CSPContribution{{ID: "x-csp", Directives: directives,
			Package: "internal/x/csp", Sources: "Sources"}}
	}

	require.NoError(t, validateCSPContributions(contribution("connect-src", "img-src", "worker-src"), true))

	for _, directive := range []CSPDirective{"script-src", "default-src", "base-uri", "form-action", "frame-ancestors"} {
		err := validateCSPContributions(contribution(directive), false)
		require.Errorf(t, err, "directive %q must not be contributable", directive)
		assert.Contains(t, err.Error(), "is not contributable")
		assert.Contains(t, err.Error(), "the framework's posture")
	}

	require.ErrorContains(t, validateCSPContributions(contribution(), false), "at least one directive")
	require.ErrorContains(t, validateCSPContributions(contribution("img-src", "img-src"), false), "twice")
	require.ErrorContains(t, validateCSPContributions(contribution("worker-src", "img-src"), true), "must be sorted")
}

func cspModule(body string) ([]Manifest, map[string][]byte) {
	return []Manifest{{
		ID: "ggg/system/identity-clerk", Kind: ModuleSystem, Name: "identity-clerk",
		Files: []ManifestFile{{Source: "internal/identity/clerk/csp/csp.go",
			Target: "internal/identity/clerk/csp/csp.go", Class: FileClassGo}},
		Runtime: RuntimeContributions{CSP: []CSPContribution{{
			ID: "identity-clerk-csp", Directives: []CSPDirective{"connect-src", "img-src", "worker-src"},
			Package: "internal/identity/clerk/csp", Sources: "Sources",
		}}},
	}}, map[string][]byte{
		"internal/identity/clerk/csp/csp.go": []byte("package csp\n\n" + body),
	}
}

// Plan-time refusal, one mutation per case, because a source list is a literal
// slice in a small function and that is exactly what can be read before any
// write happens.
func TestValidateCSPContributionSourcesRefusesEachCase(t *testing.T) {
	good := `func Sources(values map[string]string) map[string][]string {
	return map[string][]string{"img-src": {"https://img.clerk.com"}, "worker-src": {"blob:"}}
}
`
	modules, files := cspModule(good)
	require.NoError(t, ValidateCSPContributionSources(modules, files))

	for name, body := range map[string]string{
		"unsafe-inline": `func Sources(v map[string]string) map[string][]string {
	return map[string][]string{"img-src": {"'unsafe-inline'"}}
}
`,
		"bare wildcard": `func Sources(v map[string]string) map[string][]string {
	return map[string][]string{"img-src": {"*"}}
}
`,
		"plaintext http": `func Sources(v map[string]string) map[string][]string {
	return map[string][]string{"img-src": {"http://img.clerk.com"}}
}
`,
		"ungranted directive": `func Sources(v map[string]string) map[string][]string {
	return map[string][]string{"script-src": {"https://cdn.clerk.com"}}
}
`,
	} {
		modules, files := cspModule(body)
		err := ValidateCSPContributionSources(modules, files)
		require.Errorf(t, err, "case %q must refuse", name)
		assert.Contains(t, err.Error(), "identity-clerk-csp")
		if name == "ungranted directive" {
			assert.Contains(t, err.Error(), "does not grant")
		}
	}

	// A symbol nothing declares, which no directive list would ever catch.
	modules, files = cspModule(good)
	modules[0].Runtime.CSP[0].Sources = "Missing"
	require.ErrorContains(t, ValidateCSPContributionSources(modules, files),
		"which no installed payload in that package declares")
}

// The generated registry names the contribution, its grant, its own module's
// non-secret keys, and the per-environment gate — the same four things the
// shell slots get, so a reader who understands one understands the other.
func TestEmitCSPRegistryShape(t *testing.T) {
	adapter := Manifest{
		ID: "ggg/system/identity-clerk", Kind: ModuleSystem, Name: "identity-clerk",
		Environment: []EnvironmentVariable{
			{Key: "CLERK_FRONTEND_API_URL", Field: "ClerkFrontendAPIURL"},
			{Key: "CLERK_SECRET_KEY", Field: "ClerkSecretKey", Secret: true},
		},
		Runtime: RuntimeContributions{
			System: &SystemContribution{Adapter: &AdapterContribution{
				Slot: "ggg/identity", Targets: []ServiceTarget{{ID: "clerk", Mode: "managed"}},
			}},
			CSP: []CSPContribution{{ID: "identity-clerk-csp",
				Directives: []CSPDirective{"connect-src", "img-src", "worker-src"},
				Package:    "internal/identity/clerk/csp", Sources: "Sources"}},
		},
	}
	out, err := emitCSPRegistry(context.Background(), "example.com/app",
		Lock{Schema: 2, Order: []string{adapter.ID}}, []Manifest{adapter})
	require.NoError(t, err)

	for _, want := range []string{
		`cspSource0 "example.com/app/internal/identity/clerk/csp"`,
		`"identity-clerk-csp": cspSource0.Sources,`,
		`"connect-src",`,
		`"CLERK_FRONTEND_API_URL",`,
		`providerActive(env, "ggg/identity", "ggg/system/identity-clerk")`,
	} {
		assert.Contains(t, out.Content, want)
	}
	assert.NotContains(t, out.Content, "CLERK_SECRET_KEY",
		"a secret is never a rendering or header input")
	assert.True(t, strings.HasPrefix(out.Content, "// Code generated by ggg sync"))
}
