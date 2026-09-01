package modkit

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// GeneratedDrift renders the aggregates the plan implies and compares them
// byte-for-byte with the tree. Planner changes alone cannot detect this: the
// generator, not the planner, produces generated output, so a hand-edited or
// deleted aggregate would otherwise pass the gate. It backs `sync --check` and
// every dry run.
func (e *Engine) GeneratedDrift(ctx context.Context, plan Plan) ([]Diagnostic, error) {
	if e.generator == nil {
		return nil, nil
	}
	rendered, err := e.generator.Render(ctx, plan)
	if err != nil {
		return nil, err
	}
	diagnostics := make([]Diagnostic, 0)
	expected := make(map[string]struct{}, len(rendered))
	for _, file := range rendered {
		expected[file.Path] = struct{}{}
		current, readErr := os.ReadFile(filepath.Join(plan.Root, filepath.FromSlash(file.Path)))
		switch {
		case errors.Is(readErr, fs.ErrNotExist):
			diagnostics = append(diagnostics, Diagnostic{
				Code: "generated_missing", Severity: "error", Path: file.Path,
				Message: "generated output is missing; run ggg sync",
			})
		case readErr != nil:
			return nil, readErr
		case string(current) != file.Content:
			diagnostics = append(diagnostics, Diagnostic{
				Code: "generated_drift", Severity: "error", Path: file.Path,
				Message: "generated output does not match the lock; run ggg sync and do not edit generated files",
			})
		}
	}

	// A stale aggregate left behind by a removed module is drift too: it still
	// compiles into the project while nothing owns it any more. `ggg sync`
	// deletes these, so reporting one here is an instruction the operator can
	// actually act on.
	stale, err := StaleRegistryOutputs(plan.Root, expected)
	if err != nil {
		return nil, err
	}
	for _, path := range stale {
		diagnostics = append(diagnostics, Diagnostic{
			Code: "generated_stale", Severity: "error", Path: path,
			Message: "generated output is no longer owned by the selected graph; run ggg sync to delete it",
		})
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].Path < diagnostics[j].Path })
	return diagnostics, nil
}
