// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package gggcli

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `ggg new --registry github:OWNER/REPO` fetches this tree and verifies it
// against coreRegistryPublicKey, so registry.snapshot.sig is a published
// artifact of this repository, not a local build leftover. It was gitignored on
// the opposite theory, and every genesis from GitHub refused with
// "read registry.snapshot.sig: no such file or directory" — no adoption path
// worked at all. This test is the gate: it fails if the signature is missing,
// stale relative to registry.snapshot.json, or produced by a key the shipped
// CLI does not pin.
func TestCommittedSnapshotVerifiesUnderThePinnedCoreKey(t *testing.T) {
	root := repoRootFromTest(t)
	digest, err := modkit.VerifyRegistrySnapshot(root, coreRegistryPublicKey)
	if err != nil {
		t.Fatalf("the committed core snapshot does not verify under the pinned key: %v\n"+
			"remedy: ggg registry build && ggg registry sign --dir . --key-file <core signing key>", err)
	}
	if digest == "" {
		t.Fatal("verification returned an empty digest, so nothing was checked")
	}
}

// The fixtures are only a statement about this generator if they are
// byte-for-byte what it emits. Set GGG_UPDATE_RESOURCE_FIXTURE=1 to rewrite
// them after a deliberate change, then re-run `go run ./cmd/ggg registry
// validate`.
func TestCreateResourceMatchesExampleFixtures(t *testing.T) {
	root := repositoryRoot(t)
	for _, fixture := range exampleResourceFixtures {
		t.Run(fixture.module, func(t *testing.T) {
			files, _ := buildResource(t, fixture.mutation)
			dir := resourceFixtureRegistry + "/registry/modules/workflow/" + fixture.module

			// Checked before either branch: the update mode writes every key
			// it is given, so a generator that started emitting outside the
			// fixture directory would scatter files through the tree and only
			// be caught on the next assert run.
			for name := range files {
				if !strings.HasPrefix(name, dir+"/") {
					t.Fatalf("the generator wrote %s, which is outside the fixture directory", name)
				}
			}

			if os.Getenv("GGG_UPDATE_RESOURCE_FIXTURE") != "" {
				if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
					t.Fatal(err)
				}
				for name, body := range files {
					full := filepath.Join(root, filepath.FromSlash(name))
					if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(full, body, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				t.Logf("rewrote %d fixture file(s) under %s", len(files), dir)
				return
			}

			for name, want := range files {
				got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
				if err != nil {
					t.Fatalf("read %s: %v", name, err)
				}
				if string(got) != string(want) {
					t.Fatalf("%s differs from what the generator emits; "+
						"re-run with GGG_UPDATE_RESOURCE_FIXTURE=1 if the change is deliberate", name)
				}
			}
			entries := walkFixture(t, filepath.Join(root, filepath.FromSlash(dir)), dir)
			expected := make([]string, 0, len(files))
			for name := range files {
				expected = append(expected, name)
			}
			sort.Strings(expected)
			if !slices.Equal(entries, expected) {
				t.Fatalf("fixture files = %v, want exactly %v", entries, expected)
			}

			// The fixture is only installable if its own manifest parses as
			// one, which is the same check the catalog loader runs.
			var document struct {
				Schema int             `json:"schema"`
				Module modkit.Manifest `json:"module"`
			}
			if err := json.Unmarshal(files[dir+"/module.json"], &document); err != nil {
				t.Fatalf("fixture manifest does not parse: %v", err)
			}
			if got, want := document.Module.ID, "ggg/workflow/"+fixture.module; got != want {
				t.Fatalf("fixture module id = %q, want %q", got, want)
			}
			if err := modkit.ValidateManifest(document.Module); err != nil {
				t.Fatalf("fixture manifest is invalid: %v", err)
			}
		})
	}
}

func TestExternalTemplateCommandDoesNotShadowABuiltIn(t *testing.T) {
	module := templateAdapterManifest(t)
	require.Len(t, module.Runtime.CLI, 1)
	command := module.Runtime.CLI[0]

	assert.False(t, IsReservedName(command.Name),
		"contributed command %q collides with a built-in and would be skipped", command.Name)
	assert.NotEmpty(t, command.Summary)

	// The contributed command joins the real table and is dispatchable by
	// that name, which is what "installing the module adds a command" means.
	table, conflicts := commandTable([]ContributedCommand{{
		Spec:    CommandSpec{Name: command.Name, Summary: command.Summary, Usage: "ggg " + command.Name, SourceModule: module.ID},
		Handler: func(context.Context, CommandContext, []string) (Result, error) { return Result{}, nil },
	}})
	assert.Empty(t, conflicts)
	spec, ok := lookupSpec(table, command.Name)
	require.True(t, ok)
	assert.Equal(t, module.ID, spec.SourceModule)
}

// The template's declared verification commands are the ones `ggg info`
// prints, which is the promise the generated module reference repeats.
func TestExternalTemplateVerificationCommandsAreRunnable(t *testing.T) {
	module := templateAdapterManifest(t)
	commands := modkit.VerificationCommands(module)
	require.NotEmpty(t, commands)
	assert.Contains(t, commands[0], "go test -count=1 ./internal/gadgetworks/ledger")
}

// A generated module declares a contract RANGE, and the range has to include
// the contract the core registry publishes right now: a module created against
// this catalog must resolve against it. coreContractMaxima is a constant, so
// without this gate a core contract bump would leave `ggg create resource`
// emitting [1,1] and every generated slice refusing at the next sync.
func TestGeneratedRequirementsCoverCoreContracts(t *testing.T) {
	root := repositoryRoot(t)
	for id, declared := range coreContractMaxima {
		name := id[strings.LastIndex(id, "/")+1:]
		kind := strings.Split(id, "/")[1]
		path := filepath.Join(root, "registry", "modules", kind, name, "module.json")
		raw, err := os.ReadFile(path)
		require.NoErrorf(t, err, "coreContractMaxima names %s, which has no manifest", id)
		var document struct {
			Module struct {
				Contract int `json:"contract"`
			} `json:"module"`
		}
		require.NoError(t, json.Unmarshal(raw, &document))
		assert.Equalf(t, document.Module.Contract, declared,
			"%s publishes contract %d but the resource generator declares a maximum of %d",
			id, document.Module.Contract, declared)
	}

	// The other direction: a core module past contract 1 with no entry here
	// would be required as [1,1] by every generated module.
	for _, kind := range []string{"system", "workflow", "page", "component", "element"} {
		entries, err := os.ReadDir(filepath.Join(root, "registry", "modules", kind))
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		for _, entry := range entries {
			raw, readErr := os.ReadFile(filepath.Join(root, "registry", "modules", kind, entry.Name(), "module.json"))
			if readErr != nil {
				continue
			}
			var document struct {
				Module struct {
					ID       string `json:"id"`
					Contract int    `json:"contract"`
				} `json:"module"`
			}
			require.NoError(t, json.Unmarshal(raw, &document))
			if document.Module.Contract <= 1 {
				continue
			}
			if !generatedRequirementIDs[document.Module.ID] {
				continue
			}
			assert.Containsf(t, coreContractMaxima, document.Module.ID,
				"%s publishes contract %d and is required by generated modules, so it needs a coreContractMaxima entry",
				document.Module.ID, document.Module.Contract)
		}
	}
}

// generatedRequirementIDs is every module id `ggg create resource` can name.
var generatedRequirementIDs = map[string]bool{
	"ggg/system/database": true, "ggg/system/security": true, "ggg/system/server": true,
	"ggg/system/identity": true, "ggg/system/i18n": true, "ggg/system/organizations": true,
	"ggg/system/api": true, "ggg/system/search": true, "ggg/workflow/openapi-contract": true,
}

// compiledSourceTrees are the directories that hold this repository's Go
// packages. registry/ is payload bytes and testdata is not compiled, so
// neither is a package that could have tests.
var compiledSourceTrees = []string{"cmd", "content", "internal"}

// packagesWithoutTests is every Go package in this repository that declares no
// test file, with the reason it declares none.
//
// It exists because runAccountedGoTest structurally cannot see one. `go test
// -json` reports a package with no test files as a package-level skip with no
// Test field: nothing is tallied, no total moves, and the only observable is a
// package count no guard compares against anything. Deleting every _test.go
// from three packages was measured to produce `tests: 1 passed, 0 skipped, 0
// inapplicable, 0 failed across 4 packages` and refuseEmptyRun() == nil.
//
// The list is a hand-written mirror and is checked against the tree in both
// directions below, so it goes stale loudly: a package that loses its tests
// fails here by name, and a package that gains them fails here as a dead row.
var packagesWithoutTests = map[string]string{
	"cmd/server":                   "the binary's whole behaviour is internal/modules boot, exercised by e2e and smoke",
	"content":                      "an embed.FS declaration; internal/content owns every reader test",
	"internal/billing/contract":    "the shared contract TABLE itself, run by each billing adapter's package",
	"internal/cache":               "the cache.Store seam interface; internal/cache/memory and /redis run the behaviour",
	"internal/database":            "the ggg/database slot contract: type aliases onto pgx and sqlc",
	"internal/database/ops/docker": "shells pg_dump/pg_restore; covered by ggg db backup/restore-drill against a live stack",
	"internal/db/sqlc":             "sqlc output, regenerated and drift-refused by make check",
	"internal/deploy/docker":       "a DeployTarget over the docker CLI; exercised through ggg deploy against a live daemon",
	"internal/deploy/fly":          "a DeployTarget over the fly CLI; exercised through ggg deploy against a live account",
	"internal/gggcli/commands":     "the command table's leaf handlers; internal/gggcli owns the dispatch tests",
	"internal/identity/contract":   "the shared contract TABLE itself, run by each identity adapter's package",
	"internal/mail/contract":       "the shared contract TABLE itself, run by each mail adapter's package",
	"internal/notifications":       "the notify slot contract; internal/notifications/postgres and /knock run the behaviour",
	"internal/provision/neon":      "a ProviderProvisioner over the Neon API; exercised through ggg provider provision",
	"internal/remote":              "the typed provisioner/deployer/database-operator interfaces and their value types",
	"internal/storage/contract":    "the shared contract TABLE itself, run by each storage adapter's package",
}

// Every Go package in the compiled trees must declare a test file or say why
// it does not.
func TestEveryGoPackageDeclaresTestsOrSaysWhyNot(t *testing.T) {
	root := repositoryRoot(t)
	packages := map[string]bool{}
	tested := map[string]bool{}
	for _, tree := range compiledSourceTrees {
		err := filepath.WalkDir(filepath.Join(root, tree), func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" || entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".go") {
				return nil
			}
			dir, relErr := filepath.Rel(root, filepath.Dir(name))
			if relErr != nil {
				return relErr
			}
			dir = filepath.ToSlash(dir)
			packages[dir] = true
			if strings.HasSuffix(entry.Name(), "_test.go") {
				tested[dir] = true
			}
			return nil
		})
		require.NoError(t, err)
	}
	// The floor. A walk that found the wrong root would report no packages
	// at all and every row below would read as a dead exemption.
	require.GreaterOrEqual(t, len(packages), 60,
		"only %d Go packages were found under %v; the walk has collapsed, not the tree", len(packages), compiledSourceTrees)

	var undeclared []string
	for dir := range packages {
		if tested[dir] {
			continue
		}
		if _, recorded := packagesWithoutTests[dir]; !recorded {
			undeclared = append(undeclared, dir)
		}
	}
	sort.Strings(undeclared)
	require.Empty(t, undeclared,
		"these packages declare no test file and packagesWithoutTests records no reason: %v.\n"+
			"The accounted runner reports such a package as `?   pkg` and counts nothing, so a suite whose tests "+
			"were deleted reads exactly like one that ran them. Add a test, or record why there is none.", undeclared)

	var dead []string
	for dir := range packagesWithoutTests {
		if !packages[dir] {
			dead = append(dead, dir+" (no such package)")
			continue
		}
		if tested[dir] {
			dead = append(dead, dir+" (now declares tests)")
		}
	}
	sort.Strings(dead)
	require.Empty(t, dead,
		"these packagesWithoutTests rows no longer describe the tree: %v.\nDelete each one; an exemption that "+
			"outlives its reason is how the next one gets added without argument.", dead)
}

// inapplicableSkipSites is every place in this repository that writes
// InapplicableSkipMarker, keyed by `<path>:<enclosing function>`.
//
// The marker is the one self-service exemption in the accounted gate: a skip
// carrying it is counted apart and never refused, and NOTHING guarded who may
// write it. It is the `1 inapplicable` in every run this repository reports,
// and a suite that quietly marked itself inapplicable would report the same
// clean line as one that ran.
//
// So the sites are enumerated here with the claim each makes, and the walk
// below refuses a site with no row and a row with no site. The reason a
// marker gives at runtime is enforced separately, by the gate itself:
// goTestAccount.unreasonedMarkerRefusal refuses a marker with nothing after it.
var inapplicableSkipSites = map[string]string{
	"internal/config/config_test.go:TestResolvedValuesCarryTheirProvenance":                                        "a derivative on a managed database publishes no local Postgres, so there is nothing to derive and nothing to supply",
	"internal/config/config_test.go:derivedFor":                                                                    "the shared helper those derivation cases skip through, for the same reason",
	"internal/gggcli/profile_genesis_test.go:TestEveryShippedProfileCreatesAProjectThatIsSyncClean":                "the four-profile genesis sweep needs the network and 180s; CI's `profiles` job owns it and sets GGG_GENESIS_SWEEP",
	"internal/gggcli/era_walk_selfhost_test.go:TestOldEraDerivativesWalkToCurrent":                                 "the two-era cross-release walk builds era binaries, runs era genesis and walks both derivatives to current (~165s warm, minutes cold, network for the core GitHub tarballs); CI's `era-walk` workflow owns it and sets GGG_ERA_WALK",
	"internal/modkit/redproof_selfhost_test.go:TestRedProofGate":                                                   "the mutation-proof gate needs minutes and a whole-tree scratch copy; GGG_REDPROOF=off disables it where that is impractical, and the corpus and inventory floors are still checked before the skip",
	"internal/canary/live_canary_selfhost_test.go:TestManagedTargetLiveCanaries":                                   "the managed-target live canaries call every maintained managed provider with credentials this repository holds and no contributor does; the suite gate and every per-provider row skip live in that one function (each row bounded at 30s, so the suite is bounded at that times the row count); CI's `live-canary` workflow owns it and sets GGG_LIVE_CANARY",
	"internal/storage/s3/minio_test.go:TestS3StoreAgainstMinIO":                                                    "the MinIO run needs a real S3 server on STORAGE_S3_ENDPOINT (measured 0.03s warm); CI's `test` job owns it and sets STORAGE_S3_ENDPOINT beside the minio container it starts",
	"internal/mail/smtp/mailpit_test.go:TestSMTPSenderAgainstMailpit":                                              "the Mailpit run needs a real SMTP server plus its HTTP read-back API on SMTP_HOST/SMTP_PORT (measured 0.02s warm); CI's `test` job owns it and sets SMTP_HOST beside the mailpit service container",
	"internal/modkit/container_declarations_selfhost_test.go:TestEveryDeclaredContainerIsExecutable":               "executing every declared container needs Docker and the network to resolve each pinned digest and run each declared health probe in its image (measured 19s warm, 16s of it registry round-trips); CI's `test` job owns it and sets GGG_CONTAINER_DECLARATIONS=1",
	"internal/modkit/revision_baseline_selfhost_test.go:revisionBaselineHistory":                                   "the release-baseline revision guard reads the last released registry.snapshot.json out of git; a shallow clone or a tree with no git history cannot reach it, and the shared history check is where both that refusal and its remedy live",
	"internal/modkit/revision_baseline_selfhost_test.go:revisionBaselineTag":                                       "the same guard needs a release tag reachable from HEAD to have a baseline at all; a tagless or tag-pruned clone is told to fetch the tags rather than reported as a pass",
	"internal/modkit/revision_baseline_selfhost_test.go:TestReleaseBaselineGateRefusesTheV0260IntegrationIncident": "the founding-incident replay needs tag v0.25.0 and the pre-bump commit; a clone without those refs cannot materialise the tree the guard was built from",
}

// Every InapplicableSkipMarker site must be declared, and every declaration
// must still name a site.
func TestEveryInapplicableSkipSiteIsDeclared(t *testing.T) {
	root := repositoryRoot(t)
	found := map[string]bool{}
	for _, tree := range compiledSourceTrees {
		err := filepath.WalkDir(filepath.Join(root, tree), func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" || entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			raw, readErr := os.ReadFile(name)
			if readErr != nil {
				return readErr
			}
			if !bytes.Contains(raw, []byte(InapplicableSkipMarker)) && !bytes.Contains(raw, []byte(inapplicableMarkerSymbol)) {
				return nil
			}
			rel, relErr := filepath.Rel(root, name)
			if relErr != nil {
				return relErr
			}
			for _, function := range markerFunctions(t, name, raw) {
				found[filepath.ToSlash(rel)+":"+function] = true
			}
			return nil
		})
		require.NoError(t, err)
	}
	// The floor. The marker is load-bearing — it is what keeps the CI skip
	// refusal from shipping a false refusal into every derivative — so a walk
	// that finds none of it has broken, not the tree.
	require.NotEmpty(t, found, "no InapplicableSkipMarker site was found under %v; the walk has collapsed, not the tree", compiledSourceTrees)

	var undeclared, dead []string
	for site := range found {
		if _, recorded := inapplicableSkipSites[site]; !recorded {
			undeclared = append(undeclared, site)
		}
	}
	for site := range inapplicableSkipSites {
		if !found[site] {
			dead = append(dead, site)
		}
	}
	sort.Strings(undeclared)
	sort.Strings(dead)
	require.Empty(t, undeclared,
		"these sites exempt themselves from the accounted gate's skip refusal and inapplicableSkipSites records nothing about them: %v.\n"+
			"Record what each one claims, or make the skip a failure at its source.", undeclared)
	require.Empty(t, dead,
		"these inapplicableSkipSites rows name no marker site any more: %v.\nDelete each one.", dead)
}

// inapplicableMarkerSymbol is the constant's own name. A site may write the
// marker either way, and reading only one of the two forms is how a walk
// reports a clean sweep over half a tree.
const inapplicableMarkerSymbol = "InapplicableSkipMarker"

// markerFunctions names the top-level functions in one file that SKIP with
// InapplicableSkipMarker. Parsed rather than matched on the nearest preceding
// `func`, so a marker inside a closure is attributed to the declaration that
// owns it, and scoped to a `Skip`/`Skipf` argument, so a test that merely
// mentions the constant — gate_test.go builds event fixtures out of it — is
// not recorded as exempting itself.
func markerFunctions(t *testing.T, name string, raw []byte) []string {
	t.Helper()
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, name, raw, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	carriesMarker := func(node ast.Node) bool {
		found := false
		ast.Inspect(node, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.BasicLit:
				if node.Kind == token.STRING && strings.Contains(node.Value, InapplicableSkipMarker) {
					found = true
				}
			case *ast.Ident:
				if node.Name == inapplicableMarkerSymbol {
					found = true
				}
			}
			return true
		})
		return found
	}
	var out []string
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		skips := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Skip" && selector.Sel.Name != "Skipf") {
				return true
			}
			for _, argument := range call.Args {
				if carriesMarker(argument) {
					skips = true
				}
			}
			return true
		})
		if skips {
			out = append(out, function.Name.Name)
		}
	}
	sort.Strings(out)
	return out
}
