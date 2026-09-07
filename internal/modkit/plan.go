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
	DestinationAuthored   DestinationClass = "authored"
	DestinationMigration  DestinationClass = "migration"
	DestinationGenerated  DestinationClass = "generated"
	DestinationIntent     DestinationClass = "intent"
	DestinationLock       DestinationClass = "lock"
	DestinationDependency DestinationClass = "dependency"
	DestinationTool       DestinationClass = "tool"
	DestinationContainer  DestinationClass = "container"
	// DestinationStaged marks a conflict artifact under the ignored scratch
	// root: the complete upstream candidate and its unified diff. They are
	// neither an authored payload nor a generated output — nothing renders
	// them and no module owns their path — but they are bytes leaving the
	// tool, so they are planned, journalled, written by Apply and rolled back
	// with everything else rather than through a second write path.
	DestinationStaged DestinationClass = "staged"
	// DestinationRemote marks a provider-side or deployment-side change
	// reported in the envelope: the change's path is a remote resource
	// identity, not a file in the project tree.
	DestinationRemote DestinationClass = "remote"
)

// RemoteChange is one provider-side or deployment-side change in the fixed
// vocabulary shared with internal/remote. Paths follow the remote path
// grammar (provider://<adapter>@<target>/<resource-id> or
// deploy://<deploy-id>/<resource-id>); values are never included.
type RemoteChange struct {
	ChangeID        string           `json:"change_id"`
	Path            string           `json:"path"`
	Kind            string           `json:"kind"`
	IdempotencyKey  string           `json:"idempotency_key"`
	DesiredHash     string           `json:"desired_hash"`
	ObservedVersion string           `json:"observed_version"`
	DependsOn       []string         `json:"depends_on"`
	SecretKeys      []string         `json:"secret_keys"`
	Class           DestinationClass `json:"class"`
}

// Change is one deterministic destination classification.
type Change struct {
	Path    string           `json:"path"`
	Module  string           `json:"module"`
	Source  string           `json:"source"`
	Kind    ChangeKind       `json:"kind"`
	Class   DestinationClass `json:"class"`
	SHA256  string           `json:"sha256"`
	Content []byte           `json:"-"`
	// Executable installs the target with the executable bit. It is derived
	// from the payload's declared FileClassScript, never from the source
	// file's mode on the publisher's disk: a manifest declares intent and the
	// installer reproduces it. Without it every `class:"script"` payload
	// landed 0644, so a created project could not run the shell scripts its
	// own `ggg test smoke` and `ggg test visual` invoke.
	Executable bool `json:"executable,omitempty"`
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
	previousDependencies []LockedDependency
	// rendered is the generator's output for this plan, computed once while
	// planning — the planner has to render to classify unrendered outputs and
	// to scan generated imports — and reused by the drift check, the journal
	// and the generation stage. Generation is a pure function of the plan, so
	// the reuse is exact; a plan built by hand carries none and every caller
	// renders as before. One apply used to render three times.
	rendered []GeneratedFile
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
	desiredProject.Ports = maps.Clone(currentProject.Ports)
	if op.Kind == OpUpdate {
		switch {
		case len(op.Modules) != 0:
			// Targeted form: `ggg update MODULES...` advances exactly the
			// named modules plus their required closure. A ref change is the
			// other exact form, and the two never combine.
			if op.TargetedRegistry != "" || op.RegistryRef != "" {
				return Plan{}, fmt.Errorf("update accepts either module operands or --registry with --ref, not both")
			}
		case op.TargetedRegistry != "":
			// Ref-change form: exactly one registry moves, named by
			// namespace, and no module operands are accepted.
			if op.RegistryRef == "" {
				return Plan{}, fmt.Errorf("update --registry requires --ref")
			}
			if err := setRegistryRef(desiredProject.Registries, op.TargetedRegistry, op.RegistryRef); err != nil {
				return Plan{}, err
			}
		case op.RegistryRef != "":
			if strings.TrimSpace(op.RegistryRef) == "" || op.RegistryRef != strings.TrimSpace(op.RegistryRef) {
				return Plan{}, fmt.Errorf("operation registry ref must be non-empty and trimmed")
			}
			if len(desiredProject.Registries) != 1 {
				return Plan{}, fmt.Errorf("update --ref requires exactly one configured registry; name the target with --registry")
			}
			desiredProject.Registries[0].Ref = op.RegistryRef
		}
	} else {
		if op.RegistryRef != "" {
			return Plan{}, fmt.Errorf("operation %q does not accept a registry ref", op.Kind)
		}
		if op.TargetedRegistry != "" {
			return Plan{}, fmt.Errorf("operation %q does not accept a targeted registry", op.Kind)
		}
	}
	if op.SetRegistries != nil {
		if op.Kind != OpSync {
			return Plan{}, fmt.Errorf("operation %q does not accept a registry set", op.Kind)
		}
		desiredProject.Registries = append([]ProjectRegistry(nil), op.SetRegistries...)
	}
	previousDeployment := currentProject.Deployment
	if op.SetProviders != nil {
		desiredProject.Providers = maps.Clone(op.SetProviders)
	}
	if op.SetDeployment != "" {
		if err := ValidateScopedProjectModuleID(op.SetDeployment); err != nil {
			return Plan{}, fmt.Errorf("deployment module %q is not a valid scoped module id", op.SetDeployment)
		}
		desiredProject.Deployment = op.SetDeployment
	}

	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	registrySources := make([]resolvedRegistry, 0, len(desiredProject.Registries))
	// Update in every form is the sanctioned way to move a pin, and a plan
	// that replaces the registry set is changing intent outright, so both skip
	// verification; every other operation must resolve to the commit the lock
	// already recorded.
	verifyPins := hasLock && op.Kind != OpUpdate && op.SetRegistries == nil
	for _, configured := range desiredProject.Registries {
		snapshot, resolveErr := e.resolveConfiguredRegistry(ctx, configured, existingLock, op.Offline, verifyPins)
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
	// Every file in the resolved registry tree must have a declaring owner.
	// This runs on the resolved snapshot rather than on payload bytes because
	// it is a statement about the CATALOG, not about the selected graph: the
	// signature covers the whole tree, so the gate has to as well. Refusing
	// here means `ggg sync`/`add`/`update` — and therefore `make check` —
	// report an undeclared payload before any byte is written, instead of a
	// later `registry build` folding it into a snapshot and `registry sign`
	// making it look deliberate.
	for _, source := range registrySources {
		if err := ValidateRegistryTreeOwnership(source.snapshot.FS); err != nil {
			return Plan{}, err
		}
	}
	// Operands become canonical ids exactly once, here, before any operation
	// reads them. `add` and `update` resolve against the catalog because that
	// is the set they can reach; `remove` resolved against the installed graph
	// on its own path above. Doing it here is what lets an operator type
	// either form and get the same plan: the unscoped form is a convenience,
	// and a convenience that is accepted and then not found is worse than one
	// that was never offered.
	if len(op.Modules) != 0 && (op.Kind == OpAdd || op.Kind == OpUpdate) {
		op.Modules, err = ResolveModuleIDs(op.Modules, CatalogSelectableIDs(catalog), "in the catalog")
		if err != nil {
			return Plan{}, err
		}
	}
	if op.Kind == OpAdd {
		desiredProject, err = projectAfterAdd(desiredProject, catalog, op.Modules)
		if err != nil {
			return Plan{}, err
		}
	}
	// A targeted update pins every module the closure does not advance:
	// their manifests, file digests, and snapshot provenance carry forward
	// from the lock even though the fresh snapshot re-publishes them.
	retained := map[string]struct{}{}
	var targeted targetedUpdate
	if op.Kind == OpUpdate && len(op.Modules) != 0 {
		closure, closureErr := planTargetedUpdate(ctx, existingLock, catalog, op.Modules)
		if closureErr != nil {
			return Plan{}, closureErr
		}
		targeted, retained = closure, closure.retained
	}
	graph, err := resolveSelectedGraph(ctx, desiredProject, catalog)
	if err != nil {
		return Plan{}, err
	}
	if op.Kind == OpUpdate && len(op.Modules) != 0 {
		graph.modules, err = overlayTargetedGraph(graph.modules, existingLock, targeted)
		if err != nil {
			return Plan{}, err
		}
	}
	if err := preflightNamespaces(ctx, graph.modules); err != nil {
		return Plan{}, err
	}
	runtimeOrders, err := RuntimeOrdersFor(ctx, graph.modules, desiredProject)
	if err != nil {
		return Plan{}, err
	}
	plannedModules := graph.modules
	if len(retained) != 0 {
		plannedModules = make([]Manifest, 0, len(graph.modules))
		for _, module := range graph.modules {
			if _, keep := retained[module.ID]; keep {
				continue
			}
			plannedModules = append(plannedModules, module)
		}
	}
	payloads, err := readPlannedPayloadsFromCatalog(ctx, catalog, plannedModules, canonicalPrefixes(catalog), modulePath)
	if err != nil {
		return Plan{}, err
	}
	// Every invariant below is a statement about the WHOLE selected graph, so
	// it needs the whole graph's source. `payloads` is not that on a targeted
	// update: plannedModules drops the retained modules, so the file map held
	// only the advanced closure while the validators still iterated every
	// manifest. The split is two and eleven, and the record here said four and
	// eight until an experiment corrected it.
	//
	// TWO of the fourteen can refuse because a declared file is ABSENT from the
	// map. Both require a declaration and fail if they never see it, the same
	// `found := false` over every key: ValidateShellSlotRenderers, in
	// shell_scan.go, and ValidateCSPContributionSources, in csp.go.
	// Those over-reported, and they are the false refusal — `ggg update
	// ggg/element/avatar` reported that `ggg/system/analytics-posthog`'s shell
	// slot named a renderer "no installed payload in that package declares",
	// because the package that declares it belongs to a retained module whose
	// bytes were absent.
	//
	// The other TWELVE ran vacuously: an absent payload is a scan that never
	// ran, so a forbidden pattern in a retained module's bytes passed.
	// ValidateCLIHandlerPackages, ValidateShellProviderNeutrality,
	// ValidateNoCredentialPresenceSelectors, ValidateSeamVendorHosts,
	// ValidateAssetReferences, ValidateUIComponentRequires,
	// ValidateRouteReferences and ValidateNoRecorderGoroutineHandoff each
	// spell `content, ok := files[target]; if !ok { continue }`;
	// ValidateCoreCLIPackages, ValidatePayloadAdapterImports,
	// ValidateConfigFieldOwnership and ValidateDerivationPackages only ever
	// iterate `range files`, so a shorter map is a shorter scan. The last two
	// of those read as declaration-shaped and are not: AssetReferences checks
	// declarations against a set built from `module.Files` — the manifest,
	// never this map — and its byte half skips an absent target, so a
	// truncated map only makes it scan less; DerivationPackages refuses when
	// its import graph reaches a package, and absent files remove edges, so
	// fewer edges means fewer chains means it passes.
	//
	// Ordering is not the reassurance it looks like: SEVEN of the twelve run
	// BEFORE the first of the two, and the two refuse only for a closure that
	// declares a shell slot or a CSP contribution. Measured at 1416dccd with
	// this fix absent, 697fbf42's loop test PASSED and its targeted `update`
	// reached apply on exactly such a closure. The over-reporters were a
	// property of this repository's graph, not a gate, and ten static
	// invariants were silently not running on real targeted updates in
	// v0.11.0.
	//
	// A retained module's bytes come from the TREE, not from the fresh
	// snapshot. The tree is what the plan leaves installed for it — its
	// manifest is overlaid from the lock for exactly the same reason — so
	// scanning re-published upstream bytes would refuse on a change this
	// operation is not adopting.
	scanFiles := payloadsAsFiles(payloads)
	for _, module := range graph.modules {
		if _, keep := retained[module.ID]; !keep {
			continue
		}
		for _, file := range module.Files {
			if file.Class == FileClassGenerated {
				continue
			}
			if _, present := scanFiles[file.Target]; present {
				continue
			}
			content, _, missing, readErr := CurrentTargetState(canonicalRoot, file.Target)
			if readErr != nil {
				return Plan{}, readErr
			}
			if missing {
				continue
			}
			scanFiles[file.Target] = content
		}
	}
	if err := ValidateCLIHandlerPackages(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateCoreCLIPackages(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidatePayloadAdapterImports(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateConfigFieldOwnership(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateShellProviderNeutrality(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateSeamVendorHosts(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateShellSlotRenderers(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateNoCredentialPresenceSelectors(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateAssetReferences(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateUIComponentRequires(graph.modules, catalog.Modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateRouteReferences(graph.modules, catalog.Modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateNoRecorderGoroutineHandoff(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateCSPContributionSources(graph.modules, scanFiles); err != nil {
		return Plan{}, err
	}
	if err := ValidateDerivationPackages(graph.modules, scanFiles, modulePath); err != nil {
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
	for _, payload := range payloads {
		moduleDeps := append([]GoDependency{{Module: e.canonicalModule}, {Module: modulePath}}, moduleByID[payload.module].Dependencies.Go...)
		if err := ValidateModuleDeclaredImports(payload.module, map[string][]byte{payload.file.Source: payload.content}, nil, moduleDeps); err != nil {
			return Plan{}, err
		}
	}
	// The thirteenth whole-graph check now reads the same source as the twelve
	// above. It used to read `payloads` alone, keyed by registry source path,
	// while `declaredImports` was assembled from every graph.modules entry
	// — whole graph on the declaration side, advanced closure on the byte
	// side, four lines under a comment asserting the opposite.
	//
	// Widened rather than scoped down, for three reasons. An undeclared import
	// added to a retained payload otherwise survives every targeted update:
	// the digest gate that would catch the divergence is deliberately skipped
	// for retained rows, whose provenance carries forward below, and the lock's
	// dependencies are recomputed from manifests rather than from imports, so
	// go.mod is written without it. `scanFiles` already holds those bytes, so
	// reading them here costs nothing. And leaving one check of thirteen on a
	// narrower source than its own declarations is the disagreement that
	// produced this defect in the first place.
	//
	// The key space widens with it, from registry source path to installed
	// target. The key is a label: ValidateDeclaredImports uses it for the
	// .go/.templ discriminator and for the "imported by" clause, and the
	// target is the path an operator can open. The per-module ownership check
	// above keeps source keys and stays scoped to the bytes this plan writes,
	// because it asks whether ONE module declares what its OWN payload
	// imports, and a retained module's answer is the one its own install
	// already gave.
	if err := ValidateDeclaredImports(scanFiles, nil, declaredImports); err != nil {
		return Plan{}, err
	}
	claims, err := normalizedClaims(op.Claims)
	if err != nil {
		return Plan{}, err
	}
	if op.SetDeployment != "" {
		module, ok := moduleByID[op.SetDeployment]
		if !ok {
			return Plan{}, fmt.Errorf("deployment module %q is not present in the resolved registries", op.SetDeployment)
		}
		if module.Kind != ModuleSystem || module.Runtime.System == nil || len(module.Runtime.Deploy) != 1 {
			return Plan{}, fmt.Errorf("deployment module %q must provide exactly one deploy target", op.SetDeployment)
		}
	}
	retiring := map[string]struct{}{}
	if op.SetDeployment != "" && previousDeployment != "" && previousDeployment != op.SetDeployment {
		if _, installed := oldModuleByID(existingLock, previousDeployment); installed {
			retiring[previousDeployment] = struct{}{}
		}
	}
	for id := range retiredAdapters(existingLock, desiredProject) {
		retiring[id] = struct{}{}
	}
	finalLock, changes, conflicts, diagnostics, err := reconcilePlannedState(
		ctx, canonicalRoot, snapshot, graph, payloads, existingLock, hasLock, claims, retiring, retained,
	)
	if err != nil {
		return Plan{}, err
	}
	finalLock.RegistryCommit, finalLock.Registries, finalLock.Snapshots = registryProvenance(registrySources, catalog, graph.modules)
	for i := range finalLock.Modules {
		if finalLock.Modules[i].Pending != nil {
			continue
		}
		if _, keep := retained[finalLock.Modules[i].ID]; keep {
			// Retained modules keep the provenance of the snapshot they were
			// installed from; reconcile already carried it forward verbatim.
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
	if op.Kind == OpUpdate && len(op.Modules) != 0 {
		// The lock identity is the live per-module provenance: updated
		// modules contribute the new snapshot digest, retained modules their
		// prior one. Recomputing from the rows keeps the envelope commit and
		// the lock honest about a mixed-snapshot tree.
		finalLock.RegistryCommit = registryCommitForModules(finalLock.Modules)
	}
	finalLock.RuntimeOrders = runtimeOrders
	finalLock.Providers = maps.Clone(desiredProject.Providers)
	finalLock.Ports = maps.Clone(desiredProject.Ports)
	effective, err := EffectiveDependencies(graph.modules)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve dependencies: %w", err)
	}
	finalLock.Dependencies = plannedDependencies(canonicalRoot, existingLock.Dependencies, graph.modules, effective.Go)
	finalLock.GoTools = effective.GoTools
	var rendered []GeneratedFile
	if e.generator != nil {
		preview := Plan{Operation: op, Root: canonicalRoot, RegistryCommit: finalLock.RegistryCommit, ModulePath: modulePath,
			Project: desiredProject, Lock: finalLock, Resolved: append([]string{}, graph.order...)}
		generated, renderErr := e.generator.Render(ctx, preview)
		if renderErr != nil {
			return Plan{}, fmt.Errorf("render generated imports: %w", renderErr)
		}
		rendered = generated
		sources := make([]string, 0, len(generated))
		for _, file := range generated {
			if strings.HasSuffix(file.Path, ".go") {
				sources = append(sources, file.Content)
			}
		}
		if err := ValidateDeclaredImports(scanFiles, sources, declaredImports); err != nil {
			return Plan{}, err
		}
		deletes, scanErr := unrenderedOutputChanges(canonicalRoot, rendered, changes)
		if scanErr != nil {
			return Plan{}, scanErr
		}
		changes = append(changes, deletes...)
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
	sortPlanOutputs(changes, conflicts)

	operation := op
	operation.Modules = append([]string{}, op.Modules...)
	order := append([]string{}, finalLock.Order...)
	return Plan{
		Operation: operation, Root: canonicalRoot, RegistryCommit: finalLock.RegistryCommit, ModulePath: modulePath,
		Project: desiredProject, Lock: finalLock, Resolved: append([]string{}, graph.order...), Order: order,
		Changes: changes, Diagnostics: diagnostics, Conflicts: conflicts,
		previousDependencies: append([]LockedDependency{}, existingLock.Dependencies...),
		rendered:             rendered,
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
		Dependencies:  []LockedDependency{}, GoTools: []string{}, Modules: locked,
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

// retiredAdapters lists the adapters an explicit provider replacement stops
// selecting: modules referenced by the committed lock's provider selections
// that the desired selections no longer name. Retiring them keeps one
// transaction honest — a replaced adapter's files leave with the selection,
// never after it.
func retiredAdapters(existing Lock, desired Project) map[string]struct{} {
	retired := map[string]struct{}{}
	if len(existing.Providers) == 0 {
		return retired
	}
	oldSelected := map[string]struct{}{}
	for _, selections := range existing.Providers {
		for _, choice := range []ProviderSelection{selections.Development, selections.Test, selections.Production} {
			if choice.Adapter != "" {
				oldSelected[choice.Adapter] = struct{}{}
			}
		}
	}
	for _, selections := range desired.Providers {
		for _, choice := range []ProviderSelection{selections.Development, selections.Test, selections.Production} {
			delete(oldSelected, choice.Adapter)
		}
	}
	for id := range oldSelected {
		if _, installed := oldModuleByID(existing, id); installed {
			retired[id] = struct{}{}
		}
	}
	return retired
}

// oldModuleByID reports whether one module is installed (non-tombstone) in
// the current lock.
func oldModuleByID(lock Lock, id string) (LockedModule, bool) {
	for _, module := range lock.Modules {
		if module.ID == id && module.Reason != TombstoneReason {
			return module, true
		}
	}
	return LockedModule{}, false
}

// payloadsAsFiles projects planned payloads onto the (target, content) pairs
// the CLI-handler scanner reads.
func payloadsAsFiles(payloads []plannedAuthoredPayload) map[string][]byte {
	files := make(map[string][]byte, len(payloads))
	for _, payload := range payloads {
		files[payload.file.Target] = payload.content
	}
	return files
}
