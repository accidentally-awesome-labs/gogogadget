// Self-host assertions. This file is declared self_host by ggg/system/server:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. A derivative may add design rules of its own,
// so only the publishing repository can hold AGENTS.md's list to the guard's.
//
// Spec: AGENTS.md line 227 (the design-system bullet, "fails the `templates`
// package on any of its seven rules") and line 219 (the `hx-confirm` scope in
// the htmx-statuses bullet).
//
// The document says seven things fail this package. Naming them is not
// enough: a rule that is documented and unenforced reads exactly like a rule
// that works. So each prohibition gets a fixture the guard must reject, and
// the fixture set is compared to `designRules()` as a set.

package templates

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
)

// agentsBullet returns the collapsed AGENTS.md bullet that opens with prefix.
func agentsBullet(t *testing.T, prefix string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("AGENTS.md carries no bullet opening %q; this check reads it, so restore the bullet or delete the check", prefix)
	return ""
}

// between returns the text between two literal anchors, both required.
func between(t *testing.T, text, after, before string) string {
	t.Helper()
	start := strings.Index(text, after)
	if start < 0 {
		t.Fatalf("AGENTS.md no longer contains the anchor %q that this check reads", after)
	}
	rest := text[start+len(after):]
	end := strings.Index(rest, before)
	if end < 0 {
		t.Fatalf("AGENTS.md no longer contains %q after %q", before, after)
	}
	return rest[:end]
}

// codeSpans returns every `code span` in order.
func codeSpans(text string) []string {
	var spans []string
	for _, match := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(text, -1) {
		spans = append(spans, match[1])
	}
	return spans
}

// numberWords maps the counts a sentence may spell out, so "seven rules" and
// a list of eight cannot coexist.
var numberWords = map[int]string{
	1: "one", 2: "two", 3: "three", 4: "four", 5: "five",
	6: "six", 7: "seven", 8: "eight", 9: "nine", 10: "ten",
}

// ruleFixtures is one snippet per prohibition that the guard must reject. The
// snippet is the honest form of the claim: the rule is not "documented", it is
// demonstrated to fire.
//
// Every class name is ASSEMBLED rather than written whole. `input.css` carries
// `@source "internal/web/templates"`, so Tailwind scans this file: a literal
// forbidden utility here compiles that utility into `static/app.css`, and a
// fixture for a prohibition must not ship the thing it prohibits. Measured —
// the first version of this file added four utilities to the built stylesheet
// and `make check` refused the drift.
var ruleFixtures = map[string]string{
	"raw hex colour":     `<div style="color: #ff00aa">x</div>`,
	"dark: variant":      `<div class="dark` + `:bg-black">x</div>`,
	"palette ramp":       `<div class="bg-` + `red-500">x</div>`,
	"numeric brand step": `<div class="bg-brand-` + `500">x</div>`,
	"! utility override": `<div class="!` + `p-0">x</div>`,
	"arbitrary length":   `<div class="w-[3` + `px]">x</div>`,
	"templ expression inside a quoted attribute": `<a href="/thing/{ id }">x</a>`,
}

// cleanFixture must be rejected by nothing. Without it a rule that matched
// everything would pass every fixture above.
const cleanFixture = `<div class="card p-4 text-sm"><span class="badge k-success">ok</span></div>`

// The documented list and the enforced list are one set, and every member has
// a fixture the guard rejects.
func TestAgentsDesignRulesAreDocumentedAndEnforced(t *testing.T) {
	bullet := agentsBullet(t, "- **Design system is three layers, one home each**")
	documented := codeSpans(between(t, bullet, "any of its seven rules: ", " — each"))
	if len(documented) == 0 {
		t.Fatal("the AGENTS.md design-system bullet lists no rule names")
	}

	rules := designRules()
	enforced := make([]string, 0, len(rules))
	for _, rule := range rules {
		enforced = append(enforced, rule.name)
	}

	got, want := slices.Clone(documented), slices.Clone(enforced)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("the AGENTS.md design-system bullet names\n  %v\nand designRules() enforces\n  %v\n"+
			"Name every rule the guard enforces, verbatim, and nothing it does not.", documented, enforced)
	}

	if word, ok := numberWords[len(rules)]; ok && !strings.Contains(bullet, "its "+word+" rules") {
		t.Errorf("designRules() enforces %d rules and the AGENTS.md bullet does not say \"its %s rules\".\n"+
			"Update the numeral in the design-system bullet.", len(rules), word)
	}

	// A fixture per prohibition: the named rule must reject it, and no other
	// rule may, so the fixture pins that rule rather than the scanner in
	// general.
	byName := map[string]designRule{}
	for _, rule := range rules {
		byName[rule.name] = rule
	}
	for _, name := range enforced {
		fixture, ok := ruleFixtures[name]
		if !ok {
			t.Errorf("designRules() enforces %q and this file carries no fixture for it.\n"+
				"Add one to ruleFixtures — an unexercised rule is a rule nobody has proved fires.", name)
			continue
		}
		if got := len(ruleMatches(byName[name], fixture)); got == 0 {
			t.Errorf("the %q rule does not reject its own fixture %q.\n"+
				"AGENTS.md says this fails the templates package, and it does not.", name, fixture)
		}
		for _, other := range rules {
			if other.name == name {
				continue
			}
			if len(ruleMatches(other, fixture)) > 0 {
				t.Errorf("the fixture for %q is also rejected by %q, so it does not pin one prohibition.\n"+
					"Narrow the fixture: %q", name, other.name, fixture)
			}
		}
	}
	for name := range ruleFixtures {
		if _, ok := byName[name]; !ok {
			t.Errorf("this file carries a fixture for %q and designRules() enforces no such rule.\n"+
				"Remove the fixture, or restore the rule.", name)
		}
	}

	for _, rule := range rules {
		if got := len(ruleMatches(rule, cleanFixture)); got > 0 {
			t.Errorf("the %q rule rejects the clean fixture %q, so it would reject conforming markup too",
				rule.name, cleanFixture)
		}
	}
}

// The `hx-confirm` prohibition and its true SCOPE. AGENTS.md states both
// halves — the ban on production pages and the surfaces the guard does not
// scan — because the previous wording read as a tree-wide NEVER while the
// guard covered one directory non-recursively with three exemptions. Both
// halves are asserted so neither can drift.
func TestAgentsHXConfirmScopeMatchesTheGuard(t *testing.T) {
	bullet := agentsBullet(t, "- **htmx statuses**")

	guard, err := os.ReadFile("designsystem_test.go")
	if err != nil {
		t.Fatalf("read designsystem_test.go: %v", err)
	}
	body := string(guard)
	at := strings.Index(body, "func TestNoProductionTemplateFallsBackToWindowConfirm(")
	if at < 0 {
		t.Fatal("designsystem_test.go declares no TestNoProductionTemplateFallsBackToWindowConfirm; AGENTS.md names it as the guard")
	}
	guardBody := body[at:]

	prefixes := regexp.MustCompile(`devPrefixes := \[\]string\{([^}]*)\}`).FindStringSubmatch(guardBody)
	if prefixes == nil {
		t.Fatal("the guard no longer declares devPrefixes; re-derive this check against its new shape")
	}
	var enforced []string
	for _, quoted := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(prefixes[1], -1) {
		enforced = append(enforced, quoted[1])
	}

	// The document writes a skipped prefix as a glob (`gallery*`); the guard
	// writes it as a prefix. Trailing `*` is the same statement.
	var documented []string
	for _, span := range codeSpans(between(t, bullet, "skipping ", "; inside ")) {
		documented = append(documented, strings.TrimSuffix(span, "*"))
	}
	sort.Strings(documented)
	sort.Strings(enforced)
	if strings.Join(documented, " ") != strings.Join(enforced, " ") {
		t.Errorf("AGENTS.md says the hx-confirm guard skips %v; the guard skips %v.\n"+
			"A guard that narrows without the document narrowing is a NEVER that stopped being one.", documented, enforced)
	}

	// Recursive, and over both file kinds: the document says every `.templ`
	// under the directory plus the hand-written `.go` beside them, which is
	// what makes the `ui/` clause below a constraint rather than a skip.
	if !strings.Contains(guardBody, "designSources(t)") {
		t.Error("the hx-confirm guard no longer scans the tree through designSources, " +
			"so AGENTS.md's statement of its scope is wrong")
	}
	if !strings.Contains(bullet, "every `.templ` under `internal/web/templates/`") {
		t.Error("the AGENTS.md htmx-statuses bullet must state the guard's scope as every `.templ` under " +
			"`internal/web/templates/`, or a reader takes the recursion for a top-level glob")
	}
	if !strings.Contains(bullet, "hand-written `.go`") {
		t.Error("the guard reads the hand-written `.go` files too - that is where both escape-hatch emitters " +
			"live - and the bullet must say so")
	}

	// The `ui/` clause. The document states it as a constraint on the value's
	// origin, and the guard enforces exactly that, so both spellings are
	// pinned: a document that dropped the clause would read as a blanket ban
	// on a layer that has to emit the attribute, and a guard that dropped it
	// would let a component write its own prompt.
	if !strings.Contains(bullet, "caller-supplied `Confirm` field, never from a literal") {
		t.Error("the AGENTS.md htmx-statuses bullet must state that inside `ui/` the attribute may only come " +
			"from a caller-supplied Confirm field, because that is the clause the guard enforces there")
	}
	if !strings.Contains(guardBody, `\.Confirm\b`) {
		t.Error("the guard no longer distinguishes a caller-supplied Confirm field from a literal, " +
			"so AGENTS.md's statement of the ui/ clause is wrong")
	}

	// And the escape hatch the document promises still works, behaviourally:
	// both spellings emit the attribute for a dev surface.
	viaAttrs := renderComponent(t, ui.Button(ui.ButtonOpts{
		Label: "Delete",
		Attrs: ui.Attrs{HX: ui.HX{Delete: "/x", Confirm: "Delete this?"}},
	}))
	assert.Contains(t, viaAttrs, `hx-confirm="Delete this?"`,
		"AGENTS.md says Attrs.HX.Confirm still emits hx-confirm for dev-only surfaces")

	viaMenuItem := renderComponent(t, ui.DropdownMenu(ui.DropdownMenuOpts{
		Label: "Actions",
		Items: []ui.MenuItem{{Label: "Delete", Confirm: "Delete this?", HX: ui.HX{Delete: "/x"}}},
	}))
	assert.Contains(t, viaMenuItem, `hx-confirm="Delete this?"`,
		"AGENTS.md says ui.MenuItem.Confirm still emits hx-confirm for demonstration menu items")

	require.Contains(t, bullet, "`Attrs.HX.Confirm`")
	require.Contains(t, bullet, "`ui.MenuItem.Confirm`")
}
