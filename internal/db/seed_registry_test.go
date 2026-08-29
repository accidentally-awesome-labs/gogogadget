package db_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Personas and the e2e fixture must agree about who exists. Both sides are
// generated, but from different records, so this pins them together: a persona
// whose user has no seeded row is a spec that logs in as nobody, and a seeded
// actor with no persona is a fixture row nothing can reach.
//
// It reads fragments through the same registry the loader uses, so a module
// whose fixture is uninstalled fails here rather than at spec time.
func TestPersonasMatchSeededE2EUsers(t *testing.T) {
	fragments := db.SeedFragments["e2e"]
	require.NotEmpty(t, fragments)

	seededUsers := map[string]bool{}
	re := regexp.MustCompile(`\('(user_[a-z]+)',`)
	for _, fragment := range fragments {
		raw, err := os.ReadFile(filepath(fragment))
		require.NoError(t, err, "fragment listed in the registry is missing from the tree")
		for _, match := range re.FindAllStringSubmatch(string(raw), -1) {
			seededUsers[match[1]] = true
		}
	}
	require.NotEmpty(t, seededUsers)

	personaUsers := map[string]bool{}
	for _, p := range db.PersonaRecords {
		personaUsers[p.User] = true
	}

	for user := range seededUsers {
		assert.True(t, personaUsers[user],
			"user %q is seeded in e2e but no persona declares it; no spec can act as it", user)
	}
	for _, p := range db.PersonaRecords {
		assert.True(t, seededUsers[p.User],
			"persona %q references user %q, which the e2e fixture never seeds", p.ID, p.User)
	}
}

// Every registered fragment must exist and be non-empty: a registry entry
// pointing at nothing would seed silently less than it claims.
func TestSeedFragmentsExist(t *testing.T) {
	require.NotEmpty(t, db.SeedFragments)
	for set, fragments := range db.SeedFragments {
		require.NotEmpty(t, fragments, "set %q registered with no fragments", set)
		for _, fragment := range fragments {
			raw, err := os.ReadFile(filepath(fragment))
			require.NoError(t, err, "%s (%s) is registered but missing", fragment, set)
			assert.NotEmpty(t, strings.TrimSpace(string(raw)), "%s is empty", fragment)
		}
	}
}

// filepath resolves a repository-relative fragment path from this package's
// directory, which is two levels down.
func filepath(rel string) string {
	return "../../" + rel
}
