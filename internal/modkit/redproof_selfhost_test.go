package modkit

// The red-proof gate: a guard that cannot be driven red on demand is not a
// guard, it is a hope.
//
// Every guard this program shipped in v0.18.0–v0.20.0 was driven red once, at
// birth: the violation was planted, the guard was watched to fail, and the
// plant was reverted. Nothing since has kept those guards red-able. A refactor
// can gut a guard's assertions while its population floors still pass; a "fix"
// can weaken a check into tautology; a rename can strand it. The v0.20.0 sweep
// found two v0.19.0 guards already drifted one release after shipping.
//
// This test institutionalises the discipline as patch-based mutation proof:
//
//   - a RED PROOF is a unified diff that plants one violation, paired with the
//     `-run` pattern of the guard that must catch it and, where the guard
//     reports distinctive text, an expected substring;
//   - for every patch, this gate materialises a scratch copy of the tree,
//     applies the patch there, runs the paired guard, and requires a FAIL
//     whose output contains the expected substring — a guard that passes over
//     its own planted violation is TOOTHLESS, and a failure whose text lacks
//     the expected substring is an environmental MASQUERADE, not a proof;
//   - before any patch runs, every paired guard must PASS on the unmutated
//     scratch copy. A guard that is red on the clean tree makes every red
//     proof of it meaningless, and that clean pass doubles as the regression
//     canary for the guards no patch reaches;
//   - a patch that no longer applies is STALE. That is the gate working, not
//     the gate failing: someone changed the code the proof was pinned to and
//     must refresh the proof with it.
//
// The corpus lives in testdata/redproof/*.patch next to this file, one patch
// per violation, its metadata in Redproof-* headers above the diff. The guard
// inventory (testdata/redproof/inventory.txt) is DERIVED, not hand-written:
// every test matching the repository's guard naming families in the guard
// files — every `*_selfhost_test.go` under internal/{modkit,gggcli,web} and
// the web/modkit guard files the sweep walked — plus the named contract
// guards. The corpus is the inventory's floor: every inventoried guard has a
// patch or a stated allowance, and a family with no patches fails the run by
// name. Regenerate the inventory with GGG_REDPROOF_UPDATE_INVENTORY=1.
//
// This file is a self_host payload: it asserts this repository's own tree and
// never installs into a derivative, whose patch corpus would point at
// core-only paths. GGG_REDPROOF=off skips it where scratch copies are
// impractical (a laptop mid-rebase, say); the default is to run.
//
// Scratch mechanics reuse what this repo already sanctions: `bin/ggg registry
// validate` builds its own /tmp derivative, and the snapshot-healing primitives
// (RefreshManifestDigests + WriteRegistrySnapshot) exist for exactly this
// shape of "consistent copy without the release order". No patch in the corpus
// needs snapshot healing today — every current guard reads the bytes it
// asserts from the tree — but the runner carries the distinction anyway:
// "failed for the planted reason" and "failed for an environmental reason"
// are separated by the expected substring, never by luck.

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// redproofSkipEnv disables the gate; scratch copies cost minutes and a tree
// mid-rebase cannot provide a consistent one.
const redproofSkipEnv = "GGG_REDPROOF"

// redproofCorpusDir is the corpus, one .patch per red proof, beside this
// file's package as a repo-relative path (the runner resolves from the root).
const redproofCorpusDir = "internal/modkit/testdata/redproof"

// redproofInventoryFile pins the derived guard population.
const redproofInventoryFile = "internal/modkit/testdata/redproof/inventory.txt"

// redproofUpdateInventoryEnv regenerates the inventory from the derivation.
const redproofUpdateInventoryEnv = "GGG_REDPROOF_UPDATE_INVENTORY"

// redproofScratchSkipTop are ROOT entries the copy leaves out: the git
// identity (the scratch gets its own), 178MB of pinned tool binaries, local
// run state, orchestration notes and local secrets. Root-anchored on purpose:
// a basename-anywhere rule once ate `registry/modules/page/docs/` and would
// eat `internal/web/tmp` today.
var redproofScratchSkipTop = []string{
	".git", ".superpowers", ".worktrees", ".ggg", "bin", "tmp", ".env", ".DS_Store",
}

// redproofScratchSkipNames are artifact kinds that never carry source, at any
// depth: e2e's Playwright install and its run reports. `e2e/` itself ships
// with the copy — the ownership planes count its generated forms.
var redproofScratchSkipNames = []string{"node_modules", "playwright-report", "test-results"}

// redproofGuardSelfhostDirs are the package directories whose
// `*_selfhost_test.go` files are guard files by the repository's own
// convention: AGENTS.md's self_host paragraph states that a new
// self-hosting test goes in a self_host payload named just so.
var redproofGuardSelfhostDirs = []string{
	"internal/modkit",
	"internal/gggcli",
	"internal/web",
	"internal/web/templates",
	"internal/web/templates/ui",
}

// redproofGuardFiles are the non-selfhost guard files the v0.20.0 sweep
// walked: the design-system, control-id, contract, CSP and accounted-gate
// suites. A new guard file is invisible to the derivation until it is added
// here — the honest boundary of a file-scoped population, stated rather than
// hidden.
var redproofGuardFiles = []string{
	"internal/web/csp_test.go",
	"internal/web/layout_chrome_test.go",
	"internal/web/templates/designsystem_test.go",
	"internal/web/templates/templ_raw_test.go",
	"internal/web/templates/ui/imports_test.go",
	"internal/web/templates/ui/control-id_test.go",
	"internal/web/templates/ui/contract_test.go",
	"internal/web/templates/ui/rendered_classes_test.go",
	"internal/modkit/ci_workflow_test.go",
	"internal/modkit/registry_test.go",
	"internal/gggcli/gate_test.go",
}

// redproofNamedGuards are the contract guards whose names fall outside the
// naming families (a sweep-covered guard is not always family-named). Family
// is the sweep family the guard's red proof belongs to.
var redproofNamedGuards = map[string]string{
	"TestRuntimeGrammarAgreesWithThePlanTimeGrammar":     "web",
	"TestDesignSystemLayering":                           "web",
	"TestUIImportsTemplAndStdlibOnly":                    "web",
	"TestCIProfilesJobRunsTheGenesisSweep":               "modkit",
	"TestAccountNamesPackagesWithNoTestFiles":            "ownership",
	"TestAccountRefusesAnInapplicableMarkerWithNoReason": "ownership",
}

// redproofGuardNameFamily matches the repository's guard naming conventions:
// the TestAgents* document-truth gates, the TestEvery*/TestNo* universals,
// the TestPublished*/TestValidator* parity families, and the TestThe*
// restatements (TestThePublishedCatalogClaimsEveryTargetExactlyOnce et al).
var redproofGuardNameFamily = regexp.MustCompile(`(?m)^func (Test(?:Agents|Every|No|Published|Validator|The)[A-Za-z0-9_]*)\(`)

// redproofFamilies are the three families the v0.20.0 guard sweep covered.
// Each must carry patches; a family with none fails the run by name.
var redproofFamilies = []string{"web", "modkit", "ownership"}

// redproofFloors. The corpus-as-inventory floor: patches ≥ distinct guards
// means no patch pads the count without covering a guard, and the distinct
// floor means one guard many times is not coverage.
const (
	redproofMinPatches   = 20
	redproofMinDistinct  = 15
	redproofMinPerFamily = 3
)

type redproofPatch struct {
	id      string
	family  string
	pkg     string // the Go package the paired guard compiles in
	run     string // exact test names, | separated — no wildcards, so the guard↔patch map is syntactic
	expect  string // substring the guard's own failure text must contain
	summary string
	files   []string
	body    []byte // the unified diff, from the first `diff --git` line
}

type redproofEntry struct {
	guard  string
	family string
	patch  string // patch id, or ""
	allow  string // allowance reason when patch == ""
}

func TestRedProofGate(t *testing.T) {
	if os.Getenv(redproofSkipEnv) == "off" {
		t.Skip("[inapplicable] GGG_REDPROOF=off: scratch-copy mutation proofs are disabled for this run (the corpus and inventory are still checked)")
	}
	started := time.Now()

	root := redproofRepositoryRoot(t)
	corpus := redproofLoadCorpus(t, root)
	inventory := redproofLoadInventory(t, root)
	redproofCheckCoverage(t, corpus, inventory, root)

	scratch := redproofMaterialiseScratch(t, root)
	defer os.RemoveAll(scratch)

	// The clean pass: every paired guard must be green on the unmutated copy.
	// This is half the honesty of the gate — a red proof of a guard that is
	// already red proves nothing — and the regression canary besides.
	redproofCleanPass(t, scratch, corpus)

	// The mutation pass: apply, require a fail that says the guard's own
	// words, revert, require the tree come back byte-identical.
	redproofMutationPass(t, scratch, corpus)

	t.Logf("red proof: %d patches over %d guards in %s (scratch %s)",
		len(corpus), redproofDistinctGuards(corpus), time.Since(started).Round(time.Millisecond), scratch)
}

// ---------------------------------------------------------------- corpus ----

func redproofLoadCorpus(t *testing.T, root string) []redproofPatch {
	t.Helper()
	dir := filepath.Join(root, redproofCorpusDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read red-proof corpus %s: %v (the corpus is the gate; an empty run is a green lie)", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".patch") {
			names = append(names, entry.Name())
		}
	}
	if len(names) < redproofMinPatches {
		t.Fatalf("%d patch(es) in %s, want ≥ %d: the corpus shrank below its floor — restore the deleted proofs or lower the floor in the same breath", len(names), dir, redproofMinPatches)
	}
	slices.Sort(names)
	var corpus []redproofPatch
	seen := map[string]bool{}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read patch %s: %v", name, err)
		}
		patch, err := redproofParsePatch(strings.TrimSuffix(name, ".patch"), raw)
		if err != nil {
			t.Fatalf("patch %s: %v", name, err)
		}
		if seen[patch.id] {
			t.Fatalf("duplicate red-proof id %q", patch.id)
		}
		seen[patch.id] = true
		corpus = append(corpus, patch)
	}
	return corpus
}

// redproofParsePatch splits the Redproof-* header from the diff body. The
// header is this gate's own metadata; the body is exactly what `git apply`
// consumes, stored as the unified diff `git diff` writes.
func redproofParsePatch(id string, raw []byte) (redproofPatch, error) {
	var patch redproofPatch
	patch.id = id
	lines := strings.SplitAfter(string(raw), "\n")
	var bodyFrom int
	headers := map[string]string{}
	for i, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			bodyFrom = i
			break
		}
		if after, ok := strings.CutPrefix(line, "Redproof-"); ok {
			key, value, found := strings.Cut(strings.TrimRight(after, "\n"), ": ")
			if !found {
				return patch, fmt.Errorf("malformed header %q", line)
			}
			headers[key] = value
		}
		if bodyFrom == 0 && i == len(lines)-1 {
			return patch, errors.New("no diff body: a Redproof patch is a real unified diff, not a note")
		}
	}
	if bodyFrom == 0 {
		return patch, errors.New("no diff body")
	}
	patch.body = []byte(strings.Join(lines[bodyFrom:], ""))
	var missing []string
	for _, field := range [...]struct {
		key  string
		dest *string
	}{
		{"Family", &patch.family}, {"Run", &patch.run},
		{"Package", &patch.pkg}, {"Expect", &patch.expect}, {"Summary", &patch.summary},
	} {
		value := headers[field.key]
		if value == "" {
			missing = append(missing, field.key)
		}
		*field.dest = value
	}
	if len(missing) > 0 {
		return patch, fmt.Errorf("missing %s header(s)", strings.Join(missing, ", "))
	}
	if !slices.Contains(redproofFamilies, patch.family) {
		return patch, fmt.Errorf("family %q is not one of %v", patch.family, redproofFamilies)
	}
	for _, name := range strings.Split(patch.run, "|") {
		if name == "" || name != strings.TrimSpace(name) || strings.Contains(name, " ") {
			return patch, fmt.Errorf("Run %q must be exact test names joined by |, no wildcards — the guard↔patch map is checked syntactically", patch.run)
		}
	}
	// The target files, straight out of the diff headers, for stale and
	// toothless messages that must name them. A new-file diff has no --- a/
	// line and a deleted-file diff has no +++ b/, so take either side.
	for _, line := range strings.Split(string(patch.body), "\n") {
		var target string
		if t, ok := strings.CutPrefix(line, "--- a/"); ok {
			target = t
		} else if t, ok := strings.CutPrefix(line, "+++ b/"); ok {
			target = t
		} else {
			continue
		}
		if !slices.Contains(patch.files, target) {
			patch.files = append(patch.files, target)
		}
	}
	if len(patch.files) == 0 {
		return patch, errors.New("diff touches no file")
	}
	return patch, nil
}

// ---------------------------------------------------------------- inventory ----

func redproofLoadInventory(t *testing.T, root string) []redproofEntry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, redproofInventoryFile))
	if err != nil {
		if os.IsNotExist(err) && os.Getenv(redproofUpdateInventoryEnv) == "1" {
			return nil // first generation: nothing to check against yet
		}
		t.Fatalf("read inventory: %v (regenerate with %s=1)", err, redproofUpdateInventoryEnv)
	}
	var entries []redproofEntry
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			t.Fatalf("inventory line %q: want guard\tfamily\tpatch-id-or-allow:reason", line)
		}
		entry := redproofEntry{guard: parts[0], family: parts[1]}
		if reason, ok := strings.CutPrefix(parts[2], "allow:"); ok {
			if strings.TrimSpace(reason) == "" {
				t.Fatalf("inventory entry %s: an allowance with no reason is a shrug, not a decision", entry.guard)
			}
			entry.allow = reason
		} else {
			entry.patch = parts[2]
		}
		entries = append(entries, entry)
	}
	if len(entries) < redproofMinDistinct && os.Getenv(redproofUpdateInventoryEnv) != "1" {
		t.Fatalf("inventory holds %d guards, want ≥ %d — the corpus cannot have covered a population this small; regenerate with %s=1 and see what moved",
			len(entries), redproofMinDistinct, redproofUpdateInventoryEnv)
	}
	return entries
}

// redproofDeriveGuards walks the guard files for the naming families. The
// population is derived from the tree, never hand-written, so a guard deleted
// from the tree or added to a guard file moves this set and the inventory
// check below names the move.
func redproofDeriveGuards(t *testing.T, root string) map[string]string {
	t.Helper()
	guards := map[string]string{}
	for _, dir := range redproofGuardSelfhostDirs {
		matches, err := filepath.Glob(filepath.Join(root, dir, "*selfhost_test.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		for _, match := range matches {
			redproofScanGuardFile(t, root, match, guards)
		}
	}
	for _, rel := range redproofGuardFiles {
		redproofScanGuardFile(t, root, filepath.Join(root, rel), guards)
	}
	for name, family := range redproofNamedGuards {
		if _, duplicate := guards[name]; duplicate {
			t.Fatalf("named contract guard %s is also family-derived; drop it from redproofNamedGuards", name)
		}
		guards[name] = family
	}
	return guards
}

func redproofScanGuardFile(t *testing.T, root, path string, guards map[string]string) {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("rel %s: %v", path, err)
	}
	family := redproofFamilyForFile(rel)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read guard file %s: %v", rel, err)
	}
	for _, match := range redproofGuardNameFamily.FindAllStringSubmatch(string(raw), -1) {
		name := match[1]
		if previous, duplicate := guards[name]; duplicate && previous != family {
			t.Fatalf("guard %s is declared in two families (%s, %s)", name, previous, family)
		}
		guards[name] = family
	}
}

// redproofFamilyForFile maps a guard file to its sweep family.
func redproofFamilyForFile(rel string) string {
	switch {
	case strings.HasPrefix(rel, "internal/web/"):
		return "web"
	case strings.HasPrefix(rel, "internal/modkit/ownership_selfhost_test.go"),
		strings.HasPrefix(rel, "internal/modkit/snapshot_ownership_selfhost_test.go"),
		strings.HasPrefix(rel, "internal/modkit/agents_planes_selfhost_test.go"),
		strings.HasPrefix(rel, "internal/modkit/agents_counts_selfhost_test.go"),
		strings.HasPrefix(rel, "internal/modkit/agents_repomap_selfhost_test.go"),
		strings.HasPrefix(rel, "internal/modkit/agents_declarations_selfhost_test.go"),
		strings.HasPrefix(rel, "internal/gggcli/"):
		return "ownership"
	default:
		return "modkit"
	}
}

// redproofCheckCoverage holds the corpus-as-inventory floor. Both directions:
// the inventory cannot miss a derived guard (a renamed or deleted guard fails
// by name until the inventory is refreshed in the same change), and a derived
// guard cannot exist without either a patch or a stated allowance.
func redproofCheckCoverage(t *testing.T, corpus []redproofPatch, inventory []redproofEntry, root string) {
	t.Helper()
	// The floors are configuration, not verdicts: a corpus that fails them
	// lies about itself, so every message is gathered and the run stops
	// before a single proof is executed against it.
	t.Run("coverage", func(t *testing.T) {
		redproofCheckCoverageInner(t, corpus, inventory, root)
	})
	if t.Failed() {
		t.Fatalf("the corpus/inventory floors above failed; no proof was run — fix the corpus in the same change that broke it")
	}
}

func redproofCheckCoverageInner(t *testing.T, corpus []redproofPatch, inventory []redproofEntry, root string) {
	t.Helper()
	derived := redproofDeriveGuards(t, root)
	if os.Getenv(redproofUpdateInventoryEnv) == "1" {
		redproofWriteInventory(t, root, derived, corpus)
	}
	byGuard := map[string]redproofEntry{}
	for _, entry := range inventory {
		if _, duplicate := byGuard[entry.guard]; duplicate {
			t.Fatalf("inventory names guard %s twice", entry.guard)
		}
		byGuard[entry.guard] = entry
	}
	// Direction one: every derived guard is inventoried. A new guard test in a
	// guard file, or a rename, surfaces here by name.
	var unlisted, vanished []string
	for guard := range derived {
		if _, listed := byGuard[guard]; !listed {
			unlisted = append(unlisted, guard)
		}
	}
	// Direction two: every inventoried guard still exists. A deleted or
	// renamed guard leaves a stale line that names what went missing.
	for guard := range byGuard {
		if _, exists := derived[guard]; !exists {
			vanished = append(vanished, guard)
		}
	}
	slices.Sort(unlisted)
	slices.Sort(vanished)
	if len(unlisted) > 0 {
		t.Errorf("guard(s) %v exist in the guard files and the inventory records nothing. Every guard needs a red proof or a stated allowance; regenerate with %s=1 and give the new ones patches or reasons.", unlisted, redproofUpdateInventoryEnv)
	}
	if len(vanished) > 0 {
		t.Errorf("the inventory still names %v, and no guard file declares them. A guard was deleted or renamed without refreshing the corpus; regenerate with %s=1 and check whether its patch went stale too.", vanished, redproofUpdateInventoryEnv)
	}

	// The corpus floors: patches must cover guards, not pad counts.
	if len(corpus) < redproofMinPatches {
		t.Errorf("%d patches, want ≥ %d", len(corpus), redproofMinPatches)
	}
	if distinct := redproofDistinctGuards(corpus); distinct < redproofMinDistinct {
		t.Errorf("%d distinct guarded tests, want ≥ %d — one guard proved many times is not coverage", distinct, redproofMinDistinct)
	}
	byID := map[string]redproofPatch{}
	for _, patch := range corpus {
		byID[patch.id] = patch
	}
	familyPatches := map[string]int{}
	familyGuards := map[string]int{}
	for _, entry := range inventory {
		if entry.patch == "" {
			continue
		}
		patch, found := byID[entry.patch]
		if !found {
			t.Errorf("inventory says guard %s is proved by patch %s, and no such patch is in %s — restore it or drop the line in the same change", entry.guard, entry.patch, redproofCorpusDir)
			continue
		}
		if !slices.Contains(strings.Split(patch.run, "|"), entry.guard) {
			t.Errorf("inventory says patch %s proves guard %s, but its Run pattern is %q — the line and the patch disagree", patch.id, entry.guard, patch.run)
		}
		familyPatches[entry.family]++
		familyGuards[entry.family]++
	}
	// A family with no patches fails by name; the floor is higher than one so
	// the failure arrives before the family is empty.
	for _, family := range redproofFamilies {
		if familyPatches[family] < redproofMinPerFamily {
			t.Errorf("the %s family carries %d proven guards, want ≥ %d: the sweep covered it, the corpus must too — a family with no patches is a family whose guards are hopes", family, familyPatches[family], redproofMinPerFamily)
		}
	}
	// Every patch's guards exist and are inventoried (an orphan patch proving
	// an uninventoried test would make the patch set unauditable).
	for _, patch := range corpus {
		for _, guard := range strings.Split(patch.run, "|") {
			entry, listed := byGuard[guard]
			if !listed {
				t.Errorf("patch %s proves guard %s and the inventory records nothing about it; regenerate with %s=1", patch.id, guard, redproofUpdateInventoryEnv)
				continue
			}
			if entry.patch != patch.id {
				t.Errorf("guard %s is proved by both inventory line %q and patch %s; one line per guard keeps the accounting honest", guard, entry.patch, patch.id)
			}
		}
	}
}

func redproofWriteInventory(t *testing.T, root string, derived map[string]string, corpus []redproofPatch) {
	t.Helper()
	patchFor := map[string]string{}
	for _, patch := range corpus {
		for _, guard := range strings.Split(patch.run, "|") {
			patchFor[guard] = patch.id
		}
	}
	// Carry allowances forward from the existing inventory, best-effort.
	previous := map[string]string{}
	if raw, err := os.ReadFile(filepath.Join(root, redproofInventoryFile)); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			parts := strings.Split(line, "\t")
			if len(parts) == 3 {
				if reason, ok := strings.CutPrefix(parts[2], "allow:"); ok {
					previous[parts[0]] = reason
				}
			}
		}
	}
	names := make([]string, 0, len(derived))
	for name := range derived {
		names = append(names, name)
	}
	slices.Sort(names)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Red-proof guard inventory — DERIVED, do not hand-edit.\n")
	fmt.Fprintf(&buf, "# Regenerate: GGG_REDPROOF_UPDATE_INVENTORY=1 go test ./internal/modkit -run TestRedProofGate\n")
	fmt.Fprintf(&buf, "# Derivation: every test matching the guard naming families\n")
	fmt.Fprintf(&buf, "# (TestAgents*|TestEvery*|TestNo*|TestPublished*|TestValidator*|TestThe*) in the\n")
	fmt.Fprintf(&buf, "# guard files — every *_selfhost_test.go under internal/{modkit,gggcli,web} plus the\n")
	fmt.Fprintf(&buf, "# swept non-selfhost guard files — cross-checked against the named contract guards.\n")
	fmt.Fprintf(&buf, "# One line per guard: name, family, and either the patch id that proves it red or\n")
	fmt.Fprintf(&buf, "# allow:<why it carries no red proof today>.\n")
	for _, name := range names {
		family := derived[name]
		if id, ok := patchFor[name]; ok {
			fmt.Fprintf(&buf, "%s\t%s\t%s\n", name, family, id)
			continue
		}
		reason, ok := previous[name]
		if !ok {
			reason = "no red proof yet: the sweep verdicts cover it, the corpus does not — add a patch"
		}
		fmt.Fprintf(&buf, "%s\t%s\tallow:%s\n", name, family, reason)
	}
	path := filepath.Join(root, redproofInventoryFile)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write inventory: %v", err)
	}
	t.Fatalf("inventory regenerated at %s (%d guards); review the diff and re-run without %s", path, len(names), redproofUpdateInventoryEnv)
}

func redproofDistinctGuards(corpus []redproofPatch) int {
	distinct := map[string]bool{}
	for _, patch := range corpus {
		for _, guard := range strings.Split(patch.run, "|") {
			distinct[guard] = true
		}
	}
	return len(distinct)
}

// ---------------------------------------------------------------- scratch ----

// redproofRepositoryRoot walks up to the module root and refuses anything that
// is not the publishing repository: this file is a self_host payload, so a
// tree it did not reach through install is a tree it was copied into.
func redproofRepositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
	raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	module, _ := strings.CutPrefix(strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0]), "module ")
	if !InstallsSelfHostPayloads(module, "github.com/gogogadget/gogogadget") {
		t.Fatalf("[inapplicable] module %s is not the publishing repository, so this self-host assertion would run against a tree it was never installed into", module)
	}
	return dir
}

// redproofMaterialiseScratch copies the tree into a scratch directory and
// gives it a git identity. The git layer is load-bearing, not decorative:
// TestEveryTrackedSourceFileHasAnOwner derives its population from
// `git ls-files`, and `git apply` is the patch semantics the corpus stores.
func redproofMaterialiseScratch(t *testing.T, root string) string {
	t.Helper()
	scratch, err := os.MkdirTemp("", "ggg-redproof-")
	if err != nil {
		t.Fatal(err)
	}
	copyStarted := time.Now()
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, name)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		base := filepath.Base(rel)
		top := !strings.Contains(rel, string(filepath.Separator))
		if top && slices.Contains(redproofScratchSkipTop, base) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() && slices.Contains(redproofScratchSkipNames, base) {
			return fs.SkipDir
		}
		target := filepath.Join(scratch, rel)
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, 0o755)
		case entry.Type()&fs.ModeSymlink != 0:
			link, linkErr := os.Readlink(name)
			if linkErr != nil {
				return linkErr
			}
			return os.Symlink(link, target)
		default:
			info, statErr := entry.Info()
			if statErr != nil {
				return statErr
			}
			return redproofCopyFile(name, target, info.Mode().Perm())
		}
	})
	if err != nil {
		t.Fatalf("copy tree to %s: %v", scratch, err)
	}
	for _, args := range [][]string{
		{"git", "init", "-q"},
		{"git", "config", "user.email", "red@proof.invalid"},
		{"git", "config", "user.name", "red-proof gate"},
		{"git", "add", "-A"},
		{"git", "commit", "-qm", "red-proof scratch baseline"},
	} {
		if out, err := redproofExec(scratch, args...); err != nil {
			t.Fatalf("scratch git %s: %v\n%s", args[0], err, out)
		}
	}
	t.Logf("scratch copy ready in %s", time.Since(copyStarted).Round(time.Millisecond))
	return scratch
}

func redproofCopyFile(source, target string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---------------------------------------------------------------- passes ----

// redproofCleanPass runs every paired guard against the unmutated copy and
// requires green. One run per distinct (package, pattern), so a guard proved
// by several patches is canaried once.
func redproofCleanPass(t *testing.T, scratch string, corpus []redproofPatch) {
	t.Helper()
	ran := map[string]bool{}
	for _, patch := range corpus {
		key := patch.pkg + "\x00" + patch.run
		if ran[key] {
			continue
		}
		ran[key] = true
		if out, err := redproofGoTest(scratch, patch.run, patch.pkg); err != nil {
			t.Errorf("CLEAN-TREE CANARY: guard(s) %s are red on the unmutated scratch copy, so no red proof of them can mean anything. Fix the guard or the tree first.\n%s",
				patch.run, redproofTail(out, 40))
		}
	}
}

// redproofMutationPass applies each patch, requires the paired guard to fail
// with its own words, and reverts. Every failure mode is named: stale,
// toothless, masquerade, residue.
func redproofMutationPass(t *testing.T, scratch string, corpus []redproofPatch) {
	t.Helper()
	for _, patch := range corpus {
		t.Run(patch.id, func(t *testing.T) {
			// The patch body lives outside the scratch: a .patch file inside
			// it would dirty the residue check that ends every proof.
			body, err := os.CreateTemp("", "redproof-"+patch.id+"-*.patch")
			if err != nil {
				t.Fatal(err)
			}
			bodyPath := body.Name()
			defer os.Remove(bodyPath)
			if _, err := body.Write(patch.body); err != nil {
				body.Close()
				t.Fatal(err)
			}
			body.Close()

			if out, err := redproofExec(scratch, "git", "apply", bodyPath); err != nil {
				t.Fatalf("STALE: patch %s no longer applies to %s.\n%s\nThe code the proof was pinned to changed — regenerate the patch against the current tree (plant the violation in a scratch copy, git diff, restore the Redproof-* headers) in the same change that moved the code. A stale proof is the gate working, not the gate failing.",
					patch.id, strings.Join(patch.files, ", "), redproofTail(out, 30))
			}
			// Whatever this subtest finds, the mutant comes out again before
			// the next proof runs: a failed proof must not spend the scratch
			// copy for everyone after it, and the residue check below stays
			// as the net that proves the revert took.
			defer func() {
				if out, err := redproofExec(scratch, "git", "apply", "-R", bodyPath); err != nil {
					t.Errorf("revert patch %s: %v\n%s — the scratch copy is spent; every later proof would run against its residue", patch.id, err, redproofTail(out, 20))
					return
				}
				if out, err := redproofExec(scratch, "git", "status", "--porcelain"); err != nil || out != "" {
					t.Errorf("RESIDUE: after reverting, patch %s left the scratch tree dirty (err=%v):\n%s — a later proof would run against a tree this one mutated", patch.id, err, redproofTail(out, 20))
				}
			}()
			out, err := redproofGoTest(scratch, patch.run, patch.pkg)
			if err == nil {
				t.Fatalf("TOOTHLESS: guard %s PASSES over the violation patch %s plants in %s (%s).\nExpected the guard's own failure text to contain %q; the mutant stayed green instead. Either the guard lost the check this proof was born red on, or the patch plants a violation the guard never claimed — read the guard, decide which, and fix accordingly.\nobserved (green) output:\n%s",
					patch.run, patch.id, strings.Join(patch.files, ", "), patch.summary, patch.expect, redproofTail(out, 30))
			}
			if patch.expect != "" && !strings.Contains(out, patch.expect) {
				t.Fatalf("ENVIRONMENTAL MASQUERADE: guard %s failed under patch %s, but for the wrong reason.\nexpected substring: %q\nobserved failure:\n%s\nA red proof satisfied by a digest mismatch or a build break certifies vigilance that is not there; the failure must be the guard's own. Fix the patch (heal what it invalidates, or plant the violation where the guard looks), not the expectation.",
					patch.run, patch.id, patch.expect, redproofTail(out, 40))
			}
		})
	}
}

// redproofGoTest runs one guard pattern in one package in the scratch copy.
func redproofGoTest(scratch, run, pkg string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-run="+run, pkg)
	cmd.Dir = scratch
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func redproofExec(dir string, args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func redproofTail(out string, lines int) string {
	all := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

// redproofSortGuards orders guard names for reports.
func redproofSortGuards(guards []string) []string {
	slices.SortFunc(guards, cmp.Compare[string])
	return guards
}
