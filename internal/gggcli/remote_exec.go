package gggcli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/gogogadget/gogogadget/internal/remote"
)

// providerSelectionsFromChoices converts parsed SLOT:ENV=ADAPTER@TARGET
// choices into the intent shape, merged over the committed selections so a
// set touches only the environments the operator named.
func (c *Controller) providerSelectionsFromChoices(choices []ProviderChoice) (map[string]modkit.ProviderSelections, error) {
	lock, err := c.loadLock()
	if err != nil {
		return nil, err
	}
	merged := map[string]modkit.ProviderSelections{}
	for slot, selections := range lock.Providers {
		merged[slot] = selections
	}
	for _, choice := range choices {
		if choice.Slot == "" || choice.Environment == "" || choice.Adapter == "" || choice.Target == "" {
			return nil, usageError("provider must be SLOT:ENV=ADAPTER@TARGET")
		}
		if choice.Environment != "development" && choice.Environment != "test" && choice.Environment != "production" {
			return nil, usageError(fmt.Sprintf("provider %q has invalid environment %q", choice.Slot, choice.Environment))
		}
		selections := merged[choice.Slot]
		selection := modkit.ProviderSelection{Adapter: choice.Adapter, Target: choice.Target}
		switch choice.Environment {
		case "development":
			selections.Development = selection
		case "test":
			selections.Test = selection
		case "production":
			selections.Production = selection
		}
		merged[choice.Slot] = selections
	}
	return merged, nil
}

// previewProviderSet plans one provider selection transaction through the
// same engine path as every other graph mutation: selections change the
// intent, selected adapters enter with reason provider, and deselected
// adapters retire in the same plan.
func (c *Controller) previewProviderSet(ctx context.Context, mutation ProviderSetMutation) (Plan, error) {
	selections, err := c.providerSelectionsFromChoices(mutation.Choices)
	if err != nil {
		return Plan{}, err
	}
	return c.previewOperation(ctx, "provider set", modkit.Operation{
		Kind: modkit.OpSync, Offline: true, SetProviders: selections,
	}, false)
}

// previewDeploymentSet plans the deployment replacement through the engine's
// SetDeployment transaction: the new module enters with reason deployment,
// the previous one retires inside the same plan.
func (c *Controller) previewDeploymentSet(ctx context.Context, mutation DeploymentSetMutation) (Plan, error) {
	if mutation.Module == "" {
		spec, _ := lookupSpec(builtInCommands(), "deployment")
		return Plan{}, usageError(spec.Usage)
	}
	return c.previewOperation(ctx, "deployment set", modkit.Operation{
		Kind: modkit.OpSync, Offline: true, SetDeployment: scopedDeploymentModule(mutation.Module),
	}, false)
}

// executeProviderTest validates one selected adapter/target: a target with
// a provisioner is observed authoritatively through Check; any other target
// is validated against its declared inputs, reporting key names only.
func (c *Controller) executeProviderTest(ctx context.Context, request ProviderTestRequest) (Result, error) {
	choice, err := c.resolveSlotChoice(request.Slot, request.Environment)
	if err != nil {
		return Result{}, err
	}
	if request.Adapter != "" && (request.Adapter != choice.Adapter || request.Target != choice.TargetID) {
		return Result{}, refusalError(fmt.Errorf(
			"provider test refuses to probe %s@%s: %s selects %s@%s for %s",
			request.Adapter, request.Target, choice.Slot, choice.Adapter, choice.TargetID, choice.Environment,
		))
	}
	secrets := c.secretValues(choice.Environment)
	if choice.Target.Provisioner != "" {
		provisioner, ok := c.remoteProvisioner(choice.Target.Provisioner)
		if !ok {
			return Result{}, refusalError(fmt.Errorf("provisioner %s is not installed", choice.Target.Provisioner))
		}
		state, _ := c.providerPrior(choice)
		status, err := provisioner.Check(ctx, c.providerRequest(choice, state))
		if err != nil {
			return c.notConfiguredOrError("provider test", choice, err)
		}
		c.recordCheck(choice.Slot+"@"+choice.Environment, status.Healthy)
		payload := map[string]any{
			"slot": choice.Slot, "environment": choice.Environment,
			"adapter": choice.Adapter, "target": choice.TargetID,
			"state": status.State, "healthy": status.Healthy, "message": status.Message,
		}
		if !status.Healthy {
			env := c.testEnvelope("provider test", payload, status.Message)
			return Result{Envelope: env, Payload: payload}, refusalError(errors.New(status.Message))
		}
		env := normalizeEnvelope(modkit.Envelope{Command: "provider test", OK: true, Exit: exitOK})
		return Result{Envelope: env, Payload: payload}, nil
	}
	configured, missing := configuredInputs(choice, secrets)
	payload := map[string]any{
		"slot": choice.Slot, "environment": choice.Environment,
		"adapter": choice.Adapter, "target": choice.TargetID,
		"automation": choice.Target.Automation,
		"configured": configured, "missing": missing,
	}
	if len(missing) > 0 {
		message := fmt.Sprintf("missing required inputs: %s", strings.Join(missing, ", "))
		env := c.testEnvelope("provider test", payload, message)
		return Result{Envelope: env, Payload: payload}, refusalError(errors.New(message))
	}
	env := normalizeEnvelope(modkit.Envelope{Command: "provider test", OK: true, Exit: exitOK})
	return Result{Envelope: env, Payload: payload}, nil
}

// notConfiguredOrError renders the typed not-configured refusal as a
// checklist beside the target's console URL instead of a bare failure.
func (c *Controller) notConfiguredOrError(command string, choice slotChoice, err error) (Result, error) {
	var notConfigured *remote.ErrNotConfigured
	if !errors.As(err, &notConfigured) {
		return Result{}, runtimeError(err)
	}
	payload := map[string]any{
		"slot": choice.Slot, "environment": choice.Environment,
		"adapter": choice.Adapter, "target": choice.TargetID,
		"missing": notConfigured.Missing, "console_url": notConfigured.Console,
		"advice": notConfigured.Advice,
	}
	env := normalizeEnvelope(modkit.Envelope{
		Command: command, OK: false, Exit: exitRefusal,
		Diagnostics: []modkit.Diagnostic{{
			Code: "provider_not_configured", Severity: "error", Module: choice.Adapter,
			Message: c.redactCheck(notConfigured.Error()),
		}},
	})
	return Result{Envelope: env, Payload: payload}, refusalError(errors.New(notConfigured.Error()))
}

// testEnvelope builds the envelope a failing provider test emits.
func (c *Controller) testEnvelope(command string, payload map[string]any, message string) modkit.Envelope {
	return normalizeEnvelope(modkit.Envelope{
		Command: command, OK: false, Exit: exitRefusal,
		Diagnostics: []modkit.Diagnostic{{
			Code: "provider_test_failed", Severity: "error", Module: payload["adapter"].(string),
			Message: message,
		}},
	})
}

// redactCheck applies the redactor to one diagnostic message.
func (c *Controller) redactCheck(message string) string {
	if c.redactor == nil {
		return message
	}
	return c.redactor.Apply(message)
}

// recordCheck timestamps a provider check in the state store.
func (c *Controller) recordCheck(key string, healthy bool) {
	state, err := c.loadState()
	if err != nil {
		return
	}
	recordStateCheck(state, key)
	_ = state.Save(c.rootDir())
}

func (c *Controller) remoteProvisioner(id string) (remote.ProviderProvisioner, bool) {
	if c.remoteReg.Provisioner == nil {
		return nil, false
	}
	return c.remoteReg.Provisioner(id)
}

func (c *Controller) remoteDeployTarget(id string) (remote.DeployTarget, bool) {
	if c.remoteReg.DeployTarget == nil {
		return nil, false
	}
	return c.remoteReg.DeployTarget(id)
}

func (c *Controller) remoteDatabaseOperator(id string) (remote.DatabaseOperator, bool) {
	if c.remoteReg.DatabaseOperator == nil {
		return nil, false
	}
	return c.remoteReg.DatabaseOperator(id)
}

// providerPrior loads one slot/environment's persisted provider state.
func (c *Controller) providerPrior(choice slotChoice) (remote.ProviderState, bool) {
	state, err := c.loadState()
	if err != nil {
		return remote.ProviderState{}, false
	}
	return state.ProviderState(choice.Slot, choice.Environment)
}

// previewProviderProvision resolves one provision plan. Provision runs only
// against targets whose automation is provision; manual and configure
// targets get the structured checklist and console URL instead.
func (c *Controller) previewProviderProvision(ctx context.Context, mutation ProviderRemoteMutation) (Plan, error) {
	choice, err := c.resolveSlotChoice(mutation.Slot, mutation.Environment)
	if err != nil {
		return Plan{}, err
	}
	if choice.Target.Automation != "provision" {
		return Plan{}, checklistRefusal(choice)
	}
	if choice.Target.Provisioner == "" {
		return Plan{}, refusalError(fmt.Errorf("target %s@%s declares automation provision but no provisioner", choice.Adapter, choice.TargetID))
	}
	provisioner, ok := c.remoteProvisioner(choice.Target.Provisioner)
	if !ok {
		return Plan{}, refusalError(fmt.Errorf("provisioner %s is not installed", choice.Target.Provisioner))
	}
	state, _ := c.providerPrior(choice)
	plan, err := provisioner.Plan(ctx, c.providerRequest(choice, state))
	if err != nil {
		var notConfigured *remote.ErrNotConfigured
		if errors.As(err, &notConfigured) {
			return Plan{}, provisionerNotConfigured(choice, notConfigured)
		}
		return Plan{}, plannerFailure{runtimeError(err)}
	}
	summary := choice.Adapter + "@" + choice.TargetID
	return Plan{
		Command:  "provider provision",
		RunID:    remoteRunID("provider", plan.PlanHash),
		Remote:   []RemotePlan{newRemotePlan("provider", summary, choice.Slot, choice.Environment, plan.PlanHash, plan.ObservedStateHash, plan.Changes)},
		mutation: mutation,
	}, nil
}

func newRemotePlan(kind, summary, slot, environment, planHash, observedHash string, changes []remote.RemoteChange) RemotePlan {
	return RemotePlan{
		Kind: kind, Summary: summary, Slot: slot, Environment: environment,
		PlanHash: planHash, ObservedStateHash: observedHash, Changes: changes,
	}
}

// checklistRefusal refuses provisioning a manual target with the structured
// checklist and console URL the operator follows instead.
func checklistRefusal(choice slotChoice) error {
	return &checklistError{choice: choice, checklist: targetChecklist(choice)}
}

type checklistError struct {
	choice    slotChoice
	checklist []map[string]any
	advice    string
}

func (e *checklistError) Error() string {
	return fmt.Sprintf(
		"target %s@%s has automation %q; follow the checklist and the provider console %s, then run ggg provider test",
		e.choice.Adapter, e.choice.TargetID, e.choice.Target.Automation, e.choice.Target.ConsoleURL,
	)
}

func (e *checklistError) ExitCode() int { return exitRefusal }

// provisionerNotConfigured renders the typed not-configured refusal for a
// provisioner plan call.
func provisionerNotConfigured(choice slotChoice, notConfigured *remote.ErrNotConfigured) error {
	return &notConfiguredError{choice: choice, notConfigured: notConfigured}
}

type notConfiguredError struct {
	choice        slotChoice
	notConfigured *remote.ErrNotConfigured
}

func (e *notConfiguredError) Error() string { return e.notConfigured.Error() }
func (e *notConfiguredError) ExitCode() int { return exitRefusal }

// executeProviderChecklist renders the checklist payload for a refused
// provision so the operator has the structured steps either way.
func executeProviderChecklist(choice slotChoice) map[string]any {
	return map[string]any{
		"slot": choice.Slot, "environment": choice.Environment,
		"adapter": choice.Adapter, "target": choice.TargetID,
		"automation":  choice.Target.Automation,
		"console_url": choice.Target.ConsoleURL, "docs_url": choice.Target.DocsURL,
		"checklist": targetChecklist(choice),
	}
}

// applyProviderProvision runs the confirmed provision plan: fresh Check
// first (a changed target refuses as stale), the run is persisted before
// anything executes, and state lands only after every change applied.
func (c *Controller) applyProviderProvision(ctx context.Context, cc CommandContext, plan Plan, mutation ProviderRemoteMutation) (Result, error) {
	remotePlan := plan.Remote[0]
	choice, err := c.resolveSlotChoice(mutation.Slot, mutation.Environment)
	if err != nil {
		return Result{}, err
	}
	provisioner, ok := c.remoteProvisioner(choice.Target.Provisioner)
	if !ok {
		return Result{}, refusalError(fmt.Errorf("provisioner %s is not installed", choice.Target.Provisioner))
	}
	state, _ := c.providerPrior(choice)

	// Stale gate: the fresh observation must match what the plan confirmed.
	// A resume replays the persisted plan without replanning, so it skips
	// the gate by contract.
	if mutation.Resume == "" {
		status, checkErr := provisioner.Check(ctx, c.providerRequest(choice, state))
		if checkErr != nil {
			var notConfigured *remote.ErrNotConfigured
			if errors.As(checkErr, &notConfigured) {
				return Result{}, provisionerNotConfigured(choice, notConfigured)
			}
			return Result{}, runtimeError(checkErr)
		}
		if status.ObservedStateHash != remotePlan.ObservedStateHash {
			return Result{}, staleRefusal(plan.Command)
		}
	}

	store, err := c.loadState()
	if err != nil {
		return Result{}, err
	}
	run := store.StartRun(plan.RunID, "provider", plan.Command, choice.Environment, remotePlan.PlanHash, remotePlan.ObservedStateHash, remotePlan.Changes)
	if err := store.Save(c.rootDir()); err != nil {
		return Result{}, runtimeError(err)
	}

	progress := progressSink(cc)
	secrets, sinkErr := secretSink(c, cc, choice.Environment)
	if sinkErr != nil {
		return Result{}, sinkErr
	}
	applyState, err := provisioner.Apply(ctx, remote.ProviderPlan{
		PlanHash: remotePlan.PlanHash, ObservedStateHash: remotePlan.ObservedStateHash, Changes: remotePlan.Changes,
	}, secrets, progress)
	if err != nil {
		// Partial progress is durable: resource ids the apply captured land
		// in the run record so a resume reconciles instead of duplicating.
		run.ResourceIDs = applyState.ResourceIDs
		store.Runs[run.RunID] = run
		_ = store.Save(c.rootDir())
		return c.remoteFailureEnvelope(plan.Command, remotePlan, err, store, run)
	}
	for _, change := range remotePlan.Changes {
		_ = store.MarkChange(run.RunID, change.Path, true)
	}
	store.RecordProvider(choice.Slot, choice.Environment, applyState)
	run.ResourceIDs = applyState.ResourceIDs
	store.Runs[run.RunID] = run
	store.DropRun(run.RunID)
	if err := store.Save(c.rootDir()); err != nil {
		return Result{}, runtimeError(err)
	}

	env := normalizeEnvelope(modkit.Envelope{Command: plan.Command, OK: true, Exit: exitOK})
	env = remoteChangeEnvelope(env, plan.Remote)
	payload := map[string]any{
		"provider": map[string]any{
			"slot": choice.Slot, "environment": choice.Environment,
			"adapter": choice.Adapter, "target": choice.TargetID,
			"resource_ids": applyState.ResourceIDs,
			"state_hash":   applyState.ObservedStateHash,
		},
	}
	return Result{Envelope: env, Payload: payload}, nil
}

// staleRefusal is the typed remote_plan_stale refusal.
func staleRefusal(command string) error {
	return refusalError(fmt.Errorf("%s: the target changed since the plan was confirmed (remote_plan_stale); re-run the plan", command))
}

// remoteFailureEnvelope emits the partial-failure envelope: exit 1 with the
// completed and pending change split, never a rollback of confirmed work.
func (c *Controller) remoteFailureEnvelope(command string, remotePlan RemotePlan, cause error, store *remote.State, run remote.RunState) (Result, error) {
	env := normalizeEnvelope(modkit.Envelope{Command: command, OK: false, Exit: exitRuntime})
	env = remoteChangeEnvelope(env, []RemotePlan{remotePlan})
	for _, change := range run.Changes {
		if change.Status != remote.ChangeApplied {
			continue
		}
		env.Diagnostics = append(env.Diagnostics, modkit.Diagnostic{
			Code: "remote_change_applied", Severity: "info", Message: change.Path + " applied",
		})
	}
	env.Diagnostics = append(env.Diagnostics, modkit.Diagnostic{
		Code: "remote_change_pending", Severity: "error",
		Message: fmt.Sprintf("%s: %v; resume with --resume %s", cause.Error(), remotePlanPending(run), run.RunID),
	})
	return Result{Envelope: env}, runtimeError(fmt.Errorf("%s: %v; resume with --resume %s", command, cause, run.RunID))
}

func remotePlanPending(run remote.RunState) []string { return run.PendingChanges() }

// previewDeploy resolves one deploy plan through the project's deployment
// target: apply and rollback plan; status and logs observe.
func (c *Controller) previewDeploy(ctx context.Context, mutation DeployRemoteMutation) (Plan, error) {
	if mutation.Environment != "development" && mutation.Environment != "test" && mutation.Environment != "production" {
		return Plan{}, usageError("deploy environment must be development, test, or production")
	}
	manifest, contribution, err := c.resolveDeployment()
	if err != nil {
		return Plan{}, err
	}
	target, ok := c.remoteDeployTarget(contribution.ID)
	if !ok {
		return Plan{}, refusalError(fmt.Errorf("deploy target %s (from %s) is not installed", contribution.ID, manifest.ID))
	}
	state, _ := c.deployPrior(mutation.Environment)
	request := c.deployRequest(mutation.Environment, mutation.Keys, state)
	switch mutation.Action {
	case "apply":
		plan, err := target.Plan(ctx, request)
		if err != nil {
			return Plan{}, deployPlanFailure(err)
		}
		summary := manifest.ID + " (" + contribution.ID + ")"
		return Plan{
			Command:  "deploy apply",
			RunID:    remoteRunID("deploy", plan.PlanHash),
			Remote:   []RemotePlan{newRemotePlan("deploy", summary, "", mutation.Environment, plan.PlanHash, plan.ObservedStateHash, plan.Changes)},
			mutation: mutation,
		}, nil
	case "rollback":
		if state.ReleaseID == "" {
			return Plan{}, refusalError(fmt.Errorf("no recorded release to roll back to for %s; deploy first", mutation.Environment))
		}
		return Plan{
			Command:  "deploy rollback",
			RunID:    remoteRunID("rollback", state.ReleaseID+state.ObservedVersion),
			Remote:   []RemotePlan{newRemotePlan("deploy", manifest.ID+" rollback to "+state.ReleaseID, "", mutation.Environment, remote.ObservedStateHash(state), state.ObservedVersion, nil)},
			mutation: mutation,
		}, nil
	case "secrets":
		if len(mutation.Keys) == 0 {
			return Plan{}, usageError("deploy secrets requires at least one --key KEY")
		}
		return Plan{
			Command:  "deploy secrets",
			RunID:    remoteRunID("secrets", remote.ObservedStateHash(mutation.Keys)),
			Remote:   []RemotePlan{newRemotePlan("deploy", manifest.ID+" secrets", "", mutation.Environment, remote.ObservedStateHash(mutation.Keys), "", nil)},
			mutation: mutation,
		}, nil
	default:
		return Plan{}, usageError("deploy action must be apply, rollback, or secrets")
	}
}

func deployPlanFailure(err error) error {
	var notConfigured *remote.ErrNotConfigured
	if errors.As(err, &notConfigured) {
		return refusalError(fmt.Errorf("%s (console: %s)", notConfigured.Error(), notConfigured.Console))
	}
	return plannerFailure{runtimeError(err)}
}

func (c *Controller) deployPrior(environment string) (remote.DeployState, bool) {
	state, err := c.loadState()
	if err != nil {
		return remote.DeployState{}, false
	}
	return state.DeployState(environment)
}

// applyDeploy executes the confirmed deploy operation.
func (c *Controller) applyDeploy(ctx context.Context, cc CommandContext, plan Plan, mutation DeployRemoteMutation) (Result, error) {
	_, contribution, err := c.resolveDeployment()
	if err != nil {
		return Result{}, err
	}
	target, ok := c.remoteDeployTarget(contribution.ID)
	if !ok {
		return Result{}, refusalError(fmt.Errorf("deploy target %s is not installed", contribution.ID))
	}
	state, _ := c.deployPrior(mutation.Environment)
	request := c.deployRequest(mutation.Environment, mutation.Keys, state)
	progress := progressSink(cc)

	switch mutation.Action {
	case "apply":
		remotePlan := plan.Remote[0]
		if mutation.Resume == "" {
			status, statusErr := target.Status(ctx, request)
			if statusErr != nil {
				return Result{}, runtimeError(statusErr)
			}
			if status.ObservedStateHash != remotePlan.ObservedStateHash {
				return Result{}, staleRefusal(plan.Command)
			}
		}
		store, err := c.loadState()
		if err != nil {
			return Result{}, err
		}
		run := store.StartRun(plan.RunID, "deploy", plan.Command, mutation.Environment, remotePlan.PlanHash, remotePlan.ObservedStateHash, remotePlan.Changes)
		if err := store.Save(c.rootDir()); err != nil {
			return Result{}, runtimeError(err)
		}
		newState, err := target.Apply(ctx, remote.DeployPlan{
			PlanHash: remotePlan.PlanHash, ObservedStateHash: remotePlan.ObservedStateHash, Changes: remotePlan.Changes,
		}, progress)
		if err != nil {
			store.Runs[run.RunID] = run
			_ = store.Save(c.rootDir())
			return c.remoteFailureEnvelope(plan.Command, remotePlan, err, store, run)
		}
		for _, change := range remotePlan.Changes {
			_ = store.MarkChange(run.RunID, change.Path, true)
		}
		store.RecordDeploy(mutation.Environment, newState)
		store.DropRun(run.RunID)
		if err := store.Save(c.rootDir()); err != nil {
			return Result{}, runtimeError(err)
		}
		env := normalizeEnvelope(modkit.Envelope{Command: plan.Command, OK: true, Exit: exitOK})
		env = remoteChangeEnvelope(env, plan.Remote)
		return Result{Envelope: env, Payload: map[string]any{"deploy": deployPayload(mutation.Environment, newState)}}, nil

	case "rollback":
		progress.Emit(remote.ProgressEvent{Stage: "rollback", Message: "rolling back " + mutation.Environment, Current: 1, Total: 1, Done: false})
		newState, err := target.Rollback(ctx, request, state, progress)
		if err != nil {
			return Result{}, runtimeError(err)
		}
		store, err := c.loadState()
		if err != nil {
			return Result{}, err
		}
		store.RecordDeploy(mutation.Environment, newState)
		if err := store.Save(c.rootDir()); err != nil {
			return Result{}, runtimeError(err)
		}
		env := normalizeEnvelope(modkit.Envelope{Command: plan.Command, OK: true, Exit: exitOK})
		return Result{Envelope: env, Payload: map[string]any{"deploy": deployPayload(mutation.Environment, newState)}}, nil

	case "secrets":
		secrets := c.secretValues(mutation.Environment)
		if err := target.PutSecrets(ctx, request, secrets, progress); err != nil {
			return Result{}, runtimeError(err)
		}
		env := normalizeEnvelope(modkit.Envelope{Command: plan.Command, OK: true, Exit: exitOK})
		payload := map[string]any{"deploy": map[string]any{
			"environment": mutation.Environment, "keys": mutation.Keys,
		}}
		return Result{Envelope: env, Payload: payload}, nil

	default:
		return Result{}, usageError("deploy action must be apply, rollback, or secrets")
	}
}

func deployPayload(environment string, state remote.DeployState) map[string]any {
	return map[string]any{
		"environment": environment, "release_id": state.ReleaseID, "url": state.URL,
		"image_digest": state.ImageDigest, "observed_state_hash": state.ObservedVersion,
	}
}

// executeDeployPlan previews one deployment environment's change set through
// the deploy target's own Plan. It is the read-only half of the remote plan
// contract: the same ordered changes, plan hash, and observed state hash an
// apply is confirmed against, rendered as canonical paths and hashes with no
// values — so an operator can see what a deploy would do without starting
// one, and `--resume RUN_ID` has an object to reload.
func (c *Controller) executeDeployPlan(ctx context.Context, request DeployPlanRequest) (Result, error) {
	if request.Environment == "" {
		request.Environment = "production"
	}
	manifest, contribution, err := c.resolveDeployment()
	if err != nil {
		return Result{}, err
	}
	plan, err := c.previewDeploy(ctx, DeployRemoteMutation{Action: "apply", Environment: request.Environment})
	if err != nil {
		return Result{}, err
	}
	remotePlan := plan.Remote[0]
	changes := make([]map[string]any, 0, len(remotePlan.Changes))
	for _, change := range remotePlan.Changes {
		changes = append(changes, map[string]any{
			"change_id": change.ChangeID, "path": change.Path, "kind": change.Kind,
			"idempotency_key": change.IdempotencyKey, "desired_hash": change.DesiredHash,
			"observed_version": change.ObservedVersion,
			"depends_on":       change.DependsOn, "secret_keys": change.SecretKeys,
		})
	}
	payload := map[string]any{"deploy_plan": map[string]any{
		"module": manifest.ID, "target": contribution.ID,
		"environment": request.Environment,
		"plan_hash":   remotePlan.PlanHash, "observed_state_hash": remotePlan.ObservedStateHash,
		"changes": changes,
	}}
	env := normalizeEnvelope(modkit.Envelope{
		Command: "deploy plan", OK: true, Exit: exitOK, RunID: plan.RunID,
	})
	env = remoteChangeEnvelope(env, plan.Remote)
	return Result{Envelope: env, Payload: payload}, nil
}

// executeDeployStatus observes one deployment environment.
func (c *Controller) executeDeployStatus(ctx context.Context, request DeployStatusRequest) (Result, error) {
	if request.Environment == "" {
		request.Environment = "production"
	}
	manifest, contribution, err := c.resolveDeployment()
	if err != nil {
		return Result{}, err
	}
	target, ok := c.remoteDeployTarget(contribution.ID)
	if !ok {
		return Result{}, refusalError(fmt.Errorf("deploy target %s (from %s) is not installed", contribution.ID, manifest.ID))
	}
	state, _ := c.deployPrior(request.Environment)
	status, err := target.Status(ctx, c.deployRequest(request.Environment, nil, state))
	if err != nil {
		var notConfigured *remote.ErrNotConfigured
		if errors.As(err, &notConfigured) {
			return Result{}, refusalError(fmt.Errorf("%s (console: %s)", notConfigured.Error(), notConfigured.Console))
		}
		return Result{}, runtimeError(err)
	}
	payload := map[string]any{
		"deployment": map[string]any{
			"module": manifest.ID, "target": contribution.ID,
			"environment": request.Environment,
			"state":       status.State, "release_id": status.ReleaseID, "url": status.URL,
			"observed_version": status.ObservedVersion, "ready": status.Ready,
			"checked_at": status.UpdatedAt,
		},
	}
	env := normalizeEnvelope(modkit.Envelope{Command: "deploy status", OK: status.Ready, Exit: exitOK})
	if !status.Ready {
		env.Exit = exitRefusal
		return Result{Envelope: env, Payload: payload}, refusalError(fmt.Errorf("deployment %s is %s", request.Environment, status.State))
	}
	return Result{Envelope: env, Payload: payload}, nil
}

// executeDeployLogs streams deployment logs to the command's output stream.
func (c *Controller) executeDeployLogs(ctx context.Context, cc CommandContext, request DeployLogsRequest) (Result, error) {
	if request.Environment == "" {
		request.Environment = "production"
	}
	manifest, contribution, err := c.resolveDeployment()
	if err != nil {
		return Result{}, err
	}
	target, ok := c.remoteDeployTarget(contribution.ID)
	if !ok {
		return Result{}, refusalError(fmt.Errorf("deploy target %s (from %s) is not installed", contribution.ID, manifest.ID))
	}
	state, _ := c.deployPrior(request.Environment)
	deployReq := c.deployRequest(request.Environment, nil, state)
	if request.Follow {
		deployReq.State.Metadata["follow"] = "1"
	}
	if err := target.Logs(ctx, deployReq, cc.Out); err != nil {
		return Result{}, runtimeError(err)
	}
	return Result{Envelope: normalizeEnvelope(modkit.Envelope{Command: "deploy logs", OK: true, Exit: exitOK})}, nil
}

// scopedDeploymentModule resolves a deployment operand: a scoped id passes
// through; a bare name is the core namespace's system module of that name.
func scopedDeploymentModule(module string) string {
	if strings.Contains(module, "/") {
		return module
	}
	return "ggg/system/" + module
}

// executeProviderList reports the committed provider selections: one row
// per slot and environment with the selected adapter@target, the target's
// mode and automation, and the declared input key names — never values.
func (c *Controller) executeProviderList(request ProviderListRequest) (Result, error) {
	lock, err := c.loadLock()
	if err != nil {
		return Result{}, err
	}
	project, err := c.loadProject()
	if err != nil {
		return Result{}, err
	}
	slots := make([]string, 0, len(project.Providers))
	for slot := range project.Providers {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	providers := make([]map[string]any, 0, len(slots))
	for _, slot := range slots {
		if request.Slot != "" && slot != request.Slot {
			continue
		}
		row := map[string]any{"slot": slot, "environments": map[string]any{}}
		environments := map[string]any{}
		for _, environment := range []string{"development", "test", "production"} {
			choice, err := resolveSlotChoiceFromLock(lock, slot, environment)
			if err != nil {
				environments[environment] = map[string]any{"error": err.Error()}
				continue
			}
			keys := make([]string, 0, len(choice.Target.Inputs))
			for _, input := range choice.Target.Inputs {
				name := input.Key
				if input.EnvKey != "" {
					name = input.EnvKey
				}
				keys = append(keys, name)
			}
			environments[environment] = map[string]any{
				"adapter": choice.Adapter, "target": choice.TargetID,
				"mode": choice.Target.Mode, "automation": choice.Target.Automation,
				"inputs": keys,
			}
		}
		row["environments"] = environments
		providers = append(providers, row)
	}
	env := normalizeEnvelope(modkit.Envelope{Command: "provider list", OK: true, Exit: exitOK})
	return Result{Envelope: env, Payload: map[string]any{"providers": providers}}, nil
}
