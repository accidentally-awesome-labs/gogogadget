package modkit_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/stretchr/testify/require"
)

// Every profile the catalog advertises has to be able to produce a project, and
// until this test existed nothing checked that. Three of the five profiles
// shipped then could not: `web` and the since-deleted `api` were refused by
// the resolver's own provider-slot check, and `minimal` reached `go mod tidy`
// and died there because `internal/web/routes.go` imports `internal/api`
// while no module in the closure provided it.
//
// The form is deliberate. A real `ggg new` per profile is the complete proof
// and costs minutes each — five tool installs, five templ/sqlc/Tailwind runs,
// five `go build ./...` over ~300 packages. This plans each profile instead:
// seconds, no toolchain, no network, no Docker. What it proves is everything
// the planner and the generators decide — provider-slot parity, adapter and
// target legality, the deploy selection, dependency ranges, ownership,
// navigation and chrome ordering, the OpenAPI document, every generated
// aggregate — plus the two coherence properties that the three failures
// actually tripped over, asserted below against the planned bytes.
//
// What it does not prove is that the external tools succeed: templ, sqlc and
// Tailwind never run here, so a payload that parses as Go but fails templ, or
// a query sqlc rejects for a reason other than a missing table, is out of
// scope. The five real `ggg new` runs stay the release measurement; this is
// the gate that fails in `go test` on the commit that breaks a profile.
func TestEveryShippedProfileResolvesIntoACoherentProject(t *testing.T) {
	root := repoRoot(t)
	catalog, err := modkit.LoadCatalog(os.DirFS(root))
	require.NoError(t, err)
	require.NotEmpty(t, catalog.Profiles, "the core catalog advertises no profiles")

	for _, profile := range catalog.Profiles {
		t.Run(profile.Name, func(t *testing.T) {
			plan := planShippedProfile(t, root, catalog, profile, profile.ProviderDefaults)
			assertPlannedImportsAreProvided(t, plan)
			assertPlannedMigrationsReferenceCreatedTables(t, plan)
		})
	}
}

// TestShippedProfileGateCatchesABrokenProfile is the mutation proof: the gate
// above only means something if a wrong profile fails it. Each mutation is one
// of the two shapes a hand-maintained profile actually drifts into — a slot the
// closure declares with no selection, and a selection for a slot the closure
// never declares.
func TestShippedProfileGateCatchesABrokenProfile(t *testing.T) {
	root := repoRoot(t)
	catalog, err := modkit.LoadCatalog(os.DirFS(root))
	require.NoError(t, err)
	profile, ok := findShippedProfile(catalog, "minimal")
	require.True(t, ok, "the core catalog no longer ships ggg/profile/minimal")

	t.Run("a declared slot with no selection", func(t *testing.T) {
		providers := map[string]modkit.ProviderSelections{}
		for slot, selections := range profile.ProviderDefaults {
			providers[slot] = selections
		}
		delete(providers, "ggg/mail")
		_, err := planShippedProfileErr(t, root, catalog, profile, providers)
		require.ErrorContains(t, err, "project providers must exactly match selected provider slots")
		require.ErrorContains(t, err, "missing [ggg/mail]")
	})

	t.Run("a selection for a slot nothing declares", func(t *testing.T) {
		providers := map[string]modkit.ProviderSelections{}
		for slot, selections := range profile.ProviderDefaults {
			providers[slot] = selections
		}
		providers["ggg/invented"] = providers["ggg/mail"]
		_, err := planShippedProfileErr(t, root, catalog, profile, providers)
		require.ErrorContains(t, err, "unselected [ggg/invented]")
	})
}

func findShippedProfile(catalog modkit.Catalog, name string) (modkit.Profile, bool) {
	for _, profile := range catalog.Profiles {
		if profile.Name == name {
			return profile, true
		}
	}
	return modkit.Profile{}, false
}

// planShippedProfile stages the project `ggg new --profile` would write and
// plans it. The staged intent is built the same way previewNew builds it:
// the profile is the only explicit module, every deploy member other than the
// chosen deployment is excluded, and the provider selections are the ones under
// test.
func planShippedProfileErr(t *testing.T, root string, catalog modkit.Catalog, profile modkit.Profile,
	providers map[string]modkit.ProviderSelections) (modkit.Plan, error) {
	t.Helper()
	byID := make(map[string]modkit.Manifest, len(catalog.Modules))
	for _, module := range catalog.Modules {
		byID[module.ID] = module
	}
	exclude := []string{}
	for _, member := range profile.Members {
		module, ok := byID[member]
		if ok && member != profile.DefaultDeployment && len(module.Runtime.Deploy) > 0 {
			exclude = append(exclude, member)
		}
	}
	sort.Strings(exclude)

	target := t.TempDir()
	modulePath := "example.com/profile-gate"
	require.NoError(t, os.WriteFile(filepath.Join(target, "go.mod"),
		[]byte("module "+modulePath+"\n\ngo 1.26.6\n"), 0o644))
	intent, err := modkit.MarshalProject(modkit.Project{
		Schema:     2,
		Registries: []modkit.ProjectRegistry{{Namespace: "ggg", Source: "directory", Path: "."}},
		Modules:    []string{profile.ID},
		Exclude:    exclude,
		Providers:  providers,
		Deployment: profile.DefaultDeployment,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(target, modkit.ProjectFileName), intent, 0o644))

	// The registry is resolved out of this repository while the plan is rooted
	// at the staging tree, which is exactly how `ggg new --registry directory:.`
	// resolves a core catalog into a destination outside it.
	engine := modkit.New(modkit.Options{
		Source:    modkit.DirectorySource{Root: root},
		Generator: modkit.RegistryGenerator{},
	})
	return engine.Plan(t.Context(), target, modkit.Operation{Kind: modkit.OpSync, Offline: true})
}

func planShippedProfile(t *testing.T, root string, catalog modkit.Catalog, profile modkit.Profile,
	providers map[string]modkit.ProviderSelections) modkit.Plan {
	t.Helper()
	plan, err := planShippedProfileErr(t, root, catalog, profile, providers)
	require.NoErrorf(t, err, "profile %s cannot be planned into a project", profile.ID)
	return plan
}

var goImportLine = regexp.MustCompile(`^\s*(?:[\w.]+\s+)?"([^"]+)"`)

// plannedImports parses the import declarations out of one planned Go or templ
// payload. It reads the planned bytes rather than the registry source because
// the module path has already been rewritten by then, which is the form the
// created project actually compiles.
func plannedImports(content []byte) []string {
	var out []string
	lines := strings.Split(string(content), "\n")
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case inBlock:
			if trimmed == ")" {
				inBlock = false
				continue
			}
			if match := goImportLine.FindStringSubmatch(line); match != nil {
				out = append(out, match[1])
			}
		case trimmed == "import (":
			inBlock = true
		case strings.HasPrefix(trimmed, "import "):
			if match := goImportLine.FindStringSubmatch(strings.TrimPrefix(trimmed, "import")); match != nil {
				out = append(out, match[1])
			}
		}
	}
	return out
}

// assertPlannedImportsAreProvided is the `go mod tidy` precondition, checked
// without running it: every package a planned payload imports from the
// project's own module path must be a directory something in the plan writes.
// `minimal` shipped for a release violating this — `internal/web/routes.go`
// imports `internal/api`, no module in that closure owned the package, and the
// only symptom was `go mod tidy: exit status 1` after genesis had already
// written the whole tree.
func assertPlannedImportsAreProvided(t *testing.T, plan modkit.Plan) {
	t.Helper()
	prefix := plan.ModulePath + "/"
	provided := map[string]bool{}
	declare := func(path string) {
		if strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".templ") {
			provided[filepath.ToSlash(filepath.Dir(path))] = true
		}
	}
	for _, change := range plan.Changes {
		declare(change.Path)
	}
	// The generated aggregates are not plan changes — the generator renders
	// them from the plan — and cmd/ggg imports one of them, so a plan whose
	// only source of packages was Changes reported internal/gggcli/commands
	// missing on a tree that compiles.
	for _, path := range (modkit.RegistryGenerator{}).GeneratedPaths(plan) {
		declare(path)
	}
	// sqlc writes internal/db/sqlc from the declared queries. It is an
	// external-tool output rather than anything the plan describes, so the
	// package it creates is declared here rather than discovered; a missing
	// tool output is what verifyGenesisCompiles catches.
	provided["internal/db/sqlc"] = true
	missing := map[string][]string{}
	for _, change := range plan.Changes {
		if !strings.HasSuffix(change.Path, ".go") && !strings.HasSuffix(change.Path, ".templ") {
			continue
		}
		for _, imported := range plannedImports(change.Content) {
			if !strings.HasPrefix(imported, prefix) {
				continue
			}
			pkg := strings.TrimPrefix(imported, prefix)
			if provided[pkg] {
				continue
			}
			missing[pkg] = append(missing[pkg], change.Path)
		}
	}
	for _, pkg := range sortedStrings(missing) {
		sort.Strings(missing[pkg])
		t.Errorf("no planned module provides package %s, imported by %s",
			pkg, strings.Join(missing[pkg], ", "))
	}
}

var (
	sqlCreateTable = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`)
	sqlAlterTable  = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`)
	sqlReferences  = regexp.MustCompile(`(?i)REFERENCES\s+([a-z_][a-z0-9_]*)\s*\(`)
)

// assertPlannedMigrationsReferenceCreatedTables is the sqlc precondition. The
// migration set is the installed union and every module's migrations run in
// every project, so a migration that names a table another module creates only
// works in a closure containing that module. `0020_provider_neutral_ids`
// renames columns on fifteen tables including `files` and `schedules`, and in
// the `web` closure those two tables did not exist — which surfaced as
// `sqlc generate: exit status 1` mid-genesis, three steps after the decision
// that caused it.
//
// This is a textual reference check over the planned migration bodies, not an
// execution: it catches a named table nothing creates, which is the shape that
// broke three profiles, and says nothing about column types or constraints.
func assertPlannedMigrationsReferenceCreatedTables(t *testing.T, plan modkit.Plan) {
	t.Helper()
	created := map[string]bool{}
	bodies := map[string]string{}
	for _, change := range plan.Changes {
		if !strings.HasPrefix(change.Path, "internal/db/migrations/") {
			continue
		}
		body := string(change.Content)
		bodies[change.Path] = body
		for _, match := range sqlCreateTable.FindAllStringSubmatch(body, -1) {
			created[strings.ToLower(match[1])] = true
		}
	}
	missing := map[string][]string{}
	for _, path := range sortedStrings(bodies) {
		for _, pattern := range []*regexp.Regexp{sqlAlterTable, sqlReferences} {
			for _, match := range pattern.FindAllStringSubmatch(bodies[path], -1) {
				table := strings.ToLower(match[1])
				if created[table] {
					continue
				}
				missing[table] = appendOnce(missing[table], path)
			}
		}
	}
	for _, table := range sortedStrings(missing) {
		t.Errorf("migration(s) %s reference table %q, which no planned migration creates",
			strings.Join(missing[table], ", "), table)
	}
}

func appendOnce(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func sortedStrings[V any](values map[string]V) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

type documentedProfile struct {
	members int
	closure int
	slots   int
}

var (
	docProfileRow  = regexp.MustCompile("^\\|\\s*`([a-z][a-z-]*)`\\s*\\|\\s*(\\d+)\\s*\\|\\s*(\\d+)\\s*\\|\\s*(\\d+)\\s*\\|")
	describedCount = regexp.MustCompile(`(\d+) modules`)
)

// A profile count that nothing asserts goes stale silently, and this one did:
// the table in content/docs/getting-started.md said `minimal` was 150 modules
// and `web` 223 while they resolved 178 and 254, and it advertised a fifth
// profile — `api` — whose closure was identical to `web`'s, module for module.
// Deleting that name only helps if the table cannot re-acquire a row the
// catalog does not back, so this test owns both directions: every shipped
// profile has a row, and every row has a shipped profile.
//
// The closure column is `len(plan.Resolved)`, which is the installed set the
// planner resolved — the same count a real `ggg new --registry directory:.`
// writes into the destination's lock. The manifest descriptions advertise the
// same number in prose, so they are checked against the same source of truth
// rather than against the table.
func TestTheDocumentedProfileTableMatchesWhatTheProfilesResolveTo(t *testing.T) {
	root := repoRoot(t)
	catalog, err := modkit.LoadCatalog(os.DirFS(root))
	require.NoError(t, err)
	require.NotEmpty(t, catalog.Profiles)

	_, stillPublished := findShippedProfile(catalog, "api")
	require.False(t, stillPublished,
		"ggg/profile/api is back in the catalog; it resolved to ggg/profile/web's closure module for module, "+
			"and gggcli.retiredProfiles refuses the name on the assumption it is gone")

	body, err := os.ReadFile(filepath.Join(root, "content", "docs", "getting-started.md"))
	require.NoError(t, err)
	documented := map[string]documentedProfile{}
	for _, line := range strings.Split(string(body), "\n") {
		match := docProfileRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		documented[match[1]] = documentedProfile{
			members: mustAtoi(t, match[2]), closure: mustAtoi(t, match[3]), slots: mustAtoi(t, match[4]),
		}
	}
	require.NotEmpty(t, documented, "the profile table is gone from content/docs/getting-started.md")

	for _, profile := range catalog.Profiles {
		row, ok := documented[profile.Name]
		require.Truef(t, ok, "profile %s has no row in the documented profile table", profile.ID)
		delete(documented, profile.Name)
		plan := planShippedProfile(t, root, catalog, profile, profile.ProviderDefaults)
		require.Equalf(t, len(profile.Members), row.members,
			"%s names %d members; the documented table says %d", profile.ID, len(profile.Members), row.members)
		require.Equalf(t, len(plan.Resolved), row.closure,
			"%s resolves %d modules; the documented table says %d", profile.ID, len(plan.Resolved), row.closure)
		require.Equalf(t, len(profile.RequiredProviderSlots), row.slots,
			"%s requires %d slots; the documented table says %d", profile.ID, len(profile.RequiredProviderSlots), row.slots)
		stated := describedCount.FindStringSubmatch(profile.Description)
		require.NotNilf(t, stated, "%s's description states no module count", profile.ID)
		require.Equalf(t, len(plan.Resolved), mustAtoi(t, stated[1]),
			"%s resolves %d modules; its own description says %s", profile.ID, len(plan.Resolved), stated[1])
	}
	require.Emptyf(t, documented,
		"the documented profile table advertises profile(s) this catalog does not publish: %v", sortedStrings(documented))
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	number, err := strconv.Atoi(value)
	require.NoError(t, err)
	return number
}
