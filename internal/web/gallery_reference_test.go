package web

import (
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reference pages are the only description of the component API that cannot
// drift from it: every string comes from the manifest that installed the
// component. These tests pin the parts a reader actually depends on - the exact
// signature, the summary, the keyboard contract, and the declared states - plus
// the two absences that must be stated rather than omitted. A section that
// silently disappears when its manifest field is empty is indistinguishable
// from a page that failed to render, and nobody reports a gap they never saw.

// The exact sentences the reference renders in place of missing data.
// Duplicated here deliberately: they are the contract a reader relies on, so a
// silent rewording should fail.
const (
	noKeyHandling = "Adds no key handling."
	noGuidance    = "No usage guidance declared"
)

// referenceNamed returns one installed component's reference.
//
// A missing subject fails rather than skips: these tests exist because the
// catalog can lose its documentation without anything else noticing, and a skip
// is how that loss stays invisible.
func referenceNamed(t *testing.T, name string) ui.Reference {
	t.Helper()
	reference, ok := ui.ReferenceFor(name)
	require.True(t, ok, "%q is not in the generated reference registry", name)
	return reference
}

// referencePath is where a component's reference page lives. It is built from
// the reference's own family, so the URL and the registry cannot disagree.
func referencePath(reference ui.Reference) string {
	return "/dev/gallery/" + string(reference.Family) + "/" + reference.Name
}

// assertRenders asserts the page carries a string from the manifest, allowing
// for HTML escaping: a summary containing an apostrophe is still that summary.
func assertRenders(t *testing.T, body, want, what string) {
	t.Helper()
	require.NotEmpty(t, want, "%s: nothing to look for", what)
	if strings.Contains(body, want) {
		return
	}
	assert.Contains(t, body, html.EscapeString(want), what)
}

// The signature is the one string on the page that must be exact. A reader
// copies it into their own code, so a normalised or re-typeset version is worse
// than none.
func TestComponentPageRendersTheExactSignature(t *testing.T) {
	s := integrationServer(t, nil)
	reference := referenceNamed(t, "badge")

	code, _, body := serve(t, s, "GET", referencePath(reference), nil, nil)

	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, reference.Signature, "badge declares no signature")
	assert.Contains(t, body, `data-testid="component-signature"`)
	assertRenders(t, body, reference.Signature, "the declared signature, verbatim")
}

func TestComponentPageRendersItsSummary(t *testing.T) {
	s := integrationServer(t, nil)
	reference := referenceNamed(t, "badge")

	code, _, body := serve(t, s, "GET", referencePath(reference), nil, nil)

	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, reference.Summary, "badge declares no summary")
	assert.Contains(t, body, `data-testid="component-summary"`)
	assertRenders(t, body, reference.Summary, "the declared summary")
}

// The usage example is derived from the signature rather than declared, so it
// cannot describe a call the signature does not accept. It is copyable because
// a reader who has to retype it will get the options type wrong.
func TestComponentPageOffersACopyableUsageExample(t *testing.T) {
	s := integrationServer(t, nil)
	reference := referenceNamed(t, "badge")

	code, _, body := serve(t, s, "GET", referencePath(reference), nil, nil)

	require.Equal(t, http.StatusOK, code)
	require.Contains(t, reference.Signature, "Badge", "badge's renderer is no longer Badge")
	assert.Contains(t, body, `data-testid="component-usage"`)
	assert.Contains(t, body, `data-testid="component-usage-copy"`)
	// The copy control carries the value on a data attribute, never in an
	// Alpine expression, so the example survives CSP.
	assertRenders(t, body, "@ui.Badge(ui.BadgeOpts{})", "the derived usage example")
}

// A component with real key handling must publish it. This is the fact a
// keyboard user needs and the one an implementer forgets.
func TestComponentPageRendersTheKeyboardContract(t *testing.T) {
	s := integrationServer(t, nil)

	for _, name := range []string{"tree", "data-grid"} {
		reference := referenceNamed(t, name)
		require.NotEmpty(t, reference.Keyboard, "%s declares no keyboard contract", name)

		code, _, body := serve(t, s, "GET", referencePath(reference), nil, nil)

		require.Equal(t, http.StatusOK, code, name)
		assert.Contains(t, body, `data-testid="component-keyboard"`, name)
		assertRenders(t, body, reference.Keyboard, name+"'s keyboard contract")
		assert.NotContains(t, body, noKeyHandling,
			"%s handles keys, so it must not claim otherwise", name)
	}
}

// Absent key handling is a fact, not missing data. Saying so is what lets a
// reader stop looking for the section.
func TestComponentPageStatesWhenItAddsNoKeyHandling(t *testing.T) {
	s := integrationServer(t, nil)
	reference := referenceNamed(t, "badge")
	require.Empty(t, reference.Keyboard, "badge is the no-key-handling subject")

	code, _, body := serve(t, s, "GET", referencePath(reference), nil, nil)

	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="component-keyboard"`)
	assert.Contains(t, body, noKeyHandling)
}

// The declared states are what a reviewer has to check and what the visual
// runner enumerates. A state the page omits is a state nobody looks at.
func TestComponentPageListsItsDeclaredStates(t *testing.T) {
	s := integrationServer(t, nil)
	reference := referenceNamed(t, "badge")
	require.NotEmpty(t, reference.States, "badge declares no states")

	code, _, body := serve(t, s, "GET", referencePath(reference), nil, nil)

	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="component-states"`)
	for _, state := range reference.States {
		assert.Contains(t, body, ">"+state+"</code>", "state %q must be listed", state)
	}
}

// Guidance is the remainder of a renderer's doc comment, so a single-sentence
// doc yields none. The page must still be whole: the reader came for the
// signature, and losing it because one field is empty is the failure this
// guards.
func TestComponentPageWithoutGuidanceIsStillComplete(t *testing.T) {
	s := integrationServer(t, nil)

	reference, found := firstReferenceWithoutGuidance()
	if !found {
		// Every installed component documents its guidance. Then the stated
		// gap must appear nowhere, or the page is inventing an absence.
		reference = referenceNamed(t, "badge")
		_, _, body := serve(t, s, "GET", referencePath(reference), nil, nil)
		assert.NotContains(t, body, noGuidance,
			"every component declares guidance, so no page may claim otherwise")
	}

	code, _, body := serve(t, s, "GET", referencePath(reference), nil, nil)

	require.Equal(t, http.StatusOK, code)
	// Complete means: the name, the signature, the summary, and every region a
	// reader navigates by are all present.
	assert.Contains(t, body, ">"+reference.Name+"</h1>")
	assertRenders(t, body, reference.Signature, "the signature of an undocumented component")
	assertRenders(t, body, reference.Summary, "the summary of an undocumented component")
	for _, region := range []string{
		"component-summary", "component-signature", "component-guidance",
		"component-keyboard", "component-states", "component-facts",
	} {
		assert.Contains(t, body, `data-testid="`+region+`"`, "region %s", region)
	}
	if found {
		assert.Contains(t, body, noGuidance, "the missing guidance must be stated")
	}
}

// firstReferenceWithoutGuidance finds a component whose doc comment is a single
// sentence. Preferring badge keeps the failure message stable when one exists.
func firstReferenceWithoutGuidance() (ui.Reference, bool) {
	if reference, ok := ui.ReferenceFor("badge"); ok && reference.Guidance == "" {
		return reference, true
	}
	for _, reference := range ui.ReferenceRegistry {
		if reference.Guidance == "" {
			return reference, true
		}
	}
	return ui.Reference{}, false
}

// The family page renders its components twice: once at the page measure and
// once squeezed into a sidebar-width column. A component that only holds
// together at full width is the commonest defect in a catalog, and a desktop
// review never sees it.
func TestFamilyPageRendersNormalAndConstrainedRegions(t *testing.T) {
	s := integrationServer(t, nil)
	reference := referenceNamed(t, "badge")

	code, _, body := serve(t, s, "GET", "/dev/gallery/"+string(reference.Family), nil, nil)

	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="family-normal"`)
	assert.Contains(t, body, `data-testid="family-constrained"`)
	// Distinct ids in each region: two elements answering to one id is an
	// ambiguity every runner resolves differently.
	assert.Contains(t, body, `data-testid="family-components"`)
	assert.Contains(t, body, `data-testid="constrained-components"`)
	assert.Contains(t, body, `data-testid="component-`+reference.Name+`"`)
	assert.Contains(t, body, `data-testid="constrained-component-`+reference.Name+`"`)
}

// The family listing states each component's summary beside its name: a list of
// kebab-case names tells a reader nothing about which one they want.
func TestFamilyPageRendersEachSummaryBesideItsName(t *testing.T) {
	s := integrationServer(t, nil)
	reference := referenceNamed(t, "badge")

	code, _, body := serve(t, s, "GET", "/dev/gallery/"+string(reference.Family), nil, nil)

	require.Equal(t, http.StatusOK, code)
	assertRenders(t, body, reference.Summary, "the summary in the family listing")
}

// A state list that silently omits hover and focus reads as complete. The page
// says which states it cannot render and why, so the omission is information
// rather than a gap.
func TestComponentPageNamesTheStatesItCannotRender(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := serve(t, s, "GET", "/dev/gallery/feedback/badge", nil, nil)

	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "component-states-note")
	assert.Contains(t, body, "applied by the browser")
	assert.Contains(t, body, "context controls")
}
