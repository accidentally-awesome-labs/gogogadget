// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository's
// catalog and payload bytes.

package modkit

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func selfHostCatalogAndFiles(t *testing.T) (Catalog, map[string][]byte) {
	t.Helper()
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	catalog, err := LoadCatalog(os.DirFS(repo))
	require.NoError(t, err)
	require.NotEmpty(t, catalog.Modules)

	files := map[string][]byte{}
	for _, module := range catalog.Modules {
		for _, file := range module.Files {
			content, readErr := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file.Target)))
			if readErr != nil {
				continue
			}
			files[file.Target] = content
		}
	}
	return catalog, files
}

// The whole tree agrees with the gate, and the gate is not vacuous.
//
// The measurement that decided whether this scan could ship: over every
// authored payload in this repository, the declared positions yield 275 route
// references and exactly ONE of them resolved to no declared pattern —
// ggg/page/dev-gallery's command-palette demo item pointing at `/app/settings`,
// a path no module in the catalog serves. So zero false positives, one true
// finding, which is now fixed.
//
// The two variants that were measured and REFUSED are the reason the alphabet
// looks the way it does. Scanning every path-shaped string literal instead of
// declared positions finds 715 strings of which 72 resolve to nothing —
// outbound Polar, Typesense, Neon, Ably and OTLP client paths, `strings.HasPrefix`
// prefixes, mux subtree registrations, and the CLI source that emits route
// patterns as data. Keying each position to an HTTP verb costs 13 further
// refusals, all false: the gallery's inert `hx-delete="/dev/gallery"`
// demonstrations point back at the page they are on, and ui.FormOpts carries an
// explicit Method field a position-to-verb table cannot see.
func TestTheRouteReferenceGateHoldsOverTheWholeTree(t *testing.T) {
	catalog, files := selfHostCatalogAndFiles(t)

	require.NoError(t, ValidateRouteReferences(catalog.Modules, catalog.Modules, files),
		"a committed payload targets a route no module in this catalog declares")
}

// The mutation, which is the half that matters: a scan that refuses nothing
// also has zero false positives. Deleting one real route declaration a template
// really targets has to refuse, and the refusal has to name BOTH sides — the
// module that renders the control and the fact that nothing declares its route.
//
// The route deleted here is ggg/page/admin-users' own index, which
// ggg/page/admin-overview names as a literal in three places: the search
// endpoint, the pager base and the clear-filter link. Those are ordinary
// same-surface references and stay literal; the twenty-six controls this slice
// found were the ones pointing ACROSS a module boundary at a route the closure
// can leave out.
func TestTheRouteReferenceGateRefusesADeletedRouteDeclaration(t *testing.T) {
	catalog, files := selfHostCatalogAndFiles(t)

	const (
		owner    = "ggg/page/admin-users"
		routeID  = "admin.users.index"
		consumer = "ggg/page/admin-overview"
	)
	mutated := make([]Manifest, 0, len(catalog.Modules))
	dropped := ""
	for _, module := range catalog.Modules {
		if module.ID == owner {
			kept := make([]RouteContribution, 0, len(module.Runtime.Routes))
			for _, route := range module.Runtime.Routes {
				if route.ID == routeID {
					dropped = route.Pattern
					continue
				}
				kept = append(kept, route)
			}
			module.Runtime.Routes = kept
		}
		mutated = append(mutated, module)
	}
	require.NotEmptyf(t, dropped, "%s no longer declares %s, so this mutation proves nothing", owner, routeID)

	err := ValidateRouteReferences(mutated, mutated, files)
	require.Error(t, err)
	// Both sides, named: the module that renders the control, and the target
	// that nothing declares.
	require.ErrorContains(t, err, consumer)
	require.ErrorContains(t, err, dropped)
	require.ErrorContains(t, err, "no installed module declares")
}

// The other side of the same mutation, and the proof the remedy works: deleting
// the route a GATED control targets refuses nothing, because there is no
// literal left to dangle and the control simply stops rendering. This is what a
// project that deselects ggg/workflow/appearance gets, and it is why the five
// known cases could be fixed without a waiver list.
func TestDeletingAGatedRouteDeclarationPlansClean(t *testing.T) {
	catalog, files := selfHostCatalogAndFiles(t)

	const (
		owner   = "ggg/workflow/appearance"
		routeID = "appearance.set-theme"
	)
	mutated := make([]Manifest, 0, len(catalog.Modules))
	dropped := ""
	for _, module := range catalog.Modules {
		if module.ID == owner {
			kept := make([]RouteContribution, 0, len(module.Runtime.Routes))
			for _, route := range module.Runtime.Routes {
				if route.ID == routeID {
					dropped = route.Pattern
					continue
				}
				kept = append(kept, route)
			}
			module.Runtime.Routes = kept
		}
		mutated = append(mutated, module)
	}
	require.NotEmptyf(t, dropped, "%s no longer declares %s, so this mutation proves nothing", owner, routeID)

	require.NoError(t, ValidateRouteReferences(mutated, mutated, files),
		"a gated control must not dangle when its route is gone")
}

// Every route id a payload gates or resolves on exists in the PUBLISHED
// catalog, and takes the arguments the call site passes.
//
// This is a self-host assertion rather than a plan gate because a derivative's
// catalog is not the publisher's: `ggg new` copies the core registry pruned to
// what the chosen profile can reach, so ggg/profile/minimal's copy carries no
// ggg/page/admin-flag-detail and a plan-time existence check would refuse the
// very gate that page's absence is the reason for. Measured refusing exactly
// that. Here the catalog IS the published one, so a mistyped id — a control
// that would silently never render, invisible at every other layer — is caught
// where it is authored.
func TestEveryGatedRouteIDExistsInThePublishedCatalog(t *testing.T) {
	catalog, files := selfHostCatalogAndFiles(t)

	declared := declaredRoutePatternsByID(catalog.Modules)
	require.NotEmpty(t, declared)

	seen := 0
	var unknown []string
	for _, module := range catalog.Modules {
		for _, file := range module.Files {
			if !isRouteReferencingPayload(file) {
				continue
			}
			content, ok := files[file.Target]
			if !ok {
				continue
			}
			for _, ref := range routeIDReferences(stripCommentsKeepingStrings(content)) {
				seen++
				pattern, known := declared[ref.id]
				if !known {
					unknown = append(unknown, ref.id+" at "+file.Target)
					continue
				}
				if ref.args >= 0 {
					require.Equalf(t, routePatternWildcards(pattern), ref.args,
						"%s:%d resolves %s with the wrong number of path arguments for %s",
						file.Target, ref.line, ref.id, pattern)
				}
			}
		}
	}
	sort.Strings(unknown)
	require.Empty(t, unknown, "route ids no module in this catalog declares")
	// Not vacuous: the five known controls plus the twenty this slice found are
	// all gated, so the tree has real call sites to check.
	require.Greaterf(t, seen, 20, "only %d route-id references found; the scan is not seeing the gates", seen)
}
