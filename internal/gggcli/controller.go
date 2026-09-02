package gggcli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// DefaultRegistryRepository is the upstream catalog a fresh project points at.
const DefaultRegistryRepository = "gogogadget/gogogadget"

// Controller owns resolution, planning, and apply for one invocation. It is
// the only engine holder: presentation layers — flags, guided forms, and the
// TUI — reach the project exclusively through Execute, Preview, and Apply.
type Controller struct {
	root       string
	version    string
	injected   *modkit.Engine
	writeFile  func(path string, data []byte, mode os.FileMode) error
	taskRunner TaskRunner
	redactor   *Redactor
	// table is the full command table this invocation serves: built-ins plus
	// the contributed commands. Help and completions render from it, so the
	// sealed HelpRequest/CompletionRequest and the `help`/`completion`
	// handlers can never diverge. Nil means the built-ins only.
	table []CommandSpec
	// remoteReg resolves the typed provisioner/deployer/database-operator
	// registries the generated project-local registry supplies. Provider
	// and deploy modules execute only through these — never through command
	// handlers.
	remoteReg RemoteRegistries
}

// ControllerOptions configures a Controller.
type ControllerOptions struct {
	// Root is the project directory. Empty means the process working directory.
	Root string
	// Version is the running CLI version.
	Version string
	// Engine overrides the engine the controller would otherwise build from the
	// project's registry declaration. Tests inject an offline source here.
	Engine *modkit.Engine
	// WriteFile replaces direct file writes (test hook for rollback paths).
	WriteFile func(path string, data []byte, mode os.FileMode) error
	// TaskRunner executes only argv selected by trusted task handlers.
	TaskRunner TaskRunner
	// Table is the assembled command table — built-ins plus contributed
	// commands. Help and completion requests render from it.
	Table []CommandSpec
	// Remote resolves the typed provisioner, deployer, and database
	// operator registries. Nil lookups report every module as not
	// installed, which remote commands surface as refusals.
	Remote RemoteRegistries
}

// NewController constructs the command platform's single controller.
func NewController(opts ControllerOptions) *Controller {
	return &Controller{
		root:       opts.Root,
		version:    opts.Version,
		injected:   opts.Engine,
		writeFile:  opts.WriteFile,
		taskRunner: opts.TaskRunner,
		table:      opts.Table,
		remoteReg:  opts.Remote,
	}
}

// Root reports the project directory the controller operates on.
func (c *Controller) Root() string { return c.rootDir() }

// Write performs a journalled-file write through the configured hook.
func (c *Controller) Write(path string, data []byte, mode os.FileMode) error {
	if c.writeFile != nil {
		return c.writeFile(path, data, mode)
	}
	return os.WriteFile(path, data, mode)
}

func (c *Controller) rootDir() string {
	if c.root == "" {
		return "."
	}
	return c.root
}

// engine returns the engine to plan with. A caller-supplied engine wins so
// tests and offline runs never reach the network.
func (c *Controller) engine(offline bool) (*modkit.Engine, error) {
	if c.injected != nil {
		return c.injected, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, runtimeError(fmt.Errorf("locate registry cache: %w", err))
	}
	source := modkit.ProjectSource{
		Root: c.rootDir(),
		GitHub: modkit.GitHubSource{
			CacheDir: filepath.Join(cache, "ggg", "registry"),
			Offline:  offline,
			Token:    os.Getenv("GITHUB_TOKEN"),
		},
	}
	return modkit.New(modkit.Options{
		Source: source, Generator: modkit.RegistryGenerator{}, ToolRunner: modkit.OSCommandRunner{},
	}), nil
}

// loadProject reads the intent file. A missing file is a refusal, not a crash:
// the diagnostic names the command that creates one.
func (c *Controller) loadProject() (modkit.Project, error) {
	data, err := os.ReadFile(filepath.Join(c.rootDir(), modkit.ProjectFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return modkit.Project{}, refusalError(fmt.Errorf("%s not found; run `ggg init` first", modkit.ProjectFileName))
	}
	if err != nil {
		return modkit.Project{}, runtimeError(err)
	}
	project, err := modkit.ParseProject(data)
	if err != nil {
		return modkit.Project{}, usageError(err.Error())
	}
	return project, nil
}

// Redactor returns the secret redactor loaded from the project lock's declared
// secret environment keys. Values come from the process environment: any
// declared secret value the CLI can see is masked before rendering.
func (c *Controller) Redactor() *Redactor {
	if c.redactor != nil {
		return c.redactor
	}
	r := NewRedactor()
	if data, err := os.ReadFile(filepath.Join(c.rootDir(), modkit.LockFileName)); err == nil {
		if lock, parseErr := modkit.ParseLock(data); parseErr == nil {
			for i := range lock.Modules {
				for _, variable := range lock.Modules[i].Manifest.Environment {
					if !variable.Secret {
						continue
					}
					if value := os.Getenv(variable.Key); value != "" {
						r.RegisterSecret(variable.Key, value)
					}
				}
			}
		}
	}
	c.redactor = r
	return r
}

// commandTable reports the table this invocation serves: the assembled one,
// or the built-ins when none was supplied.
func (c *Controller) commandTable() []CommandSpec {
	if c.table == nil {
		return CommandTable()
	}
	return c.table
}

// Execute performs a read-only request and returns the renderer boundary
// result. It never writes to the project.
func (c *Controller) Execute(ctx context.Context, req Request) (Result, error) {
	switch request := req.(type) {
	case VersionRequest:
		version := c.version
		if version == "" {
			version = "dev"
		}
		return Result{Payload: map[string]any{"version": version}}, nil

	case CatalogRequest:
		return c.executeCatalog(ctx, request)

	case InfoRequest:
		return c.executeInfo(ctx, request)

	case DiffRequest:
		return c.executeDiff(ctx, request)

	case DoctorRequest:
		return c.executeDoctor(ctx, request)

	case HelpRequest:
		return Result{Payload: map[string]any{"text": renderHelp(c.commandTable(), request.Command)}}, nil

	case CompletionRequest:
		script, err := renderCompletion(c.commandTable(), request.Shell)
		if err != nil {
			return Result{}, err
		}
		return Result{Payload: map[string]any{"text": script}}, nil

	case RegistryReadRequest:
		return c.executeRegistryValidate(ctx, request)

	case ProviderListRequest:
		return c.executeProviderList(request)

	case ProviderTestRequest:
		return c.executeProviderTest(ctx, request)

	case DeployStatusRequest:
		return c.executeDeployStatus(ctx, request)

	case DeployLogsRequest:
		return c.executeDeployLogs(ctx, ccForLogs(ctx), request)

	default:
		return Result{}, usageError("unsupported request")
	}
}

// Preview resolves the mutation into the plan the operator confirms. It never
// writes: a Preview that touched the tree would break the zero-writes-before-
// apply contract that both the flag path and the TUI depend on.
func (c *Controller) Preview(ctx context.Context, mut Mutation) (Plan, error) {
	switch mutation := mut.(type) {
	case InitMutation:
		if err := c.previewInit(mutation); err != nil {
			return Plan{}, err
		}
		return Plan{Command: "init"}, nil

	case GraphMutation:
		return c.previewOperation(ctx, "graph", mutation.operation(), mutation.DryRun)

	case SyncMutation:
		return c.previewOperation(ctx, "sync", modkit.Operation{
			Kind: modkit.OpSync, Offline: mutation.Offline, DryRun: mutation.Check, Claims: mutation.Claims,
		}, mutation.Check)

	case ResolveMutation:
		if _, err := c.loadProject(); err != nil {
			return Plan{}, err
		}
		engine, err := c.engine(true)
		if err != nil {
			return Plan{}, err
		}
		local, err := engine.ResolveConflict(ctx, c.rootDir(), mutation.ModuleID, mutation.Path, mutation.Mode)
		if err != nil {
			return Plan{}, plannerFailure{refusalError(err)}
		}
		return c.planFor("resolve", &local, true), nil

	case RegistryMutation:
		if mutation.SetRegistries != nil {
			return c.previewOperation(ctx, "registry", modkit.Operation{
				Kind: modkit.OpSync, SetRegistries: mutation.SetRegistries,
			}, false)
		}
		return Plan{Command: "registry build", mutation: mutation}, nil

	case TaskMutation:
		if err := c.previewTask(mutation); err != nil {
			return Plan{}, err
		}
		return Plan{Command: "task " + mutation.Task, mutation: mutation}, nil

	case NewMutation:
		return c.previewNew(ctx, mutation)

	case ProviderSetMutation:
		return c.previewProviderSet(ctx, mutation)

	case DeploymentSetMutation:
		return c.previewDeploymentSet(ctx, mutation)

	case CreateMutation:
		return c.previewCreate(ctx, mutation)

	case ProviderRemoteMutation:
		switch mutation.Action {
		case "provision":
			return c.previewProviderProvision(ctx, mutation)
		case "configure":
			return c.previewProviderConfigure(ctx, mutation)
		default:
			return Plan{}, usageError("provider action must be provision or configure")
		}

	case DeployRemoteMutation:
		return c.previewDeploy(ctx, mutation)

	case DatabaseOpsMutation:
		return c.previewDatabaseOps(ctx, mutation)

	case DoctorFixMutation:
		return Plan{Command: "doctor fix", mutation: mutation}, nil

	default:
		return Plan{}, usageError("unsupported mutation")
	}
}

// Apply is the single place a previewed modkit plan becomes bytes on disk:
// every graph mutation's file writes go through it, journalled and rollback on
// failure. Mutations without a local plan write through their enumerated
// handler paths instead — the init intent file, the registry authoring
// commands, and the trusted tasks — which are fixed operations with their own
// journals, never a second planning engine. The plan must come from Preview.
func (c *Controller) Apply(ctx context.Context, plan Plan) (Result, error) {
	if plan.newProject != nil {
		return c.applyNew(ctx, plan)
	}
	if plan.create != nil {
		return c.applyCreate(ctx, plan)
	}
	if mutation, ok := plan.mutation.(ProviderRemoteMutation); ok {
		switch mutation.Action {
		case "provision":
			return c.applyProviderProvision(ctx, ccForApply(ctx), plan, mutation)
		case "configure":
			return c.applyProviderConfigure(ctx, ccForApply(ctx), plan, mutation)
		}
	}
	if mutation, ok := plan.mutation.(DeployRemoteMutation); ok {
		return c.applyDeploy(ctx, ccForApply(ctx), plan, mutation)
	}
	if mutation, ok := plan.mutation.(DatabaseOpsMutation); ok {
		return c.applyDatabaseOps(ctx, ccForApply(ctx), plan, mutation)
	}
	if mutation, ok := plan.mutation.(DoctorFixMutation); ok {
		return c.applyDoctorFix(ctx, plan, mutation)
	}
	if mutation, ok := plan.mutation.(TaskMutation); ok {
		switch mutation.Task {
		case "setup", "generate", "services", "dev", "db", "check", "test", "build":
			return c.applyTrustedTask(ctx, mutation)
		case "identity-link":
			return c.applyIdentityLink(ctx, mutation)
		case "migrate-schema-1":
			return c.applyMigrateSchema1()
		case "cache-prune":
			return c.applyCachePrune()
		}
	}
	if plan.Local != nil {
		engine, err := c.engine(operationOffline(plan))
		if err != nil {
			return Result{}, err
		}
		result, err := engine.Apply(ctx, *plan.Local)
		if err != nil {
			env := planEnvelope(*plan.Local, plan.Command, exitRollback)
			env.OK = false
			// A transaction that reached the journal is exit 5 whether the
			// restore completed or not. An incomplete restore is the MORE
			// severe outcome, so downgrading it to exit 1 would hide the one
			// case that needs a human: the error text names every path that
			// could not be put back.
			if result.Exit == exitRollback {
				return Result{Envelope: env}, rollbackError(err)
			}
			return Result{Envelope: env}, runtimeError(err)
		}
		exit := exitOK
		if len(plan.Local.Conflicts) > 0 && plan.Command != "resolve" {
			exit = exitConflict
		}
		env := planEnvelope(*plan.Local, plan.Command, exit)
		if plan.Command != "resolve" {
			env.Generated = appendUnique(env.Generated, result.Written...)
		}
		return Result{Envelope: env}, nil
	}
	// Mutations without a local plan (init intent, registry build, tasks) are
	// applied by their handlers, which own their side effects.
	return Result{}, nil
}

// operationOffline recovers the offline hint a preview captured.
func operationOffline(plan Plan) bool {
	offline, _ := plan.offlineHint()
	return offline
}

// failureEnvelope builds the envelope a failed command emits and wraps the
// cause so its exit code survives. Handlers return both; the App renders the
// envelope exactly once.
func failureEnvelope(command string, cause error) (Result, error) {
	var coder interface{ ExitCode() int }
	exit := exitRuntime
	if errors.As(cause, &coder) {
		exit = coder.ExitCode()
	}
	env := normalizeEnvelope(modkit.Envelope{
		Command: command,
		OK:      false,
		Exit:    exit,
		Diagnostics: []modkit.Diagnostic{{
			Code: "command_failed", Severity: "error", Message: cause.Error(),
		}},
	})
	return Result{Envelope: env}, cause
}
