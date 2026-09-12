// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. The walk below asserts THIS repository's own
// release history against its current binary — the tags are the fixtures, so
// the test needs the full git history and refuses to run vacuously without it.

package gggcli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// The era walk: old-tag derivatives versus today's binary.
//
// # The promise this test pins
//
// "A derivative created by an old release of the tool can walk forward" —
// with local edits preserved byte for byte through a real conflict. Until
// v0.24.0 that promise was false twice over, and both defects shipped: every
// derivative created before v0.16.0 was refused by every later tool on every
// planning command (the claims.jobs rule applied to lock rows the update was
// about to replace), and v0.16.0 derivatives were bricked one release later
// by the idempotent-route-scope rule through the same mechanism. The
// record-tier fix (a lock row is a record of what was installed, never a
// fresh authoring input) made every schema-2 era walk again — and the
// regression tests that landed with it pin the two known refusal shapes as
// unit fixtures. What they cannot pin is the CLASS: the next authoring rule
// wired into the lock-read path bricks old derivatives in a new shape no
// fixture names. Only the real walk catches that — era binary, era genesis,
// era lock, today's update — which is why it exists as this test rather than
// as a third unit fixture.
//
// # The two eras, and why not more
//
//	v0.1.1  the earliest tag that both speaks schema 2 and creates projects
//	        (v0.1.0-rc.1 is schema 1 and has no `ggg new` at all) — walks the
//	        record-tier path from the far end of the ladder
//	v0.16.0 the hardening-era boundary — the last genesis era the old
//	        authoring rules bricked, and the era whose walks stage a real
//	        conflict on a hub module with dependents (the held-module path)
//
// Exactly two: the mid-ladder hops the upgrade probe already proved
// equivalent add era binaries and genesis runs without adding a rule
// boundary, and the two direct walks cover both load-bearing paths
// (record-tier validation and generated-file staging under a held module).
// The list is written, not derived, because "which eras are load-bearing" is
// a judgment about rule boundaries, not a property of the tag set.
//
// # The walk, per era
//
//	git archive <tag>        the era tree materialized in a temp directory —
//	                         never a worktree or a checkout: the probe's
//	                         near-death experience; archive writes no worktree
//	                         registry entries and moves no HEAD
//	go build ./cmd/ggg       the era's own binary, from the era's own source
//	ggg new … --answers …    genesis per that era's documented flow: the user
//	                         shape (GitHub core registry + explicit --ref,
//	                         profile saas), driven non-interactively
//	<edit content/docs/cli.md>
//	                         one local edit in a module whose payload churns
//	                         between the era and the target (content-assets,
//	                         a hub module with dependents — the harder path)
//	update --ref <target>    today's binary; must exit 4 with exactly the one
//	                         conflict staged, never exit 3; resolve
//	                         --keep-local preserves the local bytes; the
//	                         completing update exits 0
//	sync --check --offline   the walked tree's own engine reports no pending
//	                         change, no drift, no conflict
//	generate + go build      the documented post-update step (the update
//	                         rewrites sources but not templ/sqlc outputs),
//	                         then the walked tree compiles
//
// plus per-module provenance over the final lock: every row's namespace
// matches its id, its snapshot and commit equal the ledger entry for its
// namespace, and every core row sits at the target's commit — one
// generation, no cross-generation contamination.
//
// # Cost and tier
//
// Measured on an Apple M1 Max with a warm Go build cache: ~165 s for both
// eras (era binaries 10–20 s each, genesis 25–35 s each, the walk steps
// seconds). Cold-cache CI is minutes, not seconds, and genesis needs the
// network (the era flow fetches the core GitHub tarball per ref), so this
// can never be a `make check` step — the same economics that keep the
// genesis sweep in its own CI job. It is opt-in through GGG_ERA_WALK=1;
// CI's `era-walk` workflow (workflow_dispatch plus a weekly schedule,
// never push/pull_request, never in a needs chain, never required) is the
// only thing that sets it, and the skip everywhere else carries
// InapplicableSkipMarker. A shallow clone or a missing tag is a stated
// refusal with its remedy, not a vacuous pass.
//
// # The red proof
//
// Applying internal/modkit/testdata/redproof/modkit-era-lock-authoring.patch
// (the exact revert of the record-tier split) and running this test makes
// the v0.16.0 leg die with the original brick — exit 3, the idempotent-scope
// refusal from the lock row — which is the demonstration that this gate
// would have caught the shipped defect the unit fixtures alone did not.

// eraWalkEnv un-skips the walk, mirroring GGG_GENESIS_SWEEP.
const eraWalkEnv = "GGG_ERA_WALK"

// eraWalkEras is the era matrix, exactly two, per the rationale above.
var eraWalkEras = []string{"v0.1.1", "v0.16.0"}

// eraWalkCoreRegistry is the user-shape core registry every era's documented
// genesis names. The embedded core registry public key is byte-identical
// from v0.1.1 through the current release, so an era binary trusts this
// source and today's binary trusts the snapshot it serves at the target ref.
const eraWalkCoreRegistry = "github:accidentally-awesome-labs/gogogadget"

// eraWalkEditModule and eraWalkEditPath name the local edit: content-assets
// owns the docs corpus, its cli.md payload churns across every era-to-head
// span the walk uses, and the module has dependents, so the conflict holds
// other modules and exercises the generated-file staging path.
const (
	eraWalkEditModule = "ggg/system/content-assets"
	eraWalkEditPath   = "content/docs/cli.md"
)

func TestOldEraDerivativesWalkToCurrent(t *testing.T) {
	root := repoRootFromTest(t)
	if os.Getenv(eraWalkEnv) != "1" {
		t.Skipf("%s the era walk builds two era binaries, creates two era derivatives and walks both to current (~165 s warm, minutes cold, network for the core GitHub tarballs); CI's `era-walk` workflow owns it — set GGG_ERA_WALK=1 to run it here",
			InapplicableSkipMarker)
	}

	// The tags are the fixtures. A shallow clone or a pruned tag set cannot
	// run this walk, and running nothing while reporting a pass is the
	// vacuity this guard exists to refuse: state what is missing and the one
	// command that fixes it, and stop.
	if shallow, err := eraWalkGit(root, "rev-parse", "--is-shallow-repository"); err != nil || strings.TrimSpace(shallow) == "true" {
		t.Skipf("%s the era walk materializes era trees with git archive and needs the full history; this clone is shallow or has no readable git history — run `git fetch --unshallow` and re-run",
			InapplicableSkipMarker)
	}
	target := strings.TrimSpace(mustEraWalkGit(t, root, "describe", "--tags", "--abbrev=0", "HEAD"))
	if target == "" {
		t.Skipf("%s no tag is reachable from HEAD, so the walk has no target content; tag a release (the parent's release order) and re-run",
			InapplicableSkipMarker)
	}
	targetCommit := strings.TrimSpace(mustEraWalkGit(t, root, "rev-parse", "--verify", target+"^{commit}"))
	for _, ref := range append([]string{target}, eraWalkEras...) {
		if _, err := eraWalkGit(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
			t.Skipf("%s the era walk needs tag %s and it is absent from the accessible refs — run `git fetch --tags origin` and re-run",
				InapplicableSkipMarker, ref)
		}
	}

	// Today's binary is the tool under test; the era binaries are fixtures.
	today := filepath.Join(t.TempDir(), "ggg-today")
	if out, err := eraWalkBuild(t, root, today); err != nil {
		t.Fatalf("building today's binary: %v\n%s", err, out)
	}

	walkStarted := time.Now()
	for _, era := range eraWalkEras {
		t.Run(era, func(t *testing.T) {
			eraWalkOneEra(t, root, today, era, target, targetCommit, walkStarted)
		})
	}
	t.Logf("era walk total: %s (target %s at %s)", time.Since(walkStarted).Round(time.Second), target, targetCommit[:12])
}

// eraWalkOneEra runs one era's full walk: era tree, era binary, era genesis,
// one local edit, and the exit-4 → resolve → complete → clean-check sequence
// under today's binary.
func eraWalkOneEra(t *testing.T, root, today, era, target, targetCommit string, walkStarted time.Time) {
	t.Helper()
	legStarted := time.Now()
	slug := "era-walk-" + strings.NewReplacer(".", "").Replace(era)

	// The era tree, via git archive into a temp directory. Not a worktree:
	// the probe's near-death experience — no registry entries to clean, no
	// HEAD to move, nothing that can touch the working tree.
	eraTree := t.TempDir()
	archive := exec.CommandContext(t.Context(), "git", "-C", root, "archive", "--format=tar", era)
	tarball, err := archive.Output()
	if err != nil {
		t.Fatalf("git archive %s: %v", era, err)
	}
	untar := exec.CommandContext(t.Context(), "tar", "-x", "-C", eraTree)
	untar.Stdin = bytes.NewReader(tarball)
	if out, err := untar.CombinedOutput(); err != nil {
		t.Fatalf("extracting the %s tree: %v\n%s", era, err, out)
	}

	eraBin := filepath.Join(t.TempDir(), "ggg-"+era)
	if out, err := eraWalkBuild(t, eraTree, eraBin); err != nil {
		t.Fatalf("building the %s binary: %v\n%s", era, err, out)
	}

	// Genesis per the era's documented flow: the user shape. The answers
	// carry exactly the five keys the era binaries parse, profile saas.
	answers := filepath.Join(t.TempDir(), "answers.json")
	writeEraWalkAnswers(t, answers, eraWalkAnswers{
		Name:     slug,
		Module:   "example.com/" + slug,
		Profile:  "saas",
		Registry: eraWalkCoreRegistry,
		Ref:      era,
	})
	dest := filepath.Join(t.TempDir(), "genesis")
	exit, out := eraWalkRun(t, eraBin, t.TempDir(), "new", dest, "--answers", answers, "--non-interactive")
	if exit != 0 {
		t.Fatalf("%s genesis (ggg new) = exit %d:\n%s", era, exit, out)
	}

	genesisLock := eraWalkLock(t, dest)
	if len(genesisLock.Modules) == 0 {
		t.Fatalf("%s genesis installed no modules", era)
	}
	genesisRow := eraWalkModuleRow(t, genesisLock, eraWalkEditModule)
	genesisFile := eraWalkFileRow(t, genesisRow, eraWalkEditPath)

	// The local edit, in a module whose payload churns between the era and
	// the target. Hashed the moment it is written; every later assertion
	// compares against these exact bytes.
	edit := readTestFile(t, dest, eraWalkEditPath)
	edit = append(edit, []byte("\n<!-- era-walk local edit at "+era+" -->\n")...)
	writeEraWalkFile(t, dest, eraWalkEditPath, edit)
	localSHA := eraWalkSHA256(t, dest, eraWalkEditPath)

	// The walk. Exit 4 with exactly the one conflict staged is the promised
	// shape; exit 3 here is the brick this gate exists to catch.
	conflict := eraWalkUpdate(t, today, dest, era, target)
	if conflict.BaseSHA256 != genesisFile.BaseSHA256 {
		t.Fatalf("the staged conflict's base %s is not the genesis base %s; the edit did not land where the walk expects",
			conflict.BaseSHA256, genesisFile.BaseSHA256)
	}
	if conflict.LocalSHA256 != localSHA {
		t.Fatalf("the staged conflict's local digest %s is not the edited file's digest %s", conflict.LocalSHA256, localSHA)
	}
	if conflict.UpstreamSHA256 == conflict.BaseSHA256 {
		t.Fatalf("%s's %s payload did not churn between %s and %s, so the conflict proves nothing about the walk",
			eraWalkEditModule, eraWalkEditPath, era, target)
	}
	for _, staged := range []string{conflict.CandidatePath, conflict.DiffPath} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(staged))); err != nil {
			t.Fatalf("the conflict staged no %s (%s): %v", filepath.Base(staged), staged, err)
		}
	}

	// Resolve keeping the local bytes, then complete the update.
	exit, out = eraWalkRun(t, today, dest, "resolve", eraWalkEditModule, "--path", eraWalkEditPath, "--keep-local", "--json")
	if exit != 0 {
		t.Fatalf("resolve --keep-local = exit %d:\n%s", exit, out)
	}
	if got := eraWalkSHA256(t, dest, eraWalkEditPath); got != localSHA {
		t.Fatalf("resolve --keep-local changed the local bytes: %s -> %s", localSHA, got)
	}
	eraWalkUpdateComplete(t, today, dest, era, target)
	if got := eraWalkSHA256(t, dest, eraWalkEditPath); got != localSHA {
		t.Fatalf("the completing update changed the local bytes: %s -> %s", localSHA, got)
	}

	// Provenance over the final lock: one generation per namespace, every
	// row pinned to its namespace's ledger entry, every core row at the
	// target commit, and the edited module's own snapshot moved.
	finalLock := eraWalkLock(t, dest)
	if len(finalLock.Modules) == 0 {
		t.Fatal("the walked lock has no modules")
	}
	ledger := map[string]modkit.LockedSnapshot{}
	for _, snapshot := range finalLock.Snapshots {
		ledger[snapshot.Namespace] = snapshot
	}
	for _, row := range finalLock.Modules {
		namespace := strings.SplitN(row.ID, "/", 2)[0]
		if row.RegistryNamespace != namespace {
			t.Fatalf("%s's lock row declares namespace %s", row.ID, row.RegistryNamespace)
		}
		snapshot, ok := ledger[row.RegistryNamespace]
		if !ok {
			t.Fatalf("%s's lock row references no snapshot for namespace %s", row.ID, row.RegistryNamespace)
		}
		if row.SnapshotSHA256 != snapshot.SnapshotSHA256 || row.SourceCommit != snapshot.Commit {
			t.Fatalf("%s's lock row sits at %s/%s while the ledger pins %s at %s — cross-generation contamination",
				row.ID, row.SourceCommit[:12], row.SnapshotSHA256[:12], snapshot.Namespace, snapshot.Commit[:12])
		}
		if namespace == "ggg" && row.SourceCommit != targetCommit {
			t.Fatalf("%s is a core row at commit %s; every core row must sit at the walked target %s",
				row.ID, row.SourceCommit, targetCommit)
		}
	}
	if snapshot, ok := ledger["ggg"]; !ok || snapshot.Commit != targetCommit {
		t.Fatalf("the walked lock's core snapshot ledger entry is %v; want the ggg namespace pinned at %s", ledger["ggg"].Commit, targetCommit)
	}
	finalRow := eraWalkModuleRow(t, finalLock, eraWalkEditModule)
	if finalRow.SnapshotSHA256 == genesisRow.SnapshotSHA256 {
		t.Fatalf("%s's snapshot digest did not move across the walk; the walk never changed the module's payload",
			eraWalkEditModule)
	}
	finalFile := eraWalkFileRow(t, finalRow, eraWalkEditPath)
	if finalFile.BaseSHA256 != conflict.UpstreamSHA256 {
		t.Fatalf("the resolved file row's base %s is not the upstream digest %s; the base did not advance",
			finalFile.BaseSHA256, conflict.UpstreamSHA256)
	}
	if finalFile.LocalSHA256 != localSHA || finalFile.State != modkit.FileModified {
		t.Fatalf("the resolved file row is %s at %s; want modified at the preserved local digest %s",
			finalFile.State, finalFile.LocalSHA256, localSHA)
	}

	// The walked tree's own engine reports nothing pending, offline, and the
	// tree compiles.
	exit, out = eraWalkRun(t, today, dest, "sync", "--check", "--offline", "--json")
	if exit != 0 {
		t.Fatalf("sync --check --offline after the walk = exit %d:\n%s", exit, out)
	}
	var check eraWalkEnvelope
	eraWalkDecode(t, out, &check)
	if len(check.Conflicts) != 0 || len(check.Diagnostics) != 0 {
		t.Fatalf("the post-walk check reports %d conflict(s) and %d diagnostic(s): %+v %+v",
			len(check.Conflicts), len(check.Diagnostics), check.Conflicts, check.Diagnostics)
	}
	for _, change := range check.Changes {
		if change.Kind != "unchanged" {
			t.Fatalf("the post-walk check reports %s at %s as %s; a clean check has only unchanged rows",
				change.Path, change.Module, change.Kind)
		}
	}

	// Generation, then the compile. The update rewrites sources; it does not
	// regenerate templ/sqlc outputs, and the era-generated files still in the
	// tree are five majors stale (a walked v0.1.1 tree that builds before
	// generation is the anomaly, not the rule — the probe's contiguous-era
	// walks happened to compile). `ggg generate` is the documented post-update
	// step, and asserting it here keeps the walk's last claim whole: the
	// walked project is one generation away from a compiling product.
	exit, out = eraWalkRun(t, today, dest, "generate")
	if exit != 0 {
		t.Fatalf("generate after the walk = exit %d:\n%s", exit, out)
	}
	runIn(t, dest, "go", "build", "./...")
	t.Logf("%s -> %s: genesis %d modules, %d after the walk, local edit %s preserved, total leg %s (walk so far %s)",
		era, target, len(genesisLock.Modules), len(finalLock.Modules), localSHA[:12],
		time.Since(legStarted).Round(time.Second), time.Since(walkStarted).Round(time.Second))
}

// eraWalkUpdate runs one update hop and returns the single staged conflict,
// refusing everything else: exit 4 with exactly the edited file staged is
// the promised shape.
func eraWalkUpdate(t *testing.T, binary, dest, era, target string) eraWalkConflict {
	t.Helper()
	exit, out := eraWalkRun(t, binary, dest, "update", "--registry", "ggg", "--ref", target, "--json")
	if exit != 4 {
		t.Fatalf("%s -> %s update = exit %d (want 4 with the conflict staged — exit 3 is the brick this gate exists to catch):\n%s",
			era, target, exit, out)
	}
	var envelope eraWalkEnvelope
	eraWalkDecode(t, out, &envelope)
	if len(envelope.Conflicts) != 1 {
		t.Fatalf("%s -> %s update staged %d conflict(s) (%+v); want exactly the one edit",
			era, target, len(envelope.Conflicts), envelope.Conflicts)
	}
	conflict := envelope.Conflicts[0]
	if conflict.Module != eraWalkEditModule || conflict.Path != eraWalkEditPath {
		t.Fatalf("the staged conflict is %s × %s; want %s × %s",
			conflict.Module, conflict.Path, eraWalkEditModule, eraWalkEditPath)
	}
	return conflict
}

// eraWalkUpdateComplete runs the completing update after the resolve and
// refuses any exit but a clean 0.
func eraWalkUpdateComplete(t *testing.T, binary, dest, era, target string) {
	t.Helper()
	exit, out := eraWalkRun(t, binary, dest, "update", "--registry", "ggg", "--ref", target, "--json")
	if exit != 0 {
		t.Fatalf("%s -> %s completing update = exit %d:\n%s", era, target, exit, out)
	}
	var envelope eraWalkEnvelope
	eraWalkDecode(t, out, &envelope)
	if len(envelope.Conflicts) != 0 {
		t.Fatalf("the completing update still stages %d conflict(s): %+v", len(envelope.Conflicts), envelope.Conflicts)
	}
}

type eraWalkAnswers struct {
	Name     string `json:"Name"`
	Module   string `json:"Module"`
	Profile  string `json:"Profile"`
	Registry string `json:"Registry"`
	Ref      string `json:"Ref"`
}

type eraWalkEnvelope struct {
	OK          bool              `json:"ok"`
	Exit        int               `json:"exit"`
	Changes     []eraWalkChange   `json:"changes"`
	Conflicts   []eraWalkConflict `json:"conflicts"`
	Diagnostics []eraWalkMessage  `json:"diagnostics"`
}

type eraWalkChange struct {
	Path   string `json:"path"`
	Module string `json:"module"`
	Kind   string `json:"kind"`
}

type eraWalkConflict struct {
	Module         string `json:"module"`
	Path           string `json:"path"`
	BaseSHA256     string `json:"base_sha256"`
	LocalSHA256    string `json:"local_sha256"`
	UpstreamSHA256 string `json:"upstream_sha256"`
	CandidatePath  string `json:"candidate_path"`
	DiffPath       string `json:"diff_path"`
}

type eraWalkMessage struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// eraWalkRun runs one ggg command through a built binary and returns its
// exit code with everything it printed. The walk's interesting failures are
// refusals that carry their cause in the envelope or on stderr, and the exit
// code is the assertion, so nothing is discarded — including the runner's
// own error, appended to the output so a signal death is diagnosable.
func eraWalkRun(t *testing.T, binary, dir string, argv ...string) (int, []byte) {
	t.Helper()
	command := exec.CommandContext(t.Context(), binary, argv...)
	command.Dir = dir
	out, err := command.CombinedOutput()
	if err != nil {
		out = append(out, []byte("\n[era-walk runner] "+err.Error())...)
	}
	exit := 0
	if err != nil {
		exit = -1
		var coded *exec.ExitError
		if errors.As(err, &coded) {
			exit = coded.ExitCode()
		}
	}
	return exit, out
}

// eraWalkDecode parses one JSON envelope out of a command's combined output,
// starting at the first '{' so toolchain noise on earlier lines (genesis runs
// `go mod tidy`) cannot defeat it.
func eraWalkDecode(t *testing.T, out []byte, into any) {
	t.Helper()
	at := bytes.IndexByte(out, '{')
	if at < 0 {
		t.Fatalf("no JSON envelope in:\n%s", out)
	}
	if err := json.NewDecoder(bytes.NewReader(out[at:])).Decode(into); err != nil {
		t.Fatalf("decoding the envelope: %v\n%s", err, out)
	}
}

// eraWalkBuild compiles one ggg binary from one tree with a warm shared
// build cache, returning the build's output alongside the error.
func eraWalkBuild(t *testing.T, tree, out string) ([]byte, error) {
	t.Helper()
	build := exec.CommandContext(t.Context(), "go", "build", "-o", out, "./cmd/ggg")
	build.Dir = tree
	return build.CombinedOutput()
}

// eraWalkGit runs one git query in the repository root.
func eraWalkGit(root string, argv ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", root}, argv...)...)
	out, err := command.Output()
	return string(out), err
}

// mustEraWalkGit is eraWalkGit for a query whose failure is fatal.
func mustEraWalkGit(t *testing.T, root string, argv ...string) string {
	t.Helper()
	out, err := eraWalkGit(root, argv...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(argv, " "), err)
	}
	return out
}

// eraWalkLock parses a derivative's lock with today's engine — itself an
// assertion: an era lock the current tool cannot parse is the P0-1 brick.
func eraWalkLock(t *testing.T, dest string) modkit.Lock {
	t.Helper()
	lock, err := modkit.ParseLock(readTestFile(t, dest, modkit.LockFileName))
	if err != nil {
		t.Fatalf("parsing the lock under today's engine: %v", err)
	}
	return lock
}

func eraWalkModuleRow(t *testing.T, lock modkit.Lock, id string) modkit.LockedModule {
	t.Helper()
	for _, row := range lock.Modules {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("the lock has no %s row", id)
	return modkit.LockedModule{}
}

func eraWalkFileRow(t *testing.T, row modkit.LockedModule, path string) modkit.LockedFile {
	t.Helper()
	for _, file := range row.Files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("%s's lock row has no file row for %s", row.ID, path)
	return modkit.LockedFile{}
}

func eraWalkSHA256(t *testing.T, dest, path string) string {
	t.Helper()
	sum := sha256.Sum256(readTestFile(t, dest, path))
	return hex.EncodeToString(sum[:])
}

func writeEraWalkAnswers(t *testing.T, path string, answers eraWalkAnswers) {
	t.Helper()
	raw, err := json.Marshal(answers)
	if err != nil {
		t.Fatal(err)
	}
	writeEraWalkFileBytes(t, path, raw)
}

func writeEraWalkFile(t *testing.T, dest, path string, content []byte) {
	t.Helper()
	writeEraWalkFileBytes(t, filepath.Join(dest, filepath.FromSlash(path)), content)
}

func writeEraWalkFileBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
