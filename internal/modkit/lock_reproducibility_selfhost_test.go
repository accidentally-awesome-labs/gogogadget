// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. It asserts about THIS repository's committed
// lock and its committed intent — never about the source the registry
// distributes.

package modkit_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/stretchr/testify/require"
)

// The canonical repository's own install set must be reproducible by its own
// tooling, and for a release it was not. The lock carried 293 module rows; a
// fresh `ggg new --profile ggg/profile/full` resolved 288; and the five-row
// difference was a set of adapters no environment selected, so no command
// could put them there. Four were empty tombstones — reason `removed`, zero
// files, zero migrations — left behind when adapters stopped being explicit
// modules, and one, ggg/system/feature-flags-launchdarkly, was a tombstone for
// a module the catalog no longer publishes at all. Meanwhile the live-canary
// suite in internal/canary imported four of those adapters' packages directly,
// compiling against payload sources no lock row owned.
//
// Both halves are now declared. `verification_installs` installs the managed
// adapters the canary must import, with lock reason `verification`, and the
// dead tombstones are pruned because a ledger row with no ledger for a module
// nothing publishes retains nothing. So the arithmetic closes:
//
//	installed(core lock) == closure(ggg/profile/full) + verification_installs
//	           293       ==              288          +          5
//
// This asserts that identity as a SET, not a count. The left side is the
// committed lock; the right side is re-derived here by staging and resolving
// the exact project `ggg new --profile ggg/profile/full` writes — the same
// staging planShippedProfile uses, so no network, no toolchain, and no hand
// list of 288 ids. A count alone would pass on two locks with 288 different
// members, which is the failure this guard exists to make impossible.
func TestTheCommittedLockDecomposesIntoTheFullProfileClosureAndItsVerificationInstalls(t *testing.T) {
	root := repoRoot(t)
	lock := loadLock(t, root)
	project := repoProject(t, root)

	var selected, verification, tombstones []string
	for _, module := range lock.Modules {
		switch module.Reason {
		case modkit.TombstoneReason:
			tombstones = append(tombstones, module.ID)
		case modkit.VerificationReason:
			verification = append(verification, module.ID)
		default:
			selected = append(selected, module.ID)
		}
	}
	sort.Strings(selected)
	sort.Strings(verification)
	sort.Strings(tombstones)

	// Floor. A reader that found nothing would make every equality below
	// vacuously true.
	require.NotEmpty(t, selected, "the committed lock has no selected rows; the decomposition cannot be derived")

	// The verification rows ARE the declaration, exactly. A row the intent
	// does not name is an install nothing asked for; a declaration with no row
	// is intent the engine ignored.
	require.Equal(t, project.VerificationInstalls, verification,
		"the lock's `verification` rows and gogogadget.json's verification_installs must be the same set")

	catalog, err := modkit.LoadCatalog(os.DirFS(root))
	require.NoError(t, err)
	full, ok := findShippedProfile(catalog, "full")
	require.True(t, ok, "the core catalog no longer ships ggg/profile/full")

	// The genesis is staged from the profile plus the project's OWN provider
	// and deployment choices, and from nothing else. Those two are project
	// decisions rather than profile members — `ggg/profile/full`'s
	// default_deployment is deploy-docker and this repository self-hosts on
	// deploy-fly — so taking the profile's defaults here would report a
	// one-module difference that is a correct choice, not a defect. Everything
	// else is re-derived: the explicit module set is the profile alone, and
	// exclude is computed the way previewNew computes it, so the committed
	// exclude list cannot smuggle a member in or out of the comparison.
	genesis := planFreshGenesis(t, root, catalog, full, project.Providers, project.Deployment)
	var fresh []string
	for _, module := range genesis.Lock.Modules {
		if module.Reason == modkit.TombstoneReason {
			continue
		}
		fresh = append(fresh, module.ID)
	}
	sort.Strings(fresh)

	// The load-bearing equality. Quote both numbers in the failure, because
	// the first question a reader asks is "by how much".
	require.Equal(t, fresh, selected,
		"the committed lock's selected set (%d rows) is not what a fresh ggg/profile/full genesis resolves (%d rows); "+
			"every difference is a module this repository has and its own tooling cannot reproduce",
		len(selected), len(fresh))
	require.Len(t, lock.Modules, len(fresh)+len(project.VerificationInstalls)+len(tombstones),
		"the lock's row count must be the full-profile closure plus the verification installs plus any live tombstone")

	// The prune invariant, stated where the arithmetic depends on it: a
	// tombstone is admissible only while it retains something. A migration
	// ledger is something; a module the catalog still publishes can be
	// re-added and would reuse the retained numbers. Neither, and the row is
	// a lock member that no operation can explain.
	published := make(map[string]bool, len(catalog.Modules))
	for _, module := range catalog.Modules {
		published[module.ID] = true
	}
	var dead []string
	for _, module := range lock.Modules {
		if module.Reason != modkit.TombstoneReason {
			continue
		}
		if len(module.Migrations) == 0 && !published[module.ID] {
			dead = append(dead, module.ID)
		}
	}
	require.Empty(t, dead,
		"these tombstones retain no migration ledger and name no published module, so they explain nothing and make the count above underivable: %v. "+
			"A bare `ggg sync` prunes them", dead)
}

// repoProject reads this repository's committed intent through the parser
// `ggg sync` uses, so a document this test accepts is one the engine accepts.
func repoProject(t *testing.T, root string) modkit.Project {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, modkit.ProjectFileName))
	require.NoError(t, err)
	project, err := modkit.ParseProject(raw)
	require.NoError(t, err)
	return project
}

// planFreshGenesis stages and resolves the project `ggg new --profile P
// --deployment D --provider ...` writes, and plans it offline against this
// repository's registry tree. It is planShippedProfileErr with the deployment
// as an argument rather than the profile's default: that helper's four callers
// are asserting about the profile's own defaults, and widening its signature
// for one caller with a different question would make their intent read as
// configurable when it is not.
func planFreshGenesis(t *testing.T, root string, catalog modkit.Catalog, profile modkit.Profile,
	providers map[string]modkit.ProviderSelections, deployment string) modkit.Plan {
	t.Helper()
	byID := make(map[string]modkit.Manifest, len(catalog.Modules))
	for _, module := range catalog.Modules {
		byID[module.ID] = module
	}
	exclude := []string{}
	for _, member := range profile.Members {
		module, ok := byID[member]
		if ok && member != deployment && len(module.Runtime.Deploy) > 0 {
			exclude = append(exclude, member)
		}
	}
	sort.Strings(exclude)

	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "go.mod"),
		[]byte("module example.com/fresh-genesis\n\ngo 1.26.6\n"), 0o644))
	intent, err := modkit.MarshalProject(modkit.Project{
		Schema:     2,
		Registries: []modkit.ProjectRegistry{{Namespace: "ggg", Source: "directory", Path: "."}},
		Modules:    []string{profile.ID},
		Exclude:    exclude,
		Providers:  providers,
		Deployment: deployment,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(target, modkit.ProjectFileName), intent, 0o644))

	engine := modkit.New(modkit.Options{
		Source:    modkit.DirectorySource{Root: root},
		Generator: modkit.RegistryGenerator{},
	})
	plan, err := engine.Plan(t.Context(), target, modkit.Operation{Kind: modkit.OpSync, Offline: true})
	require.NoErrorf(t, err, "a fresh %s genesis cannot be planned", profile.ID)
	return plan
}
