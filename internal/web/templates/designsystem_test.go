package templates

import (
	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The design system is three layers with one home each:
//
//	tokens            → input.css @theme + .dark   (and theme.go for email)
//	component classes → input.css @layer components
//	templ components  → components.templ + icons.templ
//
// Templates consume those and nothing else. That claim was documented for
// months while being false in eighty-odd places, so this test owns it. There
// are deliberately ZERO exemptions: an exemption list is how the previous
// claim rotted.
//
// Each rule names the fix, not just the sin — a failure should read as an
// instruction.
type designRule struct {
	name string
	re   *regexp.Regexp
	fix  string
	// reject narrows a regexp match; nil means every match is a violation.
	reject func(src string, loc []int) bool
}

func designRules() []designRule {
	return []designRule{
		{
			name: "raw hex colour",
			// RE2 has no lookahead, so the "not part of a longer word" part of
			// this rule is enforced in Go by rejectHex below.
			re:  regexp.MustCompile(`#[0-9a-fA-F]{3,8}`),
			fix: "add a token to @theme in input.css and use its utility; email colour belongs in emailStyle (theme.go)",
			reject: func(src string, loc []int) bool {
				if loc[1] >= len(src) {
					return true
				}
				return !isWordByte(src[loc[1]])
			},
		},
		{
			name: "dark: variant",
			re:   regexp.MustCompile(`dark:`),
			fix:  "dark mode is token flipping — declare the value under .dark in input.css instead",
		},
		{
			name: "palette ramp",
			re: regexp.MustCompile(`\b(bg|text|border|ring|divide|from|via|to|outline|decoration|accent|fill|stroke|shadow)-` +
				`(slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-` +
				`(50|100|200|300|400|500|600|700|800|900|950)\b`),
			fix: "use a semantic token: success / warn / danger / info, each with -fg, -text, -subtle, -subtle-fg, -border",
		},
		{
			name: "numeric brand step",
			re: regexp.MustCompile(`\b(bg|text|border|ring|divide|from|via|to|outline|decoration|accent|fill|stroke|shadow)-brand-` +
				`(50|100|200|300|400|500|600|700|800|900|950)\b`),
			fix: "the ramp belongs to input.css alone — use brand, brand-hover, brand-fg, brand-text, brand-subtle or brand-subtle-fg",
		},
		{
			name: "! utility override",
			// A utility prefix AND a dash, so x-show="!copied", if !ok and !=
			// are not matches.
			re: regexp.MustCompile(`!(?:p|m|w|h|gap|text|bg|border|rounded|py|px|pt|pb|pl|pr|my|mx|mt|mb|` +
				`leading|tracking|grid|flex|order|z|top|left|right|bottom|inset)[trblxy]?-`),
			fix: "an override means a missing variant — add a size or variant class in @layer components",
		},
		{
			name: "arbitrary length",
			re:   regexp.MustCompile(`\[[0-9.]+(px|rem|em|vh|vw|%)\]`),
			fix:  "use the spacing / text scales, or add a structural token to @theme",
		},
		{
			name: "templ expression inside a quoted attribute",
			re:   regexp.MustCompile(`="[^"]*\{[^"]*\}`),
			fix:  `templ does not interpolate inside quotes — write attr={ expr } or class={ "a", expr }`,
		},
	}
}

func isWordByte(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b == '_', b == '-':
		return true
	}
	return false
}

func TestDesignSystemLayering(t *testing.T) {
	sources := templFiles(t)
	require.NotEmpty(t, sources, "no .templ files found — the scanner is looking in the wrong place")

	rules := designRules()
	for name, src := range sources {
		for _, rule := range rules {
			for _, loc := range rule.re.FindAllStringIndex(src, -1) {
				if rule.reject != nil && !rule.reject(src, loc) {
					continue
				}
				assert.Fail(t,
					"design-system violation",
					"%s:%d — %s — %q\nfix: %s",
					name, lineOf(src, loc[0]), rule.name, src[loc[0]:loc[1]], rule.fix)
			}
		}
	}
}

// A const with no switch arm renders nothing, which is a silently missing
// icon. Walk the registry so adding the const without the arm fails here.
func TestIconRegistryIsComplete(t *testing.T) {
	require.NotEmpty(t, IconNames)
	for _, name := range IconNames {
		html := renderComponent(t, Icon(name, "w-4 h-4"))
		assert.Contains(t, html, "<svg", "icon %q has a const but no switch arm in icons.templ", name)
		assert.Contains(t, html, `class="w-4 h-4"`, "icon %q must apply the caller's class", name)
	}
}

// Every semantic kind must resolve to a real component class, in every family
// that takes a ui.Kind. A typo'd or unregistered kind renders "badge-" and no
func templFiles(t *testing.T) map[string]string {
	t.Helper()
	// go test runs with cwd = the package directory.
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".templ") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		require.NoError(t, err)
		out[e.Name()] = string(b)
	}
	return out
}

func renderComponent(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	require.NoError(t, c.Render(t.Context(), &sb))
	return sb.String()
}

func lineOf(src string, offset int) int {
	return strings.Count(src[:offset], "\n") + 1
}

// Every semantic kind must be styled for every state family. The typed API
// accepts all six kinds on Badge, Notice, Banner and the toast, so a missing
// family-kind pair is reachable from ordinary code and renders an unstyled box
// — no border, no background, no colour.
//
// This gap was real before the matrix was restructured: alert-brand,
// alert-neutral, banner-brand, banner-success and banner-neutral had no rules
// at all.
func TestEveryKindIsStyledForEveryStateFamily(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("..", "..", "..", "input.css"))
	require.NoError(t, err)
	source := string(css)

	for _, family := range []string{"badge", "alert", "banner", "toast"} {
		for _, kind := range ui.Kinds {
			selector := "." + family + "-" + string(kind)
			assert.Contains(t, source, selector,
				"%s has no rule, so a %s rendered with kind %q is unstyled", selector, family, kind)
		}
	}

	// The six variables each kind must define, which the families consume.
	for _, kind := range ui.Kinds {
		block := kindBlock(t, source, string(kind))
		for _, variable := range []string{
			"--ui-solid:", "--ui-solid-fg:", "--ui-tint:", "--ui-tint-fg:", "--ui-line:", "--ui-text:",
		} {
			assert.Contains(t, block, variable,
				"kind %q does not define %s, so a family reading it renders with no value", kind, variable)
		}
	}
}

// The built stylesheet must actually carry the variables: a rule that exists in
// input.css but is dropped by the build is still an unstyled component.
func TestBuiltStylesheetCarriesKindVariables(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("..", "..", "..", "static", "app.css"))
	require.NoError(t, err)
	built := string(css)

	for _, variable := range []string{"--ui-tint", "--ui-line", "--ui-solid"} {
		assert.Contains(t, built, variable, "%s never reached the built stylesheet", variable)
	}
	for _, kind := range ui.Kinds {
		assert.Contains(t, built, "badge-"+string(kind),
			"badge-%s never reached the built stylesheet", kind)
	}
}

// kindBlock returns the declaration block for one kind's selector list.
func kindBlock(t *testing.T, source, kind string) string {
	t.Helper()
	marker := ".k-" + kind + ","
	start := strings.Index(source, marker)
	require.GreaterOrEqual(t, start, 0, "no .k-%s selector", kind)
	end := strings.Index(source[start:], "}")
	require.Greater(t, end, 0, "unterminated .k-%s block", kind)
	return source[start : start+end]
}
