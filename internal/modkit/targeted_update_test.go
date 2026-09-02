package modkit

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// targetedUpdateFixture installs the conflict closure at v1, then moves the
// registry's declared ref forward to the v2 snapshot — the real-world shape
// of a targeted update: publishers advance the ref, `ggg update MODULES`
// advances only the named modules, and everything else stays pinned.
func targetedUpdateFixture(t *testing.T) (string, *Engine) {
	t.Helper()
	first, second := conflictRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"main":      {Commit: testCommitA, FS: first},
		"v1":        {Commit: testCommitA, FS: first},
		"v2":        {Commit: testCommitB, FS: second},
		testCommitA: {Commit: testCommitA, FS: first},
		testCommitB: {Commit: testCommitB, FS: second},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}},
		Modules:    []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
		Providers: map[string]ProviderSelections{}, Deployment: "",
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)
	source.snapshots["main"] = Snapshot{Commit: testCommitB, FS: second}
	return root, engine
}

func lockedRow(t *testing.T, plan Plan, id string) LockedModule {
	t.Helper()
	for _, module := range plan.Lock.Modules {
		if module.ID == id {
			return module
		}
	}
	t.Fatalf("lock has no row for %s", id)
	return LockedModule{}
}

func TestTargetedUpdateAdvancesNamedModulesAndRetainsOthers(t *testing.T) {
	root, engine := targetedUpdateFixture(t)
	optionalBefore, err := os.ReadFile(filepath.Join(root, "internal", "modules", "optional.go"))
	if err != nil {
		t.Fatal(err)
	}

	update, err := engine.Plan(context.Background(), root, Operation{
		Kind: OpUpdate, Modules: []string{"ggg/element/button", "ggg/component/card"},
	})
	if err != nil {
		t.Fatalf("Plan(targeted update): %v", err)
	}

	updated := lockedRow(t, update, "ggg/element/button")
	if updated.Revision != 2 || updated.Contract != 2 {
		t.Fatalf("updated button row = rev %d contract %d, want 2/2", updated.Revision, updated.Contract)
	}
	if updated.SourceCommit != testCommitB {
		t.Fatalf("updated button source commit = %q, want %q", updated.SourceCommit, testCommitB)
	}
	card := lockedRow(t, update, "ggg/component/card")
	if card.Manifest.Requires[0].Contract.Max != 2 {
		t.Fatalf("updated card requirement = %#v, want max 2", card.Manifest.Requires[0].Contract)
	}
	// The retained module keeps every byte of its prior provenance.
	retained := lockedRow(t, update, "ggg/page/optional")
	if retained.Revision != 1 || retained.SourceCommit != testCommitA || retained.SnapshotSHA256 != testCommitA {
		t.Fatalf("retained optional row = rev %d commit %q snapshot %q, want rev 1 at %q",
			retained.Revision, retained.SourceCommit, retained.SnapshotSHA256, testCommitA)
	}
	if got, want := update.RegistryCommit, registryCommitForModules(update.Lock.Modules); got != want {
		t.Fatalf("mixed-snapshot registry commit = %q, want %q", got, want)
	}

	materializeConflictPlan(t, root, update)
	optionalAfter, err := os.ReadFile(filepath.Join(root, "internal", "modules", "optional.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(optionalBefore) != string(optionalAfter) {
		t.Fatal("targeted update rewrote a retained module's bytes")
	}
	buttonAfter, err := os.ReadFile(filepath.Join(root, "internal", "modules", "button.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buttonAfter), "Upstream = 2") {
		t.Fatal("targeted update did not advance the named module's bytes")
	}

	// Repeating the same targeted update over the converged tree is a
	// no-op: nothing changes and the mixed-snapshot commit is stable. (A
	// full sync would legitimately advance the retained modules — that is
	// its job.)
	again, err := engine.Plan(context.Background(), root, Operation{
		Kind: OpUpdate, Modules: []string{"ggg/element/button", "ggg/component/card"},
	})
	if err != nil {
		t.Fatalf("Plan(repeated targeted update): %v", err)
	}
	for _, change := range again.Changes {
		if change.Kind != ChangeUnchanged {
			t.Fatalf("repeated targeted update wants %s %s; the tree is not converged", change.Kind, change.Path)
		}
	}
	if again.RegistryCommit != update.RegistryCommit {
		t.Fatalf("repeated targeted update commit = %q, want %q", again.RegistryCommit, update.RegistryCommit)
	}
}

func TestTargetedUpdateNamesReverseDependentsThatMustMoveTogether(t *testing.T) {
	root, engine := targetedUpdateFixture(t)
	_, err := engine.Plan(context.Background(), root, Operation{
		Kind: OpUpdate, Modules: []string{"ggg/element/button"},
	})
	if err == nil || !strings.Contains(err.Error(), "must move together") {
		t.Fatalf("Plan(targeted update of button only) = %v, want reverse-dependency conflict", err)
	}
}

func TestTargetedUpdateRefusesRefChangeWithModuleOperands(t *testing.T) {
	root, engine := targetedUpdateFixture(t)
	_, err := engine.Plan(context.Background(), root, Operation{
		Kind: OpUpdate, Modules: []string{"ggg/element/button"}, RegistryRef: "v2",
	})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("Plan(update with operands and ref) = %v, want refusal", err)
	}
	_, err = engine.Plan(context.Background(), root, Operation{
		Kind: OpUpdate, TargetedRegistry: "ggg", RegistryRef: "v2", Modules: []string{"ggg/element/button"},
	})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("Plan(update with targeted registry and operands) = %v, want refusal", err)
	}
}

func TestTargetedUpdateRefChangeRequiresKnownRegistryAndRef(t *testing.T) {
	root, engine := targetedUpdateFixture(t)
	_, err := engine.Plan(context.Background(), root, Operation{
		Kind: OpUpdate, TargetedRegistry: "ggg",
	})
	if err == nil || !strings.Contains(err.Error(), "requires --ref") {
		t.Fatalf("Plan(update --registry without --ref) = %v, want refusal", err)
	}
	_, err = engine.Plan(context.Background(), root, Operation{
		Kind: OpUpdate, TargetedRegistry: "nope", RegistryRef: "v2",
	})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Plan(update --registry nope) = %v, want refusal", err)
	}
}

// dualNamespaceSource resolves each namespace from its own ref map, so a
// targeted ref change on one registry is observable while the other stays put.
type dualNamespaceSource struct {
	snapshots map[string]map[string]Snapshot
}

func (s dualNamespaceSource) Resolve(_ context.Context, registry ProjectRegistry) (Snapshot, error) {
	byRef, ok := s.snapshots[registry.Namespace]
	if !ok {
		return Snapshot{}, nil
	}
	snapshot, ok := byRef[registry.Ref]
	if !ok {
		return Snapshot{}, nil
	}
	return snapshot, nil
}

func TestTargetedUpdateRefChangeMovesOnlyTheNamedRegistry(t *testing.T) {
	first, _ := conflictRegistries(t)
	core := Snapshot{Commit: testCommitA, FS: first}
	source := dualNamespaceSource{snapshots: map[string]map[string]Snapshot{
		"ggg":  {"main": core, "v1": core},
		"acme": {"pin": {Commit: "ext-commit-1", FS: externalFixtureMapFS(t, nil)}},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema: 2,
		Registries: []ProjectRegistry{
			{Namespace: "ggg", Source: "github", Repository: "local/core", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="},
			{Namespace: "acme", Source: "github", Repository: "local/ext", Ref: "pin", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="},
		},
		Modules: []string{"ggg/component/card"}, Exclude: []string{},
		Providers: map[string]ProviderSelections{}, Deployment: "",
	})
	engine := New(Options{Source: source})
	plan, err := engine.Plan(context.Background(), root, Operation{
		Kind: OpUpdate, TargetedRegistry: "acme", RegistryRef: "pin",
	})
	if err != nil {
		t.Fatalf("Plan(ref move): %v", err)
	}
	var coreRef, extRef string
	for _, registry := range plan.Project.Registries {
		switch registry.Namespace {
		case "ggg":
			coreRef = registry.Ref
		case "acme":
			extRef = registry.Ref
		}
	}
	if coreRef != "main" || extRef != "pin" {
		t.Fatalf("refs after targeted ref move = ggg:%q acme:%q, want ggg untouched", coreRef, extRef)
	}
	if len(plan.Lock.Registries) != 2 {
		t.Fatalf("lock registries = %d, want both recorded", len(plan.Lock.Registries))
	}
}

func TestSetRegistriesReplacesSourcesInOnePlan(t *testing.T) {
	root, engine := targetedUpdateFixture(t)
	plan, err := engine.Plan(context.Background(), root, Operation{
		Kind: OpSync,
		SetRegistries: []ProjectRegistry{
			{Namespace: "ggg", Source: "directory", Path: "vendored-registry"},
		},
	})
	if err != nil {
		t.Fatalf("Plan(set registries): %v", err)
	}
	if len(plan.Project.Registries) != 1 || plan.Project.Registries[0].Source != "directory" {
		t.Fatalf("project registries = %#v, want the replacement set", plan.Project.Registries)
	}
}

func TestRegistryKeyLifecycle(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "registry-private.key")
	publicPath := filepath.Join(dir, "registry-public.key")

	fingerprint, err := GenerateRegistryKeyPair(privatePath, publicPath)
	if err != nil {
		t.Fatalf("GenerateRegistryKeyPair: %v", err)
	}
	if !strings.HasPrefix(fingerprint, "sha256:") {
		t.Fatalf("fingerprint = %q", fingerprint)
	}
	privateInfo, err := os.Stat(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if privateInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %#o, want 0600", privateInfo.Mode().Perm())
	}
	publicInfo, err := os.Stat(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if publicInfo.Mode().Perm() != 0o644 {
		t.Fatalf("public key mode = %#o, want 0644", publicInfo.Mode().Perm())
	}
	if _, err := GenerateRegistryKeyPair(privatePath, filepath.Join(dir, "other.key")); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("regeneration over existing private key = %v, want refusal", err)
	}
	if _, err := GenerateRegistryKeyPair(filepath.Join(dir, "another.key"), publicPath); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("regeneration over existing public key = %v, want refusal", err)
	}

	// Sign and verify an empty-but-valid registry tree.
	tree := filepath.Join(dir, "registry")
	written, err := InitRegistryTree(tree, "acme", "example.com/acme/registry")
	if err != nil {
		t.Fatalf("InitRegistryTree: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("init wrote no scaffold files")
	}
	if again, err := InitRegistryTree(tree, "acme", "example.com/acme/registry"); err != nil || len(again) != 0 {
		t.Fatalf("re-init = %d written (%v), want an idempotent no-op", len(again), err)
	}
	private, err := LoadRegistryPrivateKey(privatePath)
	if err != nil {
		t.Fatalf("LoadRegistryPrivateKey: %v", err)
	}
	if _, err := SignRegistrySnapshot(tree, private); err != nil {
		t.Fatalf("SignRegistrySnapshot: %v", err)
	}
	publicBytes, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := strings.TrimSpace(string(publicBytes))
	digest, err := VerifyRegistrySnapshot(tree, publicKey)
	if err != nil {
		t.Fatalf("VerifyRegistrySnapshot: %v", err)
	}
	if digest == "" {
		t.Fatal("verification returned an empty digest")
	}
	if err := os.WriteFile(filepath.Join(tree, "registry", "tampered.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRegistrySnapshot(tree, publicKey); err == nil || !strings.Contains(err.Error(), "unlisted") {
		t.Fatalf("verify after payload injection = %v, want unlisted refusal", err)
	}
	if err := os.Remove(filepath.Join(tree, "registry", "tampered.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryKeyRotationTransition(t *testing.T) {
	oldPrivate := mustTestKey(t, "old")
	newPrivate := mustTestKey(t, "new")
	oldPublic := base64Key(t, oldPrivate.Public().(ed25519.PublicKey))
	newPublic := base64Key(t, newPrivate.Public().(ed25519.PublicKey))

	tree := filepath.Join(t.TempDir(), "registry")
	if _, err := InitRegistryTree(tree, "acme", "example.com/acme/registry"); err != nil {
		t.Fatal(err)
	}
	if _, err := SignRegistrySnapshot(tree, oldPrivate); err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	if err := WriteRegistryKeyRotation(tree, oldPrivate, newPrivate, future); err != nil {
		t.Fatalf("WriteRegistryKeyRotation: %v", err)
	}

	// Before not_before the old key stays effective and verification passes
	// through the pinned key's signature.
	if _, err := VerifyRegistrySnapshot(tree, oldPublic); err != nil {
		t.Fatalf("verify before not_before: %v", err)
	}
	// The new key alone is not trusted before not_before.
	if _, err := VerifyRegistrySnapshot(tree, newPublic); err == nil {
		t.Fatal("verify under the new key before not_before passed")
	}

	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if err := WriteRegistryKeyRotation(tree, oldPrivate, newPrivate, past); err != nil {
		t.Fatal(err)
	}
	// After not_before the transition signature carries the declared key.
	if _, err := VerifyRegistrySnapshot(tree, oldPublic); err != nil {
		t.Fatalf("verify after not_before under pinned old key: %v", err)
	}
	// A consumer pinning only the new key cannot complete the dual
	// verification while the record is present — the pinned-old-key
	// signature is part of the acceptance contract — so it keeps refusing
	// until the publisher republishes a clean snapshot.
	if _, err := VerifyRegistrySnapshot(tree, newPublic); err == nil {
		t.Fatal("verify under the new key while the rotation record is present passed")
	}
	// A record whose old fingerprint does not match the pinned key refuses.
	data, err := os.ReadFile(filepath.Join(tree, RegistryKeyRotationPath))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record["old_fingerprint"] = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if err := writeRotationRecord(t, tree, record); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRegistrySnapshot(tree, oldPublic); err == nil || !strings.Contains(err.Error(), "old_fingerprint") {
		t.Fatalf("verify with mismatched old fingerprint = %v, want refusal", err)
	}

	// The publisher completes the rotation: the record is republished away
	// and the snapshot is signed under the new key. The new key alone then
	// verifies and the old key is dead.
	if err := os.Remove(filepath.Join(tree, RegistryKeyRotationPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := SignRegistrySnapshot(tree, newPrivate); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRegistrySnapshot(tree, newPublic); err != nil {
		t.Fatalf("verify after the rotation record is republished away: %v", err)
	}
	if _, err := VerifyRegistrySnapshot(tree, oldPublic); err == nil {
		t.Fatal("the old key still verified after rotation completed")
	}
}

func mustTestKey(t *testing.T, name string) ed25519.PrivateKey {
	t.Helper()
	seed := sha256.Sum256([]byte("gogogadget-test-rotation-" + name))
	return ed25519.NewKeyFromSeed(seed[:])
}

func base64Key(t *testing.T, key ed25519.PublicKey) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(key)
}

func writeRotationRecord(t *testing.T, tree string, record map[string]any) error {
	t.Helper()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(tree, RegistryKeyRotationPath), append(data, '\n'), 0o644)
}
