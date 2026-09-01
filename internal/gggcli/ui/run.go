// Package ui is the interactive console for ggg: Bubble Tea screens over the
// same gggcli.Controller the flags drive. It is contributed as the `ui`
// command by the ggg/system/cli-ui module, so a project without the console
// has a plain flag-only CLI and nothing else changes.
//
// Screens: Home, Catalog, Providers, Plan, Conflicts, Tasks, Diagnostics.
// Keys: Esc back, ? help, / search, Enter select/apply, Ctrl+C cancel. No
// mouse is required. Cancellation before a plan exists exits 0; cancelling
// after a plan was previewed is the declared user_cancelled refusal.
package ui

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/gogogadget/gogogadget/internal/gggcli"
	"github.com/gogogadget/gogogadget/internal/modkit"
)

// Run is the contributed command handler. It refuses to start outside a
// terminal: a UI that cannot read a keyboard is not a UI.
func Run(ctx context.Context, cc gggcli.CommandContext, _ []string) (gggcli.Result, error) {
	if !cc.Interactive {
		return gggcli.Result{}, terminalRequiredError{}
	}
	root := newModel(ctx, cc)
	program := tea.NewProgram(root, tea.WithContext(ctx))
	root.program = program
	final, err := program.Run()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return gggcli.Result{}, gggcli.ErrCancelled
		}
		return gggcli.Result{}, runtimeError{err}
	}
	if m, ok := final.(*model); ok {
		switch m.decision {
		case cancelledAfterPlan:
			// The operator saw the plan and declined: the declared refusal.
			return gggcli.Result{Envelope: modkit.Envelope{
				Command: "ui", Exit: modkit.ExitRefusal,
				Diagnostics: []modkit.Diagnostic{{Code: "user_cancelled", Severity: "info", Message: "cancelled after preview; nothing was written"}},
			}}, gggcli.UserCancelledError{Command: "ui"}
		case applied:
			fmt.Fprintf(cc.Out, "%s", m.status)
		}
	}
	return gggcli.Result{}, nil
}

// terminalRequiredError is the declared exit-2 refusal for a UI on a
// non-terminal stream.
type terminalRequiredError struct{}

func (terminalRequiredError) Error() string {
	return "interactive_terminal_required: the console requires a terminal"
}

func (terminalRequiredError) ExitCode() int { return modkit.ExitUsage }

// runtimeError carries an unexpected program failure as exit 1.
type runtimeError struct{ err error }

func (e runtimeError) Error() string { return e.err.Error() }
func (e runtimeError) Unwrap() error { return e.err }
func (runtimeError) ExitCode() int   { return modkit.ExitRuntime }
