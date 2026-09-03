package modkit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// HealthFinding is one deterministic doctor observation about a project.
type HealthFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Module   string `json:"module"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// HealthReport is the complete read-only doctor result for one project root.
type HealthReport struct {
	Ok             bool            `json:"ok"`
	RegistryCommit string          `json:"registry_commit"`
	Conflicts      []Conflict      `json:"conflicts"`
	Findings       []HealthFinding `json:"findings"`
}

// Health inspects the committed project state without writing and without
// requiring a registry source. Pending update metadata must be backed by
// verifiable ignored candidate bytes: a fresh clone that carries conflict
// metadata without those bytes reports candidate_missing, and re-running the
// update at the pending registry commit re-stages them. Ok means no
// error-severity finding; warnings (a pending conflict awaiting resolution, a
// stale recorded digest, a missing diff artifact) still render a report.
func (e *Engine) Health(ctx context.Context, root string) (HealthReport, error) {
	if err := ctx.Err(); err != nil {
		return HealthReport{}, err
	}
	if e == nil {
		return HealthReport{}, fmt.Errorf("health requires an engine")
	}
	canonicalRoot, err := canonicalProjectRoot(root)
	if err != nil {
		return HealthReport{}, err
	}

	report := HealthReport{Ok: true, Conflicts: []Conflict{}, Findings: make([]HealthFinding, 0)}
	fail := func(finding HealthFinding) HealthReport {
		finding.Severity = "error"
		report.Findings = append(report.Findings, finding)
		report.Ok = false
		return report
	}

	if _, err := os.Stat(filepath.Join(canonicalRoot, "gogogadget.json")); errors.Is(err, fs.ErrNotExist) {
		return fail(HealthFinding{
			Code:    "project_missing",
			Message: "gogogadget.json is missing; run ggg init to adopt the module registry",
		}), nil
	} else if err != nil {
		return HealthReport{}, fmt.Errorf("stat project intent: %w", err)
	}
	if _, err := os.Stat(filepath.Join(canonicalRoot, "go.mod")); errors.Is(err, fs.ErrNotExist) {
		return fail(HealthFinding{
			Code:    "project_missing",
			Path:    "go.mod",
			Message: "go.mod is missing; the target module path cannot be resolved",
		}), nil
	} else if err != nil {
		return HealthReport{}, fmt.Errorf("stat go.mod: %w", err)
	}
	projectData, err := os.ReadFile(filepath.Join(canonicalRoot, "gogogadget.json"))
	if err != nil {
		return HealthReport{}, fmt.Errorf("read project intent: %w", err)
	}
	if _, err := ParseProject(projectData); err != nil {
		return fail(HealthFinding{
			Code: "project_invalid", Path: "gogogadget.json",
			Message: fmt.Sprintf("gogogadget.json is not canonical: %v", err),
		}), nil
	}

	lockData, err := os.ReadFile(filepath.Join(canonicalRoot, "gogogadget.lock.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return fail(HealthFinding{
			Code:    "lock_missing",
			Message: "gogogadget.lock.json is missing; run ggg sync to install the selected modules",
		}), nil
	} else if err != nil {
		return HealthReport{}, fmt.Errorf("read lock: %w", err)
	}
	lock, err := ParseLock(lockData)
	if err != nil {
		// A lock from a newer engine is not a malformed lock: reporting it as
		// "not canonical" would send the operator looking for corruption
		// instead of rebuilding. Doctor's job is to name the real remedy, so it
		// reports the refusal verbatim under its own code.
		var staleEngine EngineContractError
		if errors.As(err, &staleEngine) {
			return fail(HealthFinding{
				Code: "engine_stale", Path: "gogogadget.lock.json", Message: err.Error(),
			}), nil
		}
		return fail(HealthFinding{
			Code: "lock_invalid", Path: "gogogadget.lock.json",
			Message: fmt.Sprintf("gogogadget.lock.json is not canonical: %v", err),
		}), nil
	}

	report.RegistryCommit = lock.RegistryCommit
	report.Conflicts = conflictsFromLock(lock)
	for _, module := range lock.Modules {
		if module.Pending == nil {
			continue
		}
		if len(module.Pending.Conflicts) == 0 {
			report.Findings = append(report.Findings, HealthFinding{
				Code: "module_pinned", Severity: "warn", Module: module.ID,
				Message: "pinned by a pending update in its dependency closure; it advances on the next sync once the direct conflicts are resolved",
			})
			continue
		}
		files := lockedFilesByPath(module.Files)
		for _, conflict := range module.Pending.Conflicts {
			if err := ctx.Err(); err != nil {
				return HealthReport{}, err
			}
			report.Findings = append(report.Findings, HealthFinding{
				Code: "conflict_pending", Severity: "warn", Module: module.ID, Path: conflict.Path,
				Message: fmt.Sprintf(
					"upstream change conflicts with local edits; resolve with ggg resolve %s --path %s",
					module.ID, conflict.Path,
				),
			})
			inspectCandidateArtifact(canonicalRoot, module.ID, conflict.CandidatePath, conflict.CandidateSHA256, &report)
			inspectDiffArtifact(canonicalRoot, module.ID, conflict.DiffPath, &report)
			inspectConflictedTarget(canonicalRoot, module.ID, files[conflict.Path], &report)
		}
	}
	report.Ok = true
	for _, finding := range report.Findings {
		if finding.Severity == "error" {
			report.Ok = false
			break
		}
	}
	return report, nil
}

func inspectCandidateArtifact(root, module, path, wantDigest string, report *HealthReport) {
	if !strings.HasPrefix(path, conflictArtifactPrefix) {
		report.Findings = append(report.Findings, HealthFinding{
			Code: "candidate_path_invalid", Severity: "error", Module: module, Path: path,
			Message: fmt.Sprintf("candidate path must stay under %s", conflictArtifactPrefix),
		})
		return
	}
	_, digest, missing, err := CurrentTargetState(root, path)
	switch {
	case err != nil:
		report.Findings = append(report.Findings, HealthFinding{
			Code: "candidate_unreadable", Severity: "error", Module: module, Path: path,
			Message: fmt.Sprintf("conflict candidate cannot be read: %v", err),
		})
	case missing:
		report.Findings = append(report.Findings, HealthFinding{
			Code: "candidate_missing", Severity: "error", Module: module, Path: path,
			Message: fmt.Sprintf(
				"conflict candidate bytes are missing; run ggg update at registry commit %s to re-materialize them",
				report.RegistryCommit,
			),
		})
	case digest != wantDigest:
		report.Findings = append(report.Findings, HealthFinding{
			Code: "candidate_mismatch", Severity: "error", Module: module, Path: path,
			Message: fmt.Sprintf(
				"conflict candidate does not match its recorded digest; run ggg update at registry commit %s to re-materialize it",
				report.RegistryCommit,
			),
		})
	}
}

func inspectDiffArtifact(root, module, path string, report *HealthReport) {
	if !strings.HasPrefix(path, conflictArtifactPrefix) {
		report.Findings = append(report.Findings, HealthFinding{
			Code: "candidate_path_invalid", Severity: "error", Module: module, Path: path,
			Message: fmt.Sprintf("diff path must stay under %s", conflictArtifactPrefix),
		})
		return
	}
	_, _, missing, err := CurrentTargetState(root, path)
	if err != nil || missing {
		report.Findings = append(report.Findings, HealthFinding{
			Code: "diff_missing", Severity: "warn", Module: module, Path: path,
			Message: fmt.Sprintf(
				"conflict diff artifact is missing; run ggg update at registry commit %s to re-materialize it",
				report.RegistryCommit,
			),
		})
	}
}

func inspectConflictedTarget(root, module string, file LockedFile, report *HealthReport) {
	if file.Path == "" {
		return
	}
	_, digest, missing, err := CurrentTargetState(root, file.Path)
	switch {
	case err != nil:
		report.Findings = append(report.Findings, HealthFinding{
			Code: "owned_file_unreadable", Severity: "warn", Module: module, Path: file.Path,
			Message: fmt.Sprintf("conflicted file cannot be read: %v", err),
		})
	case missing:
		report.Findings = append(report.Findings, HealthFinding{
			Code: "owned_file_missing", Severity: "warn", Module: module, Path: file.Path,
			Message: "conflicted file is missing locally; restore it before resolving",
		})
	case digest != file.LocalSHA256:
		report.Findings = append(report.Findings, HealthFinding{
			Code: "conflict_stale", Severity: "warn", Module: module, Path: file.Path,
			Message: "conflicted file no longer matches the digest recorded in the lock; re-run ggg update to refresh pending metadata",
		})
	}
}
