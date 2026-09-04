// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package gggcli

import (
	"context"
	"encoding/json"
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
