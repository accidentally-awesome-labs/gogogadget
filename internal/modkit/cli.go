package modkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project and lock file names. These are a public contract: operators and
// automation reference them by name.
const (
	ProjectFileName = "gogogadget.json"
	LockFileName    = "gogogadget.lock.json"
)

// DefaultRegistryRepository is the upstream catalog a fresh project points at.
const DefaultRegistryRepository = "gogogadget/gogogadget"

// CLI is the command-line interface for module registry operations. Every
// mutating command converges on the same planner and Apply transaction, so the
// human and --json renderings are two views of one Plan, never a second
// interpretation of it.
type CLI struct {
	Out     io.Writer
	Err     io.Writer
	Version string
	// Root is the project directory. Empty means the process working directory.
	Root string
	// Engine overrides the engine the CLI would otherwise build from the
	// project's registry declaration. Tests inject an offline source here.
	Engine *Engine
}

// Envelope is the noninteractive result contract. Its key set is fixed: machine
// consumers depend on every field being present, so absent data encodes as an
// empty collection rather than a missing key.
type Envelope struct {
	OK             bool         `json:"ok"`
	Command        string       `json:"command"`
	RunID          string       `json:"run_id"`
	RegistryCommit string       `json:"registry_commit"`
	Resolved       []string     `json:"resolved"`
	Changes        []Change     `json:"changes"`
	Generated      []string     `json:"generated"`
	Conflicts      []Conflict   `json:"conflicts"`
	Diagnostics    []Diagnostic `json:"diagnostics"`
	Exit           int          `json:"exit"`
}

// exitError carries a declared exit code alongside a message.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }
func (e exitError) ExitCode() int { return e.code }

// Declared exit codes. These are a public contract: automation branches on them.
const (
	exitOK       = 0
	exitRuntime  = 1
	exitUsage    = 2
	exitRefusal  = 3
	exitConflict = 4
	exitRollback = 5
)

func runtimeError(err error) error  { return exitError{code: exitRuntime, err: err} }
func refusalError(err error) error  { return exitError{code: exitRefusal, err: err} }
func conflictExit(err error) error  { return exitError{code: exitConflict, err: err} }
func rollbackError(err error) error { return exitError{code: exitRollback, err: err} }

// Run executes the command described by args.
func (c CLI) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing command")
	}
	command, rest := args[0], args[1:]

	switch command {
	case "version":
		return c.runVersion(rest)
	case "init":
		return c.runInit(ctx, rest)
	case "catalog":
		return c.runCatalog(ctx, rest)
	case "info":
		return c.runInfo(ctx, rest)
	case "add":
		return c.runGraphMutation(ctx, "add", OpAdd, rest)
	case "remove":
		return c.runGraphMutation(ctx, "remove", OpRemove, rest)
	case "update":
		return c.runGraphMutation(ctx, "update", OpUpdate, rest)
	case "sync":
		return c.runSync(ctx, rest)
	case "diff":
		return c.runDiff(ctx, rest)
	case "resolve":
		return c.runResolve(ctx, rest)
	case "doctor":
		return c.runDoctor(ctx, rest)
	case "registry":
		return c.runRegistry(ctx, rest)
	default:
		return usageError(fmt.Sprintf("unknown command %q", command))
	}
}

func (c CLI) out() io.Writer {
	if c.Out == nil {
		return io.Discard
	}
	return c.Out
}

func (c CLI) root() string {
	if strings.TrimSpace(c.Root) == "" {
		return "."
	}
	return c.Root
}

// flagSet builds a flag set that reports usage failures as exit 2 and never
// prints its own usage text behind our back.
func (c CLI) flagSet(name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	return set
}

// parseFlags parses flags that may appear before, after, or between positional
// arguments. Go's flag package stops at the first non-flag token, which would
// make `ggg info component/card --json` silently drop --json.
func parseFlags(set *flag.FlagSet, args []string) ([]string, error) {
	positional := make([]string, 0, len(args))
	for {
		if err := set.Parse(args); err != nil {
			return nil, err
		}
		rest := set.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func (c CLI) runVersion(args []string) error {
	if len(args) != 0 {
		return usageError("usage: ggg version")
	}
	version := c.Version
	if version == "" {
		version = "dev"
	}
	_, err := fmt.Fprintf(c.out(), "ggg %s\n", version)
	return err
}

// engine returns the engine to plan with. A caller-supplied engine wins so
// tests and offline runs never reach the network.
func (c CLI) engine(offline bool) (*Engine, error) {
	if c.Engine != nil {
		return c.Engine, nil
	}
	// A tree that carries its own registry.json is a self-hosting registry: the
	// upstream repository, or a derivative that vendors the catalog. Resolving
	// from the tree keeps `make check` working in a fresh clone with no network
	// and no credentials.
	if _, statErr := os.Stat(filepath.Join(c.root(), "registry.json")); statErr == nil {
		return New(Options{
			Source:    DirectorySource{Root: c.root()},
			Generator: RegistryGenerator{},
		}), nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, runtimeError(statErr)
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, runtimeError(fmt.Errorf("locate registry cache: %w", err))
	}
	return New(Options{
		Source: GitHubSource{
			CacheDir: filepath.Join(cache, "ggg", "registry"),
			Offline:  offline,
		},
		Generator: RegistryGenerator{},
	}), nil
}

// loadProject reads the intent file. A missing file is a refusal, not a crash:
// the diagnostic names the command that creates one.
func (c CLI) loadProject() (Project, error) {
	data, err := os.ReadFile(filepath.Join(c.root(), ProjectFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return Project{}, refusalError(fmt.Errorf("%s not found; run `ggg init` first", ProjectFileName))
	}
	if err != nil {
		return Project{}, runtimeError(err)
	}
	project, err := ParseProject(data)
	if err != nil {
		return Project{}, usageError(err.Error())
	}
	return project, nil
}

// emit renders one result. JSON is the fixed envelope; human output renders the
// same plan fields so the two can never disagree.
func (c CLI) emit(command string, asJSON bool, env Envelope) error {
	env.Command = command
	if env.Resolved == nil {
		env.Resolved = []string{}
	}
	if env.Changes == nil {
		env.Changes = []Change{}
	}
	if env.Generated == nil {
		env.Generated = []string{}
	}
	if env.Conflicts == nil {
		env.Conflicts = []Conflict{}
	}
	if env.Diagnostics == nil {
		env.Diagnostics = []Diagnostic{}
	}
	if env.RunID == "" {
		env.RunID = envelopeRunID(env)
	}
	if !asJSON {
		return c.renderHuman(env)
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return runtimeError(err)
	}
	_, err = fmt.Fprintf(c.out(), "%s\n", data)
	return err
}

// envelopeRunID derives a stable id from the envelope's content, so the same
// plan reports the same run id and a changed plan cannot reuse one.
func envelopeRunID(env Envelope) string {
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\n%s\n%d\n", env.Command, env.RegistryCommit, env.Exit)
	for _, id := range env.Resolved {
		fmt.Fprintf(sum, "resolved:%s\n", id)
	}
	for _, change := range env.Changes {
		fmt.Fprintf(sum, "change:%s:%s:%s\n", change.Path, change.Kind, change.Class)
	}
	for _, conflict := range env.Conflicts {
		fmt.Fprintf(sum, "conflict:%s:%s\n", conflict.Module, conflict.Path)
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

func (c CLI) renderHuman(env Envelope) error {
	out := c.out()
	if env.RegistryCommit != "" {
		if _, err := fmt.Fprintf(out, "registry %s\n", env.RegistryCommit); err != nil {
			return err
		}
	}
	for _, change := range env.Changes {
		if change.Kind == ChangeUnchanged {
			continue
		}
		if _, err := fmt.Fprintf(out, "  %-9s %-10s %s\n", change.Kind, change.Class, change.Path); err != nil {
			return err
		}
	}
	for _, conflict := range env.Conflicts {
		if _, err := fmt.Fprintf(out, "  conflict  %s %s\n", conflict.Module, conflict.Path); err != nil {
			return err
		}
	}
	for _, diagnostic := range env.Diagnostics {
		if _, err := fmt.Fprintf(out, "  %-8s %s %s\n", diagnostic.Severity, diagnostic.Code, diagnostic.Message); err != nil {
			return err
		}
	}
	if !env.OK {
		_, err := fmt.Fprintf(out, "failed (exit %d)\n", env.Exit)
		return err
	}
	return nil
}

// planEnvelope projects a Plan onto the envelope. Generated paths are read off
// the plan's own classification so the report cannot drift from the transaction.
func planEnvelope(plan Plan, exit int) Envelope {
	generated := make([]string, 0)
	for _, change := range plan.Changes {
		if change.Class == DestinationGenerated {
			generated = append(generated, change.Path)
		}
	}
	sort.Strings(generated)
	return Envelope{
		OK:             exit == exitOK,
		RegistryCommit: plan.RegistryCommit,
		Resolved:       plan.Resolved,
		Changes:        plan.Changes,
		Generated:      generated,
		Conflicts:      plan.Conflicts,
		Diagnostics:    plan.Diagnostics,
		Exit:           exit,
	}
}

func (c CLI) runInit(ctx context.Context, args []string) error {
	set := c.flagSet("init")
	ref := set.String("ref", "main", "registry ref to pin")
	repository := set.String("repository", DefaultRegistryRepository, "registry repository")
	adopt := set.Bool("adopt", false, "produce the initial lock from what is already installed")
	offline := set.Bool("offline", false, "resolve only from a self-hosted or cached registry")
	asJSON := set.Bool("json", false, "emit the machine envelope")
	positional, err := parseFlags(set, args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(positional) != 0 {
		return usageError("usage: ggg init [--ref REF] [--repository REPO] [--adopt] [--offline] [--json]")
	}

	path := filepath.Join(c.root(), ProjectFileName)
	if _, err := os.Stat(path); err == nil {
		return refusalError(fmt.Errorf("%s already exists", ProjectFileName))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return runtimeError(err)
	}

	project := Project{
		Schema:   1,
		Registry: ProjectRegistry{Repository: *repository, Ref: *ref},
		Modules:  []string{},
		Exclude:  []string{},
	}
	data, err := MarshalProject(project)
	if err != nil {
		return usageError(err.Error())
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return runtimeError(err)
	}

	if !*adopt {
		return c.emit("init", *asJSON, Envelope{OK: true, Exit: exitOK})
	}
	return c.applyOperation(ctx, "init", *asJSON, Operation{Kind: OpSync, Offline: *offline}, false)
}

// runGraphMutation drives add/remove/update. All three edit intent and converge
// on the same reconciler, so they share one code path.
func (c CLI) runGraphMutation(ctx context.Context, command string, kind OperationKind, args []string) error {
	set := c.flagSet(command)
	dryRun := set.Bool("dry-run", false, "resolve and report without writing")
	asJSON := set.Bool("json", false, "emit the machine envelope")
	purge := set.Bool("purge-data", false, "run the module's reviewed teardown migration")
	ref := set.String("ref", "", "registry ref to advance to")
	modules, err := parseFlags(set, args)
	if err != nil {
		return usageError(err.Error())
	}
	if kind != OpRemove && *purge {
		return usageError("--purge-data is only valid for `ggg remove`")
	}
	if kind != OpUpdate && strings.TrimSpace(*ref) != "" {
		return usageError("--ref is only valid for `ggg update`")
	}

	switch kind {
	case OpAdd, OpRemove:
		if len(modules) == 0 {
			return usageError(fmt.Sprintf("usage: ggg %s KIND/NAME... [--dry-run] [--json]", command))
		}
		for _, id := range modules {
			if err := validateInstallableModuleID(id); err != nil {
				return usageError(fmt.Sprintf("%s: %v", id, err))
			}
		}
	case OpUpdate:
		if len(modules) != 0 {
			return usageError("usage: ggg update [--ref REF] [--dry-run] [--json]")
		}
	}

	return c.applyOperation(ctx, command, *asJSON, Operation{
		Kind:        kind,
		Modules:     modules,
		RegistryRef: *ref,
		DryRun:      *dryRun,
		PurgeData:   *purge,
	}, *dryRun)
}

func (c CLI) runSync(ctx context.Context, args []string) error {
	set := c.flagSet("sync")
	check := set.Bool("check", false, "fail on drift without writing")
	offline := set.Bool("offline", false, "resolve only from the local registry cache")
	asJSON := set.Bool("json", false, "emit the machine envelope")
	positional, err := parseFlags(set, args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(positional) != 0 {
		return usageError("usage: ggg sync [--check] [--offline] [--json]")
	}
	return c.applyOperation(ctx, "sync", *asJSON, Operation{
		Kind: OpSync, Offline: *offline, DryRun: *check,
	}, *check)
}

// applyOperation plans, then either reports (dry run / check) or applies. It is
// the single place a mutation becomes bytes on disk.
func (c CLI) applyOperation(ctx context.Context, command string, asJSON bool, op Operation, readOnly bool) error {
	if _, err := c.loadProject(); err != nil {
		return err
	}
	engine, err := c.engine(op.Offline)
	if err != nil {
		return err
	}

	plan, err := engine.Plan(ctx, c.root(), op)
	if err != nil {
		return c.failure(command, asJSON, refusalError(err))
	}

	if readOnly {
		exit := exitOK
		drift := countDrift(plan)
		generatedDrift, err := generatedDriftDiagnostics(ctx, engine, plan)
		if err != nil {
			return c.failure(command, asJSON, runtimeError(err))
		}
		if drift > 0 || len(generatedDrift) > 0 {
			exit = exitConflict
		}
		env := planEnvelope(plan, exit)
		env.Diagnostics = append(env.Diagnostics, generatedDrift...)
		if err := c.emit(command, asJSON, env); err != nil {
			return err
		}
		if exit != exitOK {
			return conflictExit(fmt.Errorf("%s: %d pending change(s), %d generated drift(s)",
				command, drift, len(generatedDrift)))
		}
		return nil
	}

	result, err := engine.Apply(ctx, plan)
	if err != nil {
		env := planEnvelope(plan, exitRollback)
		env.OK = false
		_ = c.emit(command, asJSON, env)
		if result.RolledBack {
			return rollbackError(err)
		}
		return runtimeError(err)
	}

	exit := exitOK
	if len(plan.Conflicts) > 0 {
		exit = exitConflict
	}
	env := planEnvelope(plan, exit)
	env.Generated = appendUnique(env.Generated, result.Written...)
	if err := c.emit(command, asJSON, env); err != nil {
		return err
	}
	if exit != exitOK {
		return conflictExit(fmt.Errorf("%s: %d staged conflict(s) remain; run `ggg resolve`", command, len(plan.Conflicts)))
	}
	return nil
}

// generatedDriftDiagnostics renders the aggregates the plan implies and compares
// them byte-for-byte with the tree. Planner changes alone cannot detect this:
// the generator, not the planner, produces generated output, so a hand-edited or
// deleted aggregate would otherwise pass the gate.
func generatedDriftDiagnostics(ctx context.Context, engine *Engine, plan Plan) ([]Diagnostic, error) {
	if engine.generator == nil {
		return nil, nil
	}
	rendered, err := engine.generator.Render(ctx, plan)
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
	// compiles into the project while nothing owns it any more.
	for _, path := range staleGeneratedAggregates(plan.Root, expected) {
		diagnostics = append(diagnostics, Diagnostic{
			Code: "generated_stale", Severity: "error", Path: path,
			Message: "generated output is no longer owned by the selected graph; run ggg sync",
		})
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].Path < diagnostics[j].Path })
	return diagnostics, nil
}

// staleGeneratedAggregates finds registry aggregates on disk that the current
// selection no longer renders.
func staleGeneratedAggregates(root string, expected map[string]struct{}) []string {
	stale := make([]string, 0)
	_ = filepath.WalkDir(root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, full)
		if relErr != nil {
			return nil
		}
		slashed := filepath.ToSlash(rel)
		if !strings.Contains(slashed, "_registry_gen.") {
			return nil
		}
		if _, ok := expected[slashed]; !ok {
			stale = append(stale, slashed)
		}
		return nil
	})
	sort.Strings(stale)
	return stale
}

// planHasDrift reports whether the plan would change anything on disk.
func planHasDrift(plan Plan) bool { return countDrift(plan) > 0 }

func countDrift(plan Plan) int {
	n := 0
	for _, change := range plan.Changes {
		if change.Kind != ChangeUnchanged {
			n++
		}
	}
	return n
}

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, v := range dst {
		seen[v] = struct{}{}
	}
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		dst = append(dst, v)
	}
	sort.Strings(dst)
	return dst
}

// failure emits an envelope for a failed command and returns the original error
// so the exit code survives.
func (c CLI) failure(command string, asJSON bool, cause error) error {
	var coder interface{ ExitCode() int }
	exit := exitRuntime
	if errors.As(cause, &coder) {
		exit = coder.ExitCode()
	}
	_ = c.emit(command, asJSON, Envelope{
		OK:   false,
		Exit: exit,
		Diagnostics: []Diagnostic{{
			Code: "command_failed", Severity: "error", Message: cause.Error(),
		}},
	})
	return cause
}

func (c CLI) runCatalog(ctx context.Context, args []string) error {
	set := c.flagSet("catalog")
	installed := set.Bool("installed", false, "list only installed modules")
	kind := set.String("kind", "", "restrict to one module kind")
	latest := set.Bool("latest", false, "resolve the registry ref instead of the locked commit")
	asJSON := set.Bool("json", false, "emit the machine envelope")
	positional, err := parseFlags(set, args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(positional) != 0 {
		return usageError("usage: ggg catalog [--installed] [--kind KIND] [--latest] [--json]")
	}
	if *kind != "" && !knownModuleKind(*kind) {
		return usageError(fmt.Sprintf("unknown kind %q", *kind))
	}

	catalog, commit, lock, err := c.readCatalog(ctx, *latest)
	if err != nil {
		return c.failure("catalog", *asJSON, err)
	}
	states := installedStates(lock)

	entries := make([]catalogEntry, 0, len(catalog.Modules))
	for _, module := range catalog.Modules {
		if *kind != "" && string(module.Kind) != *kind {
			continue
		}
		state, isInstalled := states[module.ID]
		if *installed && !isInstalled {
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

	if *asJSON {
		return c.emitPayload("catalog", commit, map[string]any{"modules": entries})
	}
	for _, entry := range entries {
		if _, err := fmt.Fprintf(c.out(), "%-28s %-10s %s\n", entry.ID, entry.State, entry.Title); err != nil {
			return err
		}
	}
	return nil
}

type catalogEntry struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Revision int    `json:"revision"`
	Contract int    `json:"contract"`
	State    string `json:"state"`
}

func (c CLI) runInfo(ctx context.Context, args []string) error {
	set := c.flagSet("info")
	asJSON := set.Bool("json", false, "emit the machine envelope")
	positional, err := parseFlags(set, args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(positional) != 1 {
		return usageError("usage: ggg info KIND/NAME [--json]")
	}
	id := positional[0]
	if err := validateInstallableModuleID(id); err != nil {
		return usageError(fmt.Sprintf("%s: %v", id, err))
	}

	catalog, commit, lock, err := c.readCatalog(ctx, false)
	if err != nil {
		return c.failure("info", *asJSON, err)
	}
	var found *Manifest
	for i := range catalog.Modules {
		if catalog.Modules[i].ID == id {
			found = &catalog.Modules[i]
			break
		}
	}
	if found == nil {
		return c.failure("info", *asJSON, refusalError(fmt.Errorf("module %s is not in the registry", id)))
	}
	state, ok := installedStates(lock)[id]
	if !ok {
		state = "available"
	}

	if *asJSON {
		return c.emitPayload("info", commit, map[string]any{
			"module": found, "state": state,
		})
	}
	out := c.out()
	fmt.Fprintf(out, "%s  %s\n", found.ID, found.Title)
	fmt.Fprintf(out, "state          %s\n", state)
	fmt.Fprintf(out, "revision       %d (contract %d)\n", found.Revision, found.Contract)
	fmt.Fprintf(out, "removal_policy %s\n", found.RemovalPolicy)
	if len(found.Requires) > 0 {
		fmt.Fprintf(out, "requires       %s\n", strings.Join(found.Requires, ", "))
	}
	for _, file := range found.Files {
		fmt.Fprintf(out, "  file %s\n", file.Target)
	}
	return nil
}

// emitPayload renders a non-plan result. It reuses the envelope's fixed keys and
// carries command-specific data under diagnostics-free extra fields, so machine
// consumers parse one shape.
func (c CLI) emitPayload(command, commit string, payload map[string]any) error {
	envelope := map[string]any{
		"ok": true, "command": command, "run_id": "", "registry_commit": commit,
		"resolved": []string{}, "changes": []Change{}, "generated": []string{},
		"conflicts": []Conflict{}, "diagnostics": []Diagnostic{}, "exit": exitOK,
	}
	envelope["run_id"] = envelopeRunID(Envelope{Command: command, RegistryCommit: commit})
	for k, v := range payload {
		envelope[k] = v
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return runtimeError(err)
	}
	_, err = fmt.Fprintf(c.out(), "%s\n", data)
	return err
}

// readCatalog resolves the registry the project points at and loads its catalog,
// plus the current lock when one exists.
func (c CLI) readCatalog(ctx context.Context, latest bool) (Catalog, string, Lock, error) {
	project, err := c.loadProject()
	if err != nil {
		var coder interface{ ExitCode() int }
		// Browsing the registry must work before `ggg init`: fall back to the
		// default upstream rather than refusing to list anything.
		if !errors.As(err, &coder) || coder.ExitCode() != exitRefusal {
			return Catalog{}, "", Lock{}, err
		}
		project = Project{
			Schema:   1,
			Registry: ProjectRegistry{Repository: DefaultRegistryRepository, Ref: "main"},
			Modules:  []string{}, Exclude: []string{},
		}
	}
	engine, err := c.engine(false)
	if err != nil {
		return Catalog{}, "", Lock{}, err
	}

	lock, hasLock := Lock{}, false
	if data, readErr := os.ReadFile(filepath.Join(c.root(), LockFileName)); readErr == nil {
		if parsed, parseErr := ParseLock(data); parseErr == nil {
			lock, hasLock = parsed, true
		}
	}

	ref := project.Registry.Ref
	if hasLock && !latest && lock.RegistryCommit != "" {
		ref = lock.RegistryCommit
	}
	catalog, commit, err := engine.Catalog(ctx, project.Registry.Repository, ref)
	if err != nil {
		return Catalog{}, "", Lock{}, runtimeError(err)
	}
	return catalog, commit, lock, nil
}

// installedStates maps each locked module to its observable state.
func installedStates(lock Lock) map[string]string {
	states := make(map[string]string, len(lock.Modules))
	for _, module := range lock.Modules {
		state := "clean"
		if module.Pending != nil {
			state = "conflicted"
		} else if module.Reason == removalTombstoneReason {
			state = "removed"
		}
		states[module.ID] = state
	}
	return states
}

func knownModuleKind(kind string) bool {
	switch ModuleKind(kind) {
	case ModuleElement, ModuleComponent, ModulePage, ModuleWorkflow, ModuleSystem:
		return true
	}
	return false
}

func (c CLI) runDiff(ctx context.Context, args []string) error {
	set := c.flagSet("diff")
	upstream := set.Bool("upstream", false, "show the staged upstream candidate diff")
	asJSON := set.Bool("json", false, "emit the machine envelope")
	positional, err := parseFlags(set, args)
	if err != nil {
		return usageError(err.Error())
	}
	for _, id := range positional {
		if err := validateInstallableModuleID(id); err != nil {
			return usageError(fmt.Sprintf("%s: %v", id, err))
		}
	}

	entries, commit, err := c.collectDiff(positional, *upstream)
	if err != nil {
		return c.failure("diff", *asJSON, err)
	}
	if *asJSON {
		return c.emitPayload("diff", commit, map[string]any{"files": entries})
	}
	for _, entry := range entries {
		if _, err := fmt.Fprintf(c.out(), "%-10s %-24s %s\n", entry.State, entry.Module, entry.Path); err != nil {
			return err
		}
	}
	return nil
}

// DiffEntry is one file's ownership state relative to the lock.
type DiffEntry struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	State  string `json:"state"`
	Diff   string `json:"diff,omitempty"`
}

func (c CLI) collectDiff(modules []string, upstream bool) ([]DiffEntry, string, error) {
	data, err := os.ReadFile(filepath.Join(c.root(), LockFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", refusalError(fmt.Errorf("%s not found; run `ggg sync` first", LockFileName))
	}
	if err != nil {
		return nil, "", runtimeError(err)
	}
	lock, err := ParseLock(data)
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
			_, digest, missing, stateErr := currentTargetState(c.root(), file.Path)
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

func (c CLI) runResolve(ctx context.Context, args []string) error {
	set := c.flagSet("resolve")
	path := set.String("path", "", "the conflicted file to resolve")
	acceptUpstream := set.Bool("accept-upstream", false, "replace local bytes with the staged candidate")
	keepLocal := set.Bool("keep-local", false, "keep local bytes and clear the conflict")
	merged := set.Bool("merged", false, "accept the already-merged local bytes")
	asJSON := set.Bool("json", false, "emit the machine envelope")
	positional, err := parseFlags(set, args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(positional) != 1 {
		return usageError("usage: ggg resolve KIND/NAME --path PATH (--accept-upstream|--keep-local|--merged) [--json]")
	}
	id := positional[0]
	if err := validateInstallableModuleID(id); err != nil {
		return usageError(fmt.Sprintf("%s: %v", id, err))
	}
	if strings.TrimSpace(*path) == "" {
		return usageError("resolve requires --path PATH")
	}

	chosen := 0
	mode := ResolutionKeepLocal
	if *acceptUpstream {
		chosen, mode = chosen+1, ResolutionAcceptUpstream
	}
	if *keepLocal {
		chosen, mode = chosen+1, ResolutionKeepLocal
	}
	if *merged {
		chosen, mode = chosen+1, ResolutionMerged
	}
	if chosen != 1 {
		return usageError("resolve requires exactly one of --accept-upstream, --keep-local, or --merged")
	}

	if _, err := c.loadProject(); err != nil {
		return err
	}
	engine, err := c.engine(true)
	if err != nil {
		return err
	}
	plan, err := engine.ResolveConflict(ctx, c.root(), id, *path, mode)
	if err != nil {
		return c.failure("resolve", *asJSON, refusalError(err))
	}
	result, err := engine.Apply(ctx, plan)
	if err != nil {
		env := planEnvelope(plan, exitRollback)
		env.OK = false
		_ = c.emit("resolve", *asJSON, env)
		if result.RolledBack {
			return rollbackError(err)
		}
		return runtimeError(err)
	}
	return c.emit("resolve", *asJSON, planEnvelope(plan, exitOK))
}

func (c CLI) runDoctor(ctx context.Context, args []string) error {
	set := c.flagSet("doctor")
	asJSON := set.Bool("json", false, "emit the machine envelope")
	positional, err := parseFlags(set, args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(positional) != 0 {
		return usageError("usage: ggg doctor [--json]")
	}
	engine, err := c.engine(true)
	if err != nil {
		return err
	}
	report, err := engine.Health(ctx, c.root())
	if err != nil {
		return c.failure("doctor", *asJSON, runtimeError(err))
	}

	diagnostics := make([]Diagnostic, 0, len(report.Findings))
	for _, finding := range report.Findings {
		diagnostics = append(diagnostics, Diagnostic{
			Code: finding.Code, Severity: finding.Severity,
			Module: finding.Module, Path: finding.Path, Message: finding.Message,
		})
	}
	exit := exitOK
	if !report.Ok {
		exit = exitRefusal
	}
	env := Envelope{
		OK: report.Ok, RegistryCommit: report.RegistryCommit,
		Diagnostics: diagnostics, Exit: exit,
	}
	if err := c.emit("doctor", *asJSON, env); err != nil {
		return err
	}
	if exit != exitOK {
		return refusalError(fmt.Errorf("doctor: %d finding(s)", len(diagnostics)))
	}
	return nil
}

func (c CLI) runRegistry(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("usage: ggg registry build|validate [--json]")
	}
	subcommand, rest := args[0], args[1:]
	if subcommand != "build" && subcommand != "validate" {
		return usageError(fmt.Sprintf("unknown registry subcommand %q", subcommand))
	}
	set := c.flagSet("registry " + subcommand)
	asJSON := set.Bool("json", false, "emit the machine envelope")
	positional, err := parseFlags(set, rest)
	if err != nil {
		return usageError(err.Error())
	}
	if len(positional) != 0 {
		return usageError("usage: ggg registry " + subcommand + " [--json]")
	}

	if subcommand == "build" {
		built, discovered, err := buildRegistryIndexes(c.root())
		if err != nil {
			return c.failure("registry build", *asJSON, runtimeError(err))
		}
		return c.emit("registry build", *asJSON, Envelope{
			OK: true, Resolved: discovered, Generated: built, Exit: exitOK,
		})
	}

	catalog, err := LoadCatalog(os.DirFS(c.root()))
	if err != nil {
		return c.failure("registry validate", *asJSON, usageError(err.Error()))
	}
	ids := make([]string, 0, len(catalog.Modules))
	for _, module := range catalog.Modules {
		ids = append(ids, module.ID)
	}
	for _, profile := range catalog.Profiles {
		ids = append(ids, profile.ID)
	}
	sort.Strings(ids)
	return c.emit("registry validate", *asJSON, Envelope{OK: true, Resolved: ids, Exit: exitOK})
}

// buildRegistryIndexes rewrites each kind index from the documents actually
// present under registry/. It scans the tree rather than reading the indexes it
// writes: deriving the index from itself would make a newly authored module
// permanently invisible. Item order is sorted, so output is byte-stable.
func buildRegistryIndexes(root string) (written []string, discovered []string, err error) {
	byKind := make(map[CatalogKind][]string)
	for _, include := range catalogIncludes {
		if include.kind == CatalogProfile {
			continue
		}
		dir := filepath.Join(root, "registry", "modules", string(include.kind))
		entries, readErr := os.ReadDir(dir)
		if errors.Is(readErr, fs.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, nil, fmt.Errorf("scan %s: %w", dir, readErr)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			item := "registry/modules/" + string(include.kind) + "/" + entry.Name() + "/module.json"
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(item))); statErr != nil {
				continue
			}
			byKind[include.kind] = append(byKind[include.kind], item)
			discovered = append(discovered, string(include.kind)+"/"+entry.Name())
		}
	}

	profileDir := filepath.Join(root, "registry", "profiles")
	profileEntries, readErr := os.ReadDir(profileDir)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("scan %s: %w", profileDir, readErr)
	}
	for _, entry := range profileEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		byKind[CatalogProfile] = append(byKind[CatalogProfile], "registry/profiles/"+entry.Name())
		discovered = append(discovered, "profile/"+strings.TrimSuffix(entry.Name(), ".json"))
	}

	for _, include := range catalogIncludes {
		items := byKind[include.kind]
		if items == nil {
			items = []string{}
		}
		sort.Strings(items)
		data, marshalErr := json.MarshalIndent(CatalogIndex{
			Schema: 1, Kind: include.kind, Items: items,
		}, "", "  ")
		if marshalErr != nil {
			return nil, nil, marshalErr
		}
		target := filepath.Join(root, filepath.FromSlash(include.path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
			return nil, nil, err
		}
		written = append(written, include.path)
	}
	sort.Strings(discovered)
	return written, discovered, nil
}

type usageError string

func (e usageError) Error() string {
	return string(e)
}

func (usageError) ExitCode() int {
	return exitUsage
}
