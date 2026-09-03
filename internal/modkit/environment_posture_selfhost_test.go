// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Both tests read THIS repository's committed
// lock, which is the only place the shipped catalog's actual posture output
// can be observed.

package modkit_test

import (
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `ggg new` seeds .ggg/env/<environment>.env from DeclaredEnvironmentPosture,
// so what that function returns over the REAL catalog is what lands, at mode
// 0600, in every created project. A synthetic three-key fixture cannot say
// anything about that: the filter is one boolean on one field, nothing ties a
// key's shape to `secret`, and a manifest author adding `"example"` to a
// credential would quietly turn genesis into a secret writer.
//
// So this pins the actual output. The exact-equality assertion is deliberate:
// if a declaration changes what a fresh project boots with, that is a decision
// someone must make on purpose, not a diff that appears in a destination
// nobody looked at.
func TestDeclaredPostureOverTheShippedCatalog(t *testing.T) {
	lock := loadLock(t, repoRoot(t))
	graph := make([]modkit.Manifest, 0, len(lock.Modules))
	for _, locked := range lock.Modules {
		graph = append(graph, locked.Manifest)
	}

	development, err := modkit.DeclaredEnvironmentPosture(lock, graph, "development")
	require.NoError(t, err)
	assert.Equal(t, []string{"DEV_AUTH_BYPASS=true"}, development,
		"the development posture a created project boots with changed; that is a deliberate decision, not a drift")

	test, err := modkit.DeclaredEnvironmentPosture(lock, graph, "test")
	require.NoError(t, err)
	assert.Equal(t, development, test, "development and test select the same local adapters")

	// Production is never written, so it is never asked for.
	_, err = modkit.DeclaredEnvironmentPosture(lock, graph, "production")
	require.Error(t, err)
}

// The posture filter is `Secret`, and nothing validates that a credential is
// declared secret. This is the second gate: whatever the declarations say, no
// value genesis writes may be a secret or credential-shaped. It is what keeps
// a later manifest edit from turning a 0600 seed file into a leak.
func TestDeclaredPostureNeverCarriesACredential(t *testing.T) {
	lock := loadLock(t, repoRoot(t))
	graph := make([]modkit.Manifest, 0, len(lock.Modules))
	for _, locked := range lock.Modules {
		graph = append(graph, locked.Manifest)
	}
	secret := map[string]bool{}
	for _, locked := range lock.Modules {
		for _, declaration := range locked.Manifest.Environment {
			if declaration.Secret {
				secret[declaration.Key] = true
			}
		}
	}
	require.NotEmpty(t, secret, "the catalog declares no secrets at all, so this proves nothing")

	// Shapes that are credentials whatever the declaration claims. URL is
	// included because a connection string carries its own password.
	shapes := []string{"KEY", "SECRET", "TOKEN", "PASSWORD", "CREDENTIAL", "URL"}
	for _, environment := range []string{"development", "test"} {
		lines, err := modkit.DeclaredEnvironmentPosture(lock, graph, environment)
		require.NoError(t, err)
		for _, line := range lines {
			key, value, found := strings.Cut(line, "=")
			require.True(t, found, "%s posture line %q is not KEY=VALUE", environment, line)
			assert.Falsef(t, secret[key],
				"%s posture writes %s, which its own manifest declares secret", environment, key)
			for _, shape := range shapes {
				assert.NotContainsf(t, key, shape,
					"%s posture writes credential-shaped key %s", environment, key)
			}
			assert.NotContainsf(t, value, "://",
				"%s posture writes a URL-shaped value for %s, which can carry a password", environment, key)
		}
	}

	// The two filters must not be confused for one another. Today every
	// secret in this catalog happens to declare an example equal to its
	// default, so the example-vs-default filter alone would exclude them and
	// the `Secret` filter would be doing no observable work — a guard that
	// passes for the wrong reason and stops guarding the moment one default
	// changes. DATABASE_URL is the sharp case: it is declared secret and its
	// example is a connection string carrying a password.
	//
	// So exercise the real catalog with that coincidence removed. The
	// declaration is copied, never mutated on disk, and the posture must
	// still refuse to write it.
	const credential = "DATABASE_URL"
	require.True(t, secret[credential], "%s is no longer declared secret; pick another sharp case", credential)
	unmasked := make([]modkit.Manifest, 0, len(graph))
	found := false
	for _, module := range graph {
		copied := module
		copied.Environment = append([]modkit.EnvironmentVariable(nil), module.Environment...)
		for i := range copied.Environment {
			if copied.Environment[i].Key == credential {
				require.NotEmpty(t, copied.Environment[i].Example, "%s declares no example, so this case proves nothing", credential)
				copied.Environment[i].Default = ""
				found = true
			}
		}
		unmasked = append(unmasked, copied)
	}
	require.True(t, found, "%s is not declared by any installed module", credential)
	for _, environment := range []string{"development", "test"} {
		lines, err := modkit.DeclaredEnvironmentPosture(lock, unmasked, environment)
		require.NoError(t, err)
		for _, line := range lines {
			assert.Falsef(t, strings.HasPrefix(line, credential+"="),
				"%s posture wrote the declared-secret %s once its example differed from its default: %q",
				environment, credential, line)
		}
	}
}
