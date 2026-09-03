// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The example manifests are hand-authored, and `ggg registry build` deliberately
// does not touch them: it scans registry/modules, which is the shipped catalog,
// not this separate registry. So editing a payload without updating its digest
// would leave a manifest that only fails later, inside a derivative, as a
// "payload sha256 mismatch" with no hint of the right value. This is that hint.
func TestExampleRegistryDigestsMatchPayloads(t *testing.T) {
	root := filepath.Join(exampleTestRoot(t), ExampleRegistryDir)
	for _, module := range loadExampleCatalog(t).Modules {
		for _, file := range module.Files {
			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Source)))
			if err != nil {
				t.Fatalf("%s: read payload %s: %v", module.ID, file.Source, err)
			}
			if got := digestBytes(content); got != file.SHA256 {
				t.Errorf("%s: payload %s digest is %s; update the manifest sha256 (recorded %s)",
					module.ID, file.Source, got, file.SHA256)
			}
		}
		for _, migration := range module.Migrations {
			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(migration.Source)))
			if err != nil {
				t.Fatalf("%s: read migration %s: %v", module.ID, migration.Source, err)
			}
			if got := digestBytes(content); got != migration.SHA256 {
				t.Errorf("%s: migration %s digest is %s; update the manifest sha256 (recorded %s)",
					module.ID, migration.Source, got, migration.SHA256)
			}
		}
	}
}

// The examples are installable by design, which is the whole point of them and
// also the only thing that could make them dangerous. Their isolation is
// structural rather than a flag: no shipped index names them, so this project's
// catalog cannot resolve one, no profile can list one, the committed lock has
// never installed one — and because every generated wiring file is rendered from
// that lock, Boot cannot reach them either. If any of that stops being true this
// fails here rather than in production.
func TestExampleModulesAreUnreachableFromTheShippedCatalog(t *testing.T) {
	root := exampleTestRoot(t)
	if err := assertExamplesUnreachable(root, loadExampleCatalog(t)); err != nil {
		t.Fatalf("example isolation broken: %v", err)
	}
}

// One example per kind, or the lifecycle is only proved for the kinds that
// happen to be present. A closure must also carry its example dependencies
// ahead of the module that requires them, because that ordering is what the
// planner installs.
func TestExampleClosuresCoverEveryKindInDependencyOrder(t *testing.T) {
	closures, err := exampleClosures(loadExampleCatalog(t))
	if err != nil {
		t.Fatalf("exampleClosures: %v", err)
	}
	kinds := make([]string, 0, len(closures))
	for _, closure := range closures {
		kinds = append(kinds, string(closure.root.Kind))
		if closure.modules[len(closure.modules)-1].ID != closure.root.ID {
			t.Fatalf("closure %s does not end at its own root: %v", closure.root.ID, closure.ids())
		}
		installed := make([]string, 0, len(closure.modules))
		for _, module := range closure.modules {
			for _, required := range module.Requires {
				if _, isExample := exampleIDs(closures)[required.ID]; !isExample {
					continue
				}
				if !slices.Contains(installed, required.ID) {
					t.Fatalf("closure %s installs %s before its dependency %s",
						closure.root.ID, module.ID, required.ID)
				}
			}
			installed = append(installed, module.ID)
		}
	}
	want := []string{
		"element", "component", "page",
		// Four workflow closures: the job-backed example, plus the three
		// resource-generator shapes — the full slice, the narrowed
		// platform/API-only one, and platform with its UI.
		"workflow", "workflow", "workflow", "workflow",
		"system", "system", "system",
	}
	if !slices.Equal(kinds, want) {
		t.Fatalf("closure kinds = %v, want exactly %v", kinds, want)
	}
}

// Provider fixtures are first-class closure exercises rather than metadata
// files that happen to sit beside the examples. Each one must carry both
// candidate adapters and the environment permutation the validator executes.
func TestProviderExampleClosuresDeclareEnvironmentSelections(t *testing.T) {
	closures, err := exampleClosures(loadExampleCatalog(t))
	if err != nil {
		t.Fatalf("exampleClosures: %v", err)
	}
	want := map[string]struct {
		slot       string
		candidates []string
	}{
		"fixture/system/mail-providers": {
			slot:       "ggg/mail",
			candidates: []string{"fixture/system/mail-local", "fixture/system/mail-managed"},
		},
		"fixture/system/storage-providers": {
			slot:       "ggg/storage",
			candidates: []string{"fixture/system/storage-local", "fixture/system/storage-managed"},
		},
	}
	found := map[string]bool{}
	for _, closure := range closures {
		spec, ok := providerFixtureSpecFor(closure.root.ID)
		if !ok {
			continue
		}
		expected, ok := want[closure.root.ID]
		if !ok {
			t.Fatalf("unexpected provider fixture %s", closure.root.ID)
		}
		found[closure.root.ID] = true
		if spec.slot != expected.slot || !slices.Equal(spec.candidates, expected.candidates) {
			t.Fatalf("%s fixture spec = %#v, want %#v", closure.root.ID, spec, expected)
		}
		if len(closure.modules) != 3 {
			t.Fatalf("%s closure modules = %v, want root plus two adapters", closure.root.ID, closure.ids())
		}
	}
	for id := range want {
		if !found[id] {
			t.Fatalf("provider fixture closure %s is missing", id)
		}
	}
}

// A broken example must be refused, not installed. These two cases are cheap
// because the planner answers them without building anything: an undeclared
// dependency is a graph failure and a generated target is a preflight refusal.
// The third failure the validator catches — a manifest that forgets to declare a
// file its own source needs — is only visible to a compiler, so it is proved by
// the command itself rather than here.
//
// Both cases break the leaf element, because it is the one example whose
// requires are empty: any other module would first fail on a shipped dependency
// the standalone example registry does not contain, and the test would pass for
// the wrong reason.
func TestExampleWithMissingDependencyIsRefused(t *testing.T) {
	registry := copyExampleRegistry(t)
	mutateExampleManifest(t, registry, "ggg/element/example-token", func(m *Manifest) {
		m.Requires = append(m.Requires, Requirement{ID: "ggg/element/example-missing", Contract: ContractBounds{Min: 1, Max: 1}})
		slices.SortFunc(m.Requires, func(a, b Requirement) int { return strings.Compare(a.ID, b.ID) })
	})

	_, err := planAgainstExampleRegistry(t, registry, "ggg/element/example-token")
	if err == nil {
		t.Fatal("planning a closure with an undeclared dependency succeeded")
	}
	const want = `module "ggg/element/example-token" requires missing dependency "ggg/element/example-missing"`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
}

func TestExampleClaimingGeneratedOutputIsRefused(t *testing.T) {
	registry := copyExampleRegistry(t)
	mutateExampleManifest(t, registry, "ggg/element/example-token", func(m *Manifest) {
		for i := range m.Files {
			if m.Files[i].Class == FileClassGo {
				m.Files[i].Target = "internal/web/templates/ui/example_token_templ.go"
			}
		}
	})

	_, err := planAgainstExampleRegistry(t, registry, "ggg/element/example-token")
	if err == nil {
		t.Fatal("planning a module that authors a generated output succeeded")
	}
	const want = "module ggg/element/example-token targets generated output " +
		"internal/web/templates/ui/example_token_templ.go; " +
		"generated outputs are tool-owned and cannot be authored"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
}
