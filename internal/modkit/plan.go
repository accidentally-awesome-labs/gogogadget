package modkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const defaultCanonicalModule = "github.com/gogogadget/gogogadget"

// Options configures a registry planning engine.
type Options struct {
	Source          Source
	Generator       Generator
	CanonicalModule string
}

// Engine resolves registry state and produces deterministic, read-only plans.
type Engine struct {
	source          Source
	generator       Generator
	canonicalModule string
}

// New constructs a registry planning engine.
func New(opts Options) *Engine {
	canonical := opts.CanonicalModule
	if canonical == "" {
		canonical = defaultCanonicalModule
	}
	return &Engine{source: opts.Source, generator: opts.Generator, canonicalModule: canonical}
}

// ChangeKind describes how planned bytes compare with the target tree.
type ChangeKind string

const (
	ChangeCreate    ChangeKind = "create"
	ChangeUpdate    ChangeKind = "update"
	ChangeDelete    ChangeKind = "delete"
	ChangeUnchanged ChangeKind = "unchanged"
)

// DestinationClass identifies the ownership class of a planned destination.
type DestinationClass string

const (
	DestinationAuthored  DestinationClass = "authored"
	DestinationMigration DestinationClass = "migration"
	DestinationGenerated DestinationClass = "generated"
	DestinationIntent    DestinationClass = "intent"
	DestinationLock      DestinationClass = "lock"
)

// Change is one deterministic destination classification.
type Change struct {
	Path    string           `json:"path"`
	Module  string           `json:"module"`
	Source  string           `json:"source"`
	Kind    ChangeKind       `json:"kind"`
	Class   DestinationClass `json:"class"`
	SHA256  string           `json:"sha256"`
	Content []byte           `json:"-"`
}

// Diagnostic is a stable machine-readable planner message.
type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Module   string `json:"module"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// Conflict describes one upstream/local edit collision.
type Conflict struct {
	Module         string `json:"module"`
	Path           string `json:"path"`
	BaseSHA256     string `json:"base_sha256"`
	LocalSHA256    string `json:"local_sha256"`
	UpstreamSHA256 string `json:"upstream_sha256"`
	CandidatePath  string `json:"candidate_path"`
	DiffPath       string `json:"diff_path"`
}

// StagedFile is an ignored conflict artifact written only by Apply.
type StagedFile struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Content []byte `json:"-"`
}

// ResolutionMode selects how one pending conflict is resolved.
type ResolutionMode string

const (
	ResolutionAcceptUpstream ResolutionMode = "accept-upstream"
	ResolutionKeepLocal      ResolutionMode = "keep-local"
	ResolutionMerged         ResolutionMode = "merged"
)

// Plan is the complete read-only result of resolving one operation.
type Plan struct {
	Operation      Operation    `json:"operation"`
	Root           string       `json:"root"`
	RegistryCommit string       `json:"registry_commit"`
	ModulePath     string       `json:"module_path"`
	Project        Project      `json:"project"`
	Lock           Lock         `json:"lock"`
	Resolved       []string     `json:"resolved"`
	Order          []string     `json:"order"`
	Changes        []Change     `json:"changes"`
	Diagnostics    []Diagnostic `json:"diagnostics"`
	Conflicts      []Conflict   `json:"conflicts"`
	Staged         []StagedFile `json:"staged"`
}
type plannedAuthoredPayload struct {
	module  string
	file    ManifestFile
	content []byte
}

// Plan resolves and verifies the selected registry graph without writing the target tree.
func (e *Engine) Plan(ctx context.Context, root string, op Operation) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if op.Kind != OpSync && op.Kind != OpAdd && op.Kind != OpUpdate && op.Kind != OpRemove {
		return Plan{}, fmt.Errorf("operation %q is not supported by the planner", op.Kind)
	}
	if e == nil || e.source == nil {
		return Plan{}, fmt.Errorf("planner source is required")
	}
	if !validPackagePath(e.canonicalModule) {
		return Plan{}, fmt.Errorf("canonical module path %q is invalid", e.canonicalModule)
	}

	canonicalRoot, err := canonicalProjectRoot(root)
	if err != nil {
		return Plan{}, err
	}
	currentProject, modulePath, existingLock, hasLock, err := readPlannerInputs(canonicalRoot)
	if err != nil {
		return Plan{}, err
	}
	if err := validateLockOwnership(existingLock, hasLock); err != nil {
		return Plan{}, err
	}
	if op.Kind == OpRemove {
		if !hasLock {
			return Plan{}, fmt.Errorf("remove requires an existing gogogadget.lock.json")
		}
		return e.planRemove(ctx, canonicalRoot, currentProject, modulePath, existingLock, op)
	}

	desiredProject := currentProject
	desiredProject.Modules = append([]string{}, currentProject.Modules...)
	desiredProject.Exclude = append([]string{}, currentProject.Exclude...)
	if op.Kind == OpUpdate {
		if len(op.Modules) != 0 {
			return Plan{}, fmt.Errorf("update does not accept module operands")
		}
		if op.RegistryRef != "" {
			if strings.TrimSpace(op.RegistryRef) == "" || op.RegistryRef != strings.TrimSpace(op.RegistryRef) {
				return Plan{}, fmt.Errorf("operation registry ref must be non-empty and trimmed")
			}
			desiredProject.Registry.Ref = op.RegistryRef
		}
	} else if op.RegistryRef != "" {
		return Plan{}, fmt.Errorf("operation %q does not accept a registry ref", op.Kind)
	}

	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	snapshot, err := e.source.Resolve(ctx, desiredProject.Registry.Repository, desiredProject.Registry.Ref)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve registry %s at %s: %w", desiredProject.Registry.Repository, desiredProject.Registry.Ref, err)
	}
	if strings.TrimSpace(snapshot.Commit) == "" || snapshot.FS == nil {
		return Plan{}, fmt.Errorf("resolved registry snapshot is incomplete")
	}
	catalog, err := LoadCatalog(snapshot.FS)
	if err != nil {
		return Plan{}, fmt.Errorf("load registry catalog: %w", err)
	}
	if op.Kind == OpAdd {
		desiredProject, err = projectAfterAdd(desiredProject, catalog, op.Modules)
		if err != nil {
			return Plan{}, err
		}
	}
	graph, err := resolveSelectedGraph(ctx, desiredProject, catalog)
	if err != nil {
		return Plan{}, err
	}
	if err := preflightNamespaces(ctx, graph.modules); err != nil {
		return Plan{}, err
	}

	payloads, err := readPlannedPayloads(ctx, snapshot.FS, graph.modules, e.canonicalModule, modulePath)
	if err != nil {
		return Plan{}, err
	}
	finalLock, changes, conflicts, staged, diagnostics, err := reconcilePlannedState(
		ctx, canonicalRoot, snapshot, graph, payloads, existingLock, hasLock,
	)
	if err != nil {
		return Plan{}, err
	}
	if !reflect.DeepEqual(currentProject, desiredProject) {
		intentContent, err := MarshalProject(desiredProject)
		if err != nil {
			return Plan{}, fmt.Errorf("marshal planned project intent: %w", err)
		}
		intentChange, err := classifyOwnedTarget(
			canonicalRoot, "gogogadget.json", "", "", DestinationIntent, intentContent, true,
		)
		if err != nil {
			return Plan{}, err
		}
		changes = append(changes, intentChange)
	}

	lockContent, err := MarshalLock(finalLock)
	if err != nil {
		return Plan{}, fmt.Errorf("marshal planned lock: %w", err)
	}
	lockChange, err := classifyOwnedTarget(canonicalRoot, "gogogadget.lock.json", "", "", DestinationLock, lockContent, true)
	if err != nil {
		return Plan{}, err
	}
	changes = append(changes, lockChange)
	sortPlanOutputs(changes, conflicts, staged)

	operation := op
	operation.Modules = append([]string{}, op.Modules...)
	order := append([]string{}, finalLock.Order...)
	return Plan{
		Operation: operation, Root: canonicalRoot, RegistryCommit: snapshot.Commit, ModulePath: modulePath,
		Project: desiredProject, Lock: finalLock, Resolved: append([]string{}, graph.order...), Order: order,
		Changes: changes, Diagnostics: diagnostics, Conflicts: conflicts, Staged: staged,
	}, nil
}

func buildPlannedLock(commit string, graph selectedGraph, files map[string][]LockedFile, migrations map[string][]LockedMigration) Lock {
	requiredBy := make(map[string][]string, len(graph.modules))
	for _, module := range graph.modules {
		requiredBy[module.ID] = []string{}
	}
	for _, module := range graph.modules {
		for _, dependency := range module.Requires {
			requiredBy[dependency] = append(requiredBy[dependency], module.ID)
		}
	}
	for id := range requiredBy {
		sort.Strings(requiredBy[id])
	}

	modules := append([]Manifest(nil), graph.modules...)
	sort.Slice(modules, func(i, j int) bool { return modules[i].ID < modules[j].ID })
	locked := make([]LockedModule, 0, len(modules))
	for _, module := range modules {
		locked = append(locked, LockedModule{
			ID: module.ID, Revision: module.Revision, Contract: module.Contract, SourceCommit: commit,
			Reason: graph.reasons[module.ID], RequiredBy: append([]string{}, requiredBy[module.ID]...),
			Manifest: module, Files: append([]LockedFile{}, files[module.ID]...),
			Migrations: append([]LockedMigration{}, migrations[module.ID]...),
		})
	}
	return Lock{Schema: 1, RegistryCommit: commit, Order: append([]string{}, graph.order...), Modules: locked}
}

func canonicalProjectRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("project root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	canonical = filepath.Clean(canonical)
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project root is not a directory")
	}
	return canonical, nil
}
