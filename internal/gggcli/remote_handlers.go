package gggcli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// requireRemoteConfirm is the early gate for every remote mutation: an
// interactive TTY confirms after the preview; a noninteractive or JSON run
// must carry --yes or the command refuses before any planning work.
func requireRemoteConfirm(cc CommandContext, yes bool, command string) error {
	if yes {
		return nil
	}
	if cc.Interactive && !cc.AsJSON {
		return nil
	}
	return refusalError(fmt.Errorf("%s: noninteractive runs require --yes", command))
}

// driveRemoteMutation is the remote path's one flow: preview, confirm on a
// TTY, apply. A declined confirmation is the declared user_cancelled
// refusal (exit 3) with the envelope naming it; nothing has written.
func driveRemoteMutation(ctx context.Context, cc CommandContext, command string, mutation Mutation, confirmSummary string, needsConfirm bool) (Result, error) {
	plan, err := cc.Controller.Preview(ctx, mutation)
	if err != nil {
		var planned plannerFailure
		if errors.As(err, &planned) {
			return failureEnvelope(command, err)
		}
		// A checklist refusal carries its structured payload next to the
		// refusal so machine consumers get the checklist too.
		var checklist *checklistError
		if errors.As(err, &checklist) {
			env := normalizeEnvelope(modkit.Envelope{
				Command: command, OK: false, Exit: exitRefusal,
				Diagnostics: []modkit.Diagnostic{{
					Code: "manual_automation", Severity: "error", Module: checklist.choice.Adapter,
					Message: checklist.Error(),
				}},
			})
			return Result{Envelope: env, Payload: executeProviderChecklist(checklist.choice)}, err
		}
		var notConfigured *notConfiguredError
		if errors.As(err, &notConfigured) {
			env := normalizeEnvelope(modkit.Envelope{
				Command: command, OK: false, Exit: exitRefusal,
				Diagnostics: []modkit.Diagnostic{{
					Code: "provider_not_configured", Severity: "error", Module: notConfigured.choice.Adapter,
					Message: notConfigured.Error(),
				}},
			})
			return Result{Envelope: env, Payload: executeProviderChecklist(notConfigured.choice)}, err
		}
		return Result{}, err
	}
	if needsConfirm {
		if err := confirmRemote(cc, confirmSummary); err != nil {
			var cancelled UserCancelledError
			if errors.As(err, &cancelled) {
				env := normalizeEnvelope(modkit.Envelope{
					Command: command, OK: false, Exit: exitRefusal,
					Diagnostics: []modkit.Diagnostic{{Code: "user_cancelled", Severity: "error", Message: "cancelled before apply"}},
				})
				return Result{Envelope: env}, cancelled
			}
			return Result{}, err
		}
	}
	result, err := cc.Controller.Apply(ctx, plan)
	if err != nil {
		if result.Envelope.Command == "" {
			result.Envelope.Command = command
		}
		result.Envelope = normalizeEnvelope(result.Envelope)
		return result, err
	}
	return Result{Envelope: normalizeEnvelope(result.Envelope), Payload: result.Payload}, nil
}

// runProvider dispatches ggg provider list|set|configure|provision|test.
func runProvider(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "provider")
	if len(args) == 0 {
		return Result{}, usageError(spec.Usage)
	}
	action, rest := args[0], args[1:]
	sub, ok := lookupProviderAction(action)
	if !ok {
		return Result{}, usageError(spec.Usage)
	}
	parsed, err := parseArgv(sub.spec, rest)
	if err != nil {
		return Result{}, err
	}
	asJSON := parsed.Bool("json")
	cc.AsJSON = cc.AsJSON || asJSON
	yes := parsed.Bool("yes")

	switch action {
	case "list":
		return cc.Controller.Execute(ctx, ProviderListRequest{Slot: parsed.value("slot", "")})
	case "set":
		choices := make([]ProviderChoice, 0)
		for _, value := range parsed.List("provider") {
			slot, environment, selection, parseErr := parseProviderAnswer(value)
			if parseErr != nil {
				return Result{}, usageError(parseErr.Error())
			}
			choices = append(choices, ProviderChoice{
				Slot: slot, Environment: environment,
				Adapter: selection.Adapter, Target: selection.Target,
			})
		}
		if len(choices) == 0 {
			return Result{}, usageError(sub.spec.Usage)
		}
		return drivePlanMutation(ctx, cc, "provider set", ProviderSetMutation{Choices: choices}, false)
	case "configure":
		slot := parsed.value("slot", "")
		environment := parsed.value("environment", "")
		if slot == "" || environment == "" {
			return Result{}, usageError(sub.spec.Usage)
		}
		values, err := parseSetFlags(parsed.List("set"))
		if err != nil {
			return Result{}, usageError(err.Error())
		}
		mutation := ProviderRemoteMutation{
			Action: "configure", Slot: slot, Environment: environment, Values: values, Yes: yes,
		}
		return driveRemoteMutation(ctx, cc, "provider configure", mutation, slot+" "+environment+" configure", false)
	case "provision":
		slot := parsed.value("slot", "")
		environment := parsed.value("environment", "")
		if slot == "" || environment == "" {
			return Result{}, usageError(sub.spec.Usage)
		}
		mutation := ProviderRemoteMutation{
			Action: "provision", Slot: slot, Environment: environment,
			Yes: yes, Resume: parsed.value("resume", ""),
		}
		if err := requireRemoteConfirm(cc, yes || mutation.Resume != "", "provider provision"); err != nil {
			return Result{}, err
		}
		return driveRemoteMutation(ctx, cc, "provider provision", mutation,
			fmt.Sprintf("provider provision %s %s", slot, environment), !yes && mutation.Resume == "")
	case "test":
		return cc.Controller.Execute(ctx, ProviderTestRequest{
			Slot:        parsed.value("slot", ""),
			Environment: parsed.value("environment", ""),
			Adapter:     parsed.value("adapter", ""),
			Target:      parsed.value("target", ""),
		})
	default:
		return Result{}, usageError(spec.Usage)
	}
}

// providerAction is one provider subcommand's spec.
type providerAction struct {
	name string
	spec CommandSpec
}

// lookupProviderAction resolves one provider subcommand's spec so help and
// flag parsing share one declaration.
func lookupProviderAction(action string) (providerAction, bool) {
	jsonFlag := FlagSpec{Name: "json", Help: "emit the machine envelope"}
	specs := map[string]CommandSpec{
		"list": {Name: "provider list", Usage: "ggg provider list [--slot SLOT] [--json]", Flags: []FlagSpec{
			{Name: "slot", Help: "restrict to one slot", Value: true}, jsonFlag,
		}},
		"set": {Name: "provider set", Usage: "ggg provider set --provider SLOT:ENV=ADAPTER@TARGET... [--json]", Flags: []FlagSpec{
			{Name: "provider", Help: "selection (repeatable)", Value: true, Repeatable: true}, jsonFlag,
		}},
		"configure": {Name: "provider configure", Usage: "ggg provider configure --slot SLOT --environment ENV (--set KEY=VALUE)... [--yes] [--json]", Flags: []FlagSpec{
			{Name: "slot", Help: "provider slot", Value: true}, {Name: "environment", Help: "target environment", Value: true},
			{Name: "set", Help: "declared input value KEY=VALUE (repeatable)", Value: true, Repeatable: true},
			{Name: "yes", Help: "confirm writing CLI-managed values"}, jsonFlag,
		}},
		"provision": {Name: "provider provision", Usage: "ggg provider provision --slot SLOT --environment ENV [--yes] [--resume RUN_ID] [--json]", Flags: []FlagSpec{
			{Name: "slot", Help: "provider slot", Value: true}, {Name: "environment", Help: "target environment", Value: true},
			{Name: "yes", Help: "confirm the remote apply"}, {Name: "resume", Help: "resume a persisted run", Value: true}, jsonFlag,
		}},
		"test": {Name: "provider test", Usage: "ggg provider test --slot SLOT --environment ENV [--json]", Flags: []FlagSpec{
			{Name: "slot", Help: "provider slot", Value: true}, {Name: "environment", Help: "target environment", Value: true},
			{Name: "adapter", Help: "verify the selection matches this adapter", Value: true},
			{Name: "target", Help: "verify the selection matches this target", Value: true}, jsonFlag,
		}},
	}
	spec, ok := specs[action]
	if !ok {
		return providerAction{}, false
	}
	return providerAction{action, spec}, true
}

// ProviderListRequest lists the project's committed provider selections and
// their declared target surfaces.
type ProviderListRequest struct {
	Slot string
}

func (ProviderListRequest) sealedRequest() {}

// parseSetFlags parses repeatable --set KEY=VALUE operands.
func parseSetFlags(pairs []string) (map[string]string, error) {
	values := map[string]string{}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("--set must be KEY=VALUE, got %q", pair)
		}
		values[strings.TrimSpace(key)] = value
	}
	return values, nil
}

// runDeployment dispatches ggg deployment set MODULE.
func runDeployment(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "deployment")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 2 || parsed.positional[0] != "set" {
		return Result{}, usageError(spec.Usage)
	}
	return drivePlanMutation(ctx, cc, "deployment set", DeploymentSetMutation{Module: parsed.positional[1]}, false)
}

// runDeploy dispatches ggg deploy plan|apply|status|logs|rollback|secrets.
func runDeploy(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "deploy")
	if len(args) == 0 {
		return Result{}, usageError(spec.Usage)
	}
	action, rest := args[0], args[1:]
	parsed, err := parseArgv(spec, rest)
	if err != nil {
		return Result{}, err
	}
	cc.AsJSON = cc.AsJSON || parsed.Bool("json")
	yes := parsed.Bool("yes")
	environment := parsed.value("environment", "production")
	keys := parsed.List("key")

	switch action {
	case "plan":
		return cc.Controller.Execute(ctx, DeployStatusRequest{Environment: environment})
	case "status":
		return cc.Controller.Execute(ctx, DeployStatusRequest{Environment: environment})
	case "logs":
		return cc.Controller.Execute(ctx, DeployLogsRequest{Environment: environment, Follow: parsed.Bool("follow")})
	case "apply", "rollback", "secrets":
		mutation := DeployRemoteMutation{
			Action: action, Environment: environment, Keys: keys,
			Yes: yes, Resume: parsed.value("resume", ""),
		}
		if action == "apply" {
			// plan is a preview-shaped alias: run the plan/observe path.
			return driveRemoteMutation(ctx, cc, "deploy apply", mutation,
				"deploy "+action+" ("+environment+")", true)
		}
		if action == "secrets" && len(keys) == 0 {
			return Result{}, usageError("deploy secrets requires at least one --key KEY")
		}
		if err := requireRemoteConfirm(cc, yes, "deploy "+action); err != nil {
			return Result{}, err
		}
		return driveRemoteMutation(ctx, cc, "deploy "+action, mutation,
			"deploy "+action+" ("+environment+")", !yes)
	default:
		return Result{}, usageError(spec.Usage)
	}
}
