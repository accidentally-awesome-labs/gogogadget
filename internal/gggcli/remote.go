package gggcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/gogogadget/gogogadget/internal/remote"
)

// RemoteRegistries carries the typed lookup functions the generated
// project-local registry supplies. Provider and deploy modules execute only
// through these constructors: a command handler never receives a provisioner,
// deployer, database operator, or a SecretValues — it reaches them by asking
// the controller, and the controller resolves them here.
type RemoteRegistries struct {
	Provisioner      func(id string) (remote.ProviderProvisioner, bool)
	DeployTarget     func(id string) (remote.DeployTarget, bool)
	DatabaseOperator func(id string) (remote.DatabaseOperator, bool)
}

// slotChoice is one resolved provider selection: the adapter module and
// service target the project selected for a slot in one environment.
type slotChoice struct {
	Slot        string
	Environment string
	Adapter     string
	TargetID    string
	Target      modkit.ServiceTarget
	Manifest    modkit.Manifest
}

// ErrPlanStale is the typed refusal raised when a fresh Check or Status
// disagrees with the observation a confirmed plan carries.
var ErrPlanStale = errors.New("remote_plan_stale")

// commandContextKey carries the invoking CommandContext through the
// controller's context so remote applies render progress to the caller's
// streams without widening the controller's public signatures.
type commandContextKey struct{}

// withCommandContext binds the invoking CommandContext to ctx.
func withCommandContext(ctx context.Context, cc CommandContext) context.Context {
	return context.WithValue(ctx, commandContextKey{}, cc)
}

// ccForApply recovers the invoking CommandContext, or a silent one when the
// caller had none (tests, programmatic applies).
func ccForApply(ctx context.Context) CommandContext {
	if cc, ok := ctx.Value(commandContextKey{}).(CommandContext); ok {
		return cc
	}
	return CommandContext{Out: io.Discard, Err: io.Discard}
}

func ccForLogs(ctx context.Context) CommandContext { return ccForApply(ctx) }

// loadLock reads the committed lock. Remote commands run against committed
// state: an uninstalled project has nothing to provision or deploy.
func (c *Controller) loadLock() (modkit.Lock, error) {
	data, err := os.ReadFile(c.lockPath())
	if err != nil {
		return modkit.Lock{}, refusalError(fmt.Errorf("gogogadget.lock.json is missing; run ggg sync before remote operations"))
	}
	return modkit.ParseLock(data)
}

func (c *Controller) lockPath() string {
	root := c.rootDir()
	if root == "" {
		root = "."
	}
	return root + "/" + modkit.LockFileName
}

// resolveSlotChoice resolves one slot's selected adapter/target for one
// environment from the committed lock.
func (c *Controller) resolveSlotChoice(slot, environment string) (slotChoice, error) {
	lock, err := c.loadLock()
	if err != nil {
		return slotChoice{}, err
	}
	return resolveSlotChoiceFromLock(lock, slot, environment)
}

func resolveSlotChoiceFromLock(lock modkit.Lock, slot, environment string) (slotChoice, error) {
	selections, ok := lock.Providers[slot]
	if !ok {
		return slotChoice{}, refusalError(fmt.Errorf("provider slot %s is not selected in this project", slot))
	}
	choice := modkit.ProviderSelection{}
	switch environment {
	case "development":
		choice = selections.Development
	case "test":
		choice = selections.Test
	case "production":
		choice = selections.Production
	default:
		return slotChoice{}, usageError("environment must be development, test, or production")
	}
	if choice.Adapter == "" {
		return slotChoice{}, refusalError(fmt.Errorf("provider slot %s has no %s selection", slot, environment))
	}
	for _, module := range lock.Modules {
		if module.ID != choice.Adapter {
			continue
		}
		if module.Manifest.Runtime.System == nil || module.Manifest.Runtime.System.Adapter == nil {
			return slotChoice{}, refusalError(fmt.Errorf("selected adapter %s declares no targets", choice.Adapter))
		}
		for _, target := range module.Manifest.Runtime.System.Adapter.Targets {
			if target.ID == choice.Target {
				return slotChoice{
					Slot: slot, Environment: environment,
					Adapter: choice.Adapter, TargetID: choice.Target,
					Target: target, Manifest: module.Manifest,
				}, nil
			}
		}
		return slotChoice{}, refusalError(fmt.Errorf("adapter %s does not declare target %s", choice.Adapter, choice.Target))
	}
	return slotChoice{}, refusalError(fmt.Errorf("selected adapter %s is not installed; run ggg sync", choice.Adapter))
}

// resolveDeployment resolves the project's one deployment module and its
// deploy contribution from the committed lock.
func (c *Controller) resolveDeployment() (modkit.Manifest, modkit.DeployContribution, error) {
	project, err := c.loadProject()
	if err != nil {
		return modkit.Manifest{}, modkit.DeployContribution{}, err
	}
	if project.Deployment == "" {
		return modkit.Manifest{}, modkit.DeployContribution{}, refusalError(fmt.Errorf(
			"no deployment module is selected; run ggg deployment set MODULE",
		))
	}
	lock, err := c.loadLock()
	if err != nil {
		return modkit.Manifest{}, modkit.DeployContribution{}, err
	}
	for _, module := range lock.Modules {
		if module.ID != project.Deployment || module.Reason == modkit.TombstoneReason {
			continue
		}
		if len(module.Manifest.Runtime.Deploy) != 1 {
			return modkit.Manifest{}, modkit.DeployContribution{}, refusalError(fmt.Errorf(
				"deployment module %s must provide exactly one deploy target", project.Deployment,
			))
		}
		return module.Manifest, module.Manifest.Runtime.Deploy[0], nil
	}
	return modkit.Manifest{}, modkit.DeployContribution{}, refusalError(fmt.Errorf(
		"deployment module %s is not installed; run ggg sync", project.Deployment,
	))
}

// secretValues resolves the CLI-managed environment: process env, then
// .ggg/env/<environment>.env, then the legacy .env in development only.
// Values read through it are registered with the redactor so nothing leaks
// through prompts, plans, or diagnostics.
func (c *Controller) secretValues(environment string) remote.SecretValues {
	root := c.rootDir()
	lookup := remote.LookupEnv(root, environment)
	return remote.SecretValuesFunc(func(key string) (string, bool) {
		value, ok := lookup(key)
		if ok && value != "" {
			if c.redactor == nil {
				c.redactor = NewRedactor()
			}
			c.redactor.RegisterSecret(key, value)
		}
		return value, ok
	})
}

// targetValues resolves the declared non-secret target inputs from the same
// layered lookup the secrets use.
func targetValues(choice slotChoice, lookup remote.SecretValues) map[string]string {
	values := map[string]string{}
	for _, input := range choice.Target.Inputs {
		if input.Secret || input.EnvKey == "" {
			continue
		}
		if value, ok := lookup.Get(input.EnvKey); ok && value != "" {
			values[input.Key] = value
		}
	}
	if value, ok := lookup.Get("PROJECT_ID"); ok {
		values["project_id"] = value
	}
	return values
}

// providerRequest assembles one provision request for the selected target.
func (c *Controller) providerRequest(choice slotChoice, prior remote.ProviderState) remote.ProviderRequest {
	secrets := c.secretValues(choice.Environment)
	return remote.ProviderRequest{
		Root:        c.rootDir(),
		Slot:        choice.Slot,
		Environment: choice.Environment,
		Adapter:     choice.Adapter,
		Target:      choice.TargetID,
		Values:      targetValues(choice, secrets),
		Secrets:     secrets,
		Prior:       prior,
	}
}

// loadState reads the ignored CLI state store.
func (c *Controller) loadState() (*remote.State, error) {
	state, err := remote.LoadState(c.rootDir())
	if err != nil {
		return nil, refusalError(err)
	}
	return state, nil
}

// deployRequest assembles one deploy request for the selected deployment.
func (c *Controller) deployRequest(environment string, keys []string, state remote.DeployState) remote.DeployRequest {
	root := c.rootDir()
	if root == "" {
		root = "."
	}
	return remote.DeployRequest{
		Root:        root,
		Environment: environment,
		Target:      "",
		State:       state,
		SecretKeys:  keys,
	}
}

// confirmRemote is the shared confirmation gate for remote mutations: an
// interactive TTY is asked; a noninteractive run must carry --yes, and JSON
// without it refuses before anything runs.
func confirmRemote(cc CommandContext, command string) error {
	if cc.AsJSON {
		return refusalError(fmt.Errorf("%s: noninteractive JSON requires --yes", command))
	}
	if !cc.Interactive {
		return refusalError(fmt.Errorf("%s: noninteractive runs require --yes", command))
	}
	answer, err := readLine(cc, fmt.Sprintf("Apply %s? (y/N) ", command))
	if err != nil {
		return err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return UserCancelledError{Command: command}
	}
	return nil
}

// remoteChangeEnvelope converts remote plans into the envelope's fixed
// change vocabulary: remote changes report Class remote so a machine
// consumer sees one envelope shape and the run id covers the remote content.
func remoteChangeEnvelope(env modkit.Envelope, plans []RemotePlan) modkit.Envelope {
	for _, plan := range plans {
		for _, change := range plan.Changes {
			env.Changes = append(env.Changes, modkit.Change{
				Path:   change.Path,
				Module: plan.Summary,
				Source: change.ChangeID,
				Kind:   remoteChangeKind(change.Kind),
				Class:  modkit.DestinationRemote,
				SHA256: change.DesiredHash,
			})
		}
	}
	return env
}

func remoteChangeKind(kind string) modkit.ChangeKind {
	switch {
	case strings.HasPrefix(kind, "create"), strings.HasPrefix(kind, "upsert"), strings.HasPrefix(kind, "deploy"):
		return modkit.ChangeCreate
	case strings.HasPrefix(kind, "delete"), strings.HasPrefix(kind, "destroy"):
		return modkit.ChangeDelete
	default:
		return modkit.ChangeUpdate
	}
}

// sortedTargetInputs orders a target's declared inputs by key for stable
// rendering.
func sortedTargetInputs(inputs []modkit.TargetInput) []modkit.TargetInput {
	ordered := append([]modkit.TargetInput{}, inputs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Key < ordered[j].Key })
	return ordered
}

// validateTargetInput applies the schema-level parse one declared input
// value must satisfy: type, enum membership, and URL shape. This is the same
// contract the generated config parser enforces for adapter-owned keys,
// applied to the values the CLI manages.
func validateTargetInput(input modkit.TargetInput, value string) error {
	switch input.Type {
	case "integer":
		if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", new(int)); err != nil {
			return fmt.Errorf("%s must be an integer", input.Key)
		}
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "false", "1", "0", "yes", "no", "on", "off":
		default:
			return fmt.Errorf("%s must be a boolean", input.Key)
		}
	case "url":
		if !strings.Contains(value, "://") {
			return fmt.Errorf("%s must be a URL", input.Key)
		}
	case "enum":
		for _, allowed := range input.Enum {
			if value == allowed {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of: %s", input.Key, strings.Join(input.Enum, ", "))
	case "string":
		if value == "" {
			return fmt.Errorf("%s must not be empty", input.Key)
		}
	default:
		return fmt.Errorf("input %s declares unknown type %q", input.Key, input.Type)
	}
	return nil
}

// targetChecklist renders the manual-automation checklist one target
// declares: every input as a named field plus the console and docs URLs.
// Values never appear — keys and shapes only.
func targetChecklist(choice slotChoice) []map[string]any {
	fields := make([]map[string]any, 0, len(choice.Target.Inputs))
	for _, input := range sortedTargetInputs(choice.Target.Inputs) {
		field := map[string]any{
			"key": input.Key, "label": input.Label, "type": input.Type,
			"required": input.Required, "secret": input.Secret,
		}
		if input.EnvKey != "" {
			field["env_key"] = input.EnvKey
		}
		if len(input.Enum) > 0 {
			field["enum"] = input.Enum
		}
		fields = append(fields, field)
	}
	steps := []map[string]any{
		{"step": "open", "detail": fmt.Sprintf("open the provider console %s", choice.Target.ConsoleURL)},
		{"step": "configure", "detail": fmt.Sprintf("configure target %s (%s)", choice.Target.ID, choice.Target.Title), "fields": fields},
		{"step": "verify", "detail": fmt.Sprintf("run ggg provider test --slot %s --environment %s", choice.Slot, choice.Environment)},
	}
	return steps
}

// configuredInputs reports which declared keys resolve and which are
// missing, by name only.
func configuredInputs(choice slotChoice, lookup remote.SecretValues) (configured, missing []string) {
	for _, input := range sortedTargetInputs(choice.Target.Inputs) {
		value, ok := lookup.Get(input.EnvKey)
		if input.EnvKey == "" {
			if input.Required {
				missing = append(missing, input.Key)
			}
			continue
		}
		if ok && value != "" {
			configured = append(configured, input.EnvKey)
		} else if input.Required {
			missing = append(missing, input.EnvKey)
		}
	}
	return configured, missing
}

// remoteRunID derives the run id a remote operation persists and reports:
// the plan hash plus kind, so a resumed run and its plan always agree.
func remoteRunID(kind, planHash string) string {
	return "run-" + planHash[:16]
}

// providerStateKey is the state record key for one slot/environment pair.
func providerStateKey(slot, environment string) string { return slot + "@" + environment }

// recordProviderCheck timestamps a provider test in the state store.
func recordStateCheck(state *remote.State, key string) {
	if state.Checks == nil {
		state.Checks = map[string]time.Time{}
	}
	state.Checks[key] = time.Now().UTC()
}
