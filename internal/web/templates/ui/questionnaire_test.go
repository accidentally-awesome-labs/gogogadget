package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wizardQuestions() []QuestionData {
	return []QuestionData{
		{ID: "size", Label: "How large is your team?", Type: QuestionSelect, Required: true,
			Options: []Option{{Value: "1", Label: "Just me"}, {Value: "10", Label: "2-10"}}},
		{ID: "goals", Label: "What will you ship?", Type: QuestionCheckbox,
			Options: []Option{{Value: "api", Label: "An API"}, {Value: "ui", Label: "A dashboard"}}},
		{ID: "notes", Label: "Anything else?", Type: QuestionLongText},
	}
}

func wizardOpts() QuestionnaireOpts {
	return QuestionnaireOpts{
		ID: "w", Questions: wizardQuestions(), Current: 2,
		SubmitURL: "/onboarding", Target: "#content",
	}
}

// The step lives on the server, so a reload resumes rather than restarting and
// the answers can actually be validated.
func TestStepIsSubmittedNotRemembered(t *testing.T) {
	html := renderComponent(t, Questionnaire(wizardOpts()))

	assert.Contains(t, html, `name="step" value="2"`)
	assert.Contains(t, html, `method="post"`)
	assert.Contains(t, html, `action="/onboarding"`)
}

// Only the active question renders. Showing every step at once makes the
// progress trail a lie.
func TestOnlyTheActiveQuestionRenders(t *testing.T) {
	html := renderComponent(t, Questionnaire(wizardOpts()))

	assert.Contains(t, html, "What will you ship?")
	assert.NotContains(t, html, `data-question="size"`)
	assert.NotContains(t, html, `data-question="notes"`)
}

// An out-of-range step would render a blank form with working buttons.
func TestOutOfRangeStepClamps(t *testing.T) {
	opts := wizardOpts()
	opts.Current = 99
	html := renderComponent(t, Questionnaire(opts))

	assert.Contains(t, html, `name="step" value="3"`)
	assert.Contains(t, html, "Anything else?")
}

// Radio and checkbox sets need the question as their group label, or a screen
// reader reads the options with no idea what is being asked.
func TestGroupedQuestionCarriesItsLegend(t *testing.T) {
	html := renderComponent(t, Question(QuestionOpts{Question: wizardQuestions()[1]}))

	assert.Contains(t, html, "<fieldset")
	assert.Contains(t, html, "<legend")
	assert.Contains(t, html, "What will you ship?")
}

// Multi-answer questions need the array name, or the server receives only the
// last checked box.
func TestCheckboxQuestionSubmitsEveryAnswer(t *testing.T) {
	html := renderComponent(t, Question(QuestionOpts{Question: wizardQuestions()[1]}))

	assert.Equal(t, 2, strings.Count(html, `name="goals[]"`))
	assert.NotContains(t, html, `name="goals"`)
}

// A single-answer group is not a set of independent toggles.
func TestRadioQuestionUsesOneName(t *testing.T) {
	html := renderComponent(t, Question(QuestionOpts{Question: QuestionData{
		ID: "plan", Label: "Which plan?", Type: QuestionRadio, Required: true,
		Options: []Option{{Value: "free", Label: "Free"}, {Value: "pro", Label: "Pro"}},
	}}))

	assert.Equal(t, 2, strings.Count(html, `type="radio"`))
	assert.Equal(t, 2, strings.Count(html, `name="plan"`))
}

// Revisiting a step must show the stored answer. Resetting to the first option
// silently changes what the user chose.
func TestStoredAnswerIsPreselected(t *testing.T) {
	html := renderComponent(t, Question(QuestionOpts{Question: QuestionData{
		ID: "size", Label: "Size?", Type: QuestionSelect, Value: "10",
		Options: []Option{{Value: "1", Label: "Just me"}, {Value: "10", Label: "2-10"}},
	}}))

	require.Equal(t, 1, strings.Count(html, "selected"))
	assert.Contains(t, html, `value="10" selected`)
}

// A multi-answer question restores its whole set, not just one value.
func TestStoredMultiAnswersAreChecked(t *testing.T) {
	html := renderComponent(t, Question(QuestionOpts{Question: QuestionData{
		ID: "goals", Label: "Goals?", Type: QuestionCheckbox, Values: []string{"api", "ui"},
		Options: []Option{{Value: "api", Label: "API"}, {Value: "ui", Label: "UI"}, {Value: "x", Label: "X"}},
	}}))

	assert.Equal(t, 2, strings.Count(html, "checked"))
}

// Skip is offered only where it can succeed. Offering it on a required question
// is a control that must fail.
func TestSkipIsOnlyOfferedWhereItWorks(t *testing.T) {
	required := renderComponent(t, Questionnaire(QuestionnaireOpts{
		ID: "w", Questions: wizardQuestions(), Current: 1, SubmitURL: "/o",
	}))
	optional := renderComponent(t, Questionnaire(wizardOpts()))

	assert.NotContains(t, required, `value="skip"`)
	assert.Contains(t, optional, `value="skip"`)
}

// Back is a submit, so returning keeps the answers. history.back() would replay
// a stale step.
func TestBackIsAServerRoundTrip(t *testing.T) {
	html := renderComponent(t, Questionnaire(wizardOpts()))

	assert.Contains(t, html, `name="action" value="back"`)
	assert.NotContains(t, html, "history.back")
}

// The first step has nowhere to go back to, and a disabled Back button is a
// control that explains nothing.
func TestFirstStepOffersNoBack(t *testing.T) {
	opts := wizardOpts()
	opts.Current = 1
	html := renderComponent(t, Questionnaire(opts))

	assert.NotContains(t, html, `value="back"`)
}

// The forward control keeps one name until the last step, where it states what
// it will do. Changing the wording mid-flow makes the user re-read it every step.
func TestForwardControlIsConsistentThenFinal(t *testing.T) {
	middle := renderComponent(t, Questionnaire(wizardOpts()))
	opts := wizardOpts()
	opts.Current = 3
	last := renderComponent(t, Questionnaire(opts))

	assert.Contains(t, middle, "Next")
	assert.Contains(t, middle, `value="next"`)
	assert.Contains(t, last, "Submit")
	assert.Contains(t, last, `value="submit"`)
	assert.NotContains(t, last, "Next")
}

// Validation is the server's. novalidate hands it the whole decision, so the
// browser cannot pass something the server would reject.
func TestValidationIsServerOwned(t *testing.T) {
	html := renderComponent(t, Questionnaire(wizardOpts()))

	assert.Contains(t, html, "novalidate")
}

// An unknown question type must normalize to a working control rather than
// rendering nothing.
func TestUnknownQuestionTypeStillRenders(t *testing.T) {
	html := renderComponent(t, Question(QuestionOpts{Question: QuestionData{
		ID: "q", Label: "Anything?", Type: QuestionType("interpretive-dance"),
	}}))

	assert.Contains(t, html, "<input")
	assert.Contains(t, html, "Anything?")
	assert.NotContains(t, html, "interpretive-dance")
}

// The server's error must be shown against the question that failed.
func TestQuestionShowsItsError(t *testing.T) {
	html := renderComponent(t, Question(QuestionOpts{Question: QuestionData{
		ID: "size", Label: "Size?", Required: true, Error: "Pick a team size.",
	}}))

	assert.Contains(t, html, "Pick a team size.")
	assert.Contains(t, html, `id="size-error"`)
}
