// Package gggcli is the typed ggg command platform: presentation and dispatch
// live here, while resolution, planning, and apply stay in internal/modkit.
//
// Every command — built-in, flag-driven, guided, or TUI — is one entry in the
// CommandSpec table, and every invocation converges on the same concrete
// Request or Mutation handed to the Controller. The Controller is the only
// engine owner: App and CommandContext carry *Controller, never a second
// modkit.Engine, so no caller can plan around the planner.
package gggcli

import (
	"errors"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// ExitCode reports the process exit code an error carries. Errors without a
// declared code are runtime failures.
func ExitCode(err error) int {
	// A stale-engine refusal outranks the code of whichever layer reported it.
	// Every lock read can raise it, and those layers otherwise relabel it — a
	// usage error for malformed input, a runtime error for a failed task step —
	// which is exactly the misreport this guard exists to stop.
	var stale modkit.EngineContractError
	if errors.As(err, &stale) {
		return exitRefusal
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return exitRuntime
}

// Declared exit codes. These are a public contract: automation branches on
// them. They mirror the modkit constants so the envelope and the process
// status can never disagree.
const (
	exitOK       = modkit.ExitOK
	exitRuntime  = modkit.ExitRuntime
	exitUsage    = modkit.ExitUsage
	exitRefusal  = modkit.ExitRefusal
	exitConflict = modkit.ExitConflict
	exitRollback = modkit.ExitRollback
)

// exitError carries a declared exit code alongside a message.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }
func (e exitError) ExitCode() int { return e.code }

func runtimeError(err error) error  { return exitError{code: exitRuntime, err: err} }
func refusalError(err error) error  { return exitError{code: exitRefusal, err: err} }
func conflictExit(err error) error  { return exitError{code: exitConflict, err: err} }
func rollbackError(err error) error { return exitError{code: exitRollback, err: err} }

type usageError string

func (e usageError) Error() string {
	return string(e)
}

func (usageError) ExitCode() int {
	return exitUsage
}

// ErrCancelled reports that the operator cancelled before any plan existed.
// Nothing was previewed and nothing was written, so the process exits 0.
var ErrCancelled = exitError{code: exitOK, err: errors.New("cancelled")}

// UserCancelledError reports a cancellation after a plan was previewed. The
// process exits with the declared refusal code and the envelope carries a
// `user_cancelled` diagnostic.
type UserCancelledError struct {
	// Command is the cancelled command name.
	Command string
}

func (e UserCancelledError) Error() string {
	return "user_cancelled"
}

func (UserCancelledError) ExitCode() int { return exitRefusal }

// errNotAvailable reports a typed request or mutation the controller recognizes
// but whose implementation ships with a later slice (project creation,
// provisioning, deployment). It is a refusal, never a silent no-op.
func errNotAvailable(what, when string) error {
	return refusalError(fmt.Errorf("%s is not available in this build; %s", what, when))
}
