package modkit

// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the core registry installs and runs it, and no
// derivative ever receives it. The fixtures are this repository's own release
// tags, so the guard needs the real git history and refuses to run vacuously
// without it.
//
// # The hole this closes
//
// `registry build` already refuses "payload digests changed without a revision
// bump" twice over, and both halves compare the manifest against an artifact
// the same workflow rewrites: the lock (which `ggg sync` refreshes) and the
// payload bytes on disk (which the build's own digest refresh rewrites). Each
// is a two-point comparison between two MUTABLE points, so it can only see a
// change while exactly one of the two has moved. One edit that changes a
// payload AND refreshes the digest recorded for it — what every
// digest-refreshing command does mid-edit — closes that window, and the module
// publishes new bytes under its old revision in silence.
//
// Measured at the v0.25.0..v0.26.0 integration: four modules'
// module.json bytes differed from the last published, signed snapshot while
// their revisions stood still, and `registry build` reported OK for all four.
// TestReleaseBaselineGateRefusesTheV0260IntegrationIncident below replays that
// exact tree from history and requires the refusal.
//
// # Placement, and what it costs
//
// Here — a `make check` guard — rather than in the release order.
//
// The rule needs git history, and modkit's engine reads the filesystem and
// registry sources and nothing else: internal/gggcli/gate.go is the only
// non-test code in this repository that shells out to git, and it does so at
// the command tier. Wiring `git show` into `registry build` would also make
// the build behave differently inside and outside a worktree — every /tmp
// derivative `ggg registry validate` builds, and every third-party publisher's
// tree, has no tags — so the rule would be inert in most of its invocations
// while claiming to be a build gate.
//
// The stronger argument is when the refusal arrives. At release time the
// evidence is a whole cycle of commits and the release owner has to
// archaeologise which edit moved which payload; that forensic diff is exactly
// what the v0.26.0 integration paid. In `make check` the refusal arrives in
// the commit that moves the bytes, where the remedy is one line by the person
// who knows what changed.
//
// Costs, stated:
//
//   - one `git archive <tag> registry.snapshot.json registry/modules` (1.7 MB
//     at v0.25.0, read as a tar stream, never unpacked) plus one SHA-256 over
//     each published payload still present in the tree; measured below and
//     logged by the guard on every run.
//   - the `test` job's checkout moves to fetch-depth: 0 (51.6 MiB of packed
//     history against a 37 MB tree) so the guard is not [inapplicable] in
//     the one place that gates every push. That is the cost of the guard
//     being real in CI, and internal/modkit/ci_workflow_test.go pins it.
//   - contributors now bump a module's revision in the commit that edits it
//     rather than in a release-time sweep. The bump is per RELEASE, not per
//     commit: once a module's revision is above the published one, every
//     further edit in the same cycle passes.
//
// # The red proof
//
// Not patch-shaped, and this is stated rather than hidden. The red-proof
// runner materialises a scratch copy with no `.git` and gives it a fresh
// identity, so the scratch has no release tag; a planted violation there meets
// this guard's [inapplicable] skip, which is a masquerade rather than a proof.
// The reds were driven by hand against real history and are quoted in
// .superpowers/sdd/framework-followups/task-be-report.md: the four-module
// incident tree (refused, naming each module with both revisions), a
// legitimately bumped module (green), and a module untouched since the
// baseline (green without a bump). The incident replay below is that first red
// wired in as a permanent regression test, which is the closest a
// history-shaped guard gets to a corpus patch.

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// revisionBaselineTagMatch is the release tag shape. Release tags are the only
// thing that moves the baseline, so a lightweight or unrelated tag must not.
const revisionBaselineTagMatch = "v[0-9]*"

// revisionBaselineModuleFloor guards the guard: a baseline that resolved to a
// handful of manifests would pass everything while reporting a clean line. The
// core registry published 297 modules at v0.25.0; the floor is well under that
// and exists to catch a collapsed load, not to track the catalog.
const revisionBaselineModuleFloor = 200

// The universal: every module whose bytes differ from the last released
// snapshot carries a revision above the one it published there.
//
// Both directions are live in this one assertion over real history, because
// the tree it runs against contains all three populations at once: modules
// untouched since the baseline (the overwhelming majority — they pass without
// a bump, which is the false-positive half), modules legitimately changed and
// bumped (they pass), and any module changed without a bump (it fails here,
// by name, with both revisions).
func TestEveryModuleChangedSinceTheLastReleaseCarriesAHigherRevision(t *testing.T) {
	root := specRepoRoot(t)
	tag, ok := revisionBaselineTag(t, root)
	if !ok {
		return
	}
	started := time.Now()
	baseline := revisionBaselineFromRef(t, root, tag)
	if baseline.Modules() < revisionBaselineModuleFloor {
		t.Fatalf("the %s baseline resolved %d published modules, below the floor of %d; the load has collapsed, not the catalog",
			tag, baseline.Modules(), revisionBaselineModuleFloor)
	}
	err := ValidateRevisionsAgainstReleaseBaseline(root, baseline)
	t.Logf("release baseline %s: %d published modules compared in %s", tag, baseline.Modules(), time.Since(started).Round(time.Millisecond))
	if err != nil {
		t.Fatalf("%v", err)
	}
}

// The founding incident, replayed from history.
//
// Baseline v0.25.0, tree at the commit before the v0.26.0 release bumped
// anything. Four of the modules below are the incident: their manifests had
// been rewritten by a digest refresh in the same edit that moved their
// payloads, so manifest and disk agreed again at the old revision and both
// existing gates reported OK. The other three moved their payloads without a
// refresh, which is the window the existing gates can see.
//
// The test asserts both halves: the new gate names all seven with both
// revisions, and the two existing gates — the unfixed state, run against the
// same tree — name none of the four. A guard that cannot reproduce its own
// founding incident is not proven, and a guard that cannot show the old code
// missing it has not proven the mechanism.
func TestReleaseBaselineGateRefusesTheV0260IntegrationIncident(t *testing.T) {
	root := specRepoRoot(t)
	if !revisionBaselineHistory(t, root) {
		return
	}
	for _, ref := range []string{revisionBaselineIncidentBaseline, revisionBaselineIncidentTree, revisionBaselineCorrectedTree} {
		if _, err := revisionBaselineGit(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
			t.Skipf("[inapplicable] the incident replay needs %s and it is absent from the accessible refs — run `git fetch --tags origin` and re-run", ref)
		}
	}

	tree := t.TempDir()
	revisionBaselineExtract(t, root, revisionBaselineIncidentTree, tree)
	baseline := revisionBaselineFromRef(t, root, revisionBaselineIncidentBaseline)

	err := ValidateRevisionsAgainstReleaseBaseline(tree, baseline)
	if err == nil {
		t.Fatalf("the release-baseline gate accepted the tree that published four modules' new bytes under their old revisions (%s against baseline %s)",
			revisionBaselineIncidentTree[:8], revisionBaselineIncidentBaseline)
	}
	t.Logf("refusal:\n%v", err)

	named := revisionBaselineNamed(err.Error())
	want := make([]string, 0, len(revisionBaselineIncident))
	for _, row := range revisionBaselineIncident {
		want = append(want, fmt.Sprintf("%s %d->%d", row.id, row.published, row.tree))
	}
	slices.Sort(want)
	if !slices.Equal(named, want) {
		t.Fatalf("the refusal names\n  %v\nand the incident tree is\n  %v", named, want)
	}

	// The unfixed state, over the same tree: neither existing gate can see
	// the four whose manifests were refreshed in the same edit.
	blind := map[string]bool{}
	for _, row := range revisionBaselineIncident {
		if row.refreshed {
			blind[row.id] = true
		}
	}
	for name, gate := range map[string]func(string) error{
		"ValidateManifestRevisionsAgainstSnapshot": ValidateManifestRevisionsAgainstSnapshot,
		"ValidateManifestRevisions":                ValidateManifestRevisions,
	} {
		message := "(no refusal)"
		if err := gate(tree); err != nil {
			message = err.Error()
		}
		t.Logf("%s over the same tree: %s", name, message)
		for id := range blind {
			if strings.Contains(message, id) {
				t.Fatalf("%s names %s over the incident tree; the mechanism this guard exists for — a manifest refreshed in the same edit that moved its payload — would have been visible to the existing gates, so the replay is not the incident", name, id)
			}
		}
	}

	// The other direction, over the same history: the release that corrected
	// the incident passes — and not vacuously. Nine modules moved between
	// v0.25.0 and v0.26.0 and every one carries a higher revision; the other
	// 288 are untouched and pass without a bump, which is the
	// false-positive half the gate would be useless without.
	corrected := t.TempDir()
	revisionBaselineExtract(t, root, revisionBaselineCorrectedTree, corrected)
	if err := ValidateRevisionsAgainstReleaseBaseline(corrected, baseline); err != nil {
		t.Fatalf("the release that corrected the incident was refused: %v", err)
	}
	scanned, err := scanRegistryManifests(corrected)
	if err != nil {
		t.Fatalf("scan the %s tree: %v", revisionBaselineCorrectedTree, err)
	}
	moved, untouched := 0, 0
	for _, manifest := range scanned {
		published, ok := baseline.modules[manifest.document.Module.ID]
		if !ok {
			continue
		}
		if len(movedSinceRelease(corrected, published, manifest)) > 0 {
			moved++
			continue
		}
		untouched++
	}
	t.Logf("%s against baseline %s: %d modules moved and bumped, %d untouched and unbumped",
		revisionBaselineCorrectedTree, revisionBaselineIncidentBaseline, moved, untouched)
	if moved < revisionBaselineCorrectedMoved {
		t.Fatalf("only %d module(s) moved between %s and %s; the green above is vacuous, so it proves nothing about a legitimately bumped module",
			moved, revisionBaselineIncidentBaseline, revisionBaselineCorrectedTree)
	}
	if untouched < revisionBaselineModuleFloor {
		t.Fatalf("only %d module(s) were untouched between the two releases; the untouched-module direction — the false-positive half — is not being exercised", untouched)
	}
}

// The incident fixtures: the last release before it, the tree as it stood one
// commit before the release bump that corrected it, and that corrected
// release itself — which is the same history read the other way, as the green
// a legitimately bumped module must get.
const (
	revisionBaselineIncidentBaseline = "v0.25.0"
	revisionBaselineIncidentTree     = "5b48bfa87903b8b27dbea29c99a23a323f6fcf34"
	revisionBaselineCorrectedTree    = "v0.26.0"
	// revisionBaselineCorrectedMoved is how many modules moved across that
	// release. A floor, not a census: the green above means nothing if
	// nothing actually changed between the two trees.
	revisionBaselineCorrectedMoved = 9
)

// revisionBaselineIncident is what the gate must find in that tree. refreshed
// marks the four whose manifests had already absorbed their payloads' new
// digests — the half neither existing gate can see.
var revisionBaselineIncident = []struct {
	id              string
	published, tree int
	refreshed       bool
}{
	{id: "ggg/system/ci-github", published: 6, tree: 6},
	{id: "ggg/system/content-assets", published: 43, tree: 43},
	{id: "ggg/system/mail-smtp", published: 3, tree: 3, refreshed: true},
	{id: "ggg/system/notifications-knock", published: 1, tree: 1, refreshed: true},
	{id: "ggg/system/storage-s3", published: 4, tree: 4},
	{id: "ggg/system/usage-openmeter", published: 1, tree: 1, refreshed: true},
	{id: "ggg/system/webhooks-svix", published: 1, tree: 1, refreshed: true},
}

// revisionBaselineRow reads the modules a refusal names, with both revisions,
// so the assertion is set equality against the incident rather than a
// substring search that a reworded message would silently satisfy.
var revisionBaselineRow = regexp.MustCompile(`([a-z0-9-]+/[a-z0-9-]+/[a-z0-9-]+) \(published revision (\d+), tree revision (\d+)`)

func revisionBaselineNamed(message string) []string {
	rows := make([]string, 0, 8)
	for _, match := range revisionBaselineRow.FindAllStringSubmatch(message, -1) {
		rows = append(rows, fmt.Sprintf("%s %s->%s", match[1], match[2], match[3]))
	}
	slices.Sort(rows)
	return rows
}

// revisionBaselineTag resolves the release the tree is measured against: the
// most recent release tag reachable from HEAD, which is what `ggg registry
// build` last published from. Reports false when the answer is unavailable,
// having already skipped.
func revisionBaselineTag(t *testing.T, root string) (string, bool) {
	t.Helper()
	if !revisionBaselineHistory(t, root) {
		return "", false
	}
	tag, err := revisionBaselineGit(root, "describe", "--tags", "--abbrev=0", "--match", revisionBaselineTagMatch, "HEAD")
	if err != nil || strings.TrimSpace(tag) == "" {
		t.Skipf("[inapplicable] no release tag is reachable from HEAD, so there is no published snapshot to measure this tree against; run `git fetch --tags origin` (or tag the first release) and re-run")
		return "", false
	}
	return strings.TrimSpace(tag), true
}

// revisionBaselineHistory refuses the vacuous run. A shallow clone or a tree
// with no readable git history cannot reach the released snapshot, and
// reporting a pass over a comparison that never happened is the failure mode
// this guard exists to prevent — so it states what is missing and the one
// command that fixes it, and stops.
func revisionBaselineHistory(t *testing.T, root string) bool {
	t.Helper()
	shallow, err := revisionBaselineGit(root, "rev-parse", "--is-shallow-repository")
	if err != nil || strings.TrimSpace(shallow) == "true" {
		t.Skipf("[inapplicable] the release-baseline revision gate reads the last released registry.snapshot.json out of git and needs the full history with its tags; this clone is shallow or has no readable git history — run `git fetch --unshallow && git fetch --tags origin` and re-run")
		return false
	}
	return true
}

// revisionBaselineFromRef loads one release's baseline out of git. One
// `git archive` carries the snapshot and every manifest it indexes as a tar
// stream that is never unpacked; LoadReleaseBaseline then authenticates each
// manifest against the signed snapshot's digest for it.
func revisionBaselineFromRef(t *testing.T, root, ref string) ReleaseBaseline {
	t.Helper()
	files := revisionBaselineArchive(t, root, ref, RegistrySnapshotPath, "registry/modules")
	snapshot, ok := files[RegistrySnapshotPath]
	if !ok {
		t.Fatalf("%s publishes no %s, so it is not a released registry", ref, RegistrySnapshotPath)
	}
	baseline, err := LoadReleaseBaseline(ref, snapshot, func(path string) ([]byte, error) {
		data, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("%s is indexed by the %s snapshot and absent from its tree", path, ref)
		}
		return data, nil
	})
	if err != nil {
		t.Fatalf("loading the %s baseline: %v", ref, err)
	}
	return baseline
}

// revisionBaselineArchive reads selected paths at one ref into memory.
func revisionBaselineArchive(t *testing.T, root, ref string, paths ...string) map[string][]byte {
	t.Helper()
	args := append([]string{"-C", root, "archive", "--format=tar", ref}, paths...)
	out, err := exec.CommandContext(t.Context(), "git", args...).Output()
	if err != nil {
		t.Fatalf("git archive %s %v: %v", ref, paths, err)
	}
	files := map[string][]byte{}
	reader := tar.NewReader(bytes.NewReader(out))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading the %s archive: %v", ref, err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("reading %s from the %s archive: %v", header.Name, ref, err)
		}
		files[header.Name] = data
	}
	return files
}

// revisionBaselineExtract materialises a whole tree at one ref. `git archive`
// rather than a worktree or a checkout: nothing touches the working tree, no
// registry entry to clean up, no HEAD to move — the era walk's rule.
func revisionBaselineExtract(t *testing.T, root, ref, dest string) {
	t.Helper()
	archive := exec.CommandContext(t.Context(), "git", "-C", root, "archive", "--format=tar", ref)
	tarball, err := archive.Output()
	if err != nil {
		t.Fatalf("git archive %s: %v", ref, err)
	}
	untar := exec.CommandContext(t.Context(), "tar", "-x", "-C", dest)
	untar.Stdin = bytes.NewReader(tarball)
	if out, err := untar.CombinedOutput(); err != nil {
		t.Fatalf("extracting the %s tree: %v\n%s", ref, err, out)
	}
	if _, err := os.Stat(filepath.Join(dest, "registry", "modules")); err != nil {
		t.Fatalf("the extracted %s tree carries no registry/modules: %v", ref, err)
	}
}

func revisionBaselineGit(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	return string(out), err
}
