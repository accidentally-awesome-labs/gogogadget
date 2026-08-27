package templates

import (
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
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
// templFiles walks the whole tree, not just this directory. It read only `.`
// for a long time, which meant every rule below - raw hex, `dark:` variants,
// palette ramps, `!` overrides, arbitrary lengths, templ expressions inside
// quoted attributes - was enforced on the page templates and on none of the 172
// renderers in ui/, where the design system actually lives. The tests passed
// because they were looking at the wrong files.
func templFiles(t *testing.T) map[string]string {
	t.Helper()
	// go test runs with cwd = the package directory.
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".templ") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out[path] = string(body)
		return nil
	}))
	require.NotEmpty(t, out)
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

// The solid pair is a contrast contract. {k}-fg is painted directly on {k}, and
// the smallest thing that does it is a solid badge at 12px — small text, so
// WCAG AA wants 4.5:1. Two kinds shipped below it (#16a34a at 3.30, #d97706 at
// 3.18) and nothing noticed, because until .badge-solid existed no rule ever
// put text on the fill. axe found it the moment one did.
//
// The ratio is computed from input.css rather than asserted as a list of
// approved hexes: a rebrand is supposed to change these values, and what has to
// survive the change is the ratio, not the colour.
func TestSolidFillsCarryTheirForegroundAtSmallText(t *testing.T) {
	sheet := readInputCSS(t)

	for _, kind := range ui.Kinds {
		fill := resolveHex(t, sheet, "--color-"+string(kind))
		text := resolveHex(t, sheet, "--color-"+string(kind)+"-fg")
		ratio := contrastRatio(fill, text)
		assert.GreaterOrEqualf(t, ratio, 4.5,
			"--color-%s (%s) on --color-%s-fg (%s) is %.2f:1; a solid badge renders that pair at 12px, "+
				"which WCAG AA scores as small text and needs 4.5:1",
			kind, fill, kind, text, ratio)
	}
}

// resolveHex reads a token's light-mode value out of input.css, following a
// var() alias to the hex it ultimately names. The .dark block is cut first: a
// solid fill never flips, so its light declaration is the only one.
func resolveHex(t *testing.T, sheet, token string) string {
	t.Helper()

	light := sheet
	if start := strings.Index(light, "  .dark {"); start >= 0 {
		if end := strings.Index(light[start:], "\n  }"); end > 0 {
			light = light[:start] + light[start+end:]
		}
	}

	for range 8 {
		m := regexp.MustCompile(regexp.QuoteMeta(token)+`:\s*([^;\n]+)`).FindStringSubmatch(light)
		require.Lenf(t, m, 2, "input.css declares no light-mode %s", token)
		value := strings.TrimSpace(m[1])
		if inner := regexp.MustCompile(`^var\((--[a-z0-9-]+)\)$`).FindStringSubmatch(value); inner != nil {
			token = inner[1]
			continue
		}
		require.Regexpf(t, `^#[0-9a-fA-F]{6}$`, value, "%s is not a six-digit hex", token)
		return strings.ToLower(value)
	}
	t.Fatalf("%s never resolves to a hex", token)
	return ""
}

// contrastRatio is the WCAG 2.x formula over two six-digit hexes.
func contrastRatio(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relativeLuminance(hex string) float64 {
	channel := func(offset int) float64 {
		v, err := strconv.ParseUint(hex[offset:offset+2], 16, 8)
		if err != nil {
			return 0
		}
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(1) + 0.7152*channel(3) + 0.0722*channel(5)
}

// The Kind axis has a matrix above; the Size axis had nothing, and the gap was
// real: buttonClass emitted `btn btn-lg` for Size: ui.SizeLG on Button,
// ButtonLink and IconButton, while input.css defined only .btn-sm, .btn-xs and
// .btn-icon. The entire large size axis was a dead class - a declared, typed,
// reachable enum value that rendered a control indistinguishable from the
// default.
//
// Two things have to hold, and the second is the one that matters. Naming a
// class in some other rule's selector list is not styling it: .btn-lg appeared
// in the base, disabled and focus lists the whole time, so a test that only
// looked for the string passed while the size did nothing. A size therefore has
// to contribute a class the default does not, and that class has to carry its
// own declaration block.
//
// The class lists come from what the components actually render, so a new size
// or a renamed class cannot pass by agreeing with the test instead of with the
// stylesheet.
func TestEverySizeIsStyledForEveryControlFamily(t *testing.T) {
	source := readInputCSS(t)

	families := map[string]func(ui.Action, ui.Size) templ.Component{
		"Button": func(a ui.Action, s ui.Size) templ.Component {
			return ui.Button(ui.ButtonOpts{Label: "Save", Action: a, Size: s})
		},
		"ButtonLink": func(a ui.Action, s ui.Size) templ.Component {
			return ui.ButtonLink(ui.ButtonLinkOpts{Label: "Save", Href: "/x", Action: a, Size: s})
		},
		"IconButton": func(a ui.Action, s ui.Size) templ.Component {
			return ui.IconButton(ui.IconButtonOpts{Icon: ui.IconCheck, Label: "Save", Action: a, Size: s})
		},
	}

	for family, render := range families {
		for _, action := range ui.Actions {
			// ActionLink renders a text link, which has no size axis at all.
			if action == ui.ActionLink {
				continue
			}

			base := rootClasses(t, renderComponent(t, render(action, ui.SizeMD)))
			for _, class := range base {
				assert.Containsf(t, source, "."+class,
					"%s/%s renders .%s, which input.css never mentions", family, action, class)
			}

			for _, size := range ui.Sizes {
				if size == ui.SizeMD {
					continue
				}

				added := addedClasses(base, rootClasses(t, renderComponent(t, render(action, size))))
				require.NotEmptyf(t, added,
					"%s with Size %q renders exactly what SizeMD renders, so the size is unreachable from CSS",
					family, size)

				for _, class := range added {
					assert.Truef(t, hasOwnRule(source, class),
						"%s with Action %q and Size %q renders .%s, which input.css names in other selector "+
							"lists but never declares on its own - the size is a dead class and the control "+
							"renders at the default size",
						family, action, size, class)
				}
			}
		}
	}
}

// The form controls have the same size axis the buttons do, and it had the same
// hole: .input-lg did not exist at all, and pages that wanted a smaller control
// wrote Attrs.Class: "input-xs" instead - reaching past the typed API, past
// every test, and past the one place a control's scale is decided.
//
// The rule is the button rule: a size must contribute a class the default does
// not, and that class must head its own declaration block. Naming it in the
// focus or disabled selector lists is not styling it.
func TestEveryInputSizeIsStyledForEveryFormControl(t *testing.T) {
	source := readInputCSS(t)

	families := map[string]func(ui.Size) templ.Component{
		"TextInput": func(s ui.Size) templ.Component {
			return ui.TextInput(ui.TextInputOpts{Name: "field", Size: s})
		},
		"NumberInput": func(s ui.Size) templ.Component {
			return ui.NumberInput(ui.NumberInputOpts{Name: "field", Size: s})
		},
		"Select": func(s ui.Size) templ.Component {
			return ui.Select(ui.SelectOpts{Name: "field", Size: s})
		},
		"Textarea": func(s ui.Size) templ.Component {
			return ui.Textarea(ui.TextareaOpts{Name: "field", Size: s})
		},
	}

	for family, render := range families {
		base := rootClasses(t, renderComponent(t, render(ui.SizeMD)))
		for _, class := range base {
			assert.Containsf(t, source, "."+class,
				"%s renders .%s, which input.css never mentions", family, class)
		}

		for _, size := range ui.Sizes {
			if size == ui.SizeMD {
				continue
			}

			added := addedClasses(base, rootClasses(t, renderComponent(t, render(size))))
			require.NotEmptyf(t, added,
				"%s with Size %q renders exactly what SizeMD renders, so the size is unreachable from CSS",
				family, size)

			for _, class := range added {
				assert.Truef(t, hasOwnRule(source, class),
					"%s with Size %q renders .%s, which input.css names in other selector lists "+
						"but never declares on its own - the size is a dead class and the control "+
						"renders at the default size",
					family, size, class)
			}
		}
	}
}

// Table density is the same shape of promise: TableOpts.Density and
// DataTableOpts.Density both emit .table-compact, and for as long as no rule
// declared it a compact table was byte-for-byte a comfortable one.
//
// Both cell selectors are asserted, because a rule on th alone leaves the body
// - the rows the density exists to tighten - untouched.
func TestCompactTableDensityIsStyled(t *testing.T) {
	source := readInputCSS(t)

	compact := rootClasses(t, renderComponent(t, ui.Table(ui.TableOpts{
		Caption: "Rows", Density: ui.DensityCompact,
	})))
	assert.NotContains(t, compact, "table-compact",
		"the density class belongs on the <table>, not on the card wrapper")

	for _, selector := range []string{"table-compact th", "table-compact td"} {
		assert.Truef(t, hasOwnRule(source, selector),
			"input.css never declares .%s, so DensityCompact renders a table identical to the default",
			selector)
	}
}

// hasOwnRule reports whether class heads its own declaration block, rather than
// merely appearing inside another rule's selector list.
func hasOwnRule(source, class string) bool {
	return regexp.MustCompile(`\.` + regexp.QuoteMeta(class) + `\s*\{`).MatchString(source)
}

// addedClasses returns the classes present in got and absent from base.
func addedClasses(base, got []string) []string {
	seen := map[string]struct{}{}
	for _, class := range base {
		seen[class] = struct{}{}
	}

	var added []string
	for _, class := range got {
		if _, ok := seen[class]; !ok {
			added = append(added, class)
		}
	}
	return added
}

// rootClasses returns the class list on the first element of a rendered
// component.
func rootClasses(t *testing.T, html string) []string {
	t.Helper()

	marker := ` class="`
	start := strings.Index(html, marker)
	require.GreaterOrEqual(t, start, 0, "no class attribute in %q", html)
	start += len(marker)
	end := strings.Index(html[start:], `"`)
	require.Greater(t, end, 0, "unterminated class attribute in %q", html)

	return strings.Fields(html[start : start+end])
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
		"--shadow-raised", "--shadow-overlay", "--control-height-sm",
		"--control-height-md", "--control-height-lg", "--motion-fast",
		"--motion-base", "--ease-standard", "--disabled-opacity",
		"--color-chart-1", "--color-chart-6",
		"--color-neutral", "--color-neutral-fg", "--color-neutral-text",
		"--color-neutral-subtle", "--color-neutral-subtle-fg",
		"--color-neutral-border", "--color-danger-hover",
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
		"--color-focus-ring", "--color-overlay-scrim",
		"--shadow-raised", "--shadow-overlay",
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
		"--shadow-overlay", "--ease-standard",
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
		"--shadow-overlay", "--ease-standard",
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

	// The loader must not be an Alpine component: an element carries only one
	// x-data, and every widget needing an engine already uses that slot for its
	// own behaviour. As a component, a chart could be a chart or could request
	// its engine, never both.
	assert.NotContains(t, source, "Alpine.data",
		"the loader is plain DOM code so it never competes for the x-data slot")
	assert.Contains(t, source, `document.addEventListener("htmx:after:process"`,
		"content htmx inserts needs its engines too")
	assert.Contains(t, source, "uiEngineRequested",
		"htmx processes nested content more than once, so a root must be claimed before the request")

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

// A third-party instance must never be stored on an Alpine component. Alpine
// wraps component state in a reactive Proxy, and a library that walks its own
// deeply nested internals recurses through that proxy until the stack overflows,
// then corrupts whatever it did reach. The observed symptom pointed at Chart.js
// ("Maximum call stack size exceeded" inside alpine-csp, then "cannot set
// fullSize") while the cause was ours, which is why this is a test and not a
// comment.
func TestEngineAdaptersKeepInstancesOutOfReactiveState(t *testing.T) {
	adapters, err := filepath.Glob(filepath.Join("..", "..", "..", "static", "ui", "*.js"))
	require.NoError(t, err)
	require.NotEmpty(t, adapters)

	for _, path := range adapters {
		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		source := string(body)
		// Only files that actually construct a third-party instance are in
		// scope. The loader mentions data-ui-engine but owns no instances.
		if !strings.Contains(source, "new window.") {
			continue
		}
		assert.NotContains(t, source, "this.chart =",
			"%s stores a third-party instance in Alpine state; use a WeakMap keyed by the root", path)
		assert.Contains(t, source, "WeakMap",
			"%s must hold its instances outside reactive state", path)
		assert.Contains(t, source, "destroy()",
			"%s must release its instance: Alpine calls destroy during DOM cleanup", path)
	}
}

// A template must never hand-roll a component's root. Copying the Alpine hook,
// the data attributes and the responsive classes produces a second root that
// drifts the moment the component changes - and the copy silently loses
// whatever the component computes, which for PanelGroup is the handle bounds
// that stop one panel squeezing its neighbour below its floor.
//
// The Alpine component names are the tell: they belong to exactly one renderer
// each, so seeing one outside internal/web/templates/ui means a caller rebuilt
// that renderer's root by hand.
func TestNoTemplateHandRollsAComponentRoot(t *testing.T) {
	owned := map[string]string{
		"uiPanels":   "ui.PanelGroup",
		"uiGrid":     "ui.DataGrid",
		"uiTree":     "ui.Tree or ui.TreeGrid",
		"uiKanban":   "ui.Kanban",
		"uiCarousel": "ui.Carousel",
		"uiChart":    "a chart renderer",
		"uiCommand":  "ui.CommandPalette",
		"uiCalendar": "a calendar renderer",
	}
	files, err := filepath.Glob("*.templ")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, file := range files {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		for hook, renderer := range owned {
			assert.NotContains(t, string(body), `x-data="`+hook+`"`,
				"%s declares %s by hand; use %s instead", file, hook, renderer)
		}
	}
}

// The existing guard above catches a hand-rolled component root only when the
// copy carries an Alpine hook, because that is the tell it looks for. A form
// has no hook, so ui.Form was reachable past it - and a hand-rolled <form> is
// the most expensive copy in the catalog, because every way it goes wrong is
// invisible while scripting is on:
//
//   - `<form hx-post=...>` with no method attribute is a GET to a browser. With
//     scripting off the click navigates instead of posting, and the mutation
//     silently never happens.
//   - a form with no action posts to the page it is on, not to the endpoint the
//     htmx verb names, so even a form that does declare POST lands nowhere.
//   - the CSRF token reaches htmx through the header inherited from <body>,
//     which does not exist when htmx never loaded. A raw form that does post
//     therefore gets a 403.
//
// ui.Form answers all three from one place: it derives method and action from
// Attrs.HX, and renders the hidden token field for every unsafe method.
//
// Scope is the top-level directory, which is every production page template and
// every dev reference surface. internal/web/templates/ui is excluded because
// that is where forms are legitimately built: ui.Form itself, the
// `method="dialog"` close forms inside Dialog/Drawer/AlertDialog/CommandPalette,
// and the controls that wrap one.
//
// There are deliberately ZERO exemptions. The last candidate was the dev
// content scenario's hidden multipart upload target, which needed a native
// enctype ui.Form did not have; the answer was FormOpts.Enctype, not a skip.
func TestNoTemplateHandRollsAForm(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	// Line-by-line rather than whole-file, because two templates explain in
	// prose why they have no form. A rule that cannot tell markup from a
	// comment is a rule the first person to hit it deletes.
	tag := regexp.MustCompile(`<form\b`)
	for _, file := range files {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		for i, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if tag.MatchString(line) {
				assert.Fail(t,
					"hand-rolled form",
					"%s:%d — a template must not open a <form> itself\n"+
						"fix: @ui.Form(ui.FormOpts{...}) — the endpoint goes in Attrs.HX.Post "+
						"(method and action are derived from it, and the CSRF field is rendered), "+
						"hx-target/hx-swap go in Target/Swap, a native upload encoding goes in "+
						"Enctype and htmx's in Attrs.HX.Encoding",
					file, i+1)
			}
		}
	}
}

// No production surface may fall back to window.confirm.
//
// hx-confirm calls window.confirm: copy that cannot be translated, cannot be
// styled, cannot be asserted on, and which on some platforms offers a "prevent
// further dialogs" checkbox that silently disables every later confirmation.
// ConfirmAction is the product pattern, and the docs state the ban repo-wide.
//
// That claim was defended by two integration tests that each rendered ONE page
// and asserted the absence in that page's body - so the promise covered
// /app/projects, /app/files, and nothing else. A source scan is what actually
// covers the surface the sentence describes.
//
// Two spellings reach the attribute, and both are checked: a literal hx-confirm
// in markup, and a Confirm field on a ui.MenuItem or ui.HX literal. The dev
// gallery, its fragments and the scenario pages are excluded by design - their
// prompts are demonstrations rather than product copy, and frontend.md says so.
func TestNoProductionTemplateFallsBackToWindowConfirm(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	// A dev-only surface names itself. A new one that forgets the prefix is
	// scanned as production and fails here, which is the safe direction to be
	// wrong in.
	devPrefixes := []string{"dev_", "gallery", "scenario_"}
	confirms := regexp.MustCompile(`hx-confirm|\bConfirm:`)

	scanned := 0
	for _, file := range files {
		if slices.ContainsFunc(devPrefixes, func(p string) bool { return strings.HasPrefix(file, p) }) {
			continue
		}
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		scanned++
		// Line-by-line, skipping comments: several production templates explain
		// in prose why hx-confirm is gone, and a rule that cannot tell markup
		// from a comment is a rule the first person to hit it deletes.
		for i, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if confirms.MatchString(line) {
				assert.Fail(t,
					"window.confirm fallback",
					"%s:%d — a production surface must not gate an action with hx-confirm\n"+
						"fix: @ui.ConfirmAction(ui.ConfirmActionOpts{...}) — the same HX rides the "+
						"dialog's confirm control, and the copy becomes translatable and assertable",
					file, i+1)
			}
		}
	}
	require.Greater(t, scanned, 20,
		"the scan found suspiciously few production templates, so it is looking in the wrong place")
}
