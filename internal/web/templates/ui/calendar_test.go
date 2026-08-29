package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The native input is the control. With no JavaScript the user types a date and
// the form submits, because the platform already supplies the format, keyboard
// entry and the mobile date wheel - so the picker must never replace it.
func TestDatePickerKeepsTheNativeInputAuthoritative(t *testing.T) {
	html := renderComponent(t, DatePicker(DatePickerOpts{
		Field:        DateFieldOpts{Name: "starts_on", Value: "2026-01-15", Min: "2026-01-01", Required: true},
		Locale:       "en-GB",
		TriggerLabel: "Open calendar",
	}))

	assert.Contains(t, html, `type="date"`)
	assert.Contains(t, html, `name="starts_on"`)
	assert.Contains(t, html, `value="2026-01-15"`)
	assert.Contains(t, html, `min="2026-01-01"`)
	assert.Contains(t, html, "required")
	assert.NotContains(t, html, `type="hidden"`,
		"a picker that submits through a hidden field loses the value when the script fails")

	// The engine is requested by the root's presence; the trigger is hidden
	// until it loads, because a button that opens nothing is worse than none.
	assert.Contains(t, html, `data-ui-engine="cally"`)
	assert.Contains(t, html, `x-data="uiCalendar"`)
	triggerAt := strings.Index(html, "data-calendar-trigger")
	require.Positive(t, triggerAt)
	assert.Contains(t, html[triggerAt:triggerAt+60], "hidden")
	assert.Contains(t, html, `aria-label="Open calendar"`)
}

// The popover must live in the same subtree as the input, so an htmx swap
// removes both together and cannot leave a calendar bound to a detached field.
func TestCalendarPopoverSharesTheInputSubtree(t *testing.T) {
	html := renderComponent(t, DatePicker(DatePickerOpts{
		Field: DateFieldOpts{Name: "d"}, TriggerLabel: "Open",
	}))
	root := strings.Index(html, `data-ui="date-picker"`)
	popover := strings.Index(html, "data-calendar-popover")
	input := strings.Index(html, `type="date"`)
	require.Positive(t, root)
	require.Positive(t, popover)
	require.Positive(t, input)
	assert.Less(t, root, input)
	assert.Less(t, input, popover, "both live inside the one root that gets swapped")
}

// Locale, week start and disabled dates are declared on the root, because the
// adapter cannot invent them and the server must validate them again.
func TestCalendarPassesLocalizationThrough(t *testing.T) {
	html := renderComponent(t, DatePicker(DatePickerOpts{
		Field: DateFieldOpts{Name: "d"}, Locale: "es-ES", FirstDayOfWeek: 1,
		DisabledDates: []string{"2026-01-20", "2026-01-21"}, TriggerLabel: "Abrir",
	}))
	assert.Contains(t, html, `data-calendar-locale="es-ES"`)
	assert.Contains(t, html, `data-calendar-first-day="1"`)
	assert.Contains(t, html, `data-calendar-disabled="2026-01-20,2026-01-21"`)
}

// A range is two inputs, never one string: a combined value has to be parsed and
// re-validated, and a partially filled range cannot be represented at all.
func TestDateRangePickerKeepsTwoInputs(t *testing.T) {
	html := renderComponent(t, DateRangePicker(DateRangePickerOpts{
		Range: DateRangeFieldOpts{
			StartName: "from", EndName: "to", Legend: "Window",
			StartValue: "2026-02-01", EndValue: "2026-02-07",
		},
		TriggerLabel: "Open",
	}))
	assert.Equal(t, 2, strings.Count(html, `type="date"`))
	assert.Contains(t, html, `name="from"`)
	assert.Contains(t, html, `name="to"`)
	assert.Contains(t, html, `data-calendar-kind="range"`)
	assert.Contains(t, html, "<fieldset")
	assert.Contains(t, html, "<legend")
}

// A calendar is tabular data - days across, weeks down. A grid of divs looks
// identical and tells a screen reader nothing about which weekday a date falls
// on, which is the one thing a calendar exists to convey.
func TestMonthGridIsATableWithRealHeaders(t *testing.T) {
	html := renderComponent(t, MonthGrid(MonthGridOpts{
		ID: "jan", Month: "2026-01", Caption: "January 2026",
		DayNames: []string{"Monday", "Tuesday"},
		Weeks: [][]MonthDay{{
			{},
			{Date: "2026-01-01", Label: "1", Today: true},
		}},
	}))

	assert.Contains(t, html, "<table")
	assert.Contains(t, html, "<caption")
	assert.Equal(t, 2, strings.Count(html, `scope="col"`))
	// The visual abbreviation must never become the accessible name.
	assert.Contains(t, html, `<abbr title="Monday">Mo</abbr>`)
	assert.Contains(t, html, `datetime="2026-01-01"`)
	// aria-current lets a screen-reader user find today without counting cells.
	assert.Contains(t, html, `aria-current="date"`)
	// A padding cell holds no date and must not be announced as empty content.
	assert.Contains(t, html, `<td class="py-1" aria-hidden="true">`)
}

// An agenda list, not a pixel time grid: positioning entries by offset conveys
// duration visually and nothing at all to a screen reader, and collapses badly
// on a phone.
func TestSchedulerStatesTimesAsText(t *testing.T) {
	html := renderComponent(t, Scheduler(SchedulerOpts{
		ID: "s", Label: "Maintenance",
		Days: []ScheduleDay{
			{Date: "2026-02-02", Label: "Monday", Entries: []ScheduleEntry{
				{Start: "09:00", End: "09:30", MachineStart: "2026-02-02T09:00", Title: "Migration", Kind: KindWarn},
			}},
			{Date: "2026-02-03", Label: "Tuesday", Empty: "Nothing scheduled."},
		},
	}))

	assert.Contains(t, html, "09:00–09:30")
	assert.Contains(t, html, `datetime="2026-02-02T09:00"`)
	assert.Contains(t, html, `aria-labelledby="s-2026-02-02"`)
	assert.Contains(t, html, "Nothing scheduled.", "an empty day says so rather than vanishing")
	assert.NotContains(t, html, "position: absolute")
	assert.NotContains(t, html, "top:", "entries are not positioned by offset")
}

// Checkboxes inside a fieldset, not clickable divs: the selection submits with
// no JavaScript, every slot is keyboard reachable, and the legend associates the
// question with the answers.
func TestAvailabilityGridUsesRealCheckboxes(t *testing.T) {
	html := renderComponent(t, AvailabilityGrid(AvailabilityGridOpts{
		Name: "slots", Legend: "Pick your slots",
		Slots: []AvailabilitySlot{
			{Value: "09:00", Label: "09:00", Selected: true},
			{Value: "10:00", Label: "10:00"},
			{Value: "11:00", Label: "11:00", Disabled: true, DisabledReason: "already booked"},
		},
	}))

	assert.Equal(t, 3, strings.Count(html, `type="checkbox"`))
	assert.Equal(t, 3, strings.Count(html, `name="slots"`))
	assert.Contains(t, html, "<fieldset")
	assert.Contains(t, html, "Pick your slots")
	assert.Contains(t, html, "checked")
	assert.Contains(t, html, "disabled")
	// A greyed cell with no explanation leaves the user guessing whether the
	// slot is taken or invalid.
	assert.Contains(t, html, "already booked")
	assert.NotContains(t, html, "onclick")
}
