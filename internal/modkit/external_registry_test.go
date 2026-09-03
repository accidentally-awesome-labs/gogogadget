package modkit

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const externalFixturePublicKey = "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="

func externalFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "registry", "external-testdata"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "registry.snapshot.json")); err != nil {
		t.Fatalf("external fixture is missing signed snapshot: %v", err)
	}
	return root
}

func externalFixtureMapFS(t *testing.T, rootJSON []byte) fstest.MapFS {
	t.Helper()
	source := os.DirFS(externalFixtureRoot(t))
	out := fstest.MapFS{}
	if err := fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(source, name)
		if err != nil {
			return err
		}
		if rootJSON != nil && name == "registry.json" {
			data = rootJSON
		}
		out[name] = &fstest.MapFile{Data: data}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExternalFixtureImportRewriteUsesAlignedCanonicalTarget(t *testing.T) {
	content := []byte("package bridge\n\nimport (\n\t\"example.com/acme/gadget-registry/internal/fixture/mailcontracts\"\n\t\"github.com/gogogadget/gogogadget/internal/mail\"\n)\n")
	got, err := rewriteModuleImportsForPrefixes("mail_bridge.go", content,
		[]string{"github.com/gogogadget/gogogadget", "example.com/acme/gadget-registry"}, "example.com/derivative")
	if err != nil {
		t.Fatalf("rewriteModuleImportsForPrefixes: %v", err)
	}
	text := string(got)
	for _, want := range []string{
		`"example.com/derivative/internal/fixture/mailcontracts"`,
		`"example.com/derivative/internal/mail"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("rewritten imports missing %q in %s", want, text)
		}
	}
}

func TestProjectLocalRegistryIsContainedAndNamespaced(t *testing.T) {
	registry, err := ProjectLocalRegistry(t.TempDir(), "acme-project")
	if err != nil {
		t.Fatal(err)
	}
	if registry.Source != "directory" || registry.Namespace != "acme-project" || registry.Path != "registry" {
		t.Fatalf("local registry = %#v", registry)
	}
	for _, slug := range []string{"", "../escape", "ACME", "has space"} {
		if _, err := ProjectLocalRegistry(t.TempDir(), slug); err == nil {
			t.Fatalf("slug %q unexpectedly accepted", slug)
		}
	}
}

func TestExternalFixturePublicKeyFingerprintIsStable(t *testing.T) {
	key, err := base64.StdEncoding.DecodeString(externalFixturePublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("fixture key length = %d", len(key))
	}
	fingerprint, err := RegistryKeyFingerprint(externalFixturePublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fingerprint, "sha256:") || len(fingerprint) != len("sha256:")+64 {
		t.Fatalf("fingerprint = %q", fingerprint)
	}
}

func TestExternalFixtureSignatureSeedIsReproducible(t *testing.T) {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	private, err := RegistryPrivateKeyFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	public := base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey))
	if public != externalFixturePublicKey {
		t.Fatalf("derived public key = %q, want fixture key %q", public, externalFixturePublicKey)
	}
}
func TestRegistryCachePruneRetainsSnapshotDigest(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "commit-entry", "tree")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot := []byte("signed snapshot bytes")
	if err := os.WriteFile(filepath.Join(keep, RegistrySnapshotPath), snapshot, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(snapshot)
	keepDigest := hex.EncodeToString(sum[:])
	remove := filepath.Join(root, "stale-entry")
	if err := os.MkdirAll(remove, 0o755); err != nil {
		t.Fatal(err)
	}
	if removed, err := PruneRegistryCache(root, []string{keepDigest}); err != nil || removed != 1 {
		t.Fatalf("prune = %d, %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "commit-entry")); err != nil {
		t.Fatalf("digest-referenced cache removed: %v", err)
	}
}
func TestPlanEnvelopeRegistryCommitMatchesRealEnginePlan(t *testing.T) {
	root := writeTargetProject(t, "example.com/plan-envelope", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "acme/registry", Ref: "main", PublicKey: externalFixturePublicKey}},
		Modules:    []string{"ggg/component/card"},
		Exclude:    []string{},
		Providers:  map[string]ProviderSelections{},
	})
	engine := New(Options{Source: staticSource{snapshot: Snapshot{
		Commit: testCommitA, FS: plannerRegistry(t),
	}}})
	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Engine.Plan: %v", err)
	}
	envelope := Envelope{
		RegistryCommit: plan.RegistryCommit,
		Resolved:       plan.Resolved,
		Changes:        plan.Changes,
		Conflicts:      plan.Conflicts,
		Diagnostics:    plan.Diagnostics,
		Exit:           ExitOK,
	}
	if envelope.RegistryCommit != registryCommitForModules(plan.Lock.Modules) {
		t.Fatalf("envelope registry commit = %q, want tuple hash %q", envelope.RegistryCommit, registryCommitForModules(plan.Lock.Modules))
	}
	if envelope.RegistryCommit != plan.Lock.RegistryCommit || envelope.RegistryCommit != plan.RegistryCommit {
		t.Fatalf("plan/envelope lock identity mismatch: plan=%q lock=%q envelope=%q", plan.RegistryCommit, plan.Lock.RegistryCommit, envelope.RegistryCommit)
	}
}

type dualRegistrySource struct {
	core, external Snapshot
}

func (s dualRegistrySource) Resolve(_ context.Context, registry ProjectRegistry) (Snapshot, error) {
	switch registry.Namespace {
	case "ggg":
		return s.core, nil
	case "acme":
		return s.external, nil
	default:
		return Snapshot{}, fmt.Errorf("unknown registry namespace %q", registry.Namespace)
	}
}
