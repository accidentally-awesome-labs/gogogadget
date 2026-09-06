package modkit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The very first sync has no lock on disk yet — Apply writes it last, after
// generation. A generator that reads the on-disk lock therefore generates
// nothing on a fresh install, which is precisely when the aggregates are needed.
func TestRegistryGeneratorEmitsOnFirstSync(t *testing.T) {
	root, engine, _ := installedRemovalProject(t)
	if err := os.Remove(filepath.Join(root, LockFileName)); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove lock: %v", err)
	}

	engine.generator = RegistryGenerator{}
	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := engine.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	bootstrap := filepath.Join(root, "internal", "modules", "bootstrap_registry_gen.go")
	data, err := os.ReadFile(bootstrap)
	if err != nil {
		t.Fatalf("first sync produced no bootstrap registry: %v", err)
	}
	if !strings.Contains(string(data), "func Boot(") {
		t.Fatalf("bootstrap registry has no Boot:\n%s", data)
	}
}

// A `git worktree add .worktrees/feature` inside the project root put a second
// full copy of every aggregate under the project tree. The sweep skipped
// nested checkouts by directory name, and a linked worktree carries `.git` as
// a FILE, so every one of that copy's aggregates came back stale —
// `sync --check` refused with findings naming another checkout's files, which
// the prescribed `ggg sync` could never clear.
func TestStaleSweepSkipsNestedCheckouts(t *testing.T) {
	root := t.TempDir()
	const owned = "internal/modules/bootstrap_registry_gen.go"
	if !IsRegistryOwnedOutputPath(owned) {
		t.Fatalf("%s is not a registry-owned output; pick a path the sweep considers", owned)
	}
	// Marked bytes: the partition under test is Stale, so the planted copies
	// have to claim this tool's authorship the way a real aggregate does.
	aggregate := []byte("// Code generated " + GeneratedOutputMarker + ".\n\npackage modules\n")

	for name, git := range map[string]func(dir string) error{
		"linked worktree": func(dir string) error { // .git is a file
			return os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644)
		},
		"nested clone": func(dir string) error { // .git is a directory
			return os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
		},
	} {
		t.Run(name, func(t *testing.T) {
			nested := filepath.Join(root, ".worktrees", strings.ReplaceAll(name, " ", "-"))
			if err := os.MkdirAll(filepath.Join(nested, filepath.Dir(owned)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := git(nested); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nested, owned), aggregate, 0o644); err != nil {
				t.Fatal(err)
			}

			unrendered, err := UnrenderedRegistryOutputs(root, map[string]struct{}{})
			if err != nil {
				t.Fatalf("UnrenderedRegistryOutputs: %v", err)
			}
			for _, path := range append(unrendered.Paths(unrendered.Stale), unrendered.Paths(unrendered.Unowned)...) {
				if strings.HasPrefix(path, ".worktrees/") {
					t.Fatalf("swept another checkout's tree: %q", path)
				}
			}
		})
	}

	// The same path in this project's own tree is still swept: the fix must not
	// blind the sweep to real stale output.
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(owned)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, owned), aggregate, 0o644); err != nil {
		t.Fatal(err)
	}
	unrendered, err := UnrenderedRegistryOutputs(root, map[string]struct{}{})
	if err != nil {
		t.Fatalf("UnrenderedRegistryOutputs: %v", err)
	}
	stale := unrendered.Paths(unrendered.Stale)
	if len(stale) != 1 || stale[0] != owned {
		t.Fatalf("stale = %v, want exactly [%s]", stale, owned)
	}
	if len(unrendered.Unowned) != 0 {
		t.Fatalf("marked aggregate classified as authored: %v", unrendered.Paths(unrendered.Unowned))
	}
}

// Defence in depth. Engine.Plan refuses authored bytes at a generated name
// before anything is written, so reaching the generation stage with one means
// the tree changed under a confirmed plan. The stage refuses rather than
// deleting inside the transaction, and Apply restores the tree it had already
// started writing.
func TestGenerationRefusesAuthoredBytesAtAGeneratedName(t *testing.T) {
	root, engine, _ := installedRemovalProject(t)
	engine.generator = RegistryGenerator{}
	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Planted AFTER the plan, so the planner never saw it.
	const target = "internal/modules/probe_registry_gen.go"
	authored := []byte("package modules\n\nconst Probe = \"authored\"\n")
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(target)), authored, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := engine.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("Apply deleted authored bytes the plan never named")
	}
	if !strings.Contains(err.Error(), target) {
		t.Fatalf("the failure names no path: %v", err)
	}
	if result.Exit != 5 || !result.RolledBack {
		t.Fatalf("result = exit %d rolledback %t, want a restored tree", result.Exit, result.RolledBack)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil || string(got) != string(authored) {
		t.Fatalf("authored bytes did not survive the rollback: %v %q", err, got)
	}
}
