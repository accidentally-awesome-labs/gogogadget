package web

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// AGENTS.md states the chrome rule for EVERY page reachable by a boosted link:
// identical chrome around #content, anything layout-specific inside it. It then
// names both pairings that make the rule true — "PublicLayout/DocsLayout share
// publicShell; AdminLayout *is* AppLayout" — and one guard, which tests the
// first pairing.
//
// The second was asserted by nothing. It held only because AdminLayout's body
// was a single delegating call, and that is not a property anything checked:
// writing `<footer data-testid="plant-admin-chrome">` in front of the
// delegation gave every admin page two footers, and one of them survives into
// every /app page a boosted link from /admin reaches. internal/web (live
// database, every admin integration test), internal/web/templates and .../ui
// all stayed green.
//
// So the population is derived three ways and cross-checked, because each
// derivation alone has a hole a new layout could sit in:
//
//   - the exported layouts, from layouts.templ;
//   - the dispatcher, from wrapLayout — a layout no arm returns is a layout no
//     page can be assigned, and an arm naming a layout that does not exist is
//     a page that cannot render;
//   - the Layout constants, from page.go — a constant with no arm falls to the
//     default and silently renders the public shell.
//
// The boosted-navigation classes are then derived from the delegation graph
// rather than named: two layouts belong together when one calls the other, or
// when both call the same shell. That is the same statement AGENTS.md makes,
// read out of the source that implements it, so a fifth layout joins a class or
// fails for being in none.
func TestEveryBoostedLayoutClassSharesChrome(t *testing.T) {
	layouts := exportedLayouts(t)
	require.GreaterOrEqual(t, len(layouts), 4,
		"only %d exported layouts found in layouts.templ; the scan is looking in the wrong place", len(layouts))

	dispatched, fallback := dispatchedLayouts(t)
	constants := layoutConstants(t)

	// The three derivations must name the same layouts, in both directions.
	names := make([]string, 0, len(layouts))
	for name := range layouts {
		names = append(names, name)
	}
	sort.Strings(names)
	returned := make([]string, 0, len(dispatched))
	for _, name := range dispatched {
		returned = append(returned, name)
	}
	for _, name := range names {
		assert.Containsf(t, returned, name,
			"layouts.templ exports %s and wrapLayout returns it for no Page.Layout, so no page can ever be assigned it", name)
	}
	for constant, name := range dispatched {
		assert.Containsf(t, names, name,
			"wrapLayout returns templates.%s for %s and layouts.templ exports no such layout", name, constant)
		assert.Containsf(t, constants, constant,
			"wrapLayout switches on templates.%s and page.go declares no such constant", constant)
	}
	for _, constant := range constants {
		if _, ok := dispatched[constant]; ok {
			continue
		}
		assert.Failf(t, "undispatched layout constant",
			"page.go declares templates.%s and wrapLayout has no arm for it, so a page that asks for it "+
				"silently gets %s from the default arm", constant, fallback)
	}

	classes := boostedLayoutClasses(t, layouts)
	require.GreaterOrEqual(t, len(classes), 2,
		"the delegation graph collapsed every layout into %d class(es); a comparison of everything against everything is not this rule", len(classes))

	page := templates.Page{
		Title:  "Same page",
		AppURL: "http://localhost:8080",
		Path:   "/docs/frontend",
		Docs: &content.Docs{Sections: []content.DocSection{{
			Name:  "Features",
			Pages: []content.DocPage{{Slug: "frontend", Title: "Frontend"}},
		}}},
	}
	body := templates.NotFound()

	compared := 0
	for _, class := range classes {
		require.GreaterOrEqualf(t, len(class), 2,
			"%s renders chrome no other layout is compared against.\n"+
				"Either it shares a shell with the layouts a boosted link reaches from it — which is what makes the "+
				"chrome rule true — or the rule does not hold for it and the document says it does.", class[0])

		first, firstBefore, firstAfter := class[0], "", ""
		for i, name := range class {
			html := renderLayoutByName(t, name, page, body)
			assert.Equalf(t, 1, strings.Count(html, `<main id="content"`),
				"%s must render exactly one #content swap target", name)
			before, after := chromeAround(t, html)
			if i == 0 {
				firstBefore, firstAfter = before, after
				continue
			}
			compared++
			assert.Equalf(t, firstBefore, before,
				"%s and %s are reachable from each other by a boosted link and their chrome BEFORE #content differs.\n"+
					"A navigation replaces #content only, so whatever differs here survives into a page that never asked for it.",
				first, name)
			assert.Equalf(t, firstAfter, after,
				"%s and %s are reachable from each other by a boosted link and their chrome AFTER #content differs.\n"+
					"A navigation replaces #content only, so whatever differs here goes missing from a page that did ask for it.",
				first, name)
		}
	}
	require.GreaterOrEqual(t, compared, 2,
		"only %d layout pairs were compared; the classes have collapsed to singletons and this test proves nothing", compared)
}

// renderLayoutByName renders one layout through wrapLayout, the dispatcher a
// request actually goes through, so no table of function values is needed and
// the thing under test is the layout a Page really gets.
func renderLayoutByName(t *testing.T, layout string, page templates.Page, body templ.Component) string {
	t.Helper()
	dispatched, _ := dispatchedLayouts(t)
	constants := layoutConstantValues(t)
	for constant, name := range dispatched {
		if name != layout {
			continue
		}
		value, ok := constants[constant]
		require.Truef(t, ok, "page.go declares no value for templates.%s", constant)
		page.Layout = value
		return renderToString(t, wrapLayout(page, body))
	}
	t.Fatalf("no wrapLayout arm returns %s", layout)
	return ""
}

// exportedLayouts maps each exported layout in layouts.templ to the components
// its body calls.
//
// Read from the .templ rather than the generated mirror because the mirror
// expresses one component call as several statements, and what this needs is
// the authored delegation.
func exportedLayouts(t *testing.T) map[string][]string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("templates", "layouts.templ"))
	require.NoError(t, err, "the layouts live in one file; if they moved, this derivation has to move with them")
	body := string(source)

	declaration := regexp.MustCompile(`(?m)^templ ([A-Z]\w*)\(page Page, content templ\.Component\) \{$`)
	call := regexp.MustCompile(`@(?:[a-z]\w*\.)?([A-Za-z]\w*)\(`)

	matches := declaration.FindAllStringSubmatchIndex(body, -1)
	require.NotEmpty(t, matches, "layouts.templ declares no exported layout; the signature this scan matches has changed")

	out := map[string][]string{}
	for _, match := range matches {
		name := body[match[2]:match[3]]
		block, ok := templBlockAfter(body, match[1])
		require.Truef(t, ok,
			"%s's body is not closed by a `}` at column 0; templ fmt writes it that way and this scan reads it that way", name)
		var calls []string
		for _, found := range call.FindAllStringSubmatch(block, -1) {
			calls = append(calls, found[1])
		}
		out[name] = calls
	}
	return out
}

// templBlockAfter is the body of the templ block whose opening brace ends at
// from, delimited by the closing `}` at column 0.
//
// Brace-delimited rather than "up to the next exported layout": the helpers
// between AppLayout and AdminLayout render banners with @ui.Form and the docs
// helpers below DocsLayout render a search form with the same call, so a span
// that swallowed them would report every layout as sharing a shell and collapse
// the classes this test exists to separate. Measured — it did.
func templBlockAfter(source string, from int) (string, bool) {
	rest := source[from:]
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		return rest[:end], true
	}
	if strings.HasSuffix(rest, "\n}") {
		return rest[:len(rest)-2], true
	}
	return "", false
}

// dispatchedLayouts maps each Layout constant wrapLayout switches on to the
// layout it returns, plus the layout its default arm falls back to.
func dispatchedLayouts(t *testing.T) (map[string]string, string) {
	t.Helper()
	source, err := os.ReadFile("htmx.go")
	require.NoError(t, err)
	body := string(source)
	at := strings.Index(body, "func wrapLayout(")
	require.GreaterOrEqual(t, at, 0,
		"internal/web no longer declares wrapLayout; the layout a page gets is decided somewhere else now and this derivation must follow it")
	block := body[at:]
	if end := strings.Index(block, "\n}\n"); end >= 0 {
		block = block[:end]
	}

	out := map[string]string{}
	for _, arm := range regexp.MustCompile(`case templates\.(\w+):\s*\n\s*return templates\.(\w+)\(`).FindAllStringSubmatch(block, -1) {
		out[arm[1]] = arm[2]
	}
	require.NotEmpty(t, out, "wrapLayout no longer switches on templates.Layout* constants; re-derive this against its new shape")

	fallback := regexp.MustCompile(`default:\s*\n\s*return templates\.(\w+)\(`).FindStringSubmatch(block)
	require.NotNil(t, fallback, "wrapLayout has no default arm, so an unknown Page.Layout renders nothing at all")
	out[layoutConstantFor(t, fallback[1])] = fallback[1]
	return out, fallback[1]
}

// layoutConstantFor is the constant whose name matches one layout function:
// LayoutPublic for PublicLayout. The default arm names the function, not the
// constant, and the pairing is the naming convention rather than a table.
func layoutConstantFor(t *testing.T, layout string) string {
	t.Helper()
	constant := "Layout" + strings.TrimSuffix(layout, "Layout")
	require.Containsf(t, layoutConstants(t), constant,
		"wrapLayout's default arm returns templates.%s and page.go declares no templates.%s to go with it", layout, constant)
	return constant
}

// layoutConstants is every Layout* constant page.go declares, and
// layoutConstantValues their values.
func layoutConstants(t *testing.T) []string {
	t.Helper()
	values := layoutConstantValues(t)
	out := make([]string, 0, len(values))
	for name := range values {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func layoutConstantValues(t *testing.T) map[string]string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("templates", "page.go"))
	require.NoError(t, err)
	out := map[string]string{}
	for _, declared := range regexp.MustCompile(`(?m)^\s*(Layout[A-Z]\w*)\s*=\s*"([^"]*)"`).FindAllStringSubmatch(string(source), -1) {
		out[declared[1]] = declared[2]
	}
	require.NotEmpty(t, out, "page.go declares no Layout* constants; Page.Layout is set from somewhere else now")
	return out
}

// boostedLayoutClasses groups layouts that must render identical chrome.
//
// The edges are the two shapes the source uses to make the rule true: one
// layout delegating to another (AdminLayout is AppLayout), and two layouts
// calling the same shell (PublicLayout and DocsLayout share publicShell).
// Derived rather than named, so the classes cannot disagree with the code that
// creates them — and so a layout that grows its own chrome in front of a
// delegation stays in the class it delegates into and is caught by the byte
// comparison rather than quietly leaving the population.
func boostedLayoutClasses(t *testing.T, layouts map[string][]string) [][]string {
	t.Helper()
	parent := map[string]string{}
	for name := range layouts {
		parent[name] = name
	}
	var find func(string) string
	find = func(name string) string {
		if parent[name] == name {
			return name
		}
		parent[name] = find(parent[name])
		return parent[name]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	// A shell called by more than one layout is a shared shell.
	callers := map[string][]string{}
	for name, calls := range layouts {
		for _, called := range calls {
			if _, isLayout := layouts[called]; isLayout {
				union(name, called)
				continue
			}
			if !slices.Contains(callers[called], name) {
				callers[called] = append(callers[called], name)
			}
		}
	}
	for _, sharers := range callers {
		for _, name := range sharers[1:] {
			union(sharers[0], name)
		}
	}

	grouped := map[string][]string{}
	for name := range layouts {
		root := find(name)
		grouped[root] = append(grouped[root], name)
	}
	out := make([][]string, 0, len(grouped))
	for _, class := range grouped {
		sort.Strings(class)
		out = append(out, class)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
