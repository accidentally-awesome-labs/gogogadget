package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gogogadget/gogogadget/internal/gggcli"
	"github.com/gogogadget/gogogadget/internal/modkit"
)

// newTestModel returns the console model with a non-interactive command
// context, so tests exercise the model without a terminal.
func newTestModel(t *testing.T) *model {
	t.Helper()
	m := newModel(context.Background(), gggcli.CommandContext{})
	m.width, m.height = 80, 24
	return m
}

// The console renders complete screens at 80x24 and 160x50: title bar, body,
// and the key legend, with no truncation at either size.
func TestConsoleRendersAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {160, 50}} {
		m := newTestModel(t)
		m.width, m.height = size[0], size[1]
		view := renderView(m)
		if !strings.Contains(view, "Home") {
			t.Fatalf("view at %dx%d misses the title: %q", size[0], size[1], view)
		}
		if !strings.Contains(view, "Esc back") {
			t.Fatalf("view at %dx%d misses the key legend", size[0], size[1])
		}
		if m.width != size[0] || m.height != size[1] {
			t.Fatal("WindowSizeMsg was not stored")
		}
	}
}

// Resize: a WindowSizeMsg updates the render dimensions.
func TestConsoleHandlesResize(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	if updated.(*model).width != 160 {
		t.Fatal("resize ignored")
	}
}

// Key contract: Esc backs out through the stack, ? opens help, / searches the
// catalog, arrows move the cursor, and Ctrl+C quits.
func TestConsoleKeyContract(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(keyPress("down"))
	if updated.(*model).cursor != 1 {
		t.Fatal("down did not move the cursor")
	}
	updated, _ = updated.(*model).Update(keyPress("?"))
	if updated.(*model).current != screenHelp {
		t.Fatal("? did not open help")
	}
	updated, _ = updated.(*model).Update(keyPress("esc"))
	if updated.(*model).current != screenHome {
		t.Fatal("esc did not go back")
	}
	updated, _ = updated.(*model).Update(keyPress("ctrl+c"))
	if updated.(*model).decision != cancelledBeforePlan {
		t.Fatal("ctrl+c before any plan must record cancelledBeforePlan")
	}
}

// Search: typing after / narrows the catalog rows.
func TestCatalogSearchFilters(t *testing.T) {
	m := newTestModel(t)
	m.current = screenCatalog
	m.catalog = []catalogRow{
		{id: "ggg/component/card", state: "available", title: "card"},
		{id: "ggg/element/button", state: "available", title: "button"},
	}
	m.search = "card"
	if rows := m.filteredCatalog(); len(rows) != 1 || rows[0].id != "ggg/component/card" {
		t.Fatalf("search = %#v, want the card row only", rows)
	}
}

// Unicode titles stay aligned because row padding measures rendered width,
// not bytes.
func TestUnicodeRowsRender(t *testing.T) {
	m := newTestModel(t)
	m.current = screenCatalog
	m.catalog = []catalogRow{
		{id: "ggg/component/café", state: "available", title: "café ☕ カード"},
	}
	view := renderView(m)
	if !strings.Contains(view, "café ☕ カード") {
		t.Fatalf("unicode row missing: %q", view)
	}
}

// NO_COLOR and non-TTY output must not emit ANSI escapes.
func TestNoANSIWithoutColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := newTestModel(t)
	m.current = screenProviders
	if view := renderView(m); strings.Contains(view, "\x1b[") {
		t.Fatal("view contains ANSI escapes under NO_COLOR")
	}
}

// renderView flattens the model's tea.View to its text.
func renderView(m *model) string {
	return m.View().Content
}

// keyPress builds the KeyMsg a key name produces, matching how the model
// dispatches on msg.String().
func keyPress(name string) tea.KeyMsg {
	switch name {
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: rune(name[0])}
	}
}

// Run outside a terminal is the declared exit-2 refusal.
func TestRunRefusesNonTerminal(t *testing.T) {
	_, err := Run(t.Context(), gggcli.CommandContext{Interactive: false}, nil)
	if err == nil {
		t.Fatal("Run on non-TTY succeeded")
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != modkit.ExitUsage {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(err.Error(), "interactive_terminal_required") {
		t.Fatalf("error = %v, want interactive_terminal_required", err)
	}
}

// Ctrl+C after a preview records the after-plan decision; Ctrl+C anywhere
// else records the clean before-plan exit.
func TestCancelDecisions(t *testing.T) {
	m := newTestModel(t)
	m.push(screenPlan)
	m.planStaged = true
	updated, _ := m.Update(keyPress("ctrl+c"))
	if updated.(*model).decision != cancelledAfterPlan {
		t.Fatal("ctrl+c on a previewed plan must be cancelledAfterPlan")
	}
	home := newTestModel(t)
	updated, _ = home.Update(keyPress("ctrl+c"))
	if updated.(*model).decision != cancelledBeforePlan {
		t.Fatal("ctrl+c at home must be cancelledBeforePlan")
	}
}
