package gggcli

import (
	"context"
	"io"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// Request is the sealed interface for read-only operations. The concrete set
// below is exhaustive: handlers build one of these, and Controller.Execute is
// the only thing that consumes them. New kinds are added here, never smuggled
// through stringly-typed side doors.
type Request interface {
	sealedRequest()
}

// Mutation is the sealed interface for state-changing operations. Every
// mutation follows the same path: Preview produces the Plan the operator
// confirms, Apply is the only thing that writes.
type Mutation interface {
	sealedMutation()
}

// Sealed read-only requests.
type (
	// VersionRequest reports the CLI version.
	VersionRequest struct{}

	// CatalogRequest lists the registry catalog.
	CatalogRequest struct {
		Installed bool
		Kind      string
		Latest    bool
	}

	// InfoRequest reports one module's contract.
	InfoRequest struct {
		ModuleID string
	}

	// DiffRequest reports file ownership state relative to the lock.
	DiffRequest struct {
		Modules  []string
		Upstream bool
	}

	// DoctorRequest runs the health check.
	DoctorRequest struct{}

	// ProviderTestRequest validates one selected provider adapter/target with
	// the project's configured inputs. Shipping with the provisioning slice.
	ProviderTestRequest struct {
		Slot        string
		Environment string
		Adapter     string
		Target      string
	}

	// DeployStatusRequest observes one deployment target. Shipping with the
	// deployment slice.
	DeployStatusRequest struct {
		Environment string
	}

	// DeployLogsRequest streams deployment logs. Shipping with the deployment
	// slice.
	DeployLogsRequest struct {
		Environment string
		Follow      bool
	}

	// HelpRequest renders help derived from the command table.
	HelpRequest struct {
		// Command is the command to describe. Empty lists every command.
		Command string
	}

	// CompletionRequest renders a shell completion script derived from the
	// command table.
	CompletionRequest struct {
		Shell string
	}

	// RegistryReadRequest inspects the registry the project points at.
	RegistryReadRequest struct {
		// Validate exercises the example closures after loading the catalog.
		Validate bool
	}
)

// Sealed mutations.
type (
	// NewMutation creates a project through the same preview/apply boundary
	// used by every other state change.
	NewMutation struct {
		Dir        string
		Name       string
		ModulePath string
		Profile    string
		Providers  map[string]modkit.ProviderSelections
		Deployment string
		Registry   string
		Ref        string
		InPlace    bool
	}

	// InitMutation writes the initial project intent, optionally adopting the
	// already-installed sources into an initial lock.
	InitMutation struct {
		Ref        string
		Repository string
		PublicKey  string
		Adopt      bool
		Offline    bool
		Claims     []string
	}

	// GraphMutation is one add/remove/update intent edit converged on the same
	// reconciler.
	GraphMutation struct {
		Kind      modkit.OperationKind
		Modules   []string
		Ref       string
		DryRun    bool
		PurgeData bool
	}

	// SyncMutation reconciles the tree with the intent.
	SyncMutation struct {
		Check   bool
		Offline bool
		Claims  []string
	}

	// ResolveMutation resolves one staged conflict.
	ResolveMutation struct {
		ModuleID string
		Path     string
		Mode     modkit.ResolutionMode
	}

	// ProviderSetMutation records one provider slot choice in the intent.
	// Shipping with the provisioning slice.
	ProviderSetMutation struct {
		Slot        string
		Environment string
		Adapter     string
		Target      string
	}

	// DeploymentSetMutation records the selected deployment module. Shipping
	// with the deployment slice.
	DeploymentSetMutation struct {
		Module string
	}

	// CreateMutation writes one complete project-local registry module.
	CreateMutation struct {
		Kind        string
		Name        string
		Scope       string
		Table       string
		Route       string
		API         bool
		Admin       bool
		Search      bool
		NoUI        bool
		Family      string
		Owner       string
		Migration   string
		Schedulable bool
		MaxAttempts int
		Slot        string
		Package     string
		Constructor string
		Definition  string
	}

	// ProviderRemoteMutation plans/applies provider provisioning. Shipping
	// with the provisioning slice.
	ProviderRemoteMutation struct {
		Action      string
		Slot        string
		Environment string
		Yes         bool
		Resume      string
	}

	// DeployRemoteMutation plans/applies deployments. Shipping with the
	// deployment slice.
	DeployRemoteMutation struct {
		Action      string
		Environment string
		Yes         bool
		Resume      string
	}

	// DoctorFixMutation performs the remediation attached to one typed doctor
	// finding. Shipping with the doctor --fix slice.
	DoctorFixMutation struct {
		FindingCode string
		Yes         bool
	}

	// RegistryMutation authors the self-hosting registry: digest refresh,
	// index build, vendor verification.
	RegistryMutation struct {
		Build bool
	}

	// TaskMutation is one trusted task command. Task and Action are closed
	// enumerations; Arguments never carry an executable or a shell fragment.
	TaskMutation struct {
		Task        string
		Action      string
		Environment string
		Mode        string
		Volumes     bool
		Yes         bool
		Provider    string
		Subject     string
		UserID      string
		OrgID       string
	}
)

func (VersionRequest) sealedRequest()          {}
func (CatalogRequest) sealedRequest()          {}
func (InfoRequest) sealedRequest()             {}
func (DiffRequest) sealedRequest()             {}
func (DoctorRequest) sealedRequest()           {}
func (ProviderTestRequest) sealedRequest()     {}
func (DeployStatusRequest) sealedRequest()     {}
func (DeployLogsRequest) sealedRequest()       {}
func (HelpRequest) sealedRequest()             {}
func (CompletionRequest) sealedRequest()       {}
func (RegistryReadRequest) sealedRequest()     {}
func (NewMutation) sealedMutation()            {}
func (InitMutation) sealedMutation()           {}
func (GraphMutation) sealedMutation()          {}
func (SyncMutation) sealedMutation()           {}
func (ResolveMutation) sealedMutation()        {}
func (ProviderSetMutation) sealedMutation()    {}
func (DeploymentSetMutation) sealedMutation()  {}
func (CreateMutation) sealedMutation()         {}
func (ProviderRemoteMutation) sealedMutation() {}
func (DeployRemoteMutation) sealedMutation()   {}
func (DoctorFixMutation) sealedMutation()      {}
func (RegistryMutation) sealedMutation()       {}
func (TaskMutation) sealedMutation()           {}

// Plan is the preview boundary: what a mutation would do, before anything
// writes. Local carries the modkit file plan; Remote carries provider and
// deployment changes described without secret values.
type Plan struct {
	RunID       string
	Command     string
	Local       *modkit.Plan
	Remote      []RemotePlan
	Diagnostics []modkit.Diagnostic

	// offline remembers the source mode Preview resolved with, so Apply
	// rebuilds the identical engine. Unexported: it is transport, not data.
	offline bool
	// mutation and slice-specific state are private transport from Preview to
	// Apply. They cannot leak into JSON or become a second dispatch path.
	mutation   Mutation
	newProject *newProjectPlan
	create     *createPlan
}

// RemotePlan is one provider or deployment change set. The detail it can carry
// grows with the provisioning and deployment slices; it never carries secret
// values.
type RemotePlan struct {
	// Kind is "provider" or "deploy".
	Kind string
	// Summary names the adapter/target or deployment the plan touches.
	Summary string
}

// Result is the renderer boundary: the fixed envelope plus command-specific
// payload data. Handlers return it; the App renders human or JSON output from
// exactly these fields, so the two renderings cannot disagree.
type Result struct {
	Envelope modkit.Envelope
	Payload  map[string]any
}

// CommandContext is what a command handler may touch: the controller and the
// process streams. It never carries the engine, and a contributed handler's
// only route back into the project is Controller operations.
type CommandContext struct {
	Controller *Controller
	// Table is the full command table this build serves: built-ins plus
	// contributed commands. Help and completions derive from it.
	Table []CommandSpec
	// Out is the human/machine output stream; Err is the diagnostic stream.
	Out io.Writer
	Err io.Writer
	// Stdin is the interactive input stream.
	Stdin io.Reader
	// Version is the running ggg version.
	Version string
	// AsJSON reports that the command must emit the machine envelope and never
	// prompt. JSON implies noninteractive.
	AsJSON bool
	// Interactive reports whether the streams are a terminal.
	Interactive bool
	// Accessible enables linear accessible forms (--accessible or
	// GGG_ACCESSIBLE=1).
	Accessible bool
}

// ContributedCommand is one project-local command supplied by an installed
// module's RuntimeContributions.CLI declaration. Installation executes
// nothing; the handler runs only when the operator explicitly invokes the
// command by name.
type ContributedCommand struct {
	Spec    CommandSpec
	Handler CommandHandler
}

// CommandHandler executes one command. It receives the parsed command name and
// remaining operands, and may reach the project only through the controller.
type CommandHandler func(ctx context.Context, c CommandContext, args []string) (Result, error)
