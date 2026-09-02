package modkit

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
)

// transferRegistries returns two refs of one registry where a single authored
// target moves owner between them without changing a byte. Both refs carry the
// identical payload, so the only thing under test is the ownership edit.
//
// mutate lets a case break the second ref further — to keep the old claim (a
// genuine two-owner collision) instead of releasing it.
func transferRegistries(t *testing.T, mutate func(second fstest.MapFS)) (fstest.MapFS, fstest.MapFS) {
	t.Helper()
	first := plannerRegistry(t)
	shared := []byte("package optional\n\nconst Version = 1\n")
	moved := ManifestFile{
		Source: "registry/modules/page/optional/optional.go", Target: "internal/modules/optional.go",
		Class: FileClassGo, SHA256: sha256Hex(shared), Contract: true,
	}
	optional := testLockedModule("ggg/page/optional", sha256Hex(shared)).Manifest
	optional.Files = []ManifestFile{moved}
	addPlannerModule(t, first, optional, shared)

	second := cloneMapFS(first)
	// The new owner ships the same bytes from its own directory, so the
	// transfer is byte-identical and the payload digest is unchanged.
	adopted := moved
	adopted.Source = "registry/modules/component/card/optional.go"
	second[adopted.Source] = &fstest.MapFile{Data: shared}
	mutatePlannerModule(t, second, "ggg/page/optional", func(module *Manifest) {
		module.Revision = 2
		module.Files = []ManifestFile{}
	})
	mutatePlannerModule(t, second, "ggg/component/card", func(module *Manifest) {
		module.Revision = 2
		// files must stay sorted by target: internal/modules < internal/web.
		module.Files = append([]ManifestFile{adopted}, module.Files...)
	})
	if mutate != nil {
		mutate(second)
	}
	return first, second
}

func transferProjectRoot(t *testing.T) string {
	t.Helper()
	return writeTargetProject(t, "example.com/acme/app", Project{
		Schema: 2,
		Registries: []ProjectRegistry{{
			Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main",
			PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=",
		}},
		Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
}

// Moving a target's owner is a manifest edit, not a file move — that is how the
// catalog reassigns an e2e spec to the feature module that drives it. A
// derivative that already synced the old owner must be able to take that edit
// in one pass; otherwise every consumer is stuck refusing with no migration
// path.
func TestSyncTransfersTargetOwnershipInOnePass(t *testing.T) {
	firstRegistry, secondRegistry := transferRegistries(t, nil)
	source := refSource{snapshots: map[string]Snapshot{
		"v1": {Commit: testCommitA, FS: firstRegistry},
		"v2": {Commit: testCommitB, FS: secondRegistry},
	}}
	root := transferProjectRoot(t)
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)

	update, err := engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
	if err != nil {
		t.Fatalf("Plan(transfer): %v", err)
	}

	// Exactly one change for the target, and it is not a deletion: the old
	// owner's drop must not race the new owner's write.
	var planned []Change
	for _, change := range update.Changes {
		if change.Path == "internal/modules/optional.go" {
			planned = append(planned, change)
		}
	}
	if len(planned) != 1 {
		t.Fatalf("transferred target has %d changes, want 1: %#v", len(planned), planned)
	}
	if planned[0].Kind != ChangeUnchanged || planned[0].Module != "ggg/component/card" {
		t.Fatalf("transfer change = %#v, want unchanged under ggg/component/card", planned[0])
	}

	for _, module := range update.Lock.Modules {
		switch module.ID {
		case "ggg/page/optional":
			if len(module.Files) != 0 {
				t.Fatalf("previous owner still locks the target: %#v", module.Files)
			}
		case "ggg/component/card":
			var found bool
			for _, file := range module.Files {
				if file.Path == "internal/modules/optional.go" {
					found = true
					if file.State != FileClean {
						t.Fatalf("transferred file state = %q, want clean", file.State)
					}
				}
			}
			if !found {
				t.Fatalf("new owner does not lock the target: %#v", module.Files)
			}
		}
	}
}

// A transfer is only unambiguous because the new graph has one claimant. Two
// modules claiming one target is a catalog authoring error and must still
// refuse before anything is written, naming both claimants — the relaxation
// above must not have widened into a silent double-owner install.
func TestSyncRefusesTargetClaimedByTwoModules(t *testing.T) {
	_, secondRegistry := transferRegistries(t, func(second fstest.MapFS) {
		// The previous owner keeps its claim, so both modules declare it.
		mutatePlannerModule(t, second, "ggg/page/optional", func(module *Manifest) {
			module.Files = []ManifestFile{{
				Source: "registry/modules/page/optional/optional.go", Target: "internal/modules/optional.go",
				Class: FileClassGo, SHA256: sha256Hex([]byte("package optional\n\nconst Version = 1\n")), Contract: true,
			}}
		})
	})
	source := refSource{snapshots: map[string]Snapshot{"v1": {Commit: testCommitB, FS: secondRegistry}}}
	root := transferProjectRoot(t)
	engine := New(Options{Source: source})
	_, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err == nil {
		t.Fatal("Plan succeeded, want two-claimant refusal")
	}
	message := err.Error()
	for _, want := range []string{
		"internal/modules/optional.go", "collision", "ggg/component/card", "ggg/page/optional",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("Plan error = %q, want it to name %q", message, want)
		}
	}
}

// A transfer carries the recorded base digest across, so local work is still
// protected: the new owner refuses rather than overwriting an edited file.
func TestSyncRefusesTransferOverLocallyModifiedTarget(t *testing.T) {
	firstRegistry, secondRegistry := transferRegistries(t, nil)
	source := refSource{snapshots: map[string]Snapshot{
		"v1": {Commit: testCommitA, FS: firstRegistry},
		"v2": {Commit: testCommitB, FS: secondRegistry},
	}}
	root := transferProjectRoot(t)
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)
	writeTestFile(t, root, "internal/modules/optional.go", []byte("package optional\n\nconst Version = 99 // local\n"))

	_, err = engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
	if err == nil {
		t.Fatal("Plan succeeded, want locally-modified refusal")
	}
	if !strings.Contains(err.Error(), "is locally modified") {
		t.Fatalf("Plan error = %v, want locally-modified refusal", err)
	}
}
