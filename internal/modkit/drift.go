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
//
// An aggregate the render no longer produces is NOT reported here. It used to
// be, as `generated_stale` with the instruction "run ggg sync" — prose the
// human renderer printed without the path, so the gate named nothing. The
// planner now classifies those outputs instead: a stale one is a named
// `delete`/`generated` change in the same plan this function checks, and an
// authored file at a generated name refuses the plan outright. Reporting it
// twice would double-count the same fact in `sync --check`'s summary.
func (e *Engine) GeneratedDrift(ctx context.Context, plan Plan) ([]Diagnostic, error) {
	if e.generator == nil {
		return nil, nil
	}
	rendered := plan.rendered
	if len(rendered) == 0 {
		var err error
		rendered, err = e.generator.Render(ctx, plan)
		if err != nil {
			return nil, err
		}
	}
	diagnostics := make([]Diagnostic, 0)
	for _, file := range rendered {
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
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].Path < diagnostics[j].Path })
	return diagnostics, nil
}
