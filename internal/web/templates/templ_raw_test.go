package templates

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// templ.Raw is the one hole in templ's escaping, and AGENTS.md states the rule
// as a NEVER: "templ auto-escapes; user-controlled content NEVER goes through
// templ.Raw. goldmark without html.WithUnsafe." content/docs/security.md
// restates it with "no exceptions".
//
// Nothing enforced it. The two defences that existed were per-payload escaping
// assertions on the CMS path - internal/content/content_test.go and
// internal/web/admin_content_editor_test.go - which prove that ONE goldmark
// output is escaped. Neither is a scan, so neither can see a new call site: a
// rendered project name swapped for @templ.Raw(p.Name) is stored XSS on
// /app/projects for every member of the org, and the templates package, the ui
// package and the live-database integration suite all stayed green through it.
//
// A prohibition is the wrong shape here, because six call sites are legitimate
// and load-bearing: the CMS renders Markdown server-side and the rendered HTML
// has to reach the browser as HTML. So the rule is an ALLOW-LIST keyed by the
// authored source and by the expression rendered, checked in both directions:
//
//   - a call site this table does not name, and which carries no in-source
//     justification either, fails — that is the XSS direction;
//   - an entry whose file is installed and whose call site is gone fails as
//     stale, so the table cannot accumulate permissions for code that no
//     longer exists. An entry for a file this closure never installed is
//     simply never consulted, the same rule rendererSeeds follows: `minimal`
//     installs no blog, and a guard that failed there would be a guard the
//     first derivative deletes;
//   - a `.templ` call site and its generated mirror must agree, because the
//     mirror is what the compiler reads and a hand-edited mirror is the one
//     place a call site can exist without appearing in any authored markup.
//
// Every entry names the expression, not just the file: a file cleared to render
// goldmark output is not thereby cleared to render a user's display name.
//
// templRawJustification is the second, tree-local way to clear a call site,
// and the reason this guard can ship. A derivative owns its own pages and
// cannot edit this table — the module owns this file — so a new call site
// states its case in a comment on the line above itself, where a reviewer
// reads it beside the expression rather than three files away. The two paths
// are the same decision recorded in two places, and neither is silent.
func templRawAllowlist() map[string]map[string]string {
	return map[string]map[string]string{
		"templates/blog": {
			"post.Body": "content.Entry.Body is goldmark output rendered by internal/content without html.WithUnsafe",
		},
		"templates/changelog": {
			"release.Body": "content.Entry.Body is goldmark output rendered by internal/content without html.WithUnsafe",
		},
		"templates/content": {
			"e.Body":     "content.Entry.Body is goldmark output rendered by internal/content without html.WithUnsafe",
			"entry.Body": "content.Entry.Body is goldmark output rendered by internal/content without html.WithUnsafe",
			"html":       "ContentPreview's only caller is the admin editor, which passes the same goldmark output the public page shows",
		},
		"templates/docs": {
			"p.Body": "content.DocPage.Body is goldmark output rendered by internal/content without html.WithUnsafe",
		},
		"templates/admin_content": {
			"d.PreviewHTML": "ContentEditorData.PreviewHTML is goldmark output produced without html.WithUnsafe; no author string reaches it unrendered",
		},
	}
}

// The rule, over every authored and generated source under internal/web.
func TestEveryTemplRawCallSiteIsJustified(t *testing.T) {
	sites, scanned := templRawCallSites(t)

	// Floors. A walk that stopped early, a suffix filter that stopped matching
	// or a call-site parser that stopped parsing all produce the same evidence
	// as a tree with no templ.Raw in it, and the second is the state this test
	// exists to distinguish. So the file counts are asserted per kind, and the
	// call-site count is asserted against the table rather than against zero.
	require.Greater(t, scanned["*.templ"], 150,
		"only %d .templ files scanned; the walk is looking in the wrong place", scanned["*.templ"])
	require.Greater(t, scanned["*_templ.go"], 150,
		"only %d generated templ mirrors scanned; the mirror is what the compiler reads, and it is where a hand-planted call site hides",
		scanned["*_templ.go"])
	require.Greater(t, scanned["*.go"], 25,
		"only %d hand-written .go files scanned; a component may call templ.Raw from Go as easily as from markup", scanned["*.go"])
	require.GreaterOrEqual(t, len(sites), 2*templRawJustifiedCount(t),
		"the scan found %d templ.Raw call sites and the allow-list justifies %d installed ones plus their mirrors; the scan has collapsed, not the tree",
		len(sites), templRawJustifiedCount(t))

	allowed := templRawAllowlist()
	present := templRawInstalledSources(t)
	matched := map[string]int{}
	for _, site := range sites {
		source := templRawLogicalSource(site.path)
		if reason, ok := allowed[source][site.expr]; ok {
			require.NotEmpty(t, reason,
				"an allow-list entry with no reason is a permission nobody reviewed: %s → %s", source, site.expr)
			matched[source+"\x00"+site.expr]++
			continue
		}
		if site.justified != "" {
			assert.GreaterOrEqualf(t, len(site.justified), 25,
				"%s:%d justifies templ.Raw(%s) with %q, which states nothing a reviewer can check.\n"+
					"Name the sanitiser and why its output is already safe.",
				site.path, site.line, site.expr, site.justified)
			continue
		}
		assert.Fail(t,
			"unjustified templ.Raw",
			"%s:%d — templ.Raw(%s) bypasses templ's escaping and nothing has cleared %s as already-sanitised.\n"+
				"fix: render the value with { %s } so templ escapes it. If it is genuinely server-rendered HTML "+
				"(goldmark WITHOUT html.WithUnsafe, or a constant), say so on the line above the call as "+
				"`// %s<why it is already sanitised>` — or, in this registry, add %q → %q to templRawAllowlist. "+
				"Either way it is read as a security decision.",
			site.path, site.line, site.expr, site.expr, site.expr, templRawMarker, source, site.expr)
	}

	for source, exprs := range allowed {
		if !present[source] {
			continue
		}
		for expr := range exprs {
			assert.Greaterf(t, matched[source+"\x00"+expr], 0,
				"%s is installed, the allow-list clears templ.Raw(%s) in it, and no such call site exists any more.\n"+
					"Delete the entry: a standing permission for code that is gone is a permission the next call site inherits.",
				source, expr)
		}
	}

	templRawMarkersAreNotStale(t)

	templRawMirrorsAgree(t, sites)
}

// A `.templ` call site and its generated mirror must carry the same expression.
//
// This is the direction the plant used: templ.Raw in a mirror compiles and
// renders whether or not any authored markup asks for it, and a reader diffing
// the .templ sees nothing. `ggg check` regenerates before it gates, so the
// mirror is also covered by the drift gate - two gates on one property, which
// for the escaping rule is the right number.
func templRawMirrorsAgree(t *testing.T, sites []templRawSite) {
	t.Helper()
	authored, mirrored := map[string][]string{}, map[string][]string{}
	for _, site := range sites {
		key := templRawLogicalSource(site.path)
		if strings.HasSuffix(site.path, ".templ") {
			authored[key] = append(authored[key], site.expr)
			continue
		}
		mirrored[key] = append(mirrored[key], site.expr)
	}
	for key, exprs := range authored {
		sort.Strings(exprs)
		got := slices.Clone(mirrored[key])
		sort.Strings(got)
		assert.Equalf(t, exprs, got,
			"%s.templ calls templ.Raw on %v and its generated mirror on %v; run the generator, or stop hand-editing the mirror",
			key, exprs, got)
	}
	for key, exprs := range mirrored {
		if _, ok := authored[key]; ok {
			continue
		}
		assert.Failf(t, "templ.Raw only in generated output",
			"%s_templ.go calls templ.Raw on %v and %s.templ calls it on nothing.\n"+
				"A call site that exists only in the mirror was hand-planted or the mirror is stale; either way the compiler renders it.",
			key, exprs, key)
	}
}

// templRawMarker opens the in-source justification. It names the call it
// clears, so the marker cannot be mistaken for ordinary prose about escaping —
// several of these files discuss templ.Raw at length without calling it.
const templRawMarker = "templ.Raw is sanitised: "

// A marker with no call under it fails as stale. Without this the in-source
// path would be one-directional: a justification could outlive the call it
// justifies and pre-clear whatever the next author writes on that line.
func templRawMarkersAreNotStale(t *testing.T) {
	t.Helper()
	marked := 0
	for path, src := range templRawAuthoredSources(t) {
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			at := strings.Index(line, "//")
			if at < 0 || !strings.Contains(line[at:], templRawMarker) {
				continue
			}
			marked++
			next := ""
			for _, candidate := range lines[i+1:] {
				if strings.TrimSpace(candidate) == "" {
					continue
				}
				next = candidate
				break
			}
			assert.Containsf(t, next, "templ.Raw(",
				"%s:%d justifies a templ.Raw that is not there — the next statement is %q.\n"+
					"Delete the marker: a standing justification is a permission the next line inherits.",
				path, i+1, strings.TrimSpace(next))
		}
	}
	// No floor here on purpose: this registry justifies its six call sites
	// through the table, so zero markers is the correct reading of a clean
	// tree. The floor that matters belongs to the scan, above.
	_ = marked
}

// templRawInstalledSources reports which allow-list keys name a file this
// closure actually has. Derived by stat, not by which sources the scan found a
// call in: those two differ by exactly the case the staleness check is for.
func templRawInstalledSources(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for source := range templRawAllowlist() {
		for _, suffix := range []string{".templ", ".go"} {
			if _, err := os.Stat(filepath.Join("..", source+suffix)); err == nil {
				out[source] = true
			}
		}
	}
	require.NotEmpty(t, out,
		"none of the allow-listed sources exist; the paths are relative to internal/web and one of them has moved")
	return out
}

// templRawJustifiedCount is the number of allow-listed call sites this closure
// installed, which is what the scan's floor is measured against. A closure
// that installed no blog must not be held to the blog's call site.
func templRawJustifiedCount(t *testing.T) int {
	t.Helper()
	present := templRawInstalledSources(t)
	count := 0
	for source, exprs := range templRawAllowlist() {
		if present[source] {
			count += len(exprs)
		}
	}
	require.Greater(t, count, 0, "an empty allow-list makes this test a prohibition, which is not the claim")
	return count
}

type templRawSite struct {
	path, expr, justified string
	line                  int
}

// templRawSourceFiles walks internal/web once and returns every file the rule
// applies to, keyed by path.
//
// The root is the whole web tree rather than this package, because the rule is
// about what reaches a browser and every surface that renders is under it:
// internal/web's own handlers, this package's page templates, ui/'s renderers
// and slots/'s shell fragments. Test files are excluded - they render into a
// buffer an assertion reads, never into a response - and that exclusion is the
// only one.
func templRawSourceFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || templRawScannedKind(path) == "" {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out[filepath.ToSlash(path)] = string(body)
		return nil
	}))
	require.NotEmpty(t, out)
	return out
}

// templRawAuthoredSources is the subset a human writes: the marker can only
// live there, because templ drops in-markup comments on the way to the mirror.
func templRawAuthoredSources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for path, src := range templRawSourceFiles(t) {
		if templRawScannedKind(path) != "*_templ.go" {
			out[path] = src
		}
	}
	return out
}

// templRawCallSites is every templ.Raw call in that population, plus how many
// files of each kind the walk read.
func templRawCallSites(t *testing.T) ([]templRawSite, map[string]int) {
	t.Helper()
	var sites []templRawSite
	scanned := map[string]int{}
	for path, src := range templRawSourceFiles(t) {
		scanned[templRawScannedKind(path)]++
		for _, site := range templRawSitesIn(src) {
			site.path = path
			sites = append(sites, site)
		}
	}
	return sites, scanned
}

// templRawScannedKind classifies one path, or returns "" for a file the scan
// does not read.
func templRawScannedKind(path string) string {
	switch {
	case strings.HasSuffix(path, ".templ"):
		return "*.templ"
	case strings.HasSuffix(path, "_test.go"):
		return ""
	case strings.HasSuffix(path, "_templ.go"):
		return "*_templ.go"
	case strings.HasSuffix(path, ".go"):
		return "*.go"
	default:
		return ""
	}
}

// templRawSitesIn returns one site per templ.Raw call in src, carrying the
// expression the call renders.
//
// The argument is read by balancing parentheses rather than by a regexp,
// because templ.Raw(fmt.Sprintf("%s", x)) is a legal call and a regexp that
// stops at the first `)` would record a truncated expression - which would
// then match, or fail to match, an allow-list entry for the wrong reason.
// Whole-line comments are skipped: several of these files explain in prose why
// user content must not reach this call, and a scan that cannot tell markup
// from prose is a scan the first person to hit it deletes.
func templRawSitesIn(src string) []templRawSite {
	var out []templRawSite
	line := 1
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			line++
			continue
		}
		if !strings.HasPrefix(src[i:], "templ.Raw(") {
			continue
		}
		if templRawInCommentLine(src, i) {
			continue
		}
		arg, end := templRawArgument(src[i+len("templ.Raw("):])
		if end < 0 {
			continue
		}
		out = append(out, templRawSite{expr: arg, line: line, justified: templRawJustificationAbove(src, i)})
		i += len("templ.Raw(") + end
	}
	return out
}

// templRawInCommentLine reports whether the occurrence at index sits on a line
// whose first non-space bytes are `//`.
func templRawInCommentLine(src string, index int) bool {
	start := strings.LastIndexByte(src[:index], '\n') + 1
	return strings.HasPrefix(strings.TrimSpace(src[start:index]), "//")
}

// templRawJustificationAbove is the reason a marker on one of the two lines
// above the call gives, or "".
//
// Two lines rather than one, because a call inside a templ block is commonly
// written under a blank line, and a rule that a blank line defeats is a rule
// that produces a confusing failure.
func templRawJustificationAbove(src string, index int) string {
	start := strings.LastIndexByte(src[:index], '\n') + 1
	before := strings.Split(src[:start], "\n")
	for i := len(before) - 1; i >= 0 && i >= len(before)-3; i-- {
		line := strings.TrimSpace(before[i])
		if line == "" {
			continue
		}
		at := strings.Index(line, "//")
		if at < 0 {
			return ""
		}
		reason := strings.TrimSpace(strings.TrimPrefix(line[at+2:], " "))
		if !strings.HasPrefix(reason, templRawMarker) {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(reason, templRawMarker))
	}
	return ""
}

// templRawArgument returns the first argument expression and the index of the
// closing parenthesis, tracking nesting, string literals and rune literals.
func templRawArgument(src string) (string, int) {
	depth := 0
	for i := 0; i < len(src); i++ {
		switch c := src[i]; c {
		case '"', '`', '\'':
			end := templRawLiteralEnd(src, i)
			if end < 0 {
				return "", -1
			}
			i = end
		case '(', '[', '{':
			depth++
		case ')':
			if depth == 0 {
				return strings.TrimSpace(src[:i]), i
			}
			depth--
		case ']', '}':
			depth--
		case ',':
			if depth == 0 {
				return strings.TrimSpace(src[:i]), i
			}
		}
	}
	return "", -1
}

// templRawLiteralEnd is the index of the byte closing the literal that opens at
// from, or -1 when it never closes.
func templRawLiteralEnd(src string, from int) int {
	quote := src[from]
	for i := from + 1; i < len(src); i++ {
		switch src[i] {
		case '\\':
			if quote != '`' {
				i++
			}
		case quote:
			return i
		}
	}
	return -1
}

// templRawLogicalSource is the authored source one path belongs to: a `.templ`
// file and the `_templ.go` templ renders from it are one decision, so they
// share one allow-list entry and cannot drift into two.
func templRawLogicalSource(path string) string {
	// Paths arrive relative to the walk root, which is one level above this
	// package, so `../` is trimmed: an allow-list key reads as the position in
	// the web tree rather than as the scanner's cwd.
	base := strings.TrimPrefix(filepath.ToSlash(path), "../")
	switch {
	case strings.HasSuffix(base, "_templ.go"):
		return strings.TrimSuffix(base, "_templ.go")
	case strings.HasSuffix(base, ".templ"):
		return strings.TrimSuffix(base, ".templ")
	default:
		return strings.TrimSuffix(base, ".go")
	}
}
