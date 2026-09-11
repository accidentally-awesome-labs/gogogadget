package modkit

// One regression test per fix the task-AV dogfood round shipped, each born
// red: the fix's hunk was reverted, the test was watched to fail for the
// original defect's own symptom, and the fix was restored. The test names
// mirror the ledger (.superpowers/sdd/framework-followups/task-av-report.md).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// --- P1-1: `ggg remove` dropped the lock's provider selections and ports ---

// The lock is the resolved record of the intent's provider selections; a
// removal that blanks them leaves the next `provider set` merging from an
// empty map and refusing that 17 slots are missing — blaming the intent file
// whose values were correct all along.
func TestRemoveKeepsProviderSelectionsAndPortsInTheLock(t *testing.T) {
	root, engine, _ := installedRemovalProject(t)
	intent, err := MarshalProject(Project{
		Schema: 2,
		Registries: []ProjectRegistry{{
			Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main",
			PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=",
		}},
		// Every selected adapter is installed and survives the removal —
		// the dogfood's shape: the removal touches a non-adapter module and
		// the selections must live on in the lock.
		Providers: map[string]ProviderSelections{
			"ggg/mail": {
				Development: ProviderSelection{Adapter: "ggg/component/card", Target: "one"},
				Test:        ProviderSelection{Adapter: "ggg/component/card", Target: "one"},
				Production:  ProviderSelection{Adapter: "ggg/component/card", Target: "one"},
			},
		},
		Ports:      map[string]PortOverrides{"ggg/component/card@one/smtp": {Development: 1025, Test: 1026}},
		Deployment: "",
		Modules:    []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, ProjectFileName, intent)

	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}})
	if err != nil {
		t.Fatalf("Plan(remove): %v", err)
	}
	selection, ok := plan.Lock.Providers["ggg/mail"]
	if !ok {
		t.Fatalf("lock dropped the provider selections: %#v", plan.Lock.Providers)
	}
	if selection.Production.Adapter != "ggg/component/card" || selection.Production.Target != "one" {
		t.Fatalf("lock provider selection = %#v", selection)
	}
	if plan.Lock.Ports["ggg/component/card@one/smtp"].Development != 1025 {
		t.Fatalf("lock ports = %#v", plan.Lock.Ports)
	}
}

// The refusal a blanked lock eventually produces must name both real causes
// and both working remedies: a stale lock re-stamps with a bare `ggg sync`,
// and a genuinely missing slot is a `provider set` away.
func TestProviderMismatchRefusalNamesTheSyncRemedy(t *testing.T) {
	registry := plannerRegistry(t)
	source := refSource{snapshots: map[string]Snapshot{
		"main":      {Commit: testCommitA, FS: registry},
		testCommitA: {Commit: testCommitA, FS: registry},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}},
		// The closure declares no provider slots, so any selection is the
		// "unselected" side of the same refusal site the dogfood hit from
		// the missing side.
		Providers: map[string]ProviderSelections{
			"ggg/mail": {
				Development: ProviderSelection{Adapter: "ggg/system/mail-dev", Target: "filesystem"},
				Test:        ProviderSelection{Adapter: "ggg/system/mail-dev", Target: "filesystem"},
				Production:  ProviderSelection{Adapter: "ggg/system/mail-resend", Target: "resend"},
			},
		},
		Deployment: "",
		Modules:    []string{"ggg/component/card"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	_, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err == nil {
		t.Fatal("sync over an unmatched provider map unexpectedly planned")
	}
	for _, want := range []string{"unselected", "ggg sync", "provider set"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

// --- P1-2: conflict resolution wrote another registry's snapshot provenance ---

const (
	aawCommitV1 = "aaaaaaaa0000000000000000000000000000000000"
	aawCommitV2 = "aaaaaaaa1111111111111111111111111111111111"
	aawSnapV1   = "snapshot-aaw-v1-00000000000000000000000000000000000000000000000000000000000"
	aawSnapV2   = "snapshot-aaw-v2-00000000000000000000000000000000000000000000000000000000000"
)

// aawRegistries builds the dogfood's shape in miniature: a core registry
// first in the project's list, and a second registry publishing the modules
// that actually conflict. The bug pinned the second registry's modules to
// the first registry's snapshot.
func aawRegistries(t *testing.T) (fstest.MapFS, fstest.MapFS, fstest.MapFS) {
	t.Helper()
	core := plannerRegistry(t)

	aawV1 := fstest.MapFS{}
	putJSON(t, aawV1, "registry.json", RegistryRoot{
		Schema: 2, Namespace: "aaw", CanonicalModule: "example.com/aaw/registry",
		Includes: append([]string(nil), publishedRegistryIncludes...),
	})
	for _, index := range []CatalogIndex{
		{Schema: 2, Kind: CatalogElement, Items: []string{}},
		{Schema: 2, Kind: CatalogComponent, Items: []string{}},
		{Schema: 2, Kind: CatalogPage, Items: []string{}},
		{Schema: 2, Kind: CatalogWorkflow, Items: []string{}},
		{Schema: 2, Kind: CatalogSystem, Items: []string{}},
		{Schema: 2, Kind: CatalogProfile, Items: []string{}},
	} {
		putJSON(t, aawV1, "registry/"+string(index.Kind)+"s.json", index)
	}
	thingV1 := []byte("package thing\n\nconst Version = 1\n")
	thing := testLockedModule("aaw/element/thing", sha256Hex(thingV1)).Manifest
	thing.Files = []ManifestFile{{
		Source: "registry/modules/element/thing/thing.go", Target: "internal/modules/thing.go",
		Class: FileClassGo, SHA256: sha256Hex(thingV1),
	}}
	addAawModule(t, aawV1, thing, thingV1)
	otherV1 := []byte("package other\n\nconst Version = 1\n")
	other := testLockedModule("aaw/element/other", sha256Hex(otherV1)).Manifest
	other.Files = []ManifestFile{{
		Source: "registry/modules/element/other/other.go", Target: "internal/modules/other.go",
		Class: FileClassGo, SHA256: sha256Hex(otherV1),
	}}
	addAawModule(t, aawV1, other, otherV1)

	aawV2 := cloneMapFS(aawV1)
	thingV2 := []byte("package thing\n\nconst Version = 2 // upstream edit\n")
	aawV2["registry/modules/element/thing/thing.go"].Data = thingV2
	mutatePlannerModule(t, aawV2, "aaw/element/thing", func(module *Manifest) {
		module.Revision = 2
		module.Files[0].SHA256 = sha256Hex(thingV2)
	})
	otherV2 := []byte("package other\n\nconst Version = 2\n")
	aawV2["registry/modules/element/other/other.go"].Data = otherV2
	mutatePlannerModule(t, aawV2, "aaw/element/other", func(module *Manifest) {
		module.Revision = 2
		module.Files[0].SHA256 = sha256Hex(otherV2)
	})
	return core, aawV1, aawV2
}

func addAawModule(t *testing.T, files fstest.MapFS, manifest Manifest, content []byte) {
	t.Helper()
	if len(manifest.Files) != 1 {
		t.Fatalf("test module %s must have one file", manifest.ID)
	}
	itemPath := "registry/modules/" + string(manifest.Kind) + "/" + manifest.Name + "/module.json"
	putJSON(t, files, itemPath, ModuleDocument{Schema: 2, Module: manifest})
	files[manifest.Files[0].Source] = &fstest.MapFile{Data: content}
	var index CatalogIndex
	if err := json.Unmarshal(files["registry/elements.json"].Data, &index); err != nil {
		t.Fatalf("decode aaw elements index: %v", err)
	}
	index.Items = append(index.Items, itemPath)
	for i := 1; i < len(index.Items); i++ {
		for j := i; j > 0 && index.Items[j] < index.Items[j-1]; j-- {
			index.Items[j], index.Items[j-1] = index.Items[j-1], index.Items[j]
		}
	}
	putJSON(t, files, "registry/elements.json", index)
}

// The dogfood's exact defect, end to end at the engine: a conflicted module
// in the second registry must record ITS OWN registry's commit and snapshot
// digest — while the conflict stands (installed bytes still come from the old
// snapshot) and after `resolve` moves it to the new one — never the first
// registry's values.
func TestConflictedModuleKeepsItsOwnRegistryProvenance(t *testing.T) {
	core, aawV1, aawV2 := aawRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"main": {Commit: testCommitA, FS: core},
		// ResolveConflict resolves every registry at its lock-recorded
		// commit (preferRecorded) and the conflicted module's registry at
		// the pending commit, so both shapes need keyed entries.
		testCommitA: {Commit: testCommitA, FS: core},
		"v1":        {Commit: aawCommitV1, SnapshotSHA256: aawSnapV1, FS: aawV1},
		"v2":        {Commit: aawCommitV2, SnapshotSHA256: aawSnapV2, FS: aawV2},
		aawCommitV1: {Commit: aawCommitV1, SnapshotSHA256: aawSnapV1, FS: aawV1},
		aawCommitV2: {Commit: aawCommitV2, SnapshotSHA256: aawSnapV2, FS: aawV2},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema: 2,
		Registries: []ProjectRegistry{
			{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="},
			{Namespace: "aaw", Source: "github", Repository: "aaw/registry", Ref: "v1", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="},
		},
		Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "aaw/element/thing", "aaw/element/other"},
		Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializePlanFixture(t, root, initial)

	writeTestFile(t, root, "internal/modules/thing.go", []byte("package thing\n\nconst Local = true\n"))
	update, err := engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, TargetedRegistry: "aaw", RegistryRef: "v2"})
	if err != nil {
		t.Fatalf("Plan(update): %v", err)
	}
	lockedByID := func(lock Lock) map[string]LockedModule {
		byID := make(map[string]LockedModule, len(lock.Modules))
		for _, module := range lock.Modules {
			byID[module.ID] = module
		}
		return byID
	}

	// While the conflict stands, the installed bytes are still the v1
	// snapshot's, and the pending candidate is pinned to the module's OWN
	// registry at v2 — the core registry's commit must appear in neither.
	rows := lockedByID(update.Lock)
	thing := rows["aaw/element/thing"]
	if thing.Pending == nil {
		t.Fatal("thing is not staged as conflicted")
	}
	if thing.SourceCommit != aawCommitV1 || thing.SnapshotSHA256 != aawSnapV1 {
		t.Fatalf("conflicted row provenance = %q/%q, want own v1 %q/%q",
			thing.SourceCommit, thing.SnapshotSHA256, aawCommitV1, aawSnapV1)
	}
	if thing.Pending.RegistryCommit != aawCommitV2 {
		t.Fatalf("pending registry commit = %q, want own registry's v2 commit %q (not core %q)",
			thing.Pending.RegistryCommit, aawCommitV2, testCommitA)
	}
	// The independent module in the same registry updates cleanly and lands
	// on the new snapshot of its own registry.
	other := rows["aaw/element/other"]
	if other.SourceCommit != aawCommitV2 || other.SnapshotSHA256 != aawSnapV2 {
		t.Fatalf("updated other provenance = %q/%q, want %q/%q",
			other.SourceCommit, other.SnapshotSHA256, aawCommitV2, aawSnapV2)
	}
	// The untouched core module keeps the core snapshot.
	if card := rows["ggg/component/card"]; card.SourceCommit != testCommitA {
		t.Fatalf("retained card provenance = %q, want core commit %q", card.SourceCommit, testCommitA)
	}

	materializePlanFixture(t, root, update)
	resolved, err := engine.ResolveConflict(
		context.Background(), root, "aaw/element/thing", "internal/modules/thing.go", ResolutionKeepLocal,
	)
	if err != nil {
		t.Fatalf("ResolveConflict: %v", err)
	}
	rows = lockedByID(resolved.Lock)
	thing = rows["aaw/element/thing"]
	// This is the ledger's assertion verbatim: the resolved row must carry
	// the aaw v2 pair, not the core registry's commit in either field.
	if thing.SourceCommit != aawCommitV2 || thing.SnapshotSHA256 != aawSnapV2 {
		t.Fatalf("resolved row provenance = %q/%q, want own registry %q/%q — not core commit %q",
			thing.SourceCommit, thing.SnapshotSHA256, aawCommitV2, aawSnapV2, testCommitA)
	}
	if thing.Pending != nil {
		t.Fatalf("resolved row still pending: %#v", thing.Pending)
	}
}

// --- P1-3: a private registry's raw 404 said nothing about GITHUB_TOKEN ---

func TestGitHubSource404ExplainsPrivateRepositoryAccess(t *testing.T) {
	// GitHub answers 404 — not 403 — for a private repository an anonymous
	// request cannot see, so this is the exact surface the dogfood hit.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer server.Close()

	anonymous := GitHubSource{Client: server.Client(), CacheDir: t.TempDir(), APIBaseURL: server.URL, CodeloadBaseURL: server.URL}
	_, err := anonymous.Resolve(context.Background(), ProjectRegistry{Source: "github", Repository: "acme/private-registry", Ref: "main"})
	if err == nil {
		t.Fatal("Resolve over a 404 GitHub API unexpectedly succeeded")
	}
	for _, want := range []string{"HTTP 404", "GITHUB_TOKEN", "private registries require it"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("anonymous 404 error %q does not explain %q", err, want)
		}
	}

	tokened := anonymous
	tokened.Token = "ghp_testtoken"
	_, err = tokened.Resolve(context.Background(), ProjectRegistry{Source: "github", Repository: "acme/private-registry", Ref: "main"})
	if err == nil {
		t.Fatal("Resolve over a 404 GitHub API unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN is set") {
		t.Fatalf("tokened 404 error %q does not separate the token-present cause", err)
	}
}

// --- P2-1: `registry build` absorbed a payload change at an unchanged revision ---

// snapshotGatedTree writes a standalone publisher's tree: a module, its
// payload, and a previously signed snapshot pinning every file byte — the
// state `registry build --dir .` runs against in a third-party repository
// with no lock beside it.
func snapshotGatedTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"namespace":"aaw","canonical_module":"example.com/aaw/registry","includes":["registry/systems.json"]}`))
	writeTestFile(t, root, "registry/systems.json", []byte(`{"schema":2,"kind":"system","items":["registry/modules/system/widget/module.json"]}`))
	payload := []byte("package widget\n\nconst Version = 1\n")
	writeTestFile(t, root, "internal/widget/widget.go", payload)
	document := ModuleDocument{Schema: 2, Module: Manifest{
		ID: "aaw/system/widget", Kind: ModuleKind("system"), Name: "widget", Revision: 1, Contract: 1,
		Title: "Widget", Description: "A widget.",
		Files: []ManifestFile{{
			Source: "internal/widget/widget.go", Target: "internal/widget/widget.go",
			Class: FileClassGo, SHA256: sha256Hex(payload),
		}},
		Dependencies: Dependencies{Go: []GoDependency{}, Tools: []ToolArtifact{}, Containers: []ContainerDependency{}},
	}}
	manifest, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "registry/modules/system/widget/module.json", manifest)
	snapshot := RegistrySnapshot{Schema: 2, Files: []SnapshotFile{
		{Path: "registry/modules/system/widget/module.json", SHA256: sha256Hex(manifest)},
		{Path: "internal/widget/widget.go", SHA256: sha256Hex(payload)},
	}}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "registry.snapshot.json", data)
	return root
}

func TestRegistryBuildRefusesMovedPayloadsAtAnUnchangedRevisionWithoutALock(t *testing.T) {
	t.Run("payload moved, manifest byte-identical to the published snapshot", func(t *testing.T) {
		root := snapshotGatedTree(t)
		writeTestFile(t, root, "internal/widget/widget.go", []byte("package widget\n\nconst Version = 2 // edited without a bump\n"))
		err := ValidateManifestRevisionsAgainstSnapshot(root)
		if err == nil {
			t.Fatal("build gate accepted a payload edit at an unchanged revision")
		}
		for _, want := range []string{"aaw/system/widget", "revision"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal %q does not name %q", err, want)
			}
		}
	})

	t.Run("revision bumped alongside the manifest edit passes the snapshot half", func(t *testing.T) {
		root := snapshotGatedTree(t)
		document := ModuleDocument{Schema: 2, Module: Manifest{
			ID: "aaw/system/widget", Kind: ModuleKind("system"), Name: "widget", Revision: 2, Contract: 1,
			Title: "Widget", Description: "A widget.",
			Files: []ManifestFile{{
				Source: "internal/widget/widget.go", Target: "internal/widget/widget.go",
				Class: FileClassGo, SHA256: sha256Hex([]byte("package widget\n\nconst Version = 1\n")),
			}},
			Dependencies: Dependencies{Go: []GoDependency{}, Tools: []ToolArtifact{}, Containers: []ContainerDependency{}},
		}}
		manifest, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		// The manifest bytes now differ from the published snapshot, so the
		// revision may have moved with them: not provably stale, refresh
		// will finish the job.
		writeTestFile(t, root, "registry/modules/system/widget/module.json", manifest)
		if err := ValidateManifestRevisionsAgainstSnapshot(root); err != nil {
			t.Fatalf("gate refused a bumped revision: %v", err)
		}
	})

	t.Run("no snapshot is the lock half's or refresh's problem", func(t *testing.T) {
		root := snapshotGatedTree(t)
		if err := os.Remove(filepath.Join(root, "registry.snapshot.json")); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, root, "internal/widget/widget.go", []byte("package widget\n\nconst Version = 2 // edited without a bump\n"))
		if err := ValidateManifestRevisionsAgainstSnapshot(root); err != nil {
			t.Fatalf("gate without a published snapshot = %v, want nil", err)
		}
	})
}

// --- P2-2: the staged conflict diff was a whole-file rewrite ---

func TestUnifiedConflictDiffCollapsesMatchingLinesIntoContext(t *testing.T) {
	local := make([]byte, 0, 8192)
	for i := 1; i <= 223; i++ {
		local = append(local, []byte(strings.Repeat("x", i%7)+string(rune('a'+i%26))+"\n")...)
	}
	upstream := append([]byte(nil), local...)
	// Two real edits — the shape whose 8-line `diff` once rendered as 446
	// marked lines.
	upstream[10] = '#'
	upstream[200] = '#'

	got := string(unifiedConflictDiff("internal/modules/thing.go", local, upstream))
	if strings.Contains(got, "@@ -1,223 +1,223 @@") {
		t.Fatalf("diff is the whole-file rewrite it replaced:\n%s", got)
	}
	if !strings.Contains(got, "--- a/internal/modules/thing.go") || !strings.Contains(got, "+++ b/internal/modules/thing.go") {
		t.Fatalf("diff lacks the unified header:\n%s", got)
	}
	hunks := strings.Count(got, "@@ -")
	// Two edits 190 lines apart are two real hunks of six and seven lines —
	// the whole-file rewrite this replaced opened one 446-line hunk.
	if hunks != 2 {
		t.Fatalf("hunk count = %d, want 2:\n%s", hunks, got)
	}
	if !strings.Contains(got, "@@ -1,6 +1,6 @@") || !strings.Contains(got, "@@ -38,7 +38,7 @@") {
		t.Fatalf("hunk headers lost diff(1)'s range form:\n%s", got)
	}
	if total := strings.Count(got, "\n"); total > 30 {
		t.Fatalf("diff is %d lines for a two-byte change:\n%s", total, got)
	}

	t.Run("identical bytes carry no hunks", func(t *testing.T) {
		same := string(unifiedConflictDiff("x.go", local, local))
		if strings.Contains(same, "@@") {
			t.Fatalf("identical pair produced hunks:\n%s", same)
		}
		if strings.Count(same, "\n") != 2 {
			t.Fatalf("identical pair emitted more than the bare header:\n%s", same)
		}
	})

	t.Run("binary falls back with the marker", func(t *testing.T) {
		got := string(unifiedConflictDiff("bin", []byte{0x00, 0xff}, []byte{0x00, 0xfe}))
		if !strings.Contains(got, "Binary conflict") {
			t.Fatalf("binary fallback = %q", got)
		}
	})

	t.Run("oversized pairs fall back to the whole-file form, stated", func(t *testing.T) {
		var big []byte
		for i := 0; i <= diffLineBudget; i++ {
			big = append(big, []byte("line\n")...)
		}
		got := string(unifiedConflictDiff("big.go", big, append(append([]byte(nil), big...), []byte("tail\n")...)))
		if !strings.Contains(got, "whole-file fallback") {
			t.Fatalf("oversized pair did not state its fallback:\n%s", got[:200])
		}
	})
}

// --- P2-3: authoring refusals arrived without the rule they enforce ---

func TestAuthoringRefusalsStateTheRuleAndTheFix(t *testing.T) {
	t.Run("unsorted files name the out-of-order pair", func(t *testing.T) {
		err := validateManifestFiles([]ManifestFile{
			{Target: "internal/modules/z.go", Source: "a.go", Class: FileClassGo, SHA256: sha256Hex([]byte("z"))},
			{Target: "internal/modules/a.go", Source: "b.go", Class: FileClassGo, SHA256: sha256Hex([]byte("a"))},
		}, true)
		if err == nil {
			t.Fatal("unsorted files accepted")
		}
		for _, want := range []string{"internal/modules/a.go", "internal/modules/z.go", "sorted by target"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal %q does not name %q", err, want)
			}
		}
	})

	t.Run("hyphenated cli name states the identifier rule and the spelling", func(t *testing.T) {
		err := validateCLIContributions([]CLIContribution{{Name: "aaw-uuid", Summary: "Mint ids"}}, true)
		if err == nil {
			t.Fatal("hyphenated cli name accepted")
		}
		for _, want := range []string{"aaw-uuid", "aawuuid", "no hyphens"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal %q does not state %q", err, want)
			}
		}
	})
}

// --- P2-7: a provider deselection refusal said "deployment" ---

// The guard is identical for both retirements; only the remedy differs, and
// the message must name the one that applies.
func TestPlanRetirementNamesTheReplacementItRefuses(t *testing.T) {
	root := t.TempDir()
	payload := []byte("package thing\n\nconst Local = true\n")
	writeTestFile(t, root, "internal/modules/thing.go", payload)
	module := LockedModule{ID: "aaw/system/mail-filelog", Files: []LockedFile{{
		Path: "internal/modules/thing.go", BaseSHA256: sha256Hex([]byte("package thing\n\nconst Upstream = 1\n")),
	}}}

	t.Run("provider deselection names the slot", func(t *testing.T) {
		_, _, err := planRetirement(context.Background(), root, module.ID, module, retirement{slot: "ggg/mail"})
		if err == nil {
			t.Fatal("retirement over a modified file accepted")
		}
		for _, want := range []string{"provider selection for slot ggg/mail", "ggg diff aaw/system/mail-filelog"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal %q does not name %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "replacing the deployment") {
			t.Fatalf("provider refusal names the deployment: %q", err)
		}
	})

	t.Run("deployment replacement still names the deployment", func(t *testing.T) {
		_, _, err := planRetirement(context.Background(), root, module.ID, module, retirement{deployment: true})
		if err == nil {
			t.Fatal("retirement over a modified file accepted")
		}
		if !strings.Contains(err.Error(), "replacing the deployment") {
			t.Fatalf("deployment refusal lost its noun: %q", err)
		}
	})
}

// --- P2-9: `registry remove` deadlocked on its own removal tombstones ---

// The exclude entries of a removed namespace must leave in the same planned
// transaction the registry does, or the very next plan refuses on a tombstone
// no configured registry can resolve.
func TestSetExcludeSequencesRegistryRemovalTombstones(t *testing.T) {
	core, aawV1, _ := aawRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"main": {Commit: testCommitA, FS: core},
		"v1":   {Commit: aawCommitV1, SnapshotSHA256: aawSnapV1, FS: aawV1},
	}}
	// The dogfood's exact shape: the project selects a profile, and the
	// tombstoned module sits in exclude — removal moved it out of modules.
	// MarshalProject refuses a nil exclude array, so every variant passes a
	// real slice.
	newProject := func(exclude []string) Project {
		if exclude == nil {
			exclude = []string{}
		}
		modules := []string{"aaw/element/thing"}
		if len(exclude) == 0 {
			modules = append(modules, "aaw/element/other")
		}
		return Project{
			Schema: 2,
			Registries: []ProjectRegistry{
				{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="},
				{Namespace: "aaw", Source: "github", Repository: "aaw/registry", Ref: "v1", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="},
			},
			Providers: map[string]ProviderSelections{}, Deployment: "",
			Modules: append([]string{"ggg/profile/full"}, modules...),
			Exclude: exclude,
		}
	}
	coreOnly := func() []ProjectRegistry {
		return []ProjectRegistry{{
			Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main",
			PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=",
		}}
	}

	t.Run("dropping the namespace without its tombstones deadlocks", func(t *testing.T) {
		root := writeTargetProject(t, "example.com/acme/app", newProject([]string{"aaw/element/other"}))
		engine := New(Options{Source: source})
		_, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync, SetRegistries: coreOnly()})
		if err == nil {
			t.Fatal("registry removal left its own tombstone behind and planned anyway")
		}
		if !strings.Contains(err.Error(), `exclude contains unknown module "aaw/element/other"`) {
			t.Fatalf("deadlock symptom changed: %v", err)
		}
	})

	t.Run("SetExclude clears them in the same transaction", func(t *testing.T) {
		// The dogfood end-state: the aaw modules were already removed (their
		// tombstones sit in exclude), and `registry remove aaw` must take
		// the tombstones with it rather than deadlock on them.
		project := newProject([]string{"aaw/element/other"})
		project.Modules = []string{"ggg/profile/full"}
		root := writeTargetProject(t, "example.com/acme/app", project)
		engine := New(Options{Source: source})
		plan, err := engine.Plan(context.Background(), root, Operation{
			// Every aaw tombstone leaves with the aaw registry: a selective
			// keep is the same deadlock sub-test one proves.
			Kind: OpSync, SetRegistries: coreOnly(), SetExclude: []string{},
		})
		if err != nil {
			t.Fatalf("Plan(registry remove + tombstone sweep): %v", err)
		}
		if len(plan.Project.Registries) != 1 || plan.Project.Registries[0].Namespace != "ggg" {
			t.Fatalf("planned registries = %#v", plan.Project.Registries)
		}
		if len(plan.Project.Exclude) != 0 {
			t.Fatalf("planned exclude = %#v, want the aaw tombstones swept", plan.Project.Exclude)
		}
	})

	t.Run("SetExclude refuses non-sync operations", func(t *testing.T) {
		root := writeTargetProject(t, "example.com/acme/app", newProject(nil))
		engine := New(Options{Source: source})
		if _, err := engine.Plan(context.Background(), root, Operation{Kind: OpAdd, SetExclude: []string{}}); err == nil ||
			!strings.Contains(err.Error(), "does not accept an exclude set") {
			t.Fatalf("non-sync SetExclude = %v, want refusal", err)
		}
	})
}

// The registry-validate identity-providers fixture is the original red: it
// stages a provider switch by writing the intent's selections BEFORE the
// adapters are installed, then removes the legacy adapters in the same
// breath. The removal renders its outputs against the post-removal tree, so
// a selection naming an adapter that tree does not have — incoming here,
// retiring in the mirror case — made the removal refuse to render its own
// outputs ("selected adapter … is not installed"). The removal's lock now
// keeps only the selections that tree can resolve; the intent keeps them
// all, and the next sync re-stamps.
func TestRemoveRendersOnlySelectionsThePostRemovalTreeResolves(t *testing.T) {
	root, engine, _ := installedRemovalProject(t)
	intent, err := MarshalProject(Project{
		Schema: 2,
		Registries: []ProjectRegistry{{
			Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main",
			PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=",
		}},
		Providers: map[string]ProviderSelections{
			// Survives: every environment's adapter is installed and kept.
			"ggg/kept": {
				Development: ProviderSelection{Adapter: "ggg/component/card", Target: "one"},
				Test:        ProviderSelection{Adapter: "ggg/component/card", Target: "one"},
				Production:  ProviderSelection{Adapter: "ggg/component/card", Target: "one"},
			},
			// The staged-switch shape: selected in the intent, installed by
			// the NEXT operation, absent from the tree this removal leaves.
			"ggg/incoming": {
				Development: ProviderSelection{Adapter: "ggg/element/invented", Target: "one"},
				Test:        ProviderSelection{Adapter: "ggg/element/invented", Target: "one"},
				Production:  ProviderSelection{Adapter: "ggg/element/invented", Target: "one"},
			},
		},
		Deployment: "",
		Modules:    []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, ProjectFileName, intent)

	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}})
	if err != nil {
		t.Fatalf("Plan(remove) over a staged provider switch: %v", err)
	}
	if _, kept := plan.Lock.Providers["ggg/kept"]; !kept {
		t.Fatalf("removal dropped a selection whose adapter survives: %#v", plan.Lock.Providers)
	}
	if _, incoming := plan.Lock.Providers["ggg/incoming"]; incoming {
		t.Fatalf("removal lock records a selection the post-removal tree cannot resolve: %#v", plan.Lock.Providers)
	}
	// The filter runs before the removal renders (the lock field the
	// generator reads); the identity-providers fixture exercises that render
	// end to end and is cited above as the original red.
	// The intent is the human's record and keeps every selection.
	if _, ok := plan.Project.Providers["ggg/incoming"]; !ok {
		t.Fatal("removal rewrote the intent's staged selection away")
	}
}
