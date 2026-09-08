// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. It asserts about THIS repository — the four
// ownership planes AGENTS.md publishes against the predicates and the tree
// they describe.
//
// Spec: AGENTS.md lines 8-42, "## Source vs generated — read this first".
//
// Every assertion here is BIDIRECTIONAL, and that is the whole reason the file
// exists. The audit that produced it found `compose.yaml` documented as
// ordinary editable source while being generated, and a one-way check — "every
// path the document names is generated" — would have passed on that document.
// So each plane is compared as a SET: the document's members must equal the
// tree's, and a failure names the members on each side.

package modkit

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// agentsManual returns AGENTS.md as one string. The document is a payload of
// ggg/system/project-docs and sits at the repository root.
func agentsManual(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	return string(raw)
}

// agentsSection returns the body under one `## ` heading, up to the next one.
// Tests name the heading rather than a line number so a section that moves
// still resolves.
func agentsSection(t *testing.T, heading string) string {
	t.Helper()
	doc := agentsManual(t)
	marker := "## " + heading + "\n"
	start := strings.Index(doc, marker)
	if start < 0 {
		t.Fatalf("AGENTS.md has no %q section; this check reads it, so either restore the heading or delete the check", "## "+heading)
	}
	body := doc[start+len(marker):]
	if end := strings.Index(body, "\n## "); end >= 0 {
		body = body[:end]
	}
	return body
}

// collapse joins a hard-wrapped markdown paragraph into one line so a regexp
// may span what the document breaks across lines.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// backticked returns every `code span` in order.
func backticked(s string) []string {
	var out []string
	for _, match := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(s, -1) {
		out = append(out, match[1])
	}
	return out
}

// expandBraces expands one `a/{b,c}/d` form into the paths it names. The
// document uses the brace form to keep a closed set readable; the predicate it
// describes lists the members literally, so the check has to agree with the
// members and not with the abbreviation.
func expandBraces(form string) []string {
	open := strings.Index(form, "{")
	closeAt := strings.Index(form, "}")
	if open < 0 || closeAt < open {
		return []string{form}
	}
	var out []string
	for _, alt := range strings.Split(form[open+1:closeAt], ",") {
		out = append(out, expandBraces(form[:open]+strings.TrimSpace(alt)+form[closeAt+1:])...)
	}
	return out
}

// pathForm is one path shape the document names, and the matcher it implies.
type pathForm struct {
	text  string
	match func(string) bool
}

// parsePathForms turns the document's code spans into matchers: a trailing `/`
// is a prefix, a `*` globs one path element, anything else is exact.
func parsePathForms(spans []string) []pathForm {
	var forms []pathForm
	for _, span := range spans {
		for _, expanded := range expandBraces(span) {
			literal := expanded
			switch {
			case strings.HasSuffix(literal, "/"):
				forms = append(forms, pathForm{text: literal, match: func(p string) bool {
					return strings.HasPrefix(p, literal)
				}})
			case strings.Contains(literal, "*"):
				forms = append(forms, pathForm{text: literal, match: func(p string) bool {
					ok, _ := path.Match(literal, path.Base(p))
					return ok
				}})
			default:
				forms = append(forms, pathForm{text: literal, match: func(p string) bool {
					return p == literal
				}})
			}
		}
	}
	return forms
}

// trackedRepoFiles lists every file git would carry, including files written
// but not yet added, with .gitignore applied. The same enumeration
// TestEveryTrackedSourceFileHasAnOwner uses, so the planes are compared over
// one inventory.
func trackedRepoFiles(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	seen := map[string]bool{}
	var paths []string
	for _, p := range strings.Fields(string(out)) {
		if seen[p] {
			continue
		}
		seen[p] = true
		// A deleted-but-still-cached path is not a file to own.
		if _, err := os.Lstat(filepath.Join("..", "..", filepath.FromSlash(p))); err != nil {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// docCount reads one number out of the document, so a stale figure fails here
// rather than misleading a reader.
func docCount(t *testing.T, text, pattern string) int {
	t.Helper()
	match := regexp.MustCompile(pattern).FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("AGENTS.md no longer carries the figure this check reads (pattern %q); restore the sentence or delete the check", pattern)
	}
	var n int
	if _, err := fmt.Sscanf(match[1], "%d", &n); err != nil {
		t.Fatalf("figure %q is not a number: %v", match[1], err)
	}
	return n
}

// region returns the text between two literal anchors, both of which must be
// present: a reworded sentence must be noticed, not silently skipped.
func region(t *testing.T, text, after, before string) string {
	t.Helper()
	start := strings.Index(text, after)
	if start < 0 {
		t.Fatalf("AGENTS.md plane 1 no longer contains the anchor %q, so this check cannot find the list it compares; re-anchor the check or restore the wording", after)
	}
	rest := text[start+len(after):]
	end := strings.Index(rest, before)
	if end < 0 {
		t.Fatalf("AGENTS.md plane 1 no longer contains the anchor %q after %q", before, after)
	}
	return rest[:end]
}

// Plane 1. The generated set is the one the audit found wrong, and wrong in
// the dangerous direction: two generated files the document did not name, so
// an agent read them as ordinary editable source. Both directions are asserted
// against the predicate AND against the tree — a form the document names that
// nothing satisfies is as much a defect as a generated file it omits.
func TestAgentsGeneratedPlaneEqualsTheGeneratedPredicate(t *testing.T) {
	plane := collapse(agentsSection(t, "Source vs generated — read this first"))

	total := docCount(t, plane, "`modkit\\.IsGeneratedOutputPath`, ([0-9]+) paths on")
	registryOwned := docCount(t, plane, "([0-9]+) of them satisfy `modkit\\.IsRegistryOwnedOutputPath`")
	external := docCount(t, plane, "The other ([0-9]+) are outputs")
	if total != registryOwned+external {
		t.Fatalf("AGENTS.md plane 1 does not add up: %d paths on disk, %d registry-owned + %d external-tool = %d",
			total, registryOwned, external, registryOwned+external)
	}

	registryForms := parsePathForms(backticked(region(t, plane, "and sweeps:", "The other")))
	externalForms := parsePathForms(backticked(region(t, plane, "another tool writes them:", "A hand edit")))
	if len(registryForms) == 0 || len(externalForms) == 0 {
		t.Fatal("AGENTS.md plane 1 named no path forms; the lists this check compares are gone")
	}

	var haveGenerated, haveRegistryOwned, haveExternal []string
	for _, p := range trackedRepoFiles(t) {
		if !IsGeneratedOutputPath(p) {
			continue
		}
		haveGenerated = append(haveGenerated, p)
		if IsRegistryOwnedOutputPath(p) {
			haveRegistryOwned = append(haveRegistryOwned, p)
		} else {
			haveExternal = append(haveExternal, p)
		}
	}

	if len(haveGenerated) != total {
		t.Errorf("AGENTS.md says %d generated paths on disk; modkit.IsGeneratedOutputPath answers yes for %d.\n"+
			"Update the figure in AGENTS.md plane 1.", total, len(haveGenerated))
	}
	if len(haveRegistryOwned) != registryOwned {
		t.Errorf("AGENTS.md says %d of them are registry-owned; modkit.IsRegistryOwnedOutputPath answers yes for %d.\n"+
			"Update the figure in AGENTS.md plane 1.", registryOwned, len(haveRegistryOwned))
	}
	if len(haveExternal) != external {
		t.Errorf("AGENTS.md says the other %d are external-tool outputs; the tree has %d.\n"+
			"Update the figure in AGENTS.md plane 1.", external, len(haveExternal))
	}

	compareForms(t, "registry-owned", registryForms, haveRegistryOwned, haveExternal,
		"the list after \"and sweeps:\" in AGENTS.md plane 1")
	compareForms(t, "external-tool", externalForms, haveExternal, haveRegistryOwned,
		"the list after \"another tool writes them:\" in AGENTS.md plane 1")
}

// compareForms holds one documented list against the paths it must cover, in
// both directions, and refuses a form that reaches into the other side's set.
func compareForms(t *testing.T, side string, forms []pathForm, want, other []string, where string) {
	t.Helper()

	covered := map[string]bool{}
	for _, form := range forms {
		matched := 0
		for _, p := range want {
			if form.match(p) {
				covered[p] = true
				matched++
			}
		}
		if matched == 0 {
			t.Errorf("AGENTS.md names %q in the %s list, and no %s path on disk matches it.\n"+
				"Remove the form from %s, or restore the file it named.", form.text, side, side, where)
		}
		var crossed []string
		for _, p := range other {
			if form.match(p) {
				crossed = append(crossed, p)
			}
		}
		if len(crossed) > 0 {
			t.Errorf("AGENTS.md lists %q as %s, but it also matches paths on the other side of the plane: %v.\n"+
				"Narrow the form in %s.", form.text, side, crossed, where)
		}
	}

	var missing []string
	for _, p := range want {
		if !covered[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d %s path(s) on disk are named by no form in AGENTS.md: %v.\n"+
			"Add each one to %s — an agent reading the document concludes an unlisted generated file is ordinary editable source and hand-edits it.",
			len(missing), side, missing, where)
	}
}

// planeInventory is the tree sorted into the four planes AGENTS.md publishes,
// derived from what the tree, the lock and the manifests DO rather than from
// any list. A new project-owned root file or a new format-owned catalog file
// appears here whether or not anyone remembered the document.
type planeInventory struct {
	generated     []string // plane 1
	project       []string // plane 2
	catalogFormat []string // plane 3, the files the registry format owns
	moduleOwned   []string // plane 4
}

// derivePlanes classifies every tracked file in the sweep's own precedence
// order. Generation wins first (a generated path can never be authored),
// then a declaring manifest, then the registry format, then the catalog
// content nested roots declare; anything left is the project's.
func derivePlanes(t *testing.T) planeInventory {
	t.Helper()
	declared := map[string]bool{}
	for _, module := range loadRepoLock(t).Modules {
		for _, file := range module.Files {
			declared[file.Path] = true
		}
		for _, migration := range module.Migrations {
			declared[migration.Path] = true
		}
	}
	// The lock covers the SELECTED graph only. A published-but-unselected
	// module's payloads sit in the tree with an owner, so the manifests are
	// the wider authority and both are consulted — reading only the lock
	// reported sixteen adapter sources as project-owned.
	catalog, err := LoadCatalog(os.DirFS(filepath.Join("..", "..")))
	if err != nil {
		t.Fatalf("load published catalog: %v", err)
	}
	for _, module := range catalog.Modules {
		for _, file := range module.Files {
			declared[file.Target] = true
		}
		for _, migration := range module.Migrations {
			declared[migration.Source] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("neither the lock nor the catalog declares a file; the planes cannot be derived")
	}

	var inventory planeInventory
	for _, p := range trackedRepoFiles(t) {
		switch {
		case IsGeneratedOutputPath(p):
			inventory.generated = append(inventory.generated, p)
		case declared[p]:
			inventory.moduleOwned = append(inventory.moduleOwned, p)
		case isRegistryFormatOwned(p):
			inventory.catalogFormat = append(inventory.catalogFormat, p)
		case strings.HasPrefix(p, "registry/"):
			// Catalog content a nested root's own manifests declare;
			// ValidateRegistryTreeOwnership gates it and the document does not
			// enumerate it.
		case strings.HasPrefix(p, ".superpowers/"):
			// Orchestration state, gitignored in a derivative and never
			// distributed.
		default:
			inventory.project = append(inventory.project, p)
		}
	}
	return inventory
}

// isRegistryFormatOwned reports whether one repository-relative path is a file
// the registry FORMAT owns at this root.
func isRegistryFormatOwned(p string) bool {
	_, ok := registryFormatOwnedPaths[p]
	return ok
}

// loadRepoLock parses the committed lock.
func loadRepoLock(t *testing.T) Lock {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "gogogadget.lock.json"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	lock, err := ParseLock(raw)
	if err != nil {
		t.Fatalf("parse lock: %v", err)
	}
	return lock
}

// Plane 2. The document's project plane must equal the files that end up
// unowned once generation, the catalog and every manifest have had their turn.
// A new tool-written root file that nobody documents lands here — and a file
// the document still calls project-owned after a module adopted it fails from
// the other side, which is how `.gitignore` was caught.
func TestAgentsProjectPlaneEqualsTheUnownedResidue(t *testing.T) {
	plane := collapse(agentsSection(t, "Source vs generated — read this first"))
	listed := region(t, plane, "**2. Project — the tool's and yours, never a module's.**", "(`projectOwned`")
	documented := backticked(listed)
	if len(documented) == 0 {
		t.Fatal("AGENTS.md plane 2 names no paths")
	}
	compareSets(t, "project plane", documented, derivePlanes(t).project,
		"AGENTS.md plane 2 (\"Project — the tool's and yours\")")
}

// Plane 3. The catalog plane is what the registry FORMAT owns at this root —
// the paths registryFormatOwnedPaths names that no manifest declares. Every
// other file under `registry/` is catalog content some manifest declares,
// which ValidateRegistryTreeOwnership already gates.
func TestAgentsCatalogPlaneEqualsTheFormatOwnedPaths(t *testing.T) {
	plane := collapse(agentsSection(t, "Source vs generated — read this first"))
	listed := region(t, plane, "`modkit.ValidateRegistryTreeOwnership`:", "`ggg registry build` writes")
	documented := backticked(listed)
	if len(documented) == 0 {
		t.Fatal("AGENTS.md plane 3 names no paths")
	}
	compareSets(t, "catalog plane", documented, derivePlanes(t).catalogFormat,
		"AGENTS.md plane 3 (\"Catalog — the registry format's own files\")")
}

// compareSets asserts set equality between a documented list of exact paths
// and the derived truth, naming the members on each side.
func compareSets(t *testing.T, subject string, documented, derived []string, where string) {
	t.Helper()
	forms := parsePathForms(documented)

	covered := map[string]bool{}
	for _, form := range forms {
		matched := false
		for _, p := range derived {
			if form.match(p) {
				covered[p] = true
				matched = true
			}
		}
		if !matched {
			t.Errorf("%s names %q, and nothing in the %s answers to it.\nRemove it from %s.",
				where, form.text, subject, where)
		}
	}
	var missing []string
	for _, p := range derived {
		if !covered[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d path(s) belong to the %s and %s names none of them: %v.\nAdd each one to %s.",
			len(missing), subject, where, missing, where)
	}
}

// Plane 4. The document's claim about ordinary editable source is not a list,
// it is a guard: every tracked file outside planes 1-3 is some manifest's
// `files` entry. The check the document names must therefore exist, and it
// must be the one that enumerates the tree rather than a list.
func TestAgentsSourcePlaneNamesItsGuard(t *testing.T) {
	plane := agentsSection(t, "Source vs generated — read this first")
	const guard = "TestEveryTrackedSourceFileHasAnOwner"
	if !strings.Contains(plane, guard) {
		t.Errorf("AGENTS.md plane 4 must name %s, the test that refuses an orphan; without the name a reader cannot find the enforcement", guard)
	}
	raw, err := os.ReadFile(filepath.Join("ownership_selfhost_test.go"))
	if err != nil {
		t.Fatalf("read ownership_selfhost_test.go: %v", err)
	}
	if !strings.Contains(string(raw), "func "+guard+"(") {
		t.Errorf("AGENTS.md plane 4 names %s but internal/modkit/ownership_selfhost_test.go declares no such test; the plane is documented and unenforced", guard)
	}
}
