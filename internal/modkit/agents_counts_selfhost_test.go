// Self-host assertions. Declared self_host by ggg/system/modkit: the
// repository that publishes the registry installs and runs it, and no
// derivative ever receives it.
//
// Spec: AGENTS.md lines 111-112 (the `catalog` line in "## The loop") and
// lines 96-110 (the `self_host` paragraph).
//
// A count in a document is the single thing most certain to go stale: the
// audit found "240 selected here" against a real 288, and the `self_host`
// inventory naming six of twenty-one payloads with a glob that missed two
// more. Both are derived here from the registry and the manifests, never from
// a list in a test.

package modkit

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The published and selected counts the loop block advertises. Published is
// the catalog; selected is the lock minus its tombstones, which are retained
// migration ledgers rather than selected modules.
func TestAgentsCatalogCountsMatchTheRegistryAndTheLock(t *testing.T) {
	loop := collapse(agentsSection(t, "The loop"))

	published := docCount(t, loop, "\\(([0-9]+) published")
	selected := docCount(t, loop, "published, ([0-9]+) selected here\\)")

	catalog, err := LoadCatalog(os.DirFS(filepath.Join("..", "..")))
	if err != nil {
		t.Fatalf("load published catalog: %v", err)
	}
	if published != len(catalog.Modules) {
		t.Errorf("AGENTS.md says %d published modules; the catalog publishes %d.\n"+
			"Update the `catalog` line in \"## The loop\".", published, len(catalog.Modules))
	}

	lock := loadRepoLock(t)
	live := 0
	for _, module := range lock.Modules {
		if module.Reason != TombstoneReason {
			live++
		}
	}
	if live == 0 {
		t.Fatal("the committed lock has no non-tombstone rows; the selected count cannot be derived")
	}
	if selected != live {
		t.Errorf("AGENTS.md says %d selected here; the lock records %d non-tombstone rows (of %d).\n"+
			"Update the `catalog` line in \"## The loop\".", selected, live, len(lock.Modules))
	}
}

// selfHostPayloads returns every self_host payload the published catalog
// declares, and the modules that own them.
func selfHostPayloads(t *testing.T) (targets []string, owners []string) {
	t.Helper()
	catalog, err := LoadCatalog(os.DirFS(filepath.Join("..", "..")))
	if err != nil {
		t.Fatalf("load published catalog: %v", err)
	}
	ownerSet := map[string]bool{}
	for _, module := range catalog.Modules {
		for _, file := range module.Files {
			if !file.SelfHost {
				continue
			}
			targets = append(targets, file.Target)
			ownerSet[module.ID] = true
		}
	}
	for owner := range ownerSet {
		owners = append(owners, owner)
	}
	sort.Strings(targets)
	sort.Strings(owners)
	return targets, owners
}

// The self_host paragraph, both ways: the count, the owning modules, and the
// enumeration. A payload the paragraph's glob and literals do not reach is a
// test a reader cannot find; a literal that names nothing is a file that
// moved.
func TestAgentsSelfHostInventoryMatchesTheManifests(t *testing.T) {
	paragraph := collapse(agentsSection(t, "Source vs generated — read this first"))
	region := region(t, paragraph, "So a new self-hosting test goes in", "anything portable stays")

	targets, owners := selfHostPayloads(t)

	if stated := docCount(t, region, "([0-9]+) today"); stated != len(targets) {
		t.Errorf("AGENTS.md says %d self_host payloads today; the manifests declare %d.\n"+
			"Update the figure in the self_host paragraph.", stated, len(targets))
	}

	var documentedOwners []string
	for _, span := range backticked(region) {
		if strings.HasPrefix(span, "ggg/") {
			documentedOwners = append(documentedOwners, span)
		}
	}
	sort.Strings(documentedOwners)
	if strings.Join(documentedOwners, " ") != strings.Join(owners, " ") {
		t.Errorf("AGENTS.md says the self_host payloads are owned by %v; the manifests say %v.\n"+
			"Update the owning-module list in the self_host paragraph.", documentedOwners, owners)
	}

	// The paragraph reaches a payload either through its `*_selfhost_test.go`
	// glob or by naming the file. Anything it reaches neither way is
	// undiscoverable from the document.
	glob := regexp.MustCompile(`_?selfhost_test\.go$`)
	var unreachable []string
	for _, target := range targets {
		base := filepath.Base(target)
		if glob.MatchString(base) || strings.Contains(region, "`"+base+"`") {
			continue
		}
		unreachable = append(unreachable, target)
	}
	if len(unreachable) > 0 {
		t.Errorf("%d self_host payload(s) are named by neither the glob nor a literal in the AGENTS.md self_host paragraph: %v.\n"+
			"Name each one, or rename the file so `*_selfhost_test.go` reaches it.", len(unreachable), unreachable)
	}

	// And the reverse: every file the paragraph names by hand must still be a
	// self_host payload.
	declared := map[string]bool{}
	for _, target := range targets {
		declared[filepath.Base(target)] = true
	}
	for _, span := range backticked(region) {
		if !strings.HasSuffix(span, "_test.go") || strings.Contains(span, "*") {
			continue
		}
		if !declared[filepath.Base(span)] {
			t.Errorf("AGENTS.md's self_host paragraph names %q and no manifest declares it self_host.\n"+
				"Remove it from the paragraph, or restore the declaration.", span)
		}
	}
}
