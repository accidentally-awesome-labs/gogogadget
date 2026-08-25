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

// The gallery is the catalog's reference surface: if a component is installed
// but never rendered there, nobody reviewing the design system - human or agent
// - can see it, and no visual or accessibility gate covers it. Comparing the
// rendered data-ui markers against the generated registry closes the gap that a
// hand-kept list leaves open.
func TestGalleryCoversEveryInstalledComponent(t *testing.T) {
	html := renderComponent(t, Gallery())
	rendered := renderedComponentMarkers(html)
	require.NotEmpty(t, ui.ComponentRegistry, "no components are installed")

	var missing []string
	for _, c := range ui.ComponentRegistry {
		if _, ok := rendered[c.Name]; !ok {
			missing = append(missing, c.Name+" ("+string(c.Family)+")")
		}
	}
	assert.Empty(t, missing, "installed components the gallery never renders")

	for name := range rendered {
		_, ok := ui.ComponentByName(name)
		assert.True(t, ok, "gallery renders %q, which no installed module declares", name)
	}
}

func renderedComponentMarkers(html string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range regexp.MustCompile(`data-ui="([a-z0-9-]+)"`).FindAllStringSubmatch(html, -1) {
		out[m[1]] = struct{}{}
	}
	return out
}

// Under CSP (script-src 'self') Alpine cannot evaluate expression strings, so
// every x-data name must resolve to a registered component. An unregistered
// name is silently inert - the control renders and does nothing - which is why
// this is a test and not a code review item.
func TestEveryAlpineComponentUsedIsRegistered(t *testing.T) {
	used := map[string]string{}
	templates, err := filepath.Glob("*.templ")
	require.NoError(t, err)
	nested, err := filepath.Glob("ui/*.templ")
	require.NoError(t, err)
	for _, path := range append(templates, nested...) {
		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		for _, m := range regexp.MustCompile(`x-data="([a-zA-Z][a-zA-Z0-9]*)"`).FindAllStringSubmatch(string(body), -1) {
			used[m[1]] = path
		}
	}
	require.NotEmpty(t, used, "no x-data components found - the scan is broken")

	registered := map[string]bool{}
	scripts, err := filepath.Glob("../../../static/*.js")
	require.NoError(t, err)
	fragments, err := filepath.Glob("../../../static/ui/*.js")
	require.NoError(t, err)
	for _, path := range append(scripts, fragments...) {
		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		for _, m := range regexp.MustCompile(`Alpine\.data\(\s*"([a-zA-Z][a-zA-Z0-9]*)"`).FindAllStringSubmatch(string(body), -1) {
			registered[m[1]] = true
		}
	}

	for name, path := range used {
		assert.True(t, registered[name],
			"%s uses x-data=%q but no shipped script registers it: under CSP the component is inert", path, name)
	}
}

// The generated list of expected registrations must match what the shipped
// fragments actually register, or the shell publishes a promise nothing keeps.
func TestGeneratedAlpineExpectationsAreRegistered(t *testing.T) {
	published, err := os.ReadFile("../../../static/ui-components.js")
	require.NoError(t, err)
	expected := regexp.MustCompile(`push\("([a-zA-Z0-9]+)"\)`).FindAllStringSubmatch(string(published), -1)
	require.NotEmpty(t, expected, "no module declares an Alpine component")

	registered := map[string]bool{}
	fragments, err := filepath.Glob("../../../static/ui/*.js")
	require.NoError(t, err)
	require.NotEmpty(t, fragments, "modules declare Alpine components but ship no fragment")
	for _, path := range fragments {
		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		for _, m := range regexp.MustCompile(`Alpine\.data\(\s*"([a-zA-Z0-9]+)"`).FindAllStringSubmatch(string(body), -1) {
			registered[m[1]] = true
		}
	}
	for _, m := range expected {
		assert.True(t, registered[m[1]],
			"a module declares Alpine component %q but no installed fragment registers it", m[1])
	}
}

// The semantic families a component may name. A component that reaches for a
// raw utility to express one of these meanings is how two controls end up
// disagreeing about what "focused" or "disabled" looks like.
func TestSemanticTokenFamiliesExist(t *testing.T) {
	sheet := readInputCSS(t)
	for _, token := range []string{
		"--color-focus-ring", "--color-overlay-scrim", "--color-selected",
		"--color-selected-fg", "--radius-control", "--radius-surface",
		"--shadow-raised", "--control-height-sm", "--control-height-md",
		"--control-height-lg", "--motion-fast", "--motion-base",
		"--disabled-opacity", "--color-chart-1", "--color-chart-6",
	} {
		assert.Contains(t, sheet, token+":", "no %s token is declared", token)
	}
}

// Dark mode is token flipping. A token whose light value survives into dark is
// either theme-independent on purpose or a bug; these are the ones that must
// flip, because their light values are unusable on a dark surface.
func TestDarkModeFlipsInteractionTokens(t *testing.T) {
	sheet := readInputCSS(t)
	dark := sheet[strings.Index(sheet, ".dark {"):]
	dark = dark[:strings.Index(dark, "\n  }")]

	for _, token := range []string{
		"--color-focus-ring", "--color-overlay-scrim", "--shadow-raised",
	} {
		assert.Contains(t, dark, token+":",
			"%s keeps its light value in dark mode", token)
	}
}

// One focus treatment, applied through the token. Two components that both mean
// "focused" must not look different, and an interactive element with no focus
// style at all is unusable by keyboard.
func TestFocusTreatmentIsSingleSourced(t *testing.T) {
	sheet := readInputCSS(t)

	assert.NotContains(t, sheet, "focus:ring",
		"a focus: ring paints on mouse click too; focus-visible is the keyboard signal")
	assert.NotContains(t, sheet, "outline-brand",
		"the focus colour must come from --color-focus-ring, not a palette utility")
	assert.Contains(t, sheet, "outline-color: var(--color-focus-ring)")

	// Every interactive idiom must be in the focus selector list.
	focusBlock := sheet[strings.Index(sheet, "outline-style: solid")-1400:]
	for _, idiom := range []string{".btn", ".input", ".nav-link", ".tab", ".link"} {
		assert.Contains(t, focusBlock, idiom+":focus-visible",
			"%s has no focus-visible treatment", idiom)
	}
}

// Reduced motion must be honoured once, at the token, rather than in every
// animated rule - a rule that forgets the media query is a rule that ignores
// the user's OS setting.
func TestReducedMotionCollapsesMotionTokens(t *testing.T) {
	sheet := readInputCSS(t)
	require.Contains(t, sheet, "prefers-reduced-motion: reduce")
	block := sheet[strings.Index(sheet, "prefers-reduced-motion: reduce"):]
	block = block[:strings.Index(block, "\n  }")]
	assert.Contains(t, block, "--motion-fast: 0ms")
	assert.Contains(t, block, "--motion-base: 0ms")
}

// Tokens consumed only through var() must not live in @theme. Tailwind decides
// what to emit from @theme by scanning source text for the name, so such a
// token survives only while some unrelated file happens to mention it - the
// chart series were kept alive purely by the gallery's swatch labels. Declaring
// them in :root removes the coincidence.
func TestVarOnlyTokensAreNotThemeEntries(t *testing.T) {
	sheet := readInputCSS(t)
	themeStart := strings.Index(sheet, "@theme {")
	require.GreaterOrEqual(t, themeStart, 0)
	depth, themeEnd := 0, -1
	for i := themeStart + len("@theme "); i < len(sheet); i++ {
		switch sheet[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				themeEnd = i
			}
		}
		if themeEnd >= 0 {
			break
		}
	}
	require.Greater(t, themeEnd, themeStart)
	theme := sheet[themeStart:themeEnd]

	for _, token := range []string{
		"--color-chart-1", "--color-chart-2", "--color-chart-3",
		"--color-chart-4", "--color-chart-5", "--color-chart-6",
	} {
		assert.NotContains(t, theme, token+":",
			"%s is consumed with var(), so an @theme declaration only survives while some source file mentions its name", token)
		assert.Contains(t, sheet, token+":", "%s is not declared at all", token)
	}
}

// A token declared in the source but dropped by the build is worse than a
// missing token: it resolves to nothing in one theme while the .dark override
// still applies in the other. Tailwind tree-shakes @theme entries that no class
// references, which is exactly what happened to the chart series - they are
// read with var() by the chart module, never as utilities.
func TestVarConsumedTokensSurviveTheBuild(t *testing.T) {
	// The .dark block declares many of these too, so it is stripped first: a
	// token that survives only inside .dark resolves to nothing in light mode,
	// which is the exact failure this test exists to catch.
	built := withoutDarkBlock(t, readBuiltCSS(t))
	for _, token := range []string{
		"--color-chart-1", "--color-chart-2", "--color-chart-3",
		"--color-chart-4", "--color-chart-5", "--color-chart-6",
		"--color-focus-ring", "--color-overlay-scrim",
		"--color-selected", "--color-selected-fg",
		"--radius-control", "--radius-surface", "--shadow-raised",
		"--control-height-md", "--motion-fast", "--disabled-opacity",
	} {
		assert.Contains(t, built, token,
			"%s is declared in input.css but absent from the built stylesheet", token)
	}
}

// withoutDarkBlock removes every .dark rule body from the built sheet, leaving
// only what applies in the default theme.
func withoutDarkBlock(t *testing.T, css string) string {
	t.Helper()
	out := css
	for {
		start := strings.Index(out, ".dark")
		if start < 0 {
			break
		}
		open := strings.Index(out[start:], "{")
		require.Greater(t, open, 0, "malformed .dark rule")
		depth, end := 0, -1
		for i := start + open; i < len(out); i++ {
			switch out[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		require.Greater(t, end, start, "unterminated .dark rule")
		out = out[:start] + out[end+1:]
	}
	require.NotContains(t, out, ".dark")
	return out
}

// readBuiltCSS returns the compiled stylesheet the browser actually loads.
func readBuiltCSS(t *testing.T) string {
	t.Helper()
	css, err := os.ReadFile(filepath.Join("..", "..", "..", "static", "app.css"))
	require.NoError(t, err)
	return string(css)
}

// readInputCSS returns the token and component source. The tests read the
// source rather than the built stylesheet where they are asserting intent; the
// built sheet is checked separately, because a rule present in the source and
// dropped by the build is still an unstyled component.
func readInputCSS(t *testing.T) string {
	t.Helper()
	css, err := os.ReadFile(filepath.Join("..", "..", "..", "input.css"))
	require.NoError(t, err)
	return string(css)
}

// A progressbar or meter with no accessible name reports a number with no
// subject. The gallery is the one page that renders every component, so it is
// where an unnamed one shows up - and it did: three meters shipped nameless.
func TestEveryProgressAndMeterInTheGalleryIsNamed(t *testing.T) {
	html := renderComponent(t, Gallery())
	for _, role := range []string{"progressbar", "meter"} {
		for _, tag := range findRoleTags(html, role) {
			assert.Contains(t, tag, "aria-label=",
				"a %s with no accessible name announces a number with no subject", role)
		}
	}
}

// findRoleTags returns the opening tags carrying a given role.
func findRoleTags(html, role string) []string {
	var out []string
	needle := `role="` + role + `"`
	for i := 0; i < len(html); {
		at := strings.Index(html[i:], needle)
		if at < 0 {
			break
		}
		at += i
		start := strings.LastIndex(html[:at], "<")
		end := strings.Index(html[at:], ">")
		if start >= 0 && end >= 0 {
			out = append(out, html[start:at+end])
		}
		i = at + len(needle)
	}
	return out
}

// The lazy engine loader has four constraints, and each rules out an easier
// design. They are asserted against the source because the failure modes are
// invisible at runtime until the exact wrong thing happens: a script injected
// into #content works until the first navigation, and a missing integrity hash
// works until someone swaps the file.
func TestEngineLoaderConstraints(t *testing.T) {
	loader, err := os.ReadFile(filepath.Join("..", "..", "..", "static", "ui", "engines.js"))
	require.NoError(t, err)
	source := string(loader)

	assert.Contains(t, source, "document.head.appendChild",
		"scripts must be appended to head: htmx replaces #content on every navigation")
	assert.NotContains(t, source, "content.appendChild")
	assert.NotContains(t, source, `querySelector("#content")`)

	assert.Contains(t, source, "script.integrity = asset.integrity",
		"a lazily injected script with no integrity can be swapped unnoticed")

	// One fetch per engine, not per widget: ten charts on a page must not open
	// ten requests for the same runtime.
	assert.Contains(t, source, "pending.has(name)")
	assert.Contains(t, source, "pending.set(name")

	// The vendor file is same-origin, so no CSP widening is needed anywhere.
	assert.NotContains(t, source, "crossorigin")
	assert.NotContains(t, source, "unsafe-inline")
	assert.NotContains(t, source, "https://")
}

// The shell must load the engine registry before any fragment can read it, and
// both must precede Alpine's boot: a fragment that registers on alpine:init is
// too late if Alpine already initialised.
func TestShellLoadsEngineRegistryBeforeAlpine(t *testing.T) {
	shell, err := os.ReadFile("layouts.templ")
	require.NoError(t, err)
	source := string(shell)

	registry := strings.Index(source, "/static/ui-engines.js")
	components := strings.Index(source, "/static/ui-components.js")
	alpine := strings.Index(source, "alpine-csp.min.js")
	require.Positive(t, registry)
	require.Positive(t, components)
	require.Positive(t, alpine)

	assert.Less(t, registry, components, "the registry is data the fragments read")
	assert.Less(t, components, alpine, "registrations must exist before Alpine initialises")

	// Head assets must not depend on which page was requested first: the shell
	// is persistent, and the entry URL is an accident.
	head := source[strings.Index(source, "templ headScripts"):]
	head = head[:strings.Index(head, "\n}")]
	assert.NotContains(t, head, "page.Path",
		"conditioning head assets on the entry page gives the same app different capabilities")
}
