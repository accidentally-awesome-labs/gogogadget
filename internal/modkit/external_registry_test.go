package modkit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
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

func TestExternalFixtureSnapshotIsExactSignedCatalog(t *testing.T) {
	root := externalFixtureRoot(t)
	want, err := os.ReadFile(filepath.Join(root, RegistrySnapshotPath))
	if err != nil {
		t.Fatal(err)
	}
	got, err := BuildRegistrySnapshot(os.DirFS(root))
	if err != nil {
		t.Fatalf("BuildRegistrySnapshot: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("registry.snapshot.json is not the deterministic codec output")
	}
	gotDigest, err := verifySnapshotFiles(os.DirFS(root), externalFixturePublicKey, false)
	if err != nil {
		t.Fatalf("verifySnapshotFiles: %v", err)
	}
	if gotDigest == "" {
		t.Fatal("snapshot verification returned an empty digest")
	}
	catalog, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if catalog.Namespace != "acme" || catalog.CanonicalModule != "example.com/acme/gadget-registry" {
		t.Fatalf("fixture identity = %q/%q", catalog.Namespace, catalog.CanonicalModule)
	}
	if len(catalog.Modules) != 1 {
		t.Fatalf("fixture modules = %d, want one", len(catalog.Modules))
	}
	module := catalog.Modules[0]
	if module.ID != "acme/system/mail-bridge" {
		t.Fatalf("fixture module = %q", module.ID)
	}
	if len(module.Requires) != 1 || module.Requires[0].ID != "ggg/system/mail" || module.Requires[0].Contract != (ContractBounds{Min: 1, Max: 1}) {
		t.Fatalf("fixture core slot requirement = %#v", module.Requires)
	}
	if len(module.Dependencies.Go) != 1 || module.Dependencies.Go[0] != (GoDependency{Module: "github.com/stretchr/testify", Version: "v1.11.1"}) {
		t.Fatalf("fixture third-party dependency = %#v", module.Dependencies.Go)
	}
	if module.Runtime.System == nil || module.Runtime.System.Adapter == nil || module.Runtime.System.Adapter.Slot != "ggg/mail" {
		t.Fatal("fixture does not declare a mail provider adapter")
	}
}

func TestExternalFixtureSnapshotRefusesTampering(t *testing.T) {
	t.Run("tampered payload", func(t *testing.T) {
		fsys := externalFixtureMapFS(t, nil)
		fsys["registry/modules/system/mail-bridge/mail_bridge.go.txt"].Data = []byte("tampered")
		if _, err := verifySnapshotFiles(fsys, externalFixturePublicKey, false); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("tampered payload error = %v", err)
		}
	})
	t.Run("bad signature", func(t *testing.T) {
		fsys := externalFixtureMapFS(t, nil)
		fsys[RegistrySignaturePath].Data = []byte(base64.StdEncoding.EncodeToString([]byte("bad")) + "\n")
		if _, err := verifySnapshotFiles(fsys, externalFixturePublicKey, false); err == nil || !strings.Contains(err.Error(), "signature") {
			t.Fatalf("bad signature error = %v", err)
		}
	})
	t.Run("unlisted payload", func(t *testing.T) {
		fsys := externalFixtureMapFS(t, nil)
		fsys["registry/modules/system/mail-bridge/unlisted.go"] = &fstest.MapFile{Data: []byte("package mailbridge")}
		if _, err := verifySnapshotFiles(fsys, externalFixturePublicKey, false); err == nil || !strings.Contains(err.Error(), "unlisted") {
			t.Fatalf("unlisted payload error = %v", err)
		}
	})
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

func TestExternalFixtureContractRangeRefusalHappensBeforeSelection(t *testing.T) {
	root := externalFixtureRoot(t)
	catalog, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	coreRoot := filepath.Join("..", "..", "registry")
	var coreDoc ModuleDocument
	if err := readCatalogJSON(os.DirFS(coreRoot), "modules/system/mail/module.json", &coreDoc); err != nil {
		t.Fatal(err)
	}
	coreDoc.Module.Requires = []Requirement{}
	adapter := catalog.Modules[0]
	adapter.Requires[0].Contract = ContractBounds{Min: 2, Max: 2}
	adapter.Runtime.System.Adapter.Targets[0].Environments = []string{"development", "test", "production"}
	project := Project{Schema: 2, Modules: []string{coreDoc.Module.ID}, Exclude: []string{}, Providers: map[string]ProviderSelections{
		"ggg/mail": {
			Development: ProviderSelection{Adapter: adapter.ID, Target: "bridge"},
			Test:        ProviderSelection{Adapter: adapter.ID, Target: "bridge"},
			Production:  ProviderSelection{Adapter: adapter.ID, Target: "bridge"},
		},
	}}
	catalog.Modules = []Manifest{adapter, coreDoc.Module}
	_, err = resolveSelectedGraph(context.Background(), project, catalog)
	if err == nil || !strings.Contains(err.Error(), "contract") {
		t.Fatalf("contract-range error = %v", err)
	}
}

func TestExternalFixtureUnionRefusesCanonicalCollision(t *testing.T) {
	root := externalFixtureRoot(t)
	external, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	coreRoot := filepath.Join("..", "..", "registry", "testdata")
	core, err := LoadCatalog(os.DirFS(coreRoot))
	if err != nil {
		t.Fatal(err)
	}
	_, err = mergeResolvedCatalogs(context.Background(), []resolvedRegistry{
		{config: ProjectRegistry{Namespace: core.Namespace}, snapshot: Snapshot{FS: os.DirFS(coreRoot)}},
		{config: ProjectRegistry{Namespace: external.Namespace}, snapshot: Snapshot{FS: os.DirFS(root)}},
	})
	if err != nil {
		t.Fatalf("core plus external union unexpectedly refused: %v", err)
	}

	duplicateRoot := []byte(`{"schema":2,"namespace":"acme-copy","canonical_module":"` + external.CanonicalModule + `","includes":["registry/elements.json","registry/components.json","registry/pages.json","registry/workflows.json","registry/systems.json","registry/profiles.json"]}`)
	duplicateFS := externalFixtureMapFS(t, duplicateRoot)
	modulePath := "registry/modules/system/mail-bridge.json"
	duplicateFS[modulePath].Data = bytes.Replace(duplicateFS[modulePath].Data, []byte(`"acme/system/mail-bridge"`), []byte(`"acme-copy/system/mail-bridge"`), 1)
	_, err = mergeResolvedCatalogs(context.Background(), []resolvedRegistry{
		{config: ProjectRegistry{Namespace: external.Namespace}, snapshot: Snapshot{FS: os.DirFS(root)}},
		{config: ProjectRegistry{Namespace: "acme-copy"}, snapshot: Snapshot{FS: duplicateFS}},
	})
	if err == nil || !strings.Contains(err.Error(), "canonical module") {
		t.Fatalf("canonical collision error = %v", err)
	}
	_, err = mergeResolvedCatalogs(context.Background(), []resolvedRegistry{
		{config: ProjectRegistry{Namespace: external.Namespace}, snapshot: Snapshot{FS: os.DirFS(root)}},
		{config: ProjectRegistry{Namespace: external.Namespace}, snapshot: Snapshot{FS: os.DirFS(root)}},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate scoped module id") {
		t.Fatalf("duplicate scoped id error = %v", err)
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
func TestExternalFixtureDependencyInstallAndOwnerOnlyRemoval(t *testing.T) {
	root := t.TempDir()
	original := []byte("module example.com/derivative\n\ngo 1.26.6\n")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCatalog(os.DirFS(externalFixtureRoot(t)))
	if err != nil {
		t.Fatal(err)
	}
	dependencies, err := EffectiveDependencies(catalog.Modules)
	if err != nil {
		t.Fatal(err)
	}
	locked := []LockedDependency{{Module: dependencies.Go[0].Module, ManagedVersion: dependencies.Go[0].Version, Owners: []string{catalog.Modules[0].ID}}}
	next, err := UpdateGoMod(context.Background(), root, locked, nil)
	if err != nil {
		t.Fatalf("install dependency: %v", err)
	}
	if len(next) != 1 || next[0].Owners[0] != catalog.Modules[0].ID {
		t.Fatalf("dependency ownership = %#v", next)
	}
	installed, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installed), "github.com/stretchr/testify v1.11.1") {
		t.Fatalf("installed go.mod does not contain fixture dependency: %s", installed)
	}
	if _, err := reconcileGoMod(context.Background(), root, next, nil, nil); err != nil {
		t.Fatalf("remove dependency: %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("go.mod was not restored after owner removal: %s", restored)
	}
}
func TestExternalFixtureRejectsWrongNamespace(t *testing.T) {
	root := externalFixtureRoot(t)
	_, err := validateGitHubSnapshot(
		Snapshot{FS: os.DirFS(root)},
		ProjectRegistry{Namespace: "wrong", PublicKey: externalFixturePublicKey},
	)
	if err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("wrong namespace error = %v", err)
	}
}
func TestExternalFixtureTargetCollisionRefusesBeforeWrites(t *testing.T) {
	root := externalFixtureRoot(t)
	catalog, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	first := catalog.Modules[0]
	targetA := Manifest{ID: first.ID, Files: []ManifestFile{{Target: first.Files[0].Target}}}
	targetB := Manifest{ID: "acme/system/mail-bridge-copy", Files: []ManifestFile{{Target: first.Files[0].Target}}}
	if err := preflightNamespaces(context.Background(), []Manifest{targetA, targetB}); err == nil || !strings.Contains(err.Error(), "target namespace") {
		t.Fatalf("target collision error = %v", err)
	}
}
func TestExternalFixtureDependencyConflictAndCycleRefuse(t *testing.T) {
	root := externalFixtureRoot(t)
	catalog, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	conflictA := catalog.Modules[0]
	conflictB := conflictA
	conflictB.ID = "acme/system/mail-bridge-copy"
	conflictA.Dependencies.Containers = []ContainerDependency{{Name: "mail", Image: "mail/image@sha256:" + strings.Repeat("a", 64)}}
	conflictB.Dependencies.Containers = []ContainerDependency{{Name: "mail", Image: "mail/other@sha256:" + strings.Repeat("b", 64)}}
	if _, err := EffectiveDependencies([]Manifest{conflictA, conflictB}); err == nil || !strings.Contains(err.Error(), "conflicting container") {
		t.Fatalf("dependency conflict error = %v", err)
	}

	cycleA := Manifest{ID: "acme/system/cycle-a", Requires: []Requirement{{ID: "acme/system/cycle-b", Contract: ContractBounds{Min: 1, Max: 1}}}}
	cycleB := Manifest{ID: "acme/system/cycle-b", Requires: []Requirement{{ID: "acme/system/cycle-a", Contract: ContractBounds{Min: 1, Max: 1}}}}
	_, err = resolveSelectedGraph(context.Background(), Project{Schema: 2, Modules: []string{cycleA.ID}, Exclude: []string{}, Providers: map[string]ProviderSelections{}}, Catalog{Modules: []Manifest{cycleA, cycleB}})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("dependency cycle error = %v", err)
	}
}
func TestExternalFixtureInstallCompileRemoveRestoresTree(t *testing.T) {
	root := externalFixtureRoot(t)
	catalog, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	module := catalog.Modules[0]
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "go.mod"), []byte("module example.com/external-smoke\n\ngo 1.26.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, projectRoot, "internal/config/config.go", []byte("package config\n\ntype Config struct{}\n"))
	writeTestFile(t, projectRoot, "internal/mail/mail.go", []byte("package mail\n\nimport \"context\"\n\ntype Message struct{}\ntype Sender interface { Send(context.Context, Message) error }\n"))
	baseline := map[string][]byte{}
	for _, file := range module.Files {
		if file.Class == FileClassTest {
			continue
		}
		payload, err := fs.ReadFile(catalog.ModuleSources[module.ID], file.Source)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.FromSlash(file.Target)
		content, err := rewriteModuleImportsForPrefixes(target, payload, []string{"github.com/gogogadget/gogogadget", catalog.CanonicalModule}, "example.com/external-smoke")
		if err != nil {
			t.Fatal(err)
		}
		targetPath := filepath.Join(projectRoot, target)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			t.Fatal(err)
		}
		baseline[target] = nil
		if err := os.WriteFile(targetPath, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(context.Background(), "go", "test", "./...")
	cmd.Dir = projectRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external adapter compile: %v\n%s", err, output)
	}
	for target := range baseline {
		if err := os.Remove(filepath.Join(projectRoot, target)); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"internal/config/config.go", "internal/mail/mail.go", "go.mod"} {
		if _, err := os.Stat(filepath.Join(projectRoot, target)); err != nil {
			t.Fatalf("baseline file %s disappeared: %v", target, err)
		}
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
func TestRegistryProvenanceTupleHashAndLockMetadata(t *testing.T) {
	root := externalFixtureRoot(t)
	snapshot := Snapshot{Commit: strings.Repeat("c", 40), SnapshotSHA256: "snapshot-acme", FS: os.DirFS(root), Registry: RegistryRoot{Schema: 2, Namespace: "acme", CanonicalModule: "example.com/acme/gadget-registry"}}
	catalog, err := LoadCatalog(snapshot.FS)
	if err != nil {
		t.Fatal(err)
	}
	hash, registries, snapshots := registryProvenance([]resolvedRegistry{{config: ProjectRegistry{Namespace: "acme", Source: "directory", Path: "registry"}, snapshot: snapshot}}, catalog, catalog.Modules)
	if len(registries) != 1 || registries[0].Namespace != "acme" || len(snapshots) != 1 || snapshots[0].CacheKey != "snapshot-acme" {
		t.Fatalf("provenance metadata = %#v %#v", registries, snapshots)
	}
	if hash == "" || hash == snapshot.Commit {
		t.Fatalf("tuple registry commit = %q", hash)
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
	envelope := planEnvelope(plan, exitOK)
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

func TestExternalFixtureEngineInstallApplyRemoveCompileAndRestore(t *testing.T) {
	core := registryFixture(t)
	configContent := []byte("package config\n\ntype Config struct{}\n")
	config := Manifest{
		ID: "ggg/system/config", Kind: ModuleSystem, Name: "config", Revision: 1, Contract: 1,
		Title: "Config", Description: "Core configuration.", Requires: []Requirement{},
		Dependencies: Dependencies{Go: []GoDependency{}, Tools: []ToolArtifact{}, Containers: []ContainerDependency{}},
		Files:        []ManifestFile{{Source: "registry/modules/system/config/config.go", Target: "internal/config/config.go", Class: FileClassGo, SHA256: sha256Hex(configContent), RewriteModule: true, Contract: true}},
		Claims:       NamespaceClaims{Packages: []string{"internal/config"}},
		Runtime: RuntimeContributions{System: &SystemContribution{
			Package: "internal/config", Constructor: "NewModule",
			Needs: []RuntimeNeed{}, Provides: []RuntimeProvide{{Field: "Config", Capability: "config", Type: "*config.Config"}},
		}},
		Migrations: []ManifestMigration{}, Environment: []EnvironmentVariable{}, Docs: []DocumentationRef{}, Tests: TestMetadata{}, Data: []DataDeclaration{},
		RemovalPolicy: RemovalFree,
	}
	addPlannerModule(t, core, config, configContent)
	mailContent := []byte("package mail\n\nimport \"context\"\n\ntype Message struct { To string }\ntype Sender interface { Send(context.Context, Message) error }\n")
	mail := Manifest{
		ID: "ggg/system/mail", Kind: ModuleSystem, Name: "mail", Revision: 1, Contract: 1,
		Title: "Mail seam", Description: "Core mail contract.", Requires: []Requirement{},
		Dependencies: Dependencies{Go: []GoDependency{}, Tools: []ToolArtifact{}, Containers: []ContainerDependency{}},
		Files:        []ManifestFile{{Source: "registry/modules/system/mail/mail.go", Target: "internal/mail/mail.go", Class: FileClassGo, SHA256: sha256Hex(mailContent), RewriteModule: true, Contract: true}},
		Claims:       NamespaceClaims{Packages: []string{"internal/mail"}, ProviderSlots: []string{"ggg/mail"}},
		Runtime:      RuntimeContributions{ProviderSlots: []ProviderSlotContribution{{ID: "ggg/mail", Capabilities: []CapabilityContribution{{Capability: "mail.sender", Type: "mail.Sender"}}}}},
		Migrations:   []ManifestMigration{}, Environment: []EnvironmentVariable{}, Docs: []DocumentationRef{}, Tests: TestMetadata{}, Data: []DataDeclaration{},
		RemovalPolicy: RemovalFree,
	}
	addPlannerModule(t, core, mail, mailContent)
	external := externalFixtureMapFS(t, nil)
	modulePath := "registry/modules/system/mail-bridge.json"
	var document ModuleDocument
	if err := decodeStrict(external[modulePath].Data, &document); err != nil {
		t.Fatal(err)
	}
	document.Module.Runtime.System.Adapter.Targets[0].Environments = []string{"development", "test", "production"}
	putJSON(t, external, modulePath, document)
	projectRoot := t.TempDir()
	writeTestFile(t, projectRoot, "go.mod", []byte("module example.com/external-engine\n\ngo 1.26.6\n\nrequire github.com/stretchr/testify v1.11.1\n"))
	project := Project{
		Schema: 2,
		Registries: []ProjectRegistry{
			{Namespace: "ggg", Source: "directory", Path: "registry"},
			{Namespace: "acme", Source: "directory", Path: "registry"},
		},
		Modules: []string{"ggg/system/config", "ggg/system/mail", "acme/system/mail-bridge"}, Exclude: []string{},
		Providers: map[string]ProviderSelections{"ggg/mail": {
			Development: ProviderSelection{Adapter: "acme/system/mail-bridge", Target: "bridge"},
			Test:        ProviderSelection{Adapter: "acme/system/mail-bridge", Target: "bridge"},
			Production:  ProviderSelection{Adapter: "acme/system/mail-bridge", Target: "bridge"},
		}},
	}
	projectData, err := MarshalProject(project)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, projectRoot, ProjectFileName, projectData)
	source := dualRegistrySource{
		core:     Snapshot{Commit: testCommitA, FS: core},
		external: Snapshot{Commit: testCommitB, SnapshotSHA256: testCommitB, FS: external},
	}
	engine := New(Options{Source: source, Generator: &scriptedGenerator{}, ToolRunner: OSCommandRunner{}})
	plan, err := engine.Plan(context.Background(), projectRoot, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Engine.Plan: %v", err)
	}
	if _, err := engine.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Engine.Apply: %v", err)
	}
	bridgePath := filepath.Join(projectRoot, "internal/fixture/mailbridge/mail_bridge.go")
	if _, err := os.Stat(bridgePath); err != nil {
		t.Fatalf("external adapter was not installed: %v", err)
	}
	compile := exec.CommandContext(context.Background(), "go", "test", "./...")
	compile.Dir = projectRoot
	compile.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("external adapter compile: %v\n%s", err, output)
	}
	project.Providers = map[string]ProviderSelections{}
	projectData, err = MarshalProject(project)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, projectRoot, ProjectFileName, projectData)
	beforeRemove := map[string][]byte{}
	for _, name := range []string{"go.mod", "internal/mail/mail.go"} {
		data, err := os.ReadFile(filepath.Join(projectRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		beforeRemove[name] = data
	}
	remove, err := engine.Plan(context.Background(), projectRoot, Operation{Kind: OpRemove, Modules: []string{"acme/system/mail-bridge"}})
	if err != nil {
		t.Fatalf("Engine.Plan(remove): %v", err)
	}
	if _, err := engine.Apply(context.Background(), remove); err != nil {
		t.Fatalf("Engine.Apply(remove): %v", err)
	}
	if _, err := os.Stat(bridgePath); !os.IsNotExist(err) {
		t.Fatalf("external adapter source remains after removal: %v", err)
	}
	for name, want := range beforeRemove {
		got, err := os.ReadFile(filepath.Join(projectRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s changed after external install/remove", name)
		}
	}
}
func TestExternalRegistryRemainingSecurityContracts(t *testing.T) {
	t.Run("multi-prefix rewrite", func(t *testing.T) {
		input := []byte("package p\nimport (\"example.com/acme/gadget-registry/internal/fixture/mailcontracts\"; \"github.com/gogogadget/gogogadget/internal/mail\")\n")
		output, err := rewriteModuleImportsForPrefixes("p.go", input, []string{"example.com/acme/gadget-registry", "github.com/gogogadget/gogogadget"}, "example.com/app")
		if err != nil || !strings.Contains(string(output), "example.com/app/internal/fixture/mailcontracts") || !strings.Contains(string(output), "example.com/app/internal/mail") {
			t.Fatalf("multi-prefix rewrite = %q, %v", output, err)
		}
	})
	t.Run("nested canonical collision", func(t *testing.T) {
		root := externalFixtureRoot(t)
		fsMap := externalFixtureMapFS(t, []byte(`{"schema":2,"namespace":"acme-nested","canonical_module":"example.com/acme/gadget-registry/internal","includes":["registry/elements.json","registry/components.json","registry/pages.json","registry/workflows.json","registry/systems.json","registry/profiles.json"]}`))
		if _, err := mergeResolvedCatalogs(context.Background(), []resolvedRegistry{{config: ProjectRegistry{Namespace: "acme"}, snapshot: Snapshot{FS: os.DirFS(root)}}, {config: ProjectRegistry{Namespace: "acme-nested"}, snapshot: Snapshot{FS: fsMap}}}); err == nil || !strings.Contains(err.Error(), "canonical") {
			t.Fatalf("nested canonical collision = %v", err)
		}
	})
	t.Run("github token is header-only", func(t *testing.T) {
		source := GitHubSource{Token: "secret-token"}
		request, err := http.NewRequest(http.MethodGet, "https://github.example", nil)
		if err != nil {
			t.Fatal(err)
		}
		source.setGitHubHeaders(request, "application/json")
		if request.Header.Get("Authorization") != "Bearer secret-token" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		data, _ := json.Marshal(Project{Schema: 2, Providers: map[string]ProviderSelections{}, Registries: []ProjectRegistry{{Namespace: "acme", Source: "github", Repository: "acme/r", Ref: "main", PublicKey: externalFixturePublicKey}}})
		if strings.Contains(string(data), "secret-token") {
			t.Fatal("token serialized into project")
		}
	})
	t.Run("snapshot schema and stale payload", func(t *testing.T) {
		root := externalFixtureRoot(t)
		schema, err := os.ReadFile(filepath.Join("..", "..", "registry", "schema", "snapshot.schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		var instance map[string]any
		if err := json.Unmarshal(schema, &instance); err != nil || instance["$schema"] == nil {
			t.Fatalf("snapshot schema invalid: %v", err)
		}
		fsys := externalFixtureMapFS(t, nil)
		fsys["registry/modules/system/mail-bridge/mail_bridge.go.txt"].Data = []byte("stale")
		if _, err := verifySnapshotFiles(fsys, externalFixturePublicKey, false); err == nil {
			t.Fatal("stale payload accepted")
		}
		_ = root
	})
}
