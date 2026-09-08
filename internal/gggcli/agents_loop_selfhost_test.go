// Self-host assertions. Declared self_host by ggg/system/modkit: the
// repository that publishes the registry installs and runs it, and no
// derivative ever receives it.
//
// Spec: AGENTS.md lines 129-132 (the exit-code table in "## The loop") and
// lines 264-277 ("## Task playbook").
//
// Both claims are lists an agent branches on. The exit codes are a public
// contract automation reads; the playbook is the index an agent uses to find
// the recipe it needs, and a recipe that resolves to no heading sends it
// looking for a section that does not exist.

package gggcli

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

// agentsSectionText returns the body under one `## ` heading of AGENTS.md.
func agentsSectionText(t *testing.T, heading string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	marker := "## " + heading + "\n"
	start := strings.Index(string(raw), marker)
	if start < 0 {
		t.Fatalf("AGENTS.md has no %q section; this check reads it, so restore the heading or delete the check", "## "+heading)
	}
	body := string(raw)[start+len(marker):]
	if end := strings.Index(body, "\n## "); end >= 0 {
		body = body[:end]
	}
	return body
}

// oneLine joins a hard-wrapped paragraph so a regexp may span the breaks.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// declaredExitCodes reads the `Exit*` const block out of one file, so the
// check derives the contract from the source rather than from a list in a
// test. prefix is `Exit` in modkit and `exit` in the presentation layer.
func declaredExitCodes(t *testing.T, path, prefix string) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	codes := map[string]int{}
	for _, decl := range parsed.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range value.Names {
				if !strings.HasPrefix(name.Name, prefix) || len(value.Values) <= i {
					continue
				}
				switch expr := value.Values[i].(type) {
				case *ast.BasicLit:
					if expr.Kind != token.INT {
						continue
					}
					n, err := strconv.Atoi(expr.Value)
					if err != nil {
						t.Fatalf("%s = %s is not an integer: %v", name.Name, expr.Value, err)
					}
					codes[strings.TrimPrefix(name.Name, prefix)] = n
				case *ast.SelectorExpr:
					// The presentation layer mirrors the engine constants;
					// the name is what matters on this side.
					codes[strings.TrimPrefix(name.Name, prefix)] = -1
				}
			}
		}
	}
	if len(codes) == 0 {
		t.Fatalf("%s declares no %s* constants; this check reads them", path, prefix)
	}
	return codes
}

// The exit-code table, both ways: every declared code documented, every
// documented code declared, and each documented phrase must still name its
// constant. The audit found exit 4 documented as "conflict" when
// `sync --check` returns it for pending change or generated drift with
// nothing staged — an agent then hunts for a conflict directory that does not
// exist.
func TestAgentsExitCodeTableMatchesTheDeclaredCodes(t *testing.T) {
	loop := oneLine(agentsSectionText(t, "The loop"))
	const anchor = "Exit codes:"
	at := strings.Index(loop, anchor)
	if at < 0 {
		t.Fatalf("AGENTS.md's loop section no longer carries an %q table", anchor)
	}
	table := strings.TrimSpace(loop[at+len(anchor):])

	// Each code opens a clause; its phrase runs to the next code. `exits 4
	// with nothing staged` inside a parenthetical is not a clause opener, so
	// the opener must follow the start, a comma or an "or".
	opener := regexp.MustCompile(`(?:^|, |or )([0-9]+) `)
	matches := opener.FindAllStringSubmatchIndex(table, -1)
	if len(matches) == 0 {
		t.Fatal("AGENTS.md's exit-code table lists no codes")
	}
	documented := map[int]string{}
	for i, match := range matches {
		code, err := strconv.Atoi(table[match[2]:match[3]])
		if err != nil {
			t.Fatalf("exit code %q is not a number: %v", table[match[2]:match[3]], err)
		}
		end := len(table)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		documented[code] = strings.TrimSpace(table[match[3]:end])
	}

	engine := declaredExitCodes(t, filepath.Join("..", "modkit", "contract.go"), "Exit")
	for name, code := range engine {
		phrase, ok := documented[code]
		if !ok {
			t.Errorf("modkit.Exit%s = %d and AGENTS.md's exit-code table does not document %d.\n"+
				"Add it to the table in \"## The loop\" — automation branches on these.", name, code, code)
			continue
		}
		// The phrase must still name the constant: `Rollback` against
		// "rolled back", `Conflict` against "…staged conflict".
		stem := strings.ToLower(name)
		if len(stem) > 4 {
			stem = stem[:4]
		}
		if !strings.Contains(strings.ToLower(phrase), stem) {
			t.Errorf("AGENTS.md documents exit %d as %q, and the constant is modkit.Exit%s.\n"+
				"The phrase no longer names the condition it describes; reword it or rename the constant.", code, phrase, name)
		}
	}
	for code := range documented {
		found := false
		for _, declared := range engine {
			if declared == code {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AGENTS.md documents exit %d (%q) and modkit declares no such code.\n"+
				"Remove it from the table, or declare the constant.", code, documented[code])
		}
	}

	// The presentation layer mirrors the engine block; a code the CLI cannot
	// name is a code it cannot return.
	mirror := declaredExitCodes(t, "contract.go", "exit")
	for name := range engine {
		if _, ok := mirror[name]; !ok {
			t.Errorf("internal/gggcli/contract.go does not mirror modkit.Exit%s, so the envelope and the process status can disagree", name)
		}
	}
	for name := range mirror {
		if _, ok := engine[name]; !ok {
			t.Errorf("internal/gggcli/contract.go declares exit%s and modkit declares no Exit%s", name, name)
		}
	}
}

// extendingHeadings returns the `## ` headings of the extending page in
// document order.
func extendingHeadings(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "content", "docs", "extending.md"))
	if err != nil {
		t.Fatalf("read content/docs/extending.md: %v", err)
	}
	var headings []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## ") {
			headings = append(headings, strings.TrimSpace(strings.TrimPrefix(line, "## ")))
		}
	}
	if len(headings) == 0 {
		t.Fatal("content/docs/extending.md has no ## headings; this check resolves the playbook against them")
	}
	return headings
}

// recipeCatalogue is every heading after the data-loss rules: the page puts
// its conceptual material first and appends task recipes, so the boundary is
// structural rather than a judgement encoded in this test.
const recipeBoundary = "Rules that prevent data loss"

// italics returns the `*emphasised*` spans of one section. A bold `**span**`
// is deliberately not a match: the recipes are the emphasised ones.
var italics = regexp.MustCompile(`(?:^|[^*])\*([^*\n]+)\*(?:[^*]|$)`)

// The playbook, both ways. Forward: every recipe it names resolves to a
// heading, so an agent sent to a section finds one. Reverse: every recipe the
// page carries is named, so a recipe added to the docs is surfaced in the
// manual an agent actually reads first.
func TestAgentsPlaybookRecipesResolveToExtendingHeadings(t *testing.T) {
	playbook := oneLine(agentsSectionText(t, "Task playbook"))
	headings := extendingHeadings(t)

	boundary := -1
	for i, heading := range headings {
		if heading == recipeBoundary {
			boundary = i
			break
		}
	}
	if boundary < 0 {
		t.Fatalf("content/docs/extending.md has no %q heading; this check uses it as the recipe boundary", recipeBoundary)
	}
	catalogue := headings[boundary+1:]
	if len(catalogue) == 0 {
		t.Fatalf("content/docs/extending.md carries no heading after %q", recipeBoundary)
	}

	named := map[string]bool{}
	for _, match := range italics.FindAllStringSubmatch(playbook, -1) {
		named[strings.TrimSpace(match[1])] = true
	}
	if len(named) == 0 {
		t.Fatal("the AGENTS.md task playbook emphasises no recipe names; this check resolves them")
	}

	exists := map[string]bool{}
	for _, heading := range headings {
		exists[heading] = true
	}
	var unresolved []string
	for name := range named {
		if !exists[name] {
			unresolved = append(unresolved, name)
		}
	}
	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		t.Errorf("the AGENTS.md task playbook names %v and content/docs/extending.md has no such heading.\n"+
			"Correct the name in AGENTS.md, or add the section — an agent sent to a heading that does not exist reads the whole page instead.",
			unresolved)
	}

	var unlisted []string
	for _, heading := range catalogue {
		if !named[heading] {
			unlisted = append(unlisted, heading)
		}
	}
	if len(unlisted) > 0 {
		t.Errorf("content/docs/extending.md carries %d recipe(s) the AGENTS.md task playbook never names: %v.\n"+
			"Add each as an emphasised *heading name* to \"## Task playbook\" — a recipe an agent cannot see from AGENTS.md is a recipe it reinvents.",
			len(unlisted), unlisted)
	}

	if stated := recipeCount(t, playbook); stated != len(catalogue) {
		t.Errorf("the AGENTS.md task playbook says %d recipes; content/docs/extending.md carries %d after %q",
			stated, len(catalogue), recipeBoundary)
	}
}

// recipeCount reads the figure the playbook states.
func recipeCount(t *testing.T, playbook string) int {
	t.Helper()
	match := regexp.MustCompile(`— ([0-9]+), the whole catalogue`).FindStringSubmatch(playbook)
	if match == nil {
		t.Fatal("the AGENTS.md task playbook no longer states how many recipes there are")
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("recipe count %q is not a number: %v", match[1], err)
	}
	return n
}
