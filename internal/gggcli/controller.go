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
	root      string
	version   string
	injected  *modkit.Engine
	writeFile func(path string, data []byte, mode os.FileMode) error
	redactor  *Redactor
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
}

// NewController constructs the command platform's single controller.
func NewController(opts ControllerOptions) *Controller {
	return &Controller{
		root:      opts.Root,
		version:   opts.Version,
		injected:  opts.Engine,
		writeFile: opts.WriteFile,
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
	// A tree that carries its own registry.json is a self-hosting registry: the
	// upstream repository, or a derivative that vendors the catalog. Resolving
	// from the tree keeps `make check` working in a fresh clone with no network
	// and no credentials.
	if _, statErr := os.Stat(filepath.Join(c.rootDir(), "registry.json")); statErr == nil {
		return modkit.New(modkit.Options{
			Source:     modkit.DirectorySource{Root: c.rootDir()},
			Generator:  modkit.RegistryGenerator{},
			ToolRunner: modkit.OSCommandRunner{},
		}), nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, runtimeError(statErr)
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, runtimeError(fmt.Errorf("locate registry cache: %w", err))
	}
	return modkit.New(modkit.Options{
		Source: modkit.GitHubSource{
			CacheDir: filepath.Join(cache, "ggg", "registry"),
			Offline:  offline,
			Token:    os.Getenv("GITHUB_TOKEN"),
		},
		Generator:  modkit.RegistryGenerator{},
		ToolRunner: modkit.OSCommandRunner{},
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
		return Result{Payload: map[string]any{"text": renderHelp(CommandTable(), request.Command)}}, nil

	case CompletionRequest:
		script, err := renderCompletion(CommandTable(), request.Shell)
		if err != nil {
			return Result{}, err
		}
		return Result{Payload: map[string]any{"text": script}}, nil

	case RegistryReadRequest:
		return c.executeRegistryValidate(ctx, request)

	case ProviderTestRequest:
		return Result{}, errNotAvailable("provider test", "it ships with the provider provisioning slice")

	case DeployStatusRequest:
		return Result{}, errNotAvailable("deploy status", "it ships with the deployment slice")

	case DeployLogsRequest:
		return Result{}, errNotAvailable("deploy logs", "it ships with the deployment slice")

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
		return Plan{Command: "registry build"}, nil

	case TaskMutation:
		if err := c.previewTask(mutation); err != nil {
			return Plan{}, err
		}
		return Plan{Command: "task " + mutation.Task}, nil

	case NewMutation:
		return Plan{}, errNotAvailable("project creation", "it ships with the project-creation slice")

	case ProviderSetMutation:
		return Plan{}, errNotAvailable("provider selection", "it ships with the provider provisioning slice")

	case DeploymentSetMutation:
		return Plan{}, errNotAvailable("deployment selection", "it ships with the deployment slice")

	case CreateMutation:
		return Plan{}, errNotAvailable("module creation", "it ships with the project-creation slice")

	case ProviderRemoteMutation:
		return Plan{}, errNotAvailable("provider provisioning", "it ships with the provider provisioning slice")

	case DeployRemoteMutation:
		return Plan{}, errNotAvailable("deployment", "it ships with the deployment slice")

	case DoctorFixMutation:
		return Plan{}, errNotAvailable("doctor fix", "it ships with the doctor --fix slice")

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
