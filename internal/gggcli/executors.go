package gggcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// operation converts the typed graph mutation into the modkit operation it
// plans with.
func (m GraphMutation) operation() modkit.Operation {
	return modkit.Operation{
		Kind:        m.Kind,
		Modules:     m.Modules,
		RegistryRef: m.Ref,
		DryRun:      m.DryRun,
		PurgeData:   m.PurgeData,
	}
}

// previewOperation plans one modkit operation and, for a dry run, computes the
// generated-drift diagnostics the check gates on. Nothing writes.
func (c *Controller) previewOperation(ctx context.Context, command string, op modkit.Operation, dryRun bool) (Plan, error) {
	if _, err := c.loadProject(); err != nil {
		return Plan{}, err
	}
	engine, err := c.engine(op.Offline)
	if err != nil {
		return Plan{}, err
	}
	local, err := engine.Plan(ctx, c.rootDir(), op)
	if err != nil {
		return Plan{}, refusalError(err)
	}
	if dryRun {
		generatedDrift, err := engine.GeneratedDrift(ctx, local)
		if err != nil {
			return Plan{}, runtimeError(err)
		}
		local.Diagnostics = append(local.Diagnostics, generatedDrift...)
	}
	return c.planFor(command, &local, op.Offline), nil
}

// planDriftExit maps a dry-run plan onto its declared exit code: drift in the
// planner output or in generated aggregates is exit 4.
func planDriftExit(plan Plan) int {
	if plan.Local != nil && (countDrift(*plan.Local) > 0 || len(plan.Diagnostics) > 0) {
		return exitConflict
	}
	return exitOK
}

// previewInit refuses to clobber an existing project before anything writes.
func (c *Controller) previewInit(mutation InitMutation) error {
	path := filepath.Join(c.rootDir(), modkit.ProjectFileName)
	if _, err := os.Stat(path); err == nil {
		return refusalError(fmt.Errorf("%s already exists", modkit.ProjectFileName))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return runtimeError(err)
	}
	return nil
}

// applyInit writes the initial intent file and, when adopting, produces the
// initial lock through the same plan/apply path as sync.
func (c *Controller) applyInit(ctx context.Context, mutation InitMutation) (Result, error) {
	if err := c.previewInit(mutation); err != nil {
		return Result{}, err
	}
	registry := modkit.ProjectRegistry{Namespace: "ggg", Source: "github", Repository: mutation.Repository, Ref: mutation.Ref, PublicKey: mutation.PublicKey}
	if c.injected != nil {
		registry = modkit.ProjectRegistry{Namespace: "ggg", Source: "directory", Path: "."}
	} else if _, statErr := os.Stat(filepath.Join(c.rootDir(), "registry.json")); statErr == nil {
		registry = modkit.ProjectRegistry{Namespace: "ggg", Source: "directory", Path: "."}
	}
	project := modkit.Project{
		Schema:     2,
		Registries: []modkit.ProjectRegistry{registry},
		Modules:    []string{}, Exclude: []string{},
		Providers:  map[string]modkit.ProviderSelections{},
		Deployment: "",
	}
	data, err := modkit.MarshalProject(project)
	if err != nil {
		return Result{}, usageError(err.Error())
	}
	path := filepath.Join(c.rootDir(), modkit.ProjectFileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return Result{}, runtimeError(err)
	}
	if !mutation.Adopt {
		if len(mutation.Claims) > 0 {
			return Result{}, usageError("--claim only applies to `ggg init --adopt`")
		}
		return Result{Envelope: normalizeEnvelope(modkit.Envelope{Command: "init", OK: true, Exit: exitOK})}, nil
	}
	plan, err := c.previewOperation(ctx, "init", modkit.Operation{
		Kind: modkit.OpSync, Offline: mutation.Offline, Claims: mutation.Claims,
	}, false)
	if err != nil {
		var planned plannerFailure
		if errors.As(err, &planned) {
			return failureEnvelope("init", err)
		}
		return Result{}, err
	}
	result, err := c.Apply(ctx, plan)
	if err != nil {
		if result.Envelope.Command == "" {
			result.Envelope.Command = "init"
		}
		return result, err
	}
	result.Envelope.Command = "init"
	return Result{Envelope: normalizeEnvelope(result.Envelope)}, nil
}

// previewTask validates the trusted task operands before anything runs.
func (c *Controller) previewTask(mutation TaskMutation) error {
	switch mutation.Task {
	case "migrate-schema-1", "cache-prune":
		return nil
	case "identity-link":
		if mutation.Environment != "development" && mutation.Environment != "test" && mutation.Environment != "production" {
			return usageError("identity link: environment must be development, test, or production")
		}
		if mutation.Provider == "" || mutation.Subject == "" || (mutation.UserID == "") == (mutation.OrgID == "") {
			return usageError("usage: ggg identity link --environment ENV --provider PROVIDER --subject SUBJECT (--user USER_ID|--org ORG_ID)")
		}
		return nil
	default:
		return usageError(fmt.Sprintf("unknown task %q", mutation.Task))
	}
}

// executeCatalog lists the registry catalog.
func (c *Controller) executeCatalog(ctx context.Context, request CatalogRequest) (Result, error) {
	if request.Kind != "" && !knownModuleKind(request.Kind) {
		return Result{}, usageError(fmt.Sprintf("unknown kind %q", request.Kind))
	}
	catalog, commit, lock, err := c.readCatalog(ctx, request.Latest)
	if err != nil {
		return failureEnvelope("catalog", err)
	}
	states := installedStates(lock)

	entries := make([]catalogEntry, 0, len(catalog.Modules))
	for _, module := range catalog.Modules {
		if request.Kind != "" && string(module.Kind) != request.Kind {
			continue
		}
		state, isInstalled := states[module.ID]
		if request.Installed && !isInstalled {
			continue
		}
		if !isInstalled {
			state = "available"
		}
		entries = append(entries, catalogEntry{
			ID: module.ID, Kind: string(module.Kind), Title: module.Title,
			Revision: module.Revision, Contract: module.Contract, State: state,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

	return Result{
		Envelope: envelopeForPayload("catalog", commit),
		Payload:  map[string]any{"modules": entries},
	}, nil
}

type catalogEntry struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Revision int    `json:"revision"`
	Contract int    `json:"contract"`
	State    string `json:"state"`
}

// executeInfo reports one module's contract.
func (c *Controller) executeInfo(ctx context.Context, request InfoRequest) (Result, error) {
	id := request.ModuleID
	if err := modkit.ValidateScopedProjectModuleID(id); err != nil {
		return Result{}, usageError(fmt.Sprintf("%s: %v", id, err))
	}
	catalog, commit, lock, err := c.readCatalog(ctx, false)
	if err != nil {
		return failureEnvelope("info", err)
	}
	var found *modkit.Manifest
	for i := range catalog.Modules {
		if catalog.Modules[i].ID == id {
			found = &catalog.Modules[i]
			break
		}
	}
	if found == nil {
		return failureEnvelope("info", refusalError(fmt.Errorf("module %s is not in the registry", id)))
	}
	state, ok := installedStates(lock)[id]
	if !ok {
		state = "available"
	}
	return Result{
		Envelope: envelopeForPayload("info", commit),
		Payload: map[string]any{
			"module": found, "state": state,
			"links":  surfaceLinks(*found),
			"verify": verificationCommands(*found),
		},
	}, nil
}

// executeDiff reports file ownership state relative to the lock.
func (c *Controller) executeDiff(_ context.Context, request DiffRequest) (Result, error) {
	for _, id := range request.Modules {
		if err := modkit.ValidateInstallableModuleID(id); err != nil {
			return Result{}, usageError(fmt.Sprintf("%s: %v", id, err))
		}
	}
	entries, commit, err := c.collectDiff(request.Modules, request.Upstream)
	if err != nil {
		return failureEnvelope("diff", err)
	}
	return Result{
		Envelope: envelopeForPayload("diff", commit),
		Payload:  map[string]any{"files": entries},
	}, nil
}

// DiffEntry is one file's ownership state relative to the lock.
type DiffEntry struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	State  string `json:"state"`
	Diff   string `json:"diff,omitempty"`
}

func (c *Controller) collectDiff(modules []string, upstream bool) ([]DiffEntry, string, error) {
	data, err := os.ReadFile(filepath.Join(c.rootDir(), modkit.LockFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", refusalError(fmt.Errorf("%s not found; run `ggg sync` first", modkit.LockFileName))
	}
	if err != nil {
		return nil, "", runtimeError(err)
	}
	lock, err := modkit.ParseLock(data)
	if err != nil {
		return nil, "", usageError(err.Error())
	}

	wanted := make(map[string]struct{}, len(modules))
	for _, id := range modules {
		wanted[id] = struct{}{}
	}
	entries := make([]DiffEntry, 0)
	for _, module := range lock.Modules {
		if len(wanted) > 0 {
			if _, ok := wanted[module.ID]; !ok {
				continue
			}
		}
		for _, file := range module.Files {
			// A generated target has no base digest - the snapshot excludes
			// generated outputs, so there are no canonical bytes to compare
			// against. Comparing anyway meant every generated payload read
			// `modified` in every repository, forever. Whether generated
			// output is stale is `sync --check`'s question.
			if file.State == modkit.FileGenerated {
				continue
			}
			_, digest, missing, stateErr := modkit.CurrentTargetState(c.rootDir(), file.Path)
			state := "clean"
			switch {
			case stateErr != nil:
				state = "unreadable"
			case missing:
				state = "missing"
			case digest != file.BaseSHA256:
				state = "modified"
			}
			if state == "clean" && !upstream {
				continue
			}
			entry := DiffEntry{Module: module.ID, Path: file.Path, State: state}
			if upstream && module.Pending != nil {
				for _, conflict := range module.Pending.Conflicts {
					if conflict.Path == file.Path {
						entry.Diff = conflict.DiffPath
					}
				}
			}
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Module != entries[j].Module {
			return entries[i].Module < entries[j].Module
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, lock.RegistryCommit, nil
}

// executeDoctor runs the health check.
func (c *Controller) executeDoctor(ctx context.Context, _ DoctorRequest) (Result, error) {
	engine, err := c.engine(true)
	if err != nil {
		return Result{}, err
	}
	report, err := engine.Health(ctx, c.rootDir())
	if err != nil {
		return failureEnvelope("doctor", runtimeError(err))
	}
	diagnostics := make([]modkit.Diagnostic, 0, len(report.Findings))
	for _, finding := range report.Findings {
		diagnostics = append(diagnostics, modkit.Diagnostic{
			Code: finding.Code, Severity: finding.Severity,
			Module: finding.Module, Path: finding.Path, Message: finding.Message,
		})
	}
	exit := exitOK
	if !report.Ok {
		exit = exitRefusal
	}
	env := normalizeEnvelope(modkit.Envelope{
		Command: "doctor",
		OK:      report.Ok, RegistryCommit: report.RegistryCommit,
		Diagnostics: diagnostics, Exit: exit,
	})
	if exit != exitOK {
		return Result{Envelope: env}, refusalError(fmt.Errorf("doctor: %d finding(s)", len(diagnostics)))
	}
	return Result{Envelope: env}, nil
}

// executeRegistryValidate loads the shipped catalog and exercises the example
// closures. Progress goes to the human stream even under --json, because this
// takes minutes and a silent command that long reads as a hang.
func (c *Controller) executeRegistryValidate(ctx context.Context, request RegistryReadRequest) (Result, error) {
	catalog, err := modkit.LoadCatalog(os.DirFS(c.rootDir()))
	if err != nil {
		return failureEnvelope("registry validate", usageError(err.Error()))
	}
	ids := make([]string, 0, len(catalog.Modules))
	for _, module := range catalog.Modules {
		ids = append(ids, module.ID)
	}
	for _, profile := range catalog.Profiles {
		ids = append(ids, profile.ID)
	}
	sort.Strings(ids)

	progress, ok := progressSinkFromContext(ctx)
	if !ok {
		progress = os.Stdout
	}
	examples, err := modkit.ValidateExamples(ctx, c.rootDir(), progress)
	if err != nil {
		return failureEnvelope("registry validate", refusalError(err))
	}

	generated := make([]string, 0)
	diagnostics := make([]modkit.Diagnostic, 0, len(examples))
	for _, example := range examples {
		ids = append(ids, example.ID)
		generated = append(generated, example.Generated...)
		message := fmt.Sprintf(
			"%s closure %s: installed %d file(s), regenerated %d, compiled, %d tree entries restored byte for byte",
			example.Kind, strings.Join(example.Modules, "+"),
			len(example.Installed), len(example.Generated), example.Compared)
		if len(example.Retained) != 0 {
			message += ", retained migration(s) " + strings.Join(example.Retained, " ")
		}
		diagnostics = append(diagnostics, modkit.Diagnostic{
			Code: "example_closure_verified", Severity: "info", Module: example.ID, Message: message,
		})
	}
	sort.Strings(ids)
	sort.Strings(generated)
	return Result{Envelope: normalizeEnvelope(modkit.Envelope{
		Command: "registry validate",
		OK:      true, Resolved: ids, Generated: dedupeSorted(generated),
		Diagnostics: diagnostics, Exit: exitOK,
	})}, nil
}

// applyRegistryBuild refreshes manifest digests, rebuilds the indexes, writes
// the snapshot, and verifies vendored bytes.
func (c *Controller) applyRegistryBuild() (Result, error) {
	refreshed, err := modkit.RefreshManifestDigests(c.rootDir())
	if err != nil {
		return failureEnvelope("registry build", runtimeError(err))
	}
	built, discovered, err := modkit.BuildRegistryIndexes(c.rootDir())
	if err != nil {
		return failureEnvelope("registry build", runtimeError(err))
	}
	if _, snapshotErr := modkit.WriteRegistrySnapshot(c.rootDir()); snapshotErr != nil {
		return failureEnvelope("registry build", runtimeError(snapshotErr))
	}
	built = append(built, modkit.RegistrySnapshotPath)
	// Vendored bytes are verified here rather than in a separate audit, so
	// a swapped third-party file fails the build instead of shipping.
	if err := modkit.VerifyCatalogVendors(c.rootDir()); err != nil {
		return failureEnvelope("registry build", runtimeError(err))
	}
	return Result{Envelope: normalizeEnvelope(modkit.Envelope{
		Command: "registry build",
		OK:      true, Resolved: discovered, Generated: appendUnique(built, refreshed...), Exit: exitOK,
	})}, nil
}

// envelopeForPayload builds the fixed envelope a payload command reports.
func envelopeForPayload(command, commit string) modkit.Envelope {
	return normalizeEnvelope(modkit.Envelope{OK: true, Command: command, RegistryCommit: commit, Exit: exitOK})
}

// readCatalog resolves the registry the project points at and loads its
// catalog, plus the current lock when one exists.
func (c *Controller) readCatalog(ctx context.Context, latest bool) (modkit.Catalog, string, modkit.Lock, error) {
	project, err := c.loadProject()
	if err != nil {
		var coder interface{ ExitCode() int }
		// Browsing the registry must work before `ggg init`: fall back to the
		// default upstream rather than refusing to list anything.
		if !errors.As(err, &coder) || coder.ExitCode() != exitRefusal {
			return modkit.Catalog{}, "", modkit.Lock{}, err
		}
		project = modkit.Project{
			Schema:     2,
			Registries: []modkit.ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: DefaultRegistryRepository, Ref: "main", PublicKey: ""}},
			Modules:    []string{}, Exclude: []string{},
			Providers: map[string]modkit.ProviderSelections{}, Deployment: "",
		}
	}
	engine, err := c.engine(false)
	if err != nil {
		return modkit.Catalog{}, "", modkit.Lock{}, err
	}

	lock, hasLock := modkit.Lock{}, false
	if data, readErr := os.ReadFile(filepath.Join(c.rootDir(), modkit.LockFileName)); readErr == nil {
		if parsed, parseErr := modkit.ParseLock(data); parseErr == nil {
			lock, hasLock = parsed, true
		}
	}

	if len(project.Registries) == 0 {
		return modkit.Catalog{}, "", modkit.Lock{}, runtimeError(fmt.Errorf("project has no registries"))
	}
	registry := append([]modkit.ProjectRegistry(nil), project.Registries...)
	if hasLock && !latest && len(lock.Snapshots) > 0 {
		for i := range registry {
			for _, snapshot := range lock.Snapshots {
				if snapshot.Namespace == registry[i].Namespace {
					registry[i].Ref = snapshot.Commit
				}
			}
		}
	}
	catalog, commit, err := engine.Catalog(ctx, registry)
	if err != nil {
		return modkit.Catalog{}, "", modkit.Lock{}, runtimeError(err)
	}
	return catalog, commit, lock, nil
}

// installedStates maps each locked module to its observable state.
func installedStates(lock modkit.Lock) map[string]string {
	states := make(map[string]string, len(lock.Modules))
	for _, module := range lock.Modules {
		state := "clean"
		if module.Pending != nil {
			state = "conflicted"
		} else if module.Reason == modkit.TombstoneReason {
			state = "removed"
		}
		states[module.ID] = state
	}
	return states
}

func knownModuleKind(kind string) bool {
	switch modkit.ModuleKind(kind) {
	case modkit.ModuleElement, modkit.ModuleComponent, modkit.ModulePage, modkit.ModuleWorkflow, modkit.ModuleSystem:
		return true
	}
	return false
}

// surfaceLinks reports where a module can be looked at rather than read.
func surfaceLinks(m modkit.Manifest) map[string][]string {
	links := map[string][]string{}
	add := func(key, value string) {
		for _, existing := range links[key] {
			if existing == value {
				return
			}
		}
		links[key] = append(links[key], value)
	}
	for _, item := range m.Runtime.UI {
		if item.Family == "" || item.Name == "" {
			continue
		}
		add("gallery", "/dev/gallery/"+string(item.Family))
		add("gallery", "/dev/gallery/"+string(item.Family)+"/"+item.Name)
	}
	for _, scenario := range m.Runtime.Scenarios {
		if scenario.Slug != "" {
			add("scenario", "/dev/scenarios/"+scenario.Slug)
		}
	}
	// A page's own routes are the surface it is. Only GET is a place to look;
	// a POST pattern is an endpoint, not a page.
	for _, route := range m.Runtime.Routes {
		if route.Method == http.MethodGet && !strings.Contains(route.Pattern, "{") {
			add("route", route.Pattern)
		}
	}
	return links
}

// verificationCommands turns the declared test inventory into commands that
// can be run as written.
func verificationCommands(m modkit.Manifest) []string {
	var commands []string
	if packages := m.Tests.GoPackages; len(packages) > 0 {
		commands = append(commands, "go test -count=1 ./"+strings.Join(packages, " ./"))
	}
	for _, spec := range m.Tests.E2E {
		commands = append(commands, "cd e2e && npx playwright test "+spec+" --reporter=line")
	}
	for _, spec := range m.Tests.Accessibility {
		commands = append(commands, "cd e2e && npx playwright test "+spec+" --reporter=line")
	}
	if len(m.Tests.Visual) > 0 {
		// Visual is deliberately not a plain test invocation: baselines are
		// font-rendering sensitive and only match inside the pinned container.
		commands = append(commands, "./scripts/visual.sh")
	}
	for _, target := range m.Tests.Smoke {
		commands = append(commands, "make smoke BASE_URL="+target)
	}
	return commands
}

// progressSinkKey carries an optional progress sink for long-running reads.
type progressSinkKey struct{}

// WithProgressSink binds a progress sink to the context so Execute reports
// narration to a TUI instead of raw stdout.
func WithProgressSink(ctx context.Context, sink io.Writer) context.Context {
	return context.WithValue(ctx, progressSinkKey{}, sink)
}

func progressSinkFromContext(ctx context.Context) (io.Writer, bool) {
	sink, ok := ctx.Value(progressSinkKey{}).(io.Writer)
	return sink, ok
}
