// Self-host assertions. Declared self_host by ggg/system/modkit: the
// repository that publishes the registry installs and runs it, and no
// derivative ever receives it.
//
// Spec: AGENTS.md lines 145-174, "## Repo map".
//
// Two claim shapes live here. The map's "ONLY" sentences are exclusivity
// claims about a vendor import — all five were TRUE when the audit measured
// them, and the value of the check is keeping them true against a second
// importer AND against the file moving out from under the sentence. The map's
// coverage is the other: the audit found twelve `internal/` packages with no
// bullet at all, including the CLI's whole presentation layer, and two
// near-collision pairs (`notify`/`notifications`, `db`/`database`) where
// documenting one and not the other sends an agent to the wrong package.

package modkit

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// exclusivityClaim is one "ONLY" sentence in the repo map. sentence is the
// literal the document must still carry — a claim silently reworded away is
// itself a defect — and owner is the path prefix that must hold every
// importer.
type exclusivityClaim struct {
	sentence string
	vendor   string
	owner    string
}

// The map's exclusivity claims. The documented side is the sentence; the
// truth side is a scan of every Go and templ file under internal/ and cmd/,
// so neither a new importer nor a moved owner can pass.
//
// The row SET is not the claim set: it is a mirror of it, and
// TestAgentsRepoMapStatesNoUnguardedExclusivityClaim reads the claims back out
// of the document and refuses a sentence with no row here. Without that, a
// flatly false "ONLY importer" bullet added to the manual an agent reads first
// is green — measured, with a fabricated `internal/llm` pgx claim that 92
// files contradict.
var exclusivityClaims = []exclusivityClaim{
	{
		sentence: "`identity/clerk` is the only clerk-sdk-go + svix importer",
		vendor:   "github.com/clerk/clerk-sdk-go",
		owner:    "internal/identity/clerk/",
	},
	{
		sentence: "`identity/clerk` is the only clerk-sdk-go + svix importer",
		vendor:   "github.com/svix/svix-webhooks",
		owner:    "internal/identity/clerk/",
	},
	{
		// Widened from `r2.go` to the package when the MinIO
		// protocol-container run landed: minio_test.go reaches for the SDK
		// directly to create its bucket, which no seam method exposes. The
		// invariant that matters is unchanged and is the one the manifest
		// declares — the aws dependency enters and leaves go.mod with
		// ggg/system/storage-s3 — so the allow-list is that adapter's
		// package, not one file inside it.
		sentence: "`storage/s3` is the ONLY package with an aws-sdk import in the tree",
		vendor:   "github.com/aws/aws-sdk-go",
		owner:    "internal/storage/s3/",
	},
	{
		sentence: "`observability/sentryadapter/sentry.go` is the ONLY sentry-go import in the tree",
		vendor:   "github.com/getsentry/sentry-go",
		owner:    "internal/observability/sentryadapter/sentry.go",
	},
}

// sourceFiles returns every Go and templ file under internal/ and cmd/,
// keyed by path, read once: four claims over two thousand files is four
// passes if each row reads the tree itself.
func sourceFiles(t *testing.T) map[string][]byte {
	t.Helper()
	root := filepath.Join("..", "..")
	files := map[string][]byte{}
	for _, tree := range []string{"internal", "cmd"} {
		err := fs.WalkDir(os.DirFS(root), tree, func(p string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			if !strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, ".templ") {
				return nil
			}
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
			if err != nil {
				return err
			}
			files[p] = raw
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree, err)
		}
	}
	if len(files) == 0 {
		t.Fatal("the source scan found no files; it is looking in the wrong place")
	}
	return files
}

// Each "ONLY" sentence is a tree-wide import assertion, both ways: no
// importer outside the named owner, and the named owner must still import it.
// The second half is what catches the failure mode the audit actually hit —
// every vendor implementation moved into an adapter subpackage while the map
// went on naming the seam.
func TestAgentsRepoMapExclusivityClaimsHold(t *testing.T) {
	repoMap := collapse(agentsSection(t, "Repo map"))
	files := sourceFiles(t)

	for _, claim := range exclusivityClaims {
		if !strings.Contains(repoMap, collapse(claim.sentence)) {
			t.Errorf("AGENTS.md's repo map no longer says %q.\n"+
				"This check enforces that sentence; restore it, or drop the claim and this row together — an exclusivity rule nobody states is a rule nobody keeps.",
				claim.sentence)
		}

		// An IMPORT LINE, not any mention: a quoted module path at the start
		// of a line, optionally behind an alias. Matching a bare substring
		// would count this file's own claim table as an importer, and would
		// count a doc comment quoting the path.
		importLine := regexp.MustCompile(`(?m)^\s*(?:_\s+|\.\s+|[A-Za-z_]\w*\s+)?"` + regexp.QuoteMeta(claim.vendor))
		var importers []string
		for file, raw := range files {
			if importLine.Match(raw) {
				importers = append(importers, file)
			}
		}
		sort.Strings(importers)

		if len(importers) == 0 {
			t.Errorf("AGENTS.md says %s owns every %s import, and nothing in internal/ or cmd/ imports it at all.\n"+
				"Either the dependency is gone — delete the sentence from the repo map — or the scan is wrong.",
				claim.owner, claim.vendor)
			continue
		}

		var strays []string
		ownedByTheClaim := false
		for _, file := range importers {
			if file == claim.owner || strings.HasPrefix(file, claim.owner) {
				ownedByTheClaim = true
				continue
			}
			strays = append(strays, file)
		}
		if len(strays) > 0 {
			t.Errorf("AGENTS.md says %s is the ONLY %s importer; these import it too: %v.\n"+
				"Move the import behind the seam, or correct the repo-map sentence to name the new allow-list.",
				claim.owner, claim.vendor, strays)
		}
		if !ownedByTheClaim {
			t.Errorf("AGENTS.md points at %s for %s, and nothing there imports it — the importers are %v.\n"+
				"The implementation moved; update the repo-map sentence to the path that holds it now.",
				claim.owner, claim.vendor, importers)
		}
	}
}

// repoMapExclusivitySentence is the shape of an "ONLY importer" claim in the
// repo map: a backticked owner, `is the only`, a vendor, and the word import.
// Case-insensitive, because the document writes both `only` and `ONLY`.
var repoMapExclusivitySentence = regexp.MustCompile("(?i)`([^`]+)` is the only ([^`]{1,60}?) (?:importer|import)\\b")

// Every exclusivity claim the repo map STATES must have a row above.
//
// exclusivityClaims is a hand-written table, and the half that was missing is
// the one that keeps a hand-written table honest: nothing counted the
// document's own sentences against it. The table's comment said "five
// exclusivity claims" over four rows one release after it was written, and a
// bullet claiming `internal/llm` is the only pgx importer — zero imports
// there, ninety-two elsewhere — passed every repo-map guard.
//
// Both directions, so neither list can drift: a sentence with no row is an
// unguarded claim, and a row whose sentence this reader cannot find is a row
// that has stopped mirroring the document (the literal-presence half is
// asserted per row by TestAgentsRepoMapExclusivityClaimsHold).
func TestAgentsRepoMapStatesNoUnguardedExclusivityClaim(t *testing.T) {
	repoMap := collapse(agentsSection(t, "Repo map"))
	stated := repoMapExclusivitySentence.FindAllString(repoMap, -1)
	// The floor. A regexp that stopped matching would otherwise report that
	// the document makes no claims at all, which is the vacuity this guard is.
	if len(stated) < 3 {
		t.Fatalf("only %d exclusivity sentence(s) were read out of the repo map (%q); the reader has collapsed, not the document",
			len(stated), stated)
	}

	rowed := map[string]bool{}
	for _, sentence := range stated {
		covered := false
		for _, claim := range exclusivityClaims {
			// The row carries the sentence as the document writes it, which
			// may run past the word this reader stops at ("… in the tree"),
			// so containment either way is the match.
			row := collapse(claim.sentence)
			if strings.Contains(row, sentence) || strings.Contains(sentence, row) {
				covered = true
				rowed[row] = true
			}
		}
		if !covered {
			t.Errorf("AGENTS.md's repo map claims %q and exclusivityClaims has no row for it, so nothing checks it.\n"+
				"Add a row naming the vendor module path and the owning prefix, or delete the sentence — an exclusivity claim the manual states and no test measures is worse than no claim at all.",
				sentence)
		}
	}
	for _, claim := range exclusivityClaims {
		row := collapse(claim.sentence)
		if !rowed[row] {
			t.Errorf("exclusivityClaims row %q is not one of the exclusivity sentences this reader finds in the repo map: %q.\n"+
				"Either the sentence was reworded out of the shape the reader knows — widen repoMapExclusivitySentence — or the row no longer mirrors the document.",
				claim.sentence, stated)
		}
	}
}

// nodeArtefacts are the shapes that mean "a Node project lives here".
var nodeArtefacts = regexp.MustCompile(`(^|/)(package\.json|package-lock\.json|node_modules)$|\.spec\.ts$|\.config\.ts$`)

// "node lives ONLY here" — asserted over the whole tracked tree with
// .gitignore applied, in both directions: nothing outside e2e/, and e2e/ must
// still hold some, or the claim has quietly become vacuous.
func TestAgentsRepoMapConfinesNodeToTheE2ETree(t *testing.T) {
	repoMap := collapse(agentsSection(t, "Repo map"))
	const sentence = "node lives ONLY here"
	if !strings.Contains(repoMap, sentence) {
		t.Errorf("AGENTS.md's repo map no longer says %q; restore it or drop this check", sentence)
	}

	var inside, outside []string
	for _, p := range trackedRepoFiles(t) {
		if !nodeArtefacts.MatchString(p) {
			continue
		}
		if strings.HasPrefix(p, "e2e/") {
			inside = append(inside, p)
			continue
		}
		outside = append(outside, p)
	}
	if len(outside) > 0 {
		t.Errorf("AGENTS.md says node lives ONLY under e2e/; these Node artefacts live elsewhere: %v.\n"+
			"Move them under e2e/, or correct the repo-map sentence.", outside)
	}
	if len(inside) == 0 {
		t.Error("no Node artefact exists under e2e/, so \"node lives ONLY here\" now points at nothing; " +
			"correct the repo-map sentence or restore the Playwright project")
	}
}

// internalPackages lists every directory directly under internal/.
func internalPackages(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "..", "internal"))
	if err != nil {
		t.Fatalf("read internal/: %v", err)
	}
	var packages []string
	for _, entry := range entries {
		if entry.IsDir() {
			packages = append(packages, entry.Name())
		}
	}
	if len(packages) == 0 {
		t.Fatal("internal/ holds no packages; the scan is looking in the wrong place")
	}
	sort.Strings(packages)
	return packages
}

// The map must cover every package and name no package that is gone. The
// forward direction is the one that bit: twelve packages had no bullet, so an
// agent looking for the rate limiter, the cache, the CLI layer or the search
// index found nothing and guessed. The reverse keeps a deleted package from
// lingering as a bullet that sends the next reader nowhere.
func TestAgentsRepoMapNamesEveryInternalPackage(t *testing.T) {
	repoMap := agentsSection(t, "Repo map")
	named := map[string]bool{}
	for _, match := range regexp.MustCompile(`internal/([a-z0-9_]+)`).FindAllStringSubmatch(repoMap, -1) {
		named[match[1]] = true
	}

	packages := internalPackages(t)
	present := map[string]bool{}
	var undocumented []string
	for _, pkg := range packages {
		present[pkg] = true
		if !named[pkg] {
			undocumented = append(undocumented, "internal/"+pkg)
		}
	}
	if len(undocumented) > 0 {
		t.Errorf("%d package(s) under internal/ appear nowhere in the AGENTS.md repo map: %v.\n"+
			"Add a bullet for each — an agent that greps the map and misses a package edits the nearest similarly named one instead.",
			len(undocumented), undocumented)
	}

	var phantom []string
	for pkg := range named {
		if !present[pkg] {
			phantom = append(phantom, "internal/"+pkg)
		}
	}
	if len(phantom) > 0 {
		sort.Strings(phantom)
		t.Errorf("the AGENTS.md repo map names %v and no such directory exists.\n"+
			"Remove the bullet, or restore the package.", phantom)
	}
}

// The two near-collision pairs earn their own assertion because the failure
// is silent: both members exist, the map documents both, and the ONLY thing
// that stops an agent editing the wrong one is the sentence that names the
// twin. Asserted per BULLET, not per document: a reader lands on one line and
// reads that line, so a cross-reference forty lines away does not help them.
func TestAgentsRepoMapDisambiguatesTheNearCollisions(t *testing.T) {
	lines := strings.Split(agentsSection(t, "Repo map"), "\n")
	for _, pair := range [][2]string{{"notify", "notifications"}, {"db", "database"}} {
		for _, side := range pair {
			if _, err := os.Stat(filepath.Join("..", "..", "internal", side)); err != nil {
				t.Fatalf("internal/%s no longer exists, so this disambiguation claim is stale: %v", side, err)
			}
		}
		for i, side := range pair {
			twin := pair[1-i]
			// Word-bounded: `internal/notify` is a prefix of
			// `internal/notifications`, which is the whole hazard.
			names := func(line, pkg string) bool {
				return regexp.MustCompile(`internal/` + pkg + `(?:[^a-z0-9_]|$)`).MatchString(line)
			}
			for _, line := range lines {
				if names(line, side) && !names(line, twin) {
					t.Errorf("this repo-map bullet names internal/%s and never mentions internal/%s:\n  %s\n"+
						"Both exist. Name the twin on the same line, or an agent that greps the map edits whichever one it found.",
						side, twin, strings.TrimSpace(line))
				}
			}
		}
	}
}

// repoRoots are the top-level directories a repo-map code span may name. A
// span that starts with one of them is a path claim and must resolve; a span
// that does not is a module id, a URL route or a bare package name, none of
// which this check can adjudicate.
var repoRoots = []string{"internal/", "cmd/", "static/", "content/", "e2e/", "registry/", "scripts/", "templates/", ".github/"}

// Every repository path the repo map names has to exist. Cheap, and it is the
// class of check that would have reported the map naming `internal/storage`
// for S3 code that had moved to `internal/storage/s3`.
func TestAgentsRepoMapPathsExist(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, span := range backticked(agentsSection(t, "Repo map")) {
		anchored := false
		for _, prefix := range repoRoots {
			if strings.HasPrefix(span, prefix) {
				anchored = true
				break
			}
		}
		if !anchored || strings.ContainsAny(span, " ({*") {
			continue
		}
		candidate := strings.TrimSuffix(span, "/")
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); err != nil {
			t.Errorf("the AGENTS.md repo map names %q and no such path exists.\n"+
				"Correct the bullet: %v", span, err)
		}
	}
}
