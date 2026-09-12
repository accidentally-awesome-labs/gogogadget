package modkit

// The third point.
//
// The two revision gates in registry_build.go both compare a manifest against
// an artifact the same workflow rewrites: ValidateManifestRevisions compares
// the manifest's declared payload digests against the lock (which `ggg sync`
// refreshes) and ValidateManifestRevisionsAgainstSnapshot compares the
// manifest's declared digests against the bytes on disk (which `registry
// build`'s own refresh rewrites). Both are two-point comparisons between two
// MUTABLE points, so they see a payload change only during the window in
// which exactly one of the two has moved. One edit that changes a payload AND
// refreshes the digest recorded for it — which is what every digest-refreshing
// command does mid-edit — closes that window: manifest and disk agree again,
// the lock agrees after the next sync, and the module publishes new bytes
// under its old revision with nothing to say so.
//
// The fix is a third point that no working-tree command can rewrite: the
// registry snapshot of the last RELEASED tag, signed at publication and
// indexing every registry file by path and SHA-256. A module whose manifest or
// payload bytes differ from that release must carry a revision greater than
// the one it published there.
//
// Per RELEASE, not per commit. Between two releases a module may be edited any
// number of times; one bump above the published revision covers all of them,
// because the reference point only moves when a release moves it. That is the
// same livability argument ValidateManifestRevisions makes for the lock, held
// against a point a contributor cannot advance by accident.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReleaseBaseline is one registry's published state at one release: the
// manifests its signed snapshot indexed, each authenticated against that
// snapshot, with the revision and payload digests each of them declared.
type ReleaseBaseline struct {
	// Ref names the release the baseline was read from (a tag, for this
	// repository). It appears in every refusal, because "changed since
	// WHAT" is the first question a refused release asks.
	Ref string
	// modules is keyed by module id, not by path: a module that moved
	// directories is the same module, and the move is itself a change.
	modules map[string]baselineModule
}

// Modules reports how many published manifests the baseline carries. A
// baseline that resolved to nothing would make every comparison vacuous, so
// callers state a floor rather than trusting the load.
func (b ReleaseBaseline) Modules() int { return len(b.modules) }

type baselineModule struct {
	path     string
	manifest []byte
	revision int
	payloads map[string]string
}

// publishedManifest is how a RELEASED manifest is read: as a record of what
// was published, never as an authoring input revalidated against today's
// model. Strict decoding against the current Manifest type would make every
// schema addition retroactively unreadable and turn this gate off exactly when
// the tree is moving fastest. The record tier is the same principle the era
// walk established for old lock rows.
type publishedManifest struct {
	Module struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
		Files    []struct {
			Source string `json:"source"`
			SHA256 string `json:"sha256"`
			Class  string `json:"class"`
		} `json:"files"`
		Migrations []struct {
			Source string `json:"source"`
			SHA256 string `json:"sha256"`
		} `json:"migrations"`
	} `json:"module"`
}

// publishedSnapshot is the released snapshot read the same way: only the file
// index is load-bearing here, and the registry root it also carries belongs to
// that release's schema, not to this one's.
type publishedSnapshot struct {
	Files []SnapshotFile `json:"files"`
}

// LoadReleaseBaseline reads one release's signed snapshot and, through read,
// the manifests it indexes. Every manifest is checked against the digest the
// snapshot pins for it: the snapshot is the signed artifact, so a manifest
// whose bytes disagree with it is not a baseline, it is a corrupted tree, and
// the load refuses rather than comparing against bytes nobody signed.
//
// read receives a slash-separated path in the released tree. Whatever produces
// it — `git show <tag>:<path>`, an unpacked archive, a cached snapshot
// directory — stays outside this package: modkit's engine reads the
// filesystem and registry sources, never a version-control history.
func LoadReleaseBaseline(ref string, snapshot []byte, read func(path string) ([]byte, error)) (ReleaseBaseline, error) {
	if strings.TrimSpace(ref) == "" {
		return ReleaseBaseline{}, errors.New("release baseline: no ref names the release this baseline came from")
	}
	var published publishedSnapshot
	if err := json.Unmarshal(snapshot, &published); err != nil {
		return ReleaseBaseline{}, fmt.Errorf("release baseline %s: decode %s: %w", ref, RegistrySnapshotPath, err)
	}
	if len(published.Files) == 0 {
		return ReleaseBaseline{}, fmt.Errorf("release baseline %s: its %s indexes no files, so every comparison against it would pass vacuously", ref, RegistrySnapshotPath)
	}

	baseline := ReleaseBaseline{Ref: ref, modules: make(map[string]baselineModule)}
	for _, file := range published.Files {
		if !isModuleManifestPath(file.Path) {
			continue
		}
		data, err := read(file.Path)
		if err != nil {
			return ReleaseBaseline{}, fmt.Errorf("release baseline %s: read %s: %w", ref, file.Path, err)
		}
		if digest := digestBytes(data); digest != file.SHA256 {
			return ReleaseBaseline{}, fmt.Errorf(
				"release baseline %s: %s hashes to %s and its signed snapshot pins %s; the released tree disagrees with the snapshot published from it",
				ref, file.Path, digest, file.SHA256)
		}
		var manifest publishedManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return ReleaseBaseline{}, fmt.Errorf("release baseline %s: decode %s: %w", ref, file.Path, err)
		}
		if manifest.Module.ID == "" {
			return ReleaseBaseline{}, fmt.Errorf("release baseline %s: %s declares no module id", ref, file.Path)
		}
		if previous, duplicate := baseline.modules[manifest.Module.ID]; duplicate {
			return ReleaseBaseline{}, fmt.Errorf("release baseline %s: module %s is published at both %s and %s", ref, manifest.Module.ID, previous.path, file.Path)
		}
		payloads := make(map[string]string, len(manifest.Module.Files)+len(manifest.Module.Migrations))
		for _, payload := range manifest.Module.Files {
			// Generated payloads carry no authored digest; the manifest
			// that declares them does, and it is compared whole.
			if payload.Class == string(FileClassGenerated) || payload.SHA256 == "" {
				continue
			}
			payloads[payload.Source] = payload.SHA256
		}
		for _, migration := range manifest.Module.Migrations {
			if migration.SHA256 == "" {
				continue
			}
			payloads[migration.Source] = migration.SHA256
		}
		baseline.modules[manifest.Module.ID] = baselineModule{
			path:     file.Path,
			manifest: data,
			revision: manifest.Module.Revision,
			payloads: payloads,
		}
	}
	if len(baseline.modules) == 0 {
		return ReleaseBaseline{}, fmt.Errorf("release baseline %s: its %s indexes no module manifest, so it cannot be a published registry", ref, RegistrySnapshotPath)
	}
	return baseline, nil
}

// isModuleManifestPath reports whether a snapshot path is one module's
// manifest. Kinds are not enumerated here: a release that published a kind
// this build no longer knows still published manifests, and refusing to read
// them would make the baseline narrower than the release it came from.
func isModuleManifestPath(path string) bool {
	parts := strings.Split(path, "/")
	return len(parts) == 5 && parts[0] == "registry" && parts[1] == "modules" && parts[4] == "module.json"
}

// ValidateRevisionsAgainstReleaseBaseline refuses a module whose bytes differ
// from the last released snapshot while its revision is not above the one it
// published there.
//
// Four cases, all decided:
//
//   - unchanged since the release: passes, and needs no bump. This is the
//     false-positive risk the gate is scoped around — the overwhelming
//     majority of modules in any release cycle are untouched, and demanding a
//     bump from them would make the gate noise and then make it ignored.
//   - changed and bumped above the published revision: passes, however many
//     times it changed. One bump per release, not per commit.
//   - changed at or below the published revision: REFUSED, named with both
//     revisions and the bytes that moved.
//   - published then deleted: passes. A module that no longer exists publishes
//     nothing, and the refusal's own remedy — bump the revision — names an
//     edit that cannot be made to a manifest that is gone. Removal is a
//     catalog change, owned by the index build (which drops it from
//     registry/index.json) and by the lock's tombstone rule, not by the
//     version-of-record convention. Demanding a phantom bump here would block
//     every legitimate module removal and teach the release owner to route
//     around the gate.
func ValidateRevisionsAgainstReleaseBaseline(root string, baseline ReleaseBaseline) error {
	if len(baseline.modules) == 0 {
		return fmt.Errorf("release baseline %s carries no published module; refusing to report a pass over an empty comparison", baseline.Ref)
	}
	scanned, err := scanRegistryManifests(root)
	if err != nil {
		return err
	}
	current := make(map[string]scannedManifest, len(scanned))
	for _, manifest := range scanned {
		current[manifest.document.Module.ID] = manifest
	}

	stale := make([]string, 0)
	for id, published := range baseline.modules {
		now, present := current[id]
		if !present {
			continue
		}
		moved := movedSinceRelease(root, published, now)
		if len(moved) == 0 || now.document.Module.Revision > published.revision {
			continue
		}
		stale = append(stale, fmt.Sprintf("%s (published revision %d, tree revision %d; %s)",
			id, published.revision, now.document.Module.Revision, strings.Join(moved, ", ")))
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	return fmt.Errorf(
		"bytes changed since the last released registry snapshot (%s) with no revision above the one published there: %s. "+
			"revision is the module's version of record — it feeds indexSHA and `ggg info` — so an implementation change that leaves it alone publishes a lie. "+
			"The manifest-against-lock and manifest-against-disk gates cannot see this: one edit that moves a payload AND refreshes the digest recorded for it leaves both sides agreeing at the old revision. "+
			"Bump revision above the published one — once per release, not once per commit (contract moves only when a consumer must change code)",
		baseline.Ref, strings.Join(stale, ", "))
}

// movedSinceRelease lists what a module changed since it was published, as
// evidence for the refusal. Order is stable: the manifest first, then payloads
// by path.
//
// A payload this tree cannot read is not counted. The siblings take the same
// position — an unreadable payload is the digest refresh's refusal to raise,
// not evidence of a change — and here it is also redundant: a payload actually
// removed from the module changes the manifest that declared it, which the
// first comparison already catches.
func movedSinceRelease(root string, published baselineModule, now scannedManifest) []string {
	moved := make([]string, 0, 4)
	switch {
	case published.path != now.path:
		moved = append(moved, fmt.Sprintf("manifest moved from %s to %s", published.path, now.path))
	case digestBytes(now.raw) != digestBytes(published.manifest):
		moved = append(moved, "manifest bytes")
	}
	sources := make([]string, 0, len(published.payloads))
	for source := range published.payloads {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		digest, err := payloadDigest(root, source)
		if err != nil {
			continue
		}
		if digest != published.payloads[source] {
			moved = append(moved, source)
		}
	}
	if len(moved) > 4 {
		return append(moved[:4:4], fmt.Sprintf("and %d more", len(moved)-4))
	}
	return moved
}

// scannedManifest is one module manifest as it sits in a registry tree: its
// repo-relative path, its exact bytes, and the document they decode to. The
// bytes are kept because the gates that compare against a published snapshot
// compare bytes, not re-encodings.
type scannedManifest struct {
	path     string
	raw      []byte
	document ModuleDocument
}

// scanRegistryManifests walks every module manifest in a registry tree, in
// catalog order. A manifest that cannot be read or decoded is skipped: that is
// the digest refresh's and the schema validator's refusal to raise, and every
// caller here is a gate that must not invent a second one.
func scanRegistryManifests(root string) ([]scannedManifest, error) {
	manifests := make([]scannedManifest, 0, 64)
	for _, include := range catalogIncludes {
		if include.kind == CatalogProfile {
			continue
		}
		dir := filepath.Join(root, "registry", "modules", string(include.kind))
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", dir, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			rel := "registry/modules/" + string(include.kind) + "/" + entry.Name() + "/module.json"
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				continue
			}
			var document ModuleDocument
			if err := decodeStrict(data, &document); err != nil {
				continue
			}
			manifests = append(manifests, scannedManifest{path: rel, raw: data, document: document})
		}
	}
	return manifests, nil
}
