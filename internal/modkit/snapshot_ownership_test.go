package modkit

import (
	"crypto/ed25519"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeOwnershipRegistry lays down a minimal but complete registry root: the
// root document, six empty indexes, and one system module declaring one
// payload and one migration.
func writeOwnershipRegistry(t *testing.T, root, namespace, canonical string) {
	t.Helper()
	written, err := InitRegistryTree(root, namespace, canonical)
	require.NoError(t, err)
	require.NotEmpty(t, written)

	writeTestFile(t, root, "registry/modules/system/probe/payload/probe.go.txt", []byte("package probe\n"))
	writeTestFile(t, root, "registry/modules/system/probe/payload/probe.sql", []byte("-- probe\n"))
	manifest := `{"schema":2,"module":{
		"id":"` + namespace + `/system/probe","kind":"system","name":"probe","revision":1,"contract":1,
		"title":"Probe","description":"Ownership fixture module.",
		"requires":[],"dependencies":{"go":[],"tools":[],"containers":[]},
		"files":[{"source":"registry/modules/system/probe/payload/probe.go.txt",
			"target":"internal/probe/probe.go","class":"go",
			"sha256":"` + digestBytes([]byte("package probe\n")) + `","rewrite_module":true,"contract":false}],
		"claims":{"packages":["internal/probe"]},
		"runtime":{},
		"migrations":[{"id":"probe_rows","kind":"immutable",
			"source":"registry/modules/system/probe/payload/probe.sql",
			"sha256":"` + digestBytes([]byte("-- probe\n")) + `"}],
		"environment":[],"locales":{},"docs":[],"tests":{},"data":[],"removal_policy":"free"}}`
	writeTestFile(t, root, "registry/modules/system/probe/module.json", []byte(manifest))
	_, _, err = BuildRegistryIndexes(root)
	require.NoError(t, err)
}

// A payload and a migration source are both declarations, and the document
// that carries the manifest is declared by its index. Nothing else in the
// tree is, and a clean registry has nothing else in it.
func TestRegistryTreeOwnershipAcceptsEveryDeclaredKind(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	require.NoError(t, ValidateRegistryTreeOwnership(os.DirFS(root)))
}

// The failure this closes: bytes inside the payload root that no manifest
// declares. It is not enough that they are harmless — they enter the snapshot
// and the signature makes them look deliberate.
func TestRegistryTreeOwnershipRefusesAnUndeclaredPayload(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	writeTestFile(t, root, "registry/modules/system/probe/payload/stray.go.txt", []byte("package stray\n"))

	err := ValidateRegistryTreeOwnership(os.DirFS(root))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry/modules/system/probe/payload/stray.go.txt")
	assert.Contains(t, err.Error(), "no module declares")
}

// The refusal must precede the signature, not follow it. A gate that ran
// after signing would leave a signed artifact on disk covering bytes nobody
// declared, which is the exact state this repository shipped in.
func TestSigningRefusesBeforeProducingASignature(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	private := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	_, err := SignRegistrySnapshot(root, private)
	require.NoError(t, err, "a clean registry must sign")
	for _, name := range []string{RegistrySnapshotPath, RegistrySignaturePath} {
		require.FileExists(t, filepath.Join(root, name))
		require.NoError(t, os.Remove(filepath.Join(root, name)))
	}

	writeTestFile(t, root, "registry/modules/system/probe/payload/stray.go.txt", []byte("package stray\n"))
	_, err = SignRegistrySnapshot(root, private)
	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(root, RegistrySnapshotPath),
		"a refused sign wrote the snapshot anyway")
	assert.NoFileExists(t, filepath.Join(root, RegistrySignaturePath),
		"a refused sign produced a signature anyway")
}

// A registry nested inside another registry's payload root answers to its own
// catalog. registry/testdata and registry/external-testdata are exactly this
// shape, and a flat "must appear in some files list" rule refuses all 79 of
// their files.
func TestRegistryTreeOwnershipResolvesNestedRootsAgainstTheirOwnCatalog(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	nested := filepath.Join(root, "registry", "fixtures")
	writeOwnershipRegistry(t, nested, "fixture", "example.com/acme/fixtures")

	require.NoError(t, ValidateRegistryTreeOwnership(os.DirFS(root)),
		"the nested registry's own manifests declare its files")

	writeTestFile(t, nested, "registry/modules/system/probe/payload/stray.go.txt", []byte("package stray\n"))
	err := ValidateRegistryTreeOwnership(os.DirFS(root))
	require.Error(t, err, "nesting delegates ownership, it does not escape it")
	// The path is reported as the enclosing snapshot lists it, and the root is
	// named because two registries can share a namespace.
	assert.Contains(t, err.Error(), "registry/fixtures/registry/modules/system/probe/payload/stray.go.txt")
	assert.Contains(t, err.Error(), `registry root "registry/fixtures"`)
}

// The infrastructure files are owned by the format, and by exact name. A
// suffix or prefix rule would let `registry/schema/anything.json` authorize
// itself, which is the same hole one directory over.
func TestRegistryTreeOwnershipAcceptsFormatFilesAndRefusesLookalikes(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	for _, name := range []string{
		"registry/schema/registry.schema.json", "registry/schema/module.schema.json",
		"registry/schema/project.schema.json", "registry/schema/lock.schema.json",
		"registry/schema/snapshot.schema.json",
	} {
		writeTestFile(t, root, name, []byte("{}\n"))
	}
	require.NoError(t, ValidateRegistryTreeOwnership(os.DirFS(root)))

	writeTestFile(t, root, "registry/schema/invented.schema.json", []byte("{}\n"))
	err := ValidateRegistryTreeOwnership(os.DirFS(root))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry/schema/invented.schema.json")
}

// A `.templ` payload whose source lives in the registry tree gets a
// `_templ.go` sibling from `make generate`, and a module may not declare one:
// targeting a generated output is refused outright. So tool output is owned
// by the tool here, exactly as it is everywhere else in this engine.
func TestRegistryTreeOwnershipAcceptsToolOutputBesideItsSource(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	writeTestFile(t, root, "registry/modules/system/probe/payload/probe_templ.go",
		[]byte("// Code generated by templ - DO NOT EDIT.\npackage probe\n"))
	require.NoError(t, ValidateRegistryTreeOwnership(os.DirFS(root)))

	// The class is the predicate this engine already uses, not "any .go file
	// nobody claimed".
	writeTestFile(t, root, "registry/modules/system/probe/payload/probe_helper.go",
		[]byte("package probe\n"))
	err := ValidateRegistryTreeOwnership(os.DirFS(root))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry/modules/system/probe/payload/probe_helper.go")
}

// The reported incident, exactly: `registry build --dir <root>/registry`
// wrote six kind indexes one level too deep. Those paths are not the
// registry's own index paths and no manifest declares them, and the gate has
// to see that — a `registry/*.json` pattern rule would wave them through.
func TestRegistryTreeOwnershipRefusesIndexesWrittenAtTheWrongDepth(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	misplaced := make([]string, 0, len(catalogIncludes))
	for _, include := range catalogIncludes {
		name := "registry/" + include.path
		misplaced = append(misplaced, name)
		writeTestFile(t, root, name, []byte(`{"schema":2,"kind":"system","items":[]}`))
	}
	err := ValidateRegistryTreeOwnership(os.DirFS(root))
	require.Error(t, err)
	for _, name := range misplaced {
		assert.Contains(t, err.Error(), name)
	}
}

// Ownership is a statement about the whole published catalog, not about one
// project's selection. A consumer that installs one module still receives a
// snapshot listing every module's payloads.
func TestRegistryTreeOwnershipCoversUnselectedModules(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	declared, err := declaredRegistryPaths(os.DirFS(root))
	require.NoError(t, err)
	assert.Contains(t, declared, "registry/modules/system/probe/module.json")
	assert.Contains(t, declared, "registry/modules/system/probe/payload/probe.go.txt")
	assert.Contains(t, declared, "registry/modules/system/probe/payload/probe.sql")
}

// The gate's scope has to equal the signature's scope, or it covers less than
// it claims. Both read the same walk.
func TestOwnershipScopeMatchesTheSignedSnapshotScope(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	// Outside the registry tree: project source a self-hosting registry
	// installs into, which the snapshot never lists.
	writeTestFile(t, root, "internal/probe/probe.go", []byte("package probe\n"))
	require.NoError(t, ValidateRegistryTreeOwnership(os.DirFS(root)),
		"a file outside registry/ is not part of the signed catalog")

	walked := map[string]struct{}{}
	require.NoError(t, walkRegistrySnapshotScope(os.DirFS(root), func(name string, entry fs.DirEntry) error {
		if !entry.IsDir() {
			walked[name] = struct{}{}
		}
		return nil
	}))
	assert.NotContains(t, walked, "internal/probe/probe.go")
	assert.Contains(t, walked, "registry.json")
	assert.Contains(t, walked, "registry/systems.json")
}

// A build that refuses must leave nothing behind. The reported failure wrote
// six index files and then refused on a missing registry.json, and the next
// build folded all six into the signed snapshot.
func TestBeginRegistryBuildRefusesANonRootBeforeWriting(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	// The exact operator mistake: --dir pointed one level too deep, at the
	// directory that holds the indexes rather than the one that holds
	// registry.json.
	inner := filepath.Join(root, "registry")
	before := treeListing(t, inner)

	_, err := BeginRegistryBuild(inner)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a registry root")
	assert.Equal(t, before, treeListing(t, inner), "a refused build changed the tree")
}

// Every other way a build can fail happens after a write, so the journal has
// to cover them. This drives the real sequence: refresh the digests, then
// refuse on ownership, then prove the refreshed manifest came back.
func TestRegistryBuildRollsBackARefreshedManifest(t *testing.T) {
	root := t.TempDir()
	writeOwnershipRegistry(t, root, "acme", "example.com/acme/catalog")
	manifestPath := filepath.Join(root, "registry", "modules", "system", "probe", "module.json")
	stale, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	// A wrong digest on disk is what the refresh stage rewrites.
	require.NoError(t, os.WriteFile(manifestPath,
		[]byte(strings.Replace(string(stale), digestBytes([]byte("package probe\n")), zeroDigest, 1)), 0o644))
	poisoned, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	writeTestFile(t, root, "registry/modules/system/probe/payload/stray.go.txt", []byte("package stray\n"))

	build, err := BeginRegistryBuild(root)
	require.NoError(t, err)
	refreshed, err := RefreshManifestDigests(root)
	require.NoError(t, err)
	require.Contains(t, refreshed, "registry/modules/system/probe/module.json",
		"the refresh stage must have written, or this proves nothing about rollback")
	require.Error(t, ValidateRegistryTreeOwnership(os.DirFS(root)))
	require.NoError(t, build.Rollback())

	restored, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	assert.Equal(t, string(poisoned), string(restored), "rollback did not restore the manifest bytes")
	assert.NoFileExists(t, filepath.Join(root, RegistrySnapshotPath))
}

const zeroDigest = "0000000000000000000000000000000000000000000000000000000000000000"

// treeListing is every relative path under dir, so an unchanged tree can be
// asserted rather than described.
func treeListing(t *testing.T, dir string) []string {
	t.Helper()
	listing := make([]string, 0)
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		listing = append(listing, rel)
		return nil
	}))
	return listing
}
