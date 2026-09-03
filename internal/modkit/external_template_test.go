// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// The maintained external-registry template is the tree a third-party
// publisher copies. A template that has drifted out of validity is worse than
// no template: it teaches the wrong shape and fails in someone else's
// repository, where they have no way to tell whether the mistake is theirs.
// So it is held to the same checks a shipped module is, plus the ones only a
// signed external source has: reproducible snapshot bytes, a signature that
// verifies through the real path, and a refusal for each way the tree can be
// tampered with.

func externalTemplateRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ExternalTemplateDir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "registry.json")); err != nil {
		t.Fatalf("external registry template is missing its registry root: %v", err)
	}
	return root
}

func externalTemplateCatalog(t *testing.T) Catalog {
	t.Helper()
	catalog, err := LoadCatalog(os.DirFS(externalTemplateRoot(t)))
	if err != nil {
		t.Fatalf("LoadCatalog(template): %v", err)
	}
	return catalog
}

func externalTemplateMapFS(t *testing.T, rootJSON []byte) fstest.MapFS {
	t.Helper()
	source := os.DirFS(externalTemplateRoot(t))
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

func externalTemplateModule(t *testing.T) Manifest {
	t.Helper()
	catalog := externalTemplateCatalog(t)
	for _, module := range catalog.Modules {
		if module.ID == externalTemplateAdapterID {
			return module
		}
	}
	t.Fatalf("template does not publish %s", externalTemplateAdapterID)
	return Manifest{}
}

// TestExternalRegistryTemplateStaysValid is the "the template still works"
// gate: its manifests load, the adapter resolves the exact slot it claims
// against the core seam, its targets cover every environment without putting
// a development target in production, its configuration is mapped to target
// inputs, its dependencies resolve inside the declared contract ranges, and
// its declared contract tests exist as installed payloads.
func TestExternalRegistryTemplateStaysValid(t *testing.T) {
	catalog := externalTemplateCatalog(t)
	if catalog.Namespace != ExternalTemplateNamespace || catalog.CanonicalModule != ExternalTemplateCanonicalModule {
		t.Fatalf("template identity = %q/%q", catalog.Namespace, catalog.CanonicalModule)
	}
	core, err := LoadCatalog(os.DirFS(filepath.Join("..", "..")))
	if err != nil {
		t.Fatalf("LoadCatalog(core): %v", err)
	}
	coreByID := make(map[string]Manifest, len(core.Modules))
	for _, module := range core.Modules {
		coreByID[module.ID] = module
	}

	module := externalTemplateModule(t)
	if module.Kind != ModuleSystem {
		t.Fatalf("template adapter kind = %q", module.Kind)
	}
	if !strings.HasPrefix(module.ID, ExternalTemplateNamespace+"/") {
		t.Fatalf("template module %q is not scoped to the template namespace", module.ID)
	}

	// The slot exists in the core catalog and the adapter provides exactly
	// the capability set that slot declares — not a superset, not a subset.
	system := module.Runtime.System
	if system == nil || system.Adapter == nil {
		t.Fatal("template adapter declares no provider adapter")
	}
	if system.Adapter.Slot != externalTemplateSlot {
		t.Fatalf("template adapter slot = %q, want %q", system.Adapter.Slot, externalTemplateSlot)
	}
	var declared []CapabilityContribution
	for _, seam := range core.Modules {
		for _, slot := range seam.Runtime.ProviderSlots {
			if slot.ID == system.Adapter.Slot {
				declared = slot.Capabilities
			}
		}
	}
	if len(declared) == 0 {
		t.Fatalf("core catalog declares no slot %q", system.Adapter.Slot)
	}
	if len(system.Provides) != len(declared) {
		t.Fatalf("adapter provides %d capabilities, slot declares %d", len(system.Provides), len(declared))
	}
	for _, capability := range declared {
		matched := false
		for _, provide := range system.Provides {
			if provide.Capability == capability.Capability && provide.Type == capability.Type {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("adapter does not provide %s %s", capability.Capability, capability.Type)
		}
	}

	// Between them the targets fill every environment, and no target that is
	// only meant for a developer's machine is allowed in production.
	covered := map[string]string{}
	for _, target := range system.Adapter.Targets {
		for _, environment := range target.Environments {
			if previous, clash := covered[environment]; clash {
				t.Fatalf("targets %s and %s both claim %s", previous, target.ID, environment)
			}
			covered[environment] = target.ID
			if environment == "production" && target.Mode == "development" {
				t.Fatalf("development target %s claims production", target.ID)
			}
		}
	}
	for _, environment := range []string{"development", "test", "production"} {
		if covered[environment] == "" {
			t.Fatalf("no template target covers %s", environment)
		}
	}

	// Every declared environment key is owned by this module and scoped to
	// the target that needs it, so the generated config parser enforces
	// `required` only for the active selection.
	if len(module.Environment) == 0 {
		t.Fatal("template adapter declares no configuration")
	}
	for _, declaration := range module.Environment {
		if len(declaration.Targets) == 0 {
			t.Fatalf("environment key %s is not scoped to a target", declaration.Key)
		}
		for _, target := range declaration.Targets {
			if !strings.HasPrefix(target, module.ID+"@") {
				t.Fatalf("environment key %s names foreign target %q", declaration.Key, target)
			}
		}
	}

	// Dependencies resolve in the core catalog inside the declared contract
	// range. This is the check that makes a template a compatibility promise
	// rather than a snapshot of one afternoon.
	if len(module.Requires) == 0 {
		t.Fatal("template adapter requires nothing; it cannot implement a core seam")
	}
	for _, requirement := range module.Requires {
		dependency, ok := coreByID[requirement.ID]
		if !ok {
			t.Fatalf("template requires %s, which the core catalog does not publish", requirement.ID)
		}
		if dependency.Contract < requirement.Contract.Min || dependency.Contract > requirement.Contract.Max {
			t.Fatalf("template requires %s at [%d,%d] but it publishes contract %d",
				requirement.ID, requirement.Contract.Min, requirement.Contract.Max, dependency.Contract)
		}
	}

	// Lifecycle and health are declared, because this adapter holds a file
	// handle and talks to a managed service.
	if !system.Stop || !system.Health {
		t.Fatalf("template adapter lifecycle = start:%t stop:%t health:%t", system.Start, system.Stop, system.Health)
	}

	// Installing the module installs its proof: the declared test packages
	// exist as class-test payloads under claimed packages.
	if len(module.Tests.GoPackages) == 0 {
		t.Fatal("template adapter declares no test packages")
	}
	for _, pkg := range module.Tests.GoPackages {
		if !slices.Contains(module.Claims.Packages, pkg) {
			t.Fatalf("declared test package %s is not claimed", pkg)
		}
		found := false
		for _, file := range module.Files {
			if file.Class == FileClassTest && filepath.ToSlash(filepath.Dir(file.Target)) == pkg {
				found = true
			}
		}
		if !found {
			t.Fatalf("declared test package %s ships no class-test payload", pkg)
		}
	}

	// One contributed command, claimed, with its handler inside a claimed
	// package this module owns. Reservation against the built-in table is
	// asserted in internal/gggcli, which owns that table.
	if len(module.Runtime.CLI) != 1 {
		t.Fatalf("template contributes %d commands, want exactly one", len(module.Runtime.CLI))
	}
	command := module.Runtime.CLI[0]
	if !slices.Contains(module.Claims.CLI, command.Name) {
		t.Fatalf("contributed command %q is not claimed", command.Name)
	}
	if !slices.Contains(module.Claims.Packages, command.Package) {
		t.Fatalf("handler package %q is not claimed", command.Package)
	}

	// Payload bytes match the digests the manifest pins, which is what makes
	// the signed snapshot mean anything.
	sources := catalog.ModuleSources[module.ID]
	if sources == nil {
		t.Fatal("template catalog exposes no payload source")
	}
	for _, file := range module.Files {
		payload, readErr := fs.ReadFile(sources, file.Source)
		if readErr != nil {
			t.Fatalf("read payload %s: %v", file.Source, readErr)
		}
		if got := digestBytes(payload); got != file.SHA256 {
			t.Fatalf("payload %s digest = %s, manifest pins %s", file.Source, got, file.SHA256)
		}
	}
}

// TestExternalRegistryTemplateHandlerRespectsTheCLIBoundary runs the real
// scan over the real payload bytes: a contributed handler may reach the
// project only through the controller.
func TestExternalRegistryTemplateHandlerRespectsTheCLIBoundary(t *testing.T) {
	catalog := externalTemplateCatalog(t)
	module := externalTemplateModule(t)
	files := map[string][]byte{}
	for _, file := range module.Files {
		payload, err := fs.ReadFile(catalog.ModuleSources[module.ID], file.Source)
		if err != nil {
			t.Fatal(err)
		}
		rewritten, err := rewriteModuleImportsForPrefixes(filepath.FromSlash(file.Target), payload,
			[]string{"github.com/gogogadget/gogogadget", catalog.CanonicalModule}, "example.com/consumer")
		if err != nil {
			t.Fatalf("rewrite %s: %v", file.Target, err)
		}
		files[file.Target] = rewritten
	}
	if err := ValidateCLIHandlerPackages([]Manifest{module}, files); err != nil {
		t.Fatalf("template handler violates the CLI boundary: %v", err)
	}

	// The shipped scan reads direct imports only. That is fine for the
	// framework's own refusal, but an exemplar must not teach the shape that
	// walks around it: a handler one hop from a package holding an
	// *http.Client and a bearer token has not respected the boundary, it has
	// routed around it. So this walks the handler's imports one hop inside
	// the template's own packages and fails on a banned import reached that
	// way. (The scan's general non-transitivity is out of scope here.)
	assertTemplateHandlerImportsAreCleanOneHop(t, module, files)

	// The handler must actually have been scanned, or a passing result means
	// nothing. Its package holds exactly one payload and that payload must
	// import the controller.
	handler := module.Runtime.CLI[0]
	scanned := 0
	for target, content := range files {
		if filepath.ToSlash(filepath.Dir(target)) != handler.Package {
			continue
		}
		scanned++
		if !strings.Contains(string(content), `"example.com/consumer/internal/gggcli"`) {
			t.Fatalf("handler payload %s does not import the rewritten controller package", target)
		}
	}
	if scanned != 1 {
		t.Fatalf("handler package holds %d payloads, want exactly one", scanned)
	}
}

// TestExternalRegistryTemplateSnapshotIsSignedAndTamperEvident proves the
// template's signature verifies through the same code path a consumer runs,
// and that each way of tampering with the tree refuses. Every refusal is
// asserted against an in-memory filesystem, so a passing case cannot have
// written anything: there is nowhere to write to.
func TestExternalRegistryTemplateSnapshotIsSignedAndTamperEvident(t *testing.T) {
	root := externalTemplateRoot(t)
	committed, err := os.ReadFile(filepath.Join(root, RegistrySnapshotPath))
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := BuildRegistrySnapshot(os.DirFS(root))
	if err != nil {
		t.Fatalf("BuildRegistrySnapshot: %v", err)
	}
	if !bytes.Equal(rebuilt, committed) {
		t.Fatal("templates/external-registry/registry.snapshot.json is not the deterministic codec output; run `ggg registry build --dir templates/external-registry`")
	}

	// The real path: `ggg registry verify --dir … --public-key …`.
	digest, err := VerifyRegistrySnapshot(root, ExternalTemplatePublicKey)
	if err != nil {
		t.Fatalf("VerifyRegistrySnapshot: %v", err)
	}
	sum := sha256.Sum256(committed)
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("verified digest = %q", digest)
	}
	fingerprint, err := RegistryKeyFingerprint(ExternalTemplatePublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fingerprint, "sha256:") || len(fingerprint) != len("sha256:")+64 {
		t.Fatalf("template key fingerprint = %q", fingerprint)
	}

	t.Run("tampered payload", func(t *testing.T) {
		fsys := externalTemplateMapFS(t, nil)
		fsys["registry/modules/system/audit-export-ledger/ledger.go.txt"].Data = []byte("package ledger // tampered\n")
		if _, err := verifySnapshotFiles(fsys, ExternalTemplatePublicKey, false); err == nil ||
			!strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("tampered payload error = %v", err)
		}
	})
	t.Run("tampered signature", func(t *testing.T) {
		fsys := externalTemplateMapFS(t, nil)
		fsys[RegistrySignaturePath].Data = []byte(base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)) + "\n")
		if _, err := verifySnapshotFiles(fsys, ExternalTemplatePublicKey, false); err == nil ||
			!strings.Contains(err.Error(), "signature") {
			t.Fatalf("tampered signature error = %v", err)
		}
	})
	t.Run("tampered namespace", func(t *testing.T) {
		// Renaming the namespace changes registry.json, so the snapshot no
		// longer describes the tree AND the declared namespace no longer
		// matches what a consumer pinned. Both refuse.
		renamed := bytes.Replace(mustReadTemplateFile(t, "registry.json"),
			[]byte(`"`+ExternalTemplateNamespace+`"`), []byte(`"impostor"`), 1)
		fsys := externalTemplateMapFS(t, renamed)
		if _, err := verifySnapshotFiles(fsys, ExternalTemplatePublicKey, false); err == nil ||
			!strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("renamed namespace snapshot error = %v", err)
		}
		if _, err := validateGitHubSnapshot(
			Snapshot{FS: externalTemplateMapFS(t, nil)},
			ProjectRegistry{Namespace: "impostor", PublicKey: ExternalTemplatePublicKey},
		); err == nil || !strings.Contains(err.Error(), "namespace") {
			t.Fatalf("wrong requested namespace error = %v", err)
		}
		if _, err := (DirectorySource{Root: filepath.Join("..", "..")}).Resolve(t.Context(),
			ProjectRegistry{Namespace: "impostor", Source: "directory", Path: ExternalTemplateDir},
		); err == nil || !strings.Contains(err.Error(), "namespace") {
			t.Fatalf("directory resolve namespace error = %v", err)
		}
	})
	t.Run("unlisted payload", func(t *testing.T) {
		fsys := externalTemplateMapFS(t, nil)
		fsys["registry/modules/system/audit-export-ledger/extra.go.txt"] = &fstest.MapFile{Data: []byte("package ledger\n")}
		if _, err := verifySnapshotFiles(fsys, ExternalTemplatePublicKey, false); err == nil ||
			!strings.Contains(err.Error(), "unlisted") {
			t.Fatalf("unlisted payload error = %v", err)
		}
	})
}

// TestExternalRegistryTemplateSignatureIsReproducible ties the published
// demonstration key to its documented seed, so anyone can re-derive it, and
// keeps the key in the template's own documentation from drifting away from
// the one the harness verifies against.
func TestExternalRegistryTemplateSignatureIsReproducible(t *testing.T) {
	seed := sha256.Sum256([]byte(externalTemplateSeedPhrase))
	private, err := RegistryPrivateKeyFromSeed(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	public, ok := private.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("derived key has no ed25519 public part")
	}
	if got := base64.StdEncoding.EncodeToString(public); got != ExternalTemplatePublicKey {
		t.Fatalf("derived public key = %q, want %q", got, ExternalTemplatePublicKey)
	}

	root := externalTemplateRoot(t)
	snapshot, err := os.ReadFile(filepath.Join(root, RegistrySnapshotPath))
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(filepath.Join(root, RegistrySignaturePath))
	if err != nil {
		t.Fatal(err)
	}
	want := base64.StdEncoding.EncodeToString(ed25519.Sign(private, snapshot)) + "\n"
	if string(committed) != want {
		t.Fatal("committed signature is not the seed-derived signature over the committed snapshot")
	}

	readme := string(mustReadTemplateFile(t, "README.md"))
	if !strings.Contains(readme, ExternalTemplatePublicKey) {
		t.Fatal("the template README does not publish the public key consumers must pin")
	}
	if !strings.Contains(readme, externalTemplateSeedPhrase) {
		t.Fatal("the template README does not document the demonstration key's seed phrase")
	}
	for _, step := range []string{
		"ggg registry init", "ggg registry keygen", "ggg registry build",
		"ggg registry sign", "ggg registry verify", "ggg registry validate",
		"GGG_REGISTRY_SIGNING_KEY", "registry-key-rotation.json",
	} {
		if !strings.Contains(readme, step) {
			t.Fatalf("the template README does not document %s", step)
		}
	}
	assertTemplateWorkflowIsRunnable(t, externalTemplateModule(t))
}

// assertTemplateWorkflowIsRunnable checks the publisher gate against the
// refusals it would actually hit. A workflow is the one deliverable nobody
// here executes — a publisher does, in their repository — so substring
// presence is not enough: the first version of this file called `ggg new`
// with no registry answer and added a registry by absolute path, and both are
// hard refusals in the commands they invoke.
func assertTemplateWorkflowIsRunnable(t *testing.T, module Manifest) {
	t.Helper()
	workflow := string(mustReadTemplateFile(t, ".github/workflows/registry.yml"))
	for _, step := range []string{
		"ggg registry build --dir", "ggg registry sign --dir", "ggg registry verify --dir",
		"GGG_REGISTRY_SIGNING_KEY", "git diff --exit-code",
	} {
		if !strings.Contains(workflow, step) {
			t.Fatalf("the template CI workflow does not run %s", step)
		}
	}

	// `ggg new` refuses a noninteractive run with no registry answer
	// (internal/gggcli/task8_handlers.go), and a GitHub source on a
	// development build refuses without an explicit ref.
	newIndex := strings.Index(workflow, "ggg new ")
	if newIndex < 0 {
		t.Fatal("the template CI workflow never creates a consumer")
	}
	newInvocation := workflow[newIndex:]
	if end := strings.Index(newInvocation, "--json"); end >= 0 {
		newInvocation = newInvocation[:end]
	}
	for _, flag := range []string{"--registry", "--ref", "--module", "--profile", "--non-interactive"} {
		if !strings.Contains(newInvocation, flag) {
			t.Fatalf("ggg new in the template CI workflow omits %s: %q", flag, newInvocation)
		}
	}

	// A `directory` registry path must be project-contained: no absolute
	// path and no shell expansion, both of which validateProjectRegistry
	// refuses outright.
	for _, line := range strings.Split(workflow, "\n") {
		index := strings.Index(line, "directory:")
		if index < 0 {
			continue
		}
		argument := line[index+len("directory:"):]
		argument = strings.TrimLeft(argument, "\"'")
		if argument == "" {
			t.Fatalf("empty directory registry argument in %q", strings.TrimSpace(line))
		}
		if argument[0] == '/' || argument[0] == '$' {
			t.Fatalf("directory registry argument is not project-contained in %q", strings.TrimSpace(line))
		}
	}

	// `ggg registry validate` in a scratch consumer finds no closures and
	// exits 0 having proven nothing, so the workflow must not claim it as the
	// lifecycle proof. The install/compile/test steps are the honest ones.
	if strings.Contains(workflow, "ggg registry validate") &&
		!strings.Contains(workflow, "does NOT do is run `ggg registry validate`") {
		t.Fatal("the template CI workflow runs `ggg registry validate` where it has no closures to exercise")
	}
	for _, real := range []string{"go build ./...", "go test -count=1", "provider set", "registry add"} {
		if !strings.Contains(workflow, real) {
			t.Fatalf("the template CI workflow does not actually install and exercise the module: missing %s", real)
		}
	}

	// An adapter is never an explicit module, so `ggg remove` cannot be its
	// removal check: while it is selected removal is a designed refusal, and
	// once it is deselected the retirement already folded its files out and
	// the lock no longer lists it. Deselection is the removal.
	names := []string{module.ID, "${MODULE_ID}", "$MODULE_ID"}
	for _, line := range strings.Split(workflow, "\n") {
		command := strings.TrimSpace(line)
		if !strings.Contains(command, "ggg remove") {
			continue
		}
		for _, name := range names {
			if strings.Contains(command, name) {
				t.Fatalf("the template CI workflow removes the adapter with `ggg remove` in %q; "+
					"an adapter leaves by deselection", command)
			}
		}
	}

	// Apply deletes the files it installed; the directories they lived in
	// survive. An assertion on a directory therefore fails after a correct
	// removal, which is worse than no assertion.
	installedDirs := map[string]struct{}{}
	for _, file := range module.Files {
		for dir := path.Dir(filepath.ToSlash(file.Target)); dir != "." && dir != "/"; dir = path.Dir(dir) {
			installedDirs[dir] = struct{}{}
		}
	}
	for _, line := range strings.Split(workflow, "\n") {
		command := strings.TrimSpace(line)
		rest, isDirTest := strings.CutPrefix(command, "test ! -d ")
		if !isDirTest {
			continue
		}
		argument := strings.Trim(strings.Fields(rest)[0], `"'`)
		if _, installed := installedDirs[strings.TrimSuffix(argument, "/")]; installed {
			t.Fatalf("the template CI workflow asserts %q, but apply removes files and leaves the "+
				"directory behind; assert on the installed files", command)
		}
	}
}

// TestExternalRegistryTemplateIsRebuildable proves the committed tree is
// exactly what `ggg registry build --dir` plus `ggg registry sign` produce,
// so the publisher gate in the template's own CI — which refuses a dirty tree
// after building — is a check a copy of this template can actually pass.
func TestExternalRegistryTemplateIsRebuildable(t *testing.T) {
	source := externalTemplateRoot(t)
	work := filepath.Join(t.TempDir(), "template")
	if err := copyProjectTree(source, work); err != nil {
		t.Fatal(err)
	}
	if refreshed, err := RefreshManifestDigests(work); err != nil {
		t.Fatalf("RefreshManifestDigests: %v", err)
	} else if len(refreshed) != 0 {
		t.Fatalf("committed template manifests carry stale digests: %v", refreshed)
	}
	if _, _, err := BuildRegistryIndexes(work); err != nil {
		t.Fatalf("BuildRegistryIndexes: %v", err)
	}
	seed := sha256.Sum256([]byte(externalTemplateSeedPhrase))
	private, err := RegistryPrivateKeyFromSeed(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSignedRegistrySnapshot(work, private); err != nil {
		t.Fatalf("WriteSignedRegistrySnapshot: %v", err)
	}
	for _, name := range []string{
		"registry.json", "registry/systems.json", "registry/elements.json",
		"registry/components.json", "registry/pages.json", "registry/workflows.json",
		"registry/profiles.json", "registry/modules/system/audit-export-ledger/module.json",
		RegistrySnapshotPath, RegistrySignaturePath,
	} {
		want, readErr := os.ReadFile(filepath.Join(source, filepath.FromSlash(name)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		got, readErr := os.ReadFile(filepath.Join(work, filepath.FromSlash(name)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s is not what a rebuild produces", name)
		}
	}
}

// TestExternalRegistryTemplateTreeHasExactlyOneOwner keeps the template on
// the same footing as every other tracked file: one module owns it, so it is
// installed, verified, and removed with a feature rather than being
// scaffolding nobody can reason about.
func TestExternalRegistryTemplateTreeHasExactlyOneOwner(t *testing.T) {
	repo := filepath.Join("..", "..")
	catalog, err := LoadCatalog(os.DirFS(repo))
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string][]string{}
	for _, module := range catalog.Modules {
		for _, file := range module.Files {
			if strings.HasPrefix(file.Target, ExternalTemplateDir+"/") {
				owners[file.Target] = append(owners[file.Target], module.ID)
			}
		}
	}
	if len(owners) == 0 {
		t.Fatalf("no module owns %s", ExternalTemplateDir)
	}
	err = filepath.WalkDir(filepath.Join(repo, filepath.FromSlash(ExternalTemplateDir)),
		func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			relative, relErr := filepath.Rel(repo, path)
			if relErr != nil {
				return relErr
			}
			target := filepath.ToSlash(relative)
			switch found := owners[target]; len(found) {
			case 1:
				return nil
			case 0:
				t.Errorf("%s belongs to no module", target)
			default:
				t.Errorf("%s is claimed by %v", target, found)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

// assertTemplateHandlerImportsAreCleanOneHop resolves the handler package's
// imports against the template's own claimed packages and checks each reached
// package's direct imports against the same banned set.
func assertTemplateHandlerImportsAreCleanOneHop(t *testing.T, module Manifest, files map[string][]byte) {
	t.Helper()
	const derivative = "example.com/consumer"
	byPackage := map[string][][]byte{}
	for target, content := range files {
		pkg := path.Dir(filepath.ToSlash(target))
		byPackage[pkg] = append(byPackage[pkg], content)
	}
	handler := module.Runtime.CLI[0].Package
	reached := 0
	for _, content := range byPackage[handler] {
		parsed, err := parser.ParseFile(token.NewFileSet(), "handler.go", content, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse handler: %v", err)
		}
		for _, imported := range parsed.Imports {
			raw := strings.Trim(imported.Path.Value, `"`)
			own, isOwn := strings.CutPrefix(raw, derivative+"/")
			if !isOwn {
				continue
			}
			sources, published := byPackage[own]
			if !published {
				// A core package under the same rewritten prefix — the
				// controller, the envelope. Those are the framework's own,
				// covered by the direct scan; only this module's graph is in
				// scope here.
				continue
			}
			reached++
			for _, source := range sources {
				nested, parseErr := parser.ParseFile(token.NewFileSet(), "reached.go", source, parser.ImportsOnly)
				if parseErr != nil {
					t.Fatalf("parse %s: %v", own, parseErr)
				}
				for _, nestedImport := range nested.Imports {
					nestedPath := strings.Trim(nestedImport.Path.Value, `"`)
					if banned, reason := matchBannedImport(nestedPath, map[string]string{}); reason != "" {
						t.Fatalf("handler package %s reaches %s through %s, which imports the banned %s: %s",
							handler, nestedPath, own, banned, reason)
					}
				}
			}
		}
	}
	if reached == 0 {
		t.Fatal("the handler imports none of this module's own packages; the one-hop check proved nothing")
	}
}

func mustReadTemplateFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(externalTemplateRoot(t), filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
