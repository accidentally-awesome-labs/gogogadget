package modkit

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Which of IsRegistryOwnedOutputPath's explicit names the pipeline renders
// UNCONDITIONALLY, derived rather than asserted from a list.
//
// This exists to finish an argument. content/docs/modules.md#stale-sweep
// reasons that the refusal over authored bytes at a registry-owned name is
// narrower than it reads, because the gate only ever sees a name the graph
// does NOT render — and it discharged that for three names (`.env.example`,
// `compose.yaml`, `compose.test.yaml`) by naming their emitters as
// unconditional. The other explicit names were left unexamined, so the
// reassurance was incomplete: any one of them being conditional is a name the
// refusal CAN reach after an upgrade.
//
// The split is computed, not listed. GenerateAll runs twice — once over this
// repository's committed lock, once over the smallest fixture graph that
// generates at all — and the conditional names are exactly the difference.
// A hand-written list would be one more copy to fall behind, which is the
// defect class this change belongs to.
//
// `_registry_gen.` names are excluded from the comparison on purpose. That
// infix is the wide class and most of it IS conditional (routes, locales, the
// OpenAPI document, jobs, seeds), but no operator hand-authors a file with
// `_registry_gen.` in its name, so the refusal's blast radius there is
// theoretical. The explicitly listed names are the ones that collide with
// plausible hand-written paths, and they are what the paragraph is about.
func TestOnlyThreeExplicitRegistryOwnedNamesAreConditionallyRendered(t *testing.T) {
	root := testRepoRoot(t)

	full := explicitRenderedNames(t, committedGraphOutputs(t, root))
	fixtureLock, fixtureGraph := genFixtureLock(t)
	minimal := explicitRenderedNames(t, generatedOutputs(t, fixtureLock, fixtureGraph))

	conditional := make([]string, 0, 4)
	for _, name := range full {
		if !contains(minimal, name) {
			conditional = append(conditional, name)
		}
	}
	sort.Strings(conditional)

	// Each of the three returns (nil, nil) when its declaration set is empty:
	// emitPersonasRegistry on no declared persona, emitScenarioRegistry on no
	// declared scenario, emitVisualSurfaces on neither a scenario nor a
	// visual page. Every other explicitly listed name comes from an emitter
	// that returns a file even when that file lists nothing.
	want := []string{
		"e2e/generated/personas.ts",
		"e2e/generated/surfaces.ts",
		"internal/web/templates/scenarios_gen.go",
	}
	if strings.Join(conditional, " ") != strings.Join(want, " ") {
		t.Fatalf("conditional explicit outputs = %v, want %v\n"+
			"the stale-sweep scoping paragraph in content/docs/modules.md enumerates this exact set, "+
			"so a change here is a change there", conditional, want)
	}

	// The paragraph has to name them, or the argument is incomplete again in
	// the other direction: a reader is told the refusal is narrow and cannot
	// find out which names it can still reach.
	section := staleSweepSection(t, string(readRepoFile(t, root, "content/docs/modules.md")))
	for _, name := range conditional {
		if !strings.Contains(section, "`"+name+"`") {
			t.Fatalf("the stale-sweep section does not name the conditional output %s", name)
		}
	}
	// And it must not list an unconditional one as conditional, which would
	// be the same defect wearing the opposite sign.
	for _, name := range minimal {
		if strings.Contains(section, "| `"+name+"` |") {
			t.Fatalf("the stale-sweep section lists %s as conditional; it is rendered on every graph", name)
		}
	}
}

// staleSweepSection is the `### The stale sweep` block: from its heading to
// the next heading at any level. Scoping the search is what stops a mention
// elsewhere on the page from satisfying the assertion.
func staleSweepSection(t *testing.T, docs string) string {
	t.Helper()
	start := strings.Index(docs, "### The stale sweep")
	if start < 0 {
		t.Fatal("content/docs/modules.md has no `### The stale sweep` heading")
	}
	body := docs[start+1:]
	end := len(body)
	for _, heading := range []string{"\n## ", "\n### "} {
		if at := strings.Index(body, heading); at >= 0 && at < end {
			end = at
		}
	}
	return body[:end]
}

// committedGraphOutputs generates over this repository's own installed graph,
// read out of the lock. It is the only graph in the tree that declares
// personas, scenarios and visual pages, which is what makes it the upper
// bound of the comparison.
func committedGraphOutputs(t *testing.T, root string) []GeneratedFile {
	t.Helper()
	lock, err := ParseLock(readRepoFile(t, root, LockFileName))
	if err != nil {
		t.Fatalf("parsing the committed lock: %v", err)
	}
	graph := make([]Manifest, 0, len(lock.Modules))
	for _, module := range lock.Modules {
		graph = append(graph, module.Manifest)
	}
	return generatedOutputs(t, lock, graph)
}

func generatedOutputs(t *testing.T, lock Lock, graph []Manifest) []GeneratedFile {
	t.Helper()
	files, err := GenerateAll(context.Background(), "example.com/scope", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	return files
}

// explicitRenderedNames is the rendered registry-owned paths that are on the
// explicit switch rather than in the `_registry_gen.` class.
func explicitRenderedNames(t *testing.T, files []GeneratedFile) []string {
	t.Helper()
	names := make([]string, 0, 16)
	for _, file := range files {
		if !IsRegistryOwnedOutputPath(file.Path) || strings.Contains(file.Path, "_registry_gen.") {
			continue
		}
		names = append(names, file.Path)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("no explicitly named registry-owned output was rendered at all")
	}
	return names
}

func testRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..")
}

func readRepoFile(t *testing.T, root, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
