package modkit

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

const defaultCanonicalModule = "github.com/gogogadget/gogogadget"

// Options configures a registry planning engine.
type Options struct {
	Source          Source
	Generator       Generator
	CanonicalModule string
	ToolRunner      ToolRunner
}

// Engine resolves registry state and produces deterministic, read-only plans.
type Engine struct {
	source          Source
	generator       Generator
	canonicalModule string
	toolRunner      ToolRunner
}

// New constructs a registry planning engine.
func New(opts Options) *Engine {
	canonical := opts.CanonicalModule
	if canonical == "" {
		canonical = defaultCanonicalModule
	}
	return &Engine{source: opts.Source, generator: opts.Generator, canonicalModule: canonical, toolRunner: opts.ToolRunner}
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
	Operation            Operation    `json:"operation"`
	Root                 string       `json:"root"`
	RegistryCommit       string       `json:"registry_commit"`
	ModulePath           string       `json:"module_path"`
	Project              Project      `json:"project"`
	Lock                 Lock         `json:"lock"`
	Resolved             []string     `json:"resolved"`
	Order                []string     `json:"order"`
	Changes              []Change     `json:"changes"`
	Diagnostics          []Diagnostic `json:"diagnostics"`
	Conflicts            []Conflict   `json:"conflicts"`
	Staged               []StagedFile `json:"staged"`
	previousDependencies []LockedDependency
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
	desiredProject.Registries = append([]ProjectRegistry{}, currentProject.Registries...)
	desiredProject.Modules = append([]string{}, currentProject.Modules...)
	desiredProject.Exclude = append([]string{}, currentProject.Exclude...)
	desiredProject.Providers = maps.Clone(currentProject.Providers)
	if op.Kind == OpUpdate {
		if len(op.Modules) != 0 {
			return Plan{}, fmt.Errorf("update does not accept module operands")
		}
		if op.RegistryRef != "" {
			if strings.TrimSpace(op.RegistryRef) == "" || op.RegistryRef != strings.TrimSpace(op.RegistryRef) {
				return Plan{}, fmt.Errorf("operation registry ref must be non-empty and trimmed")
			}
			desiredProject.Registries[0].Ref = op.RegistryRef
		}
	} else if op.RegistryRef != "" {
		return Plan{}, fmt.Errorf("operation %q does not accept a registry ref", op.Kind)
	}

	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	registrySources := make([]resolvedRegistry, 0, len(desiredProject.Registries))
	for _, configured := range desiredProject.Registries {
		snapshot, resolveErr := resolveSnapshot(ctx, e.source, configured, configured.Repository, configured.Ref)
		if resolveErr != nil {
			return Plan{}, fmt.Errorf("resolve registry %s at %s: %w", configured.Namespace, configured.Ref, resolveErr)
		}
		if strings.TrimSpace(snapshot.Commit) == "" || snapshot.FS == nil {
			return Plan{}, fmt.Errorf("resolved registry snapshot is incomplete")
		}
		registrySources = append(registrySources, resolvedRegistry{config: configured, snapshot: snapshot})
	}
	if len(registrySources) == 0 {
		return Plan{}, fmt.Errorf("project has no registries")
	}
	snapshot := registrySources[0].snapshot
	catalog, err := mergeResolvedCatalogs(ctx, registrySources)
	if err != nil {
		return Plan{}, err
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
	runtimeOrders, err := RuntimeOrdersFor(ctx, graph.modules, desiredProject)
	if err != nil {
		return Plan{}, err
	}
	payloads, err := readPlannedPayloadsFromCatalog(ctx, catalog, graph.modules, canonicalPrefixes(catalog), modulePath)
	if err != nil {
		return Plan{}, err
	}
	declaredImports := []GoDependency{{Module: e.canonicalModule}, {Module: modulePath}}
	if op.Kind == OpInit || (op.Kind == OpSync && hasLock) {
		if goMod, readErr := os.ReadFile(filepath.Join(canonicalRoot, "go.mod")); readErr == nil {
			if parsed, parseErr := modfile.Parse("go.mod", goMod, nil); parseErr == nil {
				for _, requirement := range parsed.Require {
					declaredImports = append(declaredImports, GoDependency{Module: requirement.Mod.Path})
				}
			}
		}
	}
	moduleByID := make(map[string]Manifest, len(graph.modules))
	for _, module := range graph.modules {
		moduleByID[module.ID] = module
		declaredImports = append(declaredImports, module.Dependencies.Go...)
	}
	authored := map[string][]byte{}
	for _, payload := range payloads {
		authored[payload.file.Source] = payload.content
		moduleDeps := append([]GoDependency{{Module: e.canonicalModule}, {Module: modulePath}}, moduleByID[payload.module].Dependencies.Go...)
		if err := ValidateModuleDeclaredImports(payload.module, map[string][]byte{payload.file.Source: payload.content}, nil, moduleDeps); err != nil {
			return Plan{}, err
		}
	}
	if err := ValidateDeclaredImports(authored, nil, declaredImports); err != nil {
		return Plan{}, err
	}
	claims, err := normalizedClaims(op.Claims)
	if err != nil {
		return Plan{}, err
	}
	finalLock, changes, conflicts, staged, diagnostics, err := reconcilePlannedState(
		ctx, canonicalRoot, snapshot, graph, payloads, existingLock, hasLock, claims,
	)
	if err != nil {
		return Plan{}, err
	}
	finalLock.RegistryCommit, finalLock.Registries, finalLock.Snapshots = registryProvenance(registrySources, catalog, graph.modules)
	for i := range finalLock.Modules {
		if finalLock.Modules[i].Pending != nil {
			continue
		}
		namespace := catalog.ModuleRegistries[finalLock.Modules[i].ID]
		for _, source := range registrySources {
			if source.config.Namespace == namespace {
				finalLock.Modules[i].RegistryNamespace = namespace
				finalLock.Modules[i].SourceCommit = source.snapshot.Commit
				finalLock.Modules[i].SnapshotSHA256 = source.snapshot.SnapshotSHA256
				if finalLock.Modules[i].SnapshotSHA256 == "" {
					finalLock.Modules[i].SnapshotSHA256 = source.snapshot.Commit
				}
			}
		}
	}
	finalLock.RuntimeOrders = runtimeOrders
	finalLock.Providers = maps.Clone(desiredProject.Providers)
	effective, err := EffectiveDependencies(graph.modules)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve dependencies: %w", err)
	}
	finalLock.Dependencies = plannedDependencies(canonicalRoot, existingLock.Dependencies, graph.modules, effective.Go)
	if e.generator != nil {
		preview := Plan{Operation: op, Root: canonicalRoot, RegistryCommit: snapshot.Commit, ModulePath: modulePath,
			Project: desiredProject, Lock: finalLock, Resolved: append([]string{}, graph.order...)}
		generated, renderErr := e.generator.Render(ctx, preview)
		if renderErr != nil {
			return Plan{}, fmt.Errorf("render generated imports: %w", renderErr)
		}
		sources := make([]string, 0, len(generated))
		for _, file := range generated {
			if strings.HasSuffix(file.Path, ".go") {
				sources = append(sources, file.Content)
			}
		}
		if err := ValidateDeclaredImports(authored, sources, declaredImports); err != nil {
			return Plan{}, err
		}
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
		previousDependencies: append([]LockedDependency{}, existingLock.Dependencies...),
	}, nil
}

func moduleNamespace(id string) string {
	namespace, _, _, ok := splitScopedModuleID(id)
	if ok {
		return namespace
	}
	return ""
}

func buildPlannedLock(commit string, graph selectedGraph, files map[string][]LockedFile, migrations map[string][]LockedMigration) Lock {
	requiredBy := make(map[string][]string, len(graph.modules))
	for _, module := range graph.modules {
		requiredBy[module.ID] = []string{}
	}
	for _, module := range graph.modules {
		for _, requirement := range module.Requires {
			requiredBy[requirement.ID] = append(requiredBy[requirement.ID], module.ID)
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
			ID: module.ID, Revision: module.Revision, Contract: module.Contract, RegistryNamespace: moduleNamespace(module.ID), SourceCommit: commit,
			SnapshotSHA256: commit, Reason: graph.reasons[module.ID], RequiredBy: append([]string{}, requiredBy[module.ID]...),
			Manifest: module, Files: append([]LockedFile{}, files[module.ID]...),
			Migrations: append([]LockedMigration{}, migrations[module.ID]...),
		})
	}
	return Lock{
		Schema: 2, RegistryCommit: commit, Registries: []LockedRegistry{},
		Snapshots: []LockedSnapshot{}, Order: append([]string{}, graph.order...),
		RuntimeOrders: RuntimeOrders{Development: append([]string{}, graph.order...), Test: append([]string{}, graph.order...), Production: append([]string{}, graph.order...)},
		Dependencies:  []LockedDependency{}, Modules: locked,
	}
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

// plannedDependencies records effective owners and preserves the baseline
// needed to distinguish managed requirements from user-owned requirements.
func plannedDependencies(root string, previous []LockedDependency, modules []Manifest, effective []GoDependency) []LockedDependency {
	previousBy := make(map[string]LockedDependency, len(previous))
	for _, dep := range previous {
		previousBy[dep.Module] = dep
	}
	current := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		if file, err := modfile.Parse("go.mod", data, nil); err == nil {
			for _, req := range file.Require {
				current[req.Mod.Path] = req.Mod.Version
			}
		}
	}
	owners := make(map[string][]string)
	for _, module := range modules {
		for _, dependency := range module.Dependencies.Go {
			owners[dependency.Module] = append(owners[dependency.Module], module.ID)
		}
	}
	out := make([]LockedDependency, 0, len(effective))
	for _, dependency := range effective {
		sort.Strings(owners[dependency.Module])
		locked := LockedDependency{Module: dependency.Module, ManagedVersion: dependency.Version, Owners: append([]string{}, owners[dependency.Module]...)}
		if prior, ok := previousBy[dependency.Module]; ok {
			locked.Preexisting, locked.BaselineVersion = prior.Preexisting, prior.BaselineVersion
		} else if version, ok := current[dependency.Module]; ok {
			locked.Preexisting, locked.BaselineVersion = true, version
		}
		out = append(out, locked)
	}
	return out
}

// normalizedClaims validates and de-duplicates the claimed paths. A claim names
// a project path, so an unsafe or absolute path is a usage error rather than a
// silently ignored flag.
func normalizedClaims(claims []string) (map[string]struct{}, error) {
	normalized := make(map[string]struct{}, len(claims))
	for _, claim := range claims {
		trimmed := strings.TrimSpace(claim)
		if trimmed == "" {
			return nil, fmt.Errorf("claim path must be non-empty")
		}
		if err := validateSafePath(trimmed); err != nil {
			return nil, fmt.Errorf("claim path %q: %w", trimmed, err)
		}
		normalized[trimmed] = struct{}{}
	}
	return normalized, nil
}
