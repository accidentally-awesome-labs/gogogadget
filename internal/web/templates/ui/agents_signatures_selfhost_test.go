// Self-host assertions. This file is declared self_host by ggg/element/ui-core:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. A derivative installs whichever components it
// selected, so its renderer count is its own — only the publishing repository
// can hold AGENTS.md's figures to the catalogue.
//
// Spec: AGENTS.md line 230, the "One options struct per UI component" bullet
// ("the 174 documented signatures … the one exported renderer of 175 with no
// `Reference` entry is `CSRFField`").
//
// Three numbers were in circulation for this one claim — 172, 174 and 175 —
// because each counted a different thing and two of them were produced by
// brace-naive scans. The document now states two of them and names the
// difference; this check makes both mechanical and forces the allow-list to
// be justified if it ever grows.

package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// optionsBullet returns the collapsed AGENTS.md bullet this check reads.
func optionsBullet(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	const prefix = "- **One options struct per UI component**"
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("AGENTS.md carries no bullet opening %q; this check reads it", prefix)
	return ""
}

// figure reads one number out of the bullet.
func figure(t *testing.T, bullet, pattern string) int {
	t.Helper()
	match := regexp.MustCompile(pattern).FindStringSubmatch(bullet)
	if match == nil {
		t.Fatalf("the AGENTS.md options bullet no longer carries the figure this check reads (pattern %q)", pattern)
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("figure %q is not a number: %v", match[1], err)
	}
	return n
}

// exportedRenderers lists every exported renderer in this package, derived
// from the AST of its Go files rather than from a regexp over the .templ
// sources — which is how 172 and 175 came to disagree.
//
// Every Go file, not only the generated templ output: a renderer hand-written
// in a plain .go file is exported, installed and rendered like any other, and
// while the glob read *_templ.go it was invisible here too — so it could
// carry no ReferenceRegistry entry and no allow-list mention and still leave
// both counts agreeing.
func exportedRenderers(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	var renderers []string
	scanned := 0
	for _, entry := range entries {
		path := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		scanned++
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || !returnsTemplComponent(fn) {
				continue
			}
			renderers = append(renderers, fn.Name.Name)
		}
	}
	if scanned < 100 {
		t.Fatalf("only %d non-test .go files in this package; run `make generate` before this check can answer", scanned)
	}
	sort.Strings(renderers)
	return renderers
}

// documentedRenderers is the renderer each ReferenceRegistry entry documents,
// read out of its own Signature so an entry whose signature drifts from its
// name is caught too.
func documentedRenderers(t *testing.T) []string {
	t.Helper()
	signature := regexp.MustCompile(`^templ ([A-Z]\w*)\(o ([A-Z]\w*)Opts\)$`)
	var names []string
	seen := map[string]bool{}
	for _, entry := range ReferenceRegistry {
		match := signature.FindStringSubmatch(entry.Signature)
		if match == nil || match[1] != match[2] {
			t.Errorf("ReferenceRegistry entry %q carries signature %q, which is not `templ Name(o NameOpts)`.\n"+
				"AGENTS.md states that shape for every renderer in this package.", entry.Name, entry.Signature)
			continue
		}
		if seen[entry.Signature] {
			t.Errorf("ReferenceRegistry documents %q twice; the documented signatures must be distinct", entry.Signature)
		}
		seen[entry.Signature] = true
		names = append(names, match[1])
	}
	sort.Strings(names)
	return names
}

// Both counts and the difference between them. The set comparison is the
// point: a new renderer with no Reference entry has to be added to the
// document's allow-list, which is where the judgement about whether it should
// be a gallery component belongs.
func TestAgentsRendererCountsMatchTheReferenceRegistry(t *testing.T) {
	bullet := optionsBullet(t)

	renderers := exportedRenderers(t)
	documented := documentedRenderers(t)

	if stated := figure(t, bullet, `the ([0-9]+) documented signatures`); stated != len(ReferenceRegistry) {
		t.Errorf("AGENTS.md says %d documented signatures; ui/reference_gen.go carries %d Reference entries.\n"+
			"Update the figure in the options bullet.", stated, len(ReferenceRegistry))
	}
	if stated := figure(t, bullet, `exported renderer of ([0-9]+) with no`); stated != len(renderers) {
		t.Errorf("AGENTS.md says %d exported renderers; this package exports %d.\n"+
			"Update the figure in the options bullet.", stated, len(renderers))
	}
	if len(documented) != len(ReferenceRegistry) {
		t.Fatalf("%d of %d Reference entries carry an unparseable signature", len(ReferenceRegistry)-len(documented), len(ReferenceRegistry))
	}

	// The document names the renderers it expects to have no entry.
	allowed := map[string]bool{}
	for _, span := range regexp.MustCompile("entry is (?:`[A-Za-z]+`(?:, | and )?)+").FindAllString(bullet, -1) {
		for _, name := range regexp.MustCompile("`([A-Za-z]+)`").FindAllStringSubmatch(span, -1) {
			allowed[name[1]] = true
		}
	}
	if len(allowed) == 0 {
		t.Fatal("the AGENTS.md options bullet no longer names the renderer(s) with no Reference entry; this check reads that allow-list")
	}

	inRegistry := map[string]bool{}
	for _, name := range documented {
		inRegistry[name] = true
	}
	exported := map[string]bool{}
	var undocumented []string
	for _, name := range renderers {
		exported[name] = true
		if inRegistry[name] || allowed[name] {
			continue
		}
		undocumented = append(undocumented, name)
	}
	if len(undocumented) > 0 {
		t.Errorf("%d exported renderer(s) have no Reference entry and are not in AGENTS.md's allow-list: %v.\n"+
			"Declare each one's reference in its module manifest, or name it in the options bullet and say why it is not a gallery component.",
			len(undocumented), undocumented)
	}

	var phantom []string
	for _, name := range documented {
		if !exported[name] {
			phantom = append(phantom, name)
		}
	}
	if len(phantom) > 0 {
		t.Errorf("ReferenceRegistry documents %v and this package exports no such renderer", phantom)
	}

	for name := range allowed {
		if !exported[name] {
			t.Errorf("AGENTS.md's options bullet names %s as the exported renderer with no Reference entry, and no such renderer exists.\n"+
				"Correct the bullet.", name)
			continue
		}
		if inRegistry[name] {
			t.Errorf("AGENTS.md says %s has no Reference entry, and ReferenceRegistry documents it.\n"+
				"Remove it from the bullet's allow-list.", name)
		}
	}
	if len(renderers)-len(ReferenceRegistry) != len(allowed) {
		t.Errorf("AGENTS.md allows %d renderer(s) with no Reference entry (%v) and the gap is %d.\n"+
			"Name every one of them, or the two figures in the bullet cannot both be right.",
			len(allowed), sortedKeys(allowed), len(renderers)-len(ReferenceRegistry))
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
