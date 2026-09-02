package gggcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/gogogadget/gogogadget/internal/remote"
)

// ---------------------------------------------------------------------------
// Progress and secret plumbing for remote applies

// progressSink renders apply progress: the human stream gets prefixed
// lines, JSON runs discard because the envelope is the machine output.
func progressSink(cc CommandContext) remote.ProgressSink {
	if cc.AsJSON {
		return remote.DiscardProgress
	}
	return remote.ProgressFunc(func(event remote.ProgressEvent) {
		line := fmt.Sprintf("[%s] %s", event.Stage, event.Message)
		if event.Total > 0 {
			line += fmt.Sprintf(" (%d/%d)", event.Current, event.Total)
		}
		fmt.Fprintln(cc.Err, line)
	})
}

// secretSink is the only channel a produced credential leaves through.
// Development and test values land in the CLI-managed, mode-0600
// .ggg/env/<environment>.env file. Production refuses: production secrets
// are never persisted by the CLI — they belong to the deployment
// environment or the deploy target's secret store, reached with
// `ggg deploy secrets`.
func secretSink(c *Controller, cc CommandContext, environment string) (remote.SecretSink, error) {
	if environment == "production" {
		return nil, refusalError(errors.New(
			"the CLI never persists production secrets; provision, then deliver the produced values with " +
				"`ggg deploy secrets --key DATABASE_URL --yes` or your deployment environment",
		))
	}
	return envFileSecretSink{controller: c, environment: environment}, nil
}

// envFileSecretSink merges produced values into the environment's
// CLI-managed env file at mode 0600.
type envFileSecretSink struct {
	controller  *Controller
	environment string
}

func (s envFileSecretSink) Put(_ context.Context, key, value string) error {
	if err := remote.WriteEnvironmentEnvFile(s.controller.rootDir(), s.environment, map[string]string{key: value}); err != nil {
		return runtimeError(err)
	}
	if s.controller.redactor == nil {
		s.controller.redactor = NewRedactor()
	}
	s.controller.redactor.RegisterSecret(key, value)
	return nil
}

// ---------------------------------------------------------------------------
// provider configure

// previewProviderConfigure resolves the selected target's declared fields
// and validates the operator-supplied values. Nothing writes; the payload
// reports field declarations and, after validation, configured and missing
// key names — never values.
func (c *Controller) previewProviderConfigure(ctx context.Context, mutation ProviderRemoteMutation) (Plan, error) {
	if _, err := c.resolveSlotChoice(mutation.Slot, mutation.Environment); err != nil {
		return Plan{}, err
	}
	if _, err := c.parseConfigureValues(mutation.Slot, mutation.Environment, mutation.Values); err != nil {
		return Plan{}, err
	}
	if mutation.Environment == "production" {
		for key, value := range mutation.Values {
			if value != "" {
				return Plan{}, refusalError(fmt.Errorf(
					"provider configure never writes %s (production); production values come from the deployment environment", key,
				))
			}
		}
	}
	return Plan{Command: "provider configure", mutation: mutation}, nil
}

// parseConfigureValues validates --set KEY=VALUE values against the target's
// declared inputs: unknown keys refuse, declared types parse.
func (c *Controller) parseConfigureValues(slot, environment string, values map[string]string) (map[string]string, error) {
	choice, err := c.resolveSlotChoice(slot, environment)
	if err != nil {
		return nil, err
	}
	declared := map[string]modkit.TargetInput{}
	for _, input := range choice.Target.Inputs {
		declared[input.Key] = input
		if input.EnvKey != "" {
			declared[input.EnvKey] = input
		}
	}
	resolved := map[string]string{}
	for key, value := range values {
		input, ok := declared[key]
		if !ok {
			return nil, usageError(fmt.Sprintf("%s is not a declared input of %s@%s", key, choice.Adapter, choice.TargetID))
		}
		if err := validateTargetInput(input, value); err != nil {
			return nil, usageError(err.Error())
		}
		resolved[key] = value
	}
	return resolved, nil
}

// applyProviderConfigure merges the validated values into the CLI-managed
// env file (development and test only) and reports configured/missing key
// names without values.
func (c *Controller) applyProviderConfigure(ctx context.Context, cc CommandContext, plan Plan, mutation ProviderRemoteMutation) (Result, error) {
	choice, err := c.resolveSlotChoice(mutation.Slot, mutation.Environment)
	if err != nil {
		return Result{}, err
	}
	values, err := c.parseConfigureValues(mutation.Slot, mutation.Environment, mutation.Values)
	if err != nil {
		return Result{}, err
	}
	// Values travel to the env file keyed by the input's env_key: the
	// generated runtime parses adapter-owned keys from the environment.
	envValues := map[string]string{}
	for key, value := range values {
		envKey := key
		for _, input := range choice.Target.Inputs {
			if input.Key == key && input.EnvKey != "" {
				envKey = input.EnvKey
			}
		}
		envValues[envKey] = value
	}
	if len(envValues) > 0 {
		if err := remote.WriteEnvironmentEnvFile(c.rootDir(), mutation.Environment, envValues); err != nil {
			return Result{}, runtimeError(err)
		}
	}
	secrets := c.secretValues(mutation.Environment)
	configured, missing := configuredInputs(choice, secrets)
	payload := map[string]any{
		"provider": map[string]any{
			"slot": choice.Slot, "environment": mutation.Environment,
			"adapter": choice.Adapter, "target": choice.TargetID,
			"configured": configured, "missing": missing,
			"fields": targetChecklist(choice)[1]["fields"],
		},
	}
	env := normalizeEnvelope(modkit.Envelope{Command: plan.Command, OK: true, Exit: exitOK})
	return Result{Envelope: env, Payload: payload}, nil
}

// ---------------------------------------------------------------------------
// database operations

// resolveDatabaseTarget resolves the selected database adapter's operator
// for one environment, with the compose facts the local operator needs.
func (c *Controller) resolveDatabaseTarget(environment string) (slotChoice, remote.DatabaseOperator, error) {
	choice, err := c.resolveSlotChoice("ggg/database", environment)
	if err != nil {
		return slotChoice{}, nil, err
	}
	if choice.Target.DatabaseOperator == "" {
		return slotChoice{}, nil, &checklistError{
			choice:    choice,
			checklist: targetChecklist(choice),
			advice: fmt.Sprintf(
				"target %s@%s declares no database operator; backups are the provider console's job (%s)",
				choice.Adapter, choice.TargetID, choice.Target.ConsoleURL,
			),
		}
	}
	operator, ok := c.remoteDatabaseOperator(choice.Target.DatabaseOperator)
	if !ok {
		return slotChoice{}, nil, refusalError(fmt.Errorf("database operator %s is not installed", choice.Target.DatabaseOperator))
	}
	return choice, operator, nil
}

// databaseRequestState derives the operator state from the selected target's
// local service declaration: compose service name, user, and database name.
func databaseRequestState(choice slotChoice) map[string]string {
	state := map[string]string{
		"service":  modkit.ComposeServiceName(choice.Adapter, choice.TargetID),
		"user":     "postgres",
		"database": "gogogadget",
	}
	if service := choice.Target.LocalService; service != nil {
		for _, variable := range service.Environment {
			switch variable.Key {
			case "POSTGRES_USER":
				state["user"] = variable.Value
			case "POSTGRES_DB":
				state["database"] = variable.Value
			}
		}
	}
	return state
}

// previewDatabaseOps resolves what one database operation would do. Backup
// is a local artifact; restore and drill create a brand-new database and
// verify it — the active database is never touched.
func (c *Controller) previewDatabaseOps(ctx context.Context, mutation DatabaseOpsMutation) (Plan, error) {
	if mutation.Environment == "" {
		mutation.Environment = "development"
	}
	choice, operator, err := c.resolveDatabaseTarget(mutation.Environment)
	if err != nil {
		return Plan{}, err
	}
	var checklistErr *checklistError
	if errors.As(err, &checklistErr) {
		return Plan{}, checklistErr
	}
	switch mutation.Action {
	case "backup":
		if mutation.Destination == "" {
			return Plan{}, usageError("db backup requires --destination PATH|provider")
		}
	case "restore", "restore-drill":
		if mutation.Action == "restore" {
			if mutation.BackupID == "" || mutation.DestURLKey == "" {
				return Plan{}, usageError("db restore requires --backup ID --to-env KEY")
			}
			if _, err := c.recordedBackup(mutation.BackupID); err != nil {
				return Plan{}, err
			}
		}
		if mutation.Action == "restore-drill" {
			if mutation.BackupID == "" {
				return Plan{}, usageError("db restore-drill requires --backup ID")
			}
			if _, err := c.recordedBackup(mutation.BackupID); err != nil {
				return Plan{}, err
			}
		}
		_ = operator
	default:
		return Plan{}, usageError("db action must be backup, restore, or restore-drill")
	}
	summary := choice.Adapter + "@" + choice.TargetID + " " + mutation.Action
	return Plan{
		Command:  "db " + mutation.Action,
		RunID:    remoteRunID("db", remote.ObservedStateHash(mutation)),
		Remote:   []RemotePlan{newRemotePlan("deploy", summary, "ggg/database", mutation.Environment, remote.ObservedStateHash(mutation), "", nil)},
		mutation: mutation,
	}, nil
}

// recordedBackup reads one backup the state store recorded.
func (c *Controller) recordedBackup(id string) (remote.BackupState, error) {
	state, err := c.loadState()
	if err != nil {
		return remote.BackupState{}, err
	}
	record, ok := state.Backups[id]
	if !ok {
		return remote.BackupState{}, refusalError(fmt.Errorf("backup %s is not recorded in %s; run ggg db backup first", id, remote.StateFilePath()))
	}
	return remote.BackupState{
		ID: record.ID, Location: record.Location, SHA256: record.SHA256, CreatedAt: record.CreatedAt,
	}, nil
}

// applyDatabaseOps executes the confirmed database operation.
func (c *Controller) applyDatabaseOps(ctx context.Context, cc CommandContext, plan Plan, mutation DatabaseOpsMutation) (Result, error) {
	if mutation.Environment == "" {
		mutation.Environment = "development"
	}
	choice, operator, err := c.resolveDatabaseTarget(mutation.Environment)
	if err != nil {
		return Result{}, err
	}
	request := remote.DatabaseRequest{
		Root: c.rootDir(), Environment: mutation.Environment,
		Adapter: choice.Adapter, Target: choice.TargetID,
		Secrets: c.secretValues(mutation.Environment),
		State:   databaseRequestState(choice),
	}
	progress := progressSink(cc)
	command := "db " + mutation.Action
	switch mutation.Action {
	case "backup":
		backup, err := operator.Backup(ctx, request, mutation.Destination, progress)
		if err != nil {
			return c.databaseFailure(command, err)
		}
		store, err := c.loadState()
		if err != nil {
			return Result{}, err
		}
		store.RecordBackup(remote.BackupRecord{
			ID: backup.ID, Location: backup.Location, SHA256: backup.SHA256, CreatedAt: backup.CreatedAt,
		})
		if err := store.Save(c.rootDir()); err != nil {
			return Result{}, runtimeError(err)
		}
		env := normalizeEnvelope(modkit.Envelope{Command: command, OK: true, Exit: exitOK})
		return Result{Envelope: env, Payload: map[string]any{"backup": backupPayload(backup)}}, nil
	case "restore":
		backup, err := c.recordedBackup(mutation.BackupID)
		if err != nil {
			return Result{}, err
		}
		restored, err := operator.Restore(ctx, request, backup, mutation.DestURLKey, request.Secrets, progress)
		if err != nil {
			return c.databaseFailure(command, err)
		}
		env := normalizeEnvelope(modkit.Envelope{Command: command, OK: true, Exit: exitOK})
		payload := map[string]any{"restore": map[string]any{
			"database_id": restored.DatabaseID, "url_key": restored.URLKey, "ready": restored.Ready,
		}}
		return Result{Envelope: env, Payload: payload}, nil
	case "restore-drill":
		backup, err := c.recordedBackup(mutation.BackupID)
		if err != nil {
			return Result{}, err
		}
		result, err := operator.RestoreDrill(ctx, request, backup, request.Secrets, progress)
		if err != nil {
			return c.databaseFailure(command, err)
		}
		env := normalizeEnvelope(modkit.Envelope{Command: command, OK: true, Exit: exitOK})
		payload := map[string]any{"drill": map[string]any{
			"backup_id": result.BackupID, "database_id": result.DatabaseID,
			"ready": result.Ready, "smoke_passed": result.SmokePassed,
			"duration": result.Duration.String(),
		}}
		return Result{Envelope: env, Payload: payload}, nil
	}
	return Result{}, usageError("db action must be backup, restore, or restore-drill")
}

func (c *Controller) databaseFailure(command string, err error) (Result, error) {
	var notConfigured *remote.ErrNotConfigured
	if errors.As(err, &notConfigured) {
		return Result{}, refusalError(fmt.Errorf("%s: %s", command, notConfigured.Advice))
	}
	return Result{}, runtimeError(err)
}

func backupPayload(backup remote.BackupState) map[string]any {
	return map[string]any{
		"id": backup.ID, "location": backup.Location, "sha256": backup.SHA256,
		"created_at": backup.CreatedAt,
	}
}

// ---------------------------------------------------------------------------
// doctor --runtime and --fix

// runtimeFindings runs the live doctor checks: selected provider keys,
// provider health, deployment linkage, and backup policy. Findings are
// typed by code; every fixable finding names its remediation.
func (c *Controller) runtimeFindings(ctx context.Context, project modkit.Project) []modkit.HealthFinding {
	findings := make([]modkit.HealthFinding, 0)

	// Deployment linkage: the intent names a deployment and it is installed.
	if project.Deployment == "" {
		findings = append(findings, modkit.HealthFinding{
			Code: "deployment_unlinked", Severity: "warn",
			Message: "no deployment module is selected; run ggg deployment set MODULE",
		})
	} else {
		lock, err := c.loadLock()
		if err == nil {
			installed := false
			for _, module := range lock.Modules {
				if module.ID == project.Deployment && module.Reason != modkit.TombstoneReason {
					installed = true
					break
				}
			}
			if !installed {
				findings = append(findings, modkit.HealthFinding{
					Code: "deployment_unlinked", Severity: "error", Module: project.Deployment,
					Message: "deployment module " + project.Deployment + " is not installed; run ggg sync",
				})
			}
		}
	}

	lock, err := c.loadLock()
	if err != nil {
		return findings
	}

	// Selected provider keys and health, per slot per environment.
	slots := make([]string, 0, len(project.Providers))
	for slot := range project.Providers {
		slots = append(slots, slot)
	}
	sortStrings(slots)
	for _, slot := range slots {
		for _, environment := range []string{"development", "test", "production"} {
			choice, err := resolveSlotChoiceFromLock(lock, slot, environment)
			if err != nil {
				continue
			}
			secrets := c.secretValues(environment)
			configured, missing := configuredInputs(choice, secrets)
			_ = configured
			if len(missing) > 0 {
				findings = append(findings, modkit.HealthFinding{
					Code: "provider_keys_missing", Severity: "warn", Module: choice.Adapter,
					Message: fmt.Sprintf("%s %s: missing %s", slot, environment, strings.Join(missing, ", ")),
				})
				continue
			}
			if choice.Target.Provisioner == "" {
				continue
			}
			provisioner, ok := c.remoteProvisioner(choice.Target.Provisioner)
			if !ok {
				continue
			}
			state, _ := c.providerPrior(choice)
			status, err := provisioner.Check(ctx, c.providerRequest(choice, state))
			if err != nil {
				var notConfigured *remote.ErrNotConfigured
				if errors.As(err, &notConfigured) {
					findings = append(findings, modkit.HealthFinding{
						Code: "provider_not_configured", Severity: "warn", Module: choice.Adapter,
						Message: fmt.Sprintf("%s %s: %s", slot, environment, notConfigured.Error()),
					})
					continue
				}
				findings = append(findings, modkit.HealthFinding{
					Code: "provider_unhealthy", Severity: "warn", Module: choice.Adapter,
					Message: fmt.Sprintf("%s %s check failed: %v", slot, environment, err),
				})
				continue
			}
			if !status.Healthy {
				findings = append(findings, modkit.HealthFinding{
					Code: "provider_unhealthy", Severity: "warn", Module: choice.Adapter,
					Message: fmt.Sprintf("%s %s: %s", slot, environment, status.Message),
				})
			}
		}
	}

	// Database operator availability and backup policy.
	if _, selections, ok := databaseSelection(project); ok {
		_ = selections
		if _, operator, err := c.resolveDatabaseTarget("development"); err == nil {
			_ = operator
			state, stateErr := c.loadState()
			if stateErr == nil && len(state.Backups) == 0 {
				findings = append(findings, modkit.HealthFinding{
					Code: "backup_missing", Severity: "warn",
					Message: "no backup has been taken; run ggg db backup --destination backups/ before relying on this database",
				})
			}
		}
	}

	// Environment file readiness: compose parses .ggg/env/<env>.env as
	// env_file for every services subcommand.
	for _, environment := range []string{"development", "test"} {
		keys := remote.EnvFileKeys(c.rootDir(), environment)
		_ = keys
		if !envFileExists(c.rootDir(), environment) {
			findings = append(findings, modkit.HealthFinding{
				Code: "env_file_missing", Severity: "warn", Path: remote.EnvironmentEnvFile(environment),
				Message: remote.EnvironmentEnvFile(environment) + " is missing; compose env_file parsing needs it (ggg doctor --fix creates it)",
			})
		}
	}
	return findings
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func databaseSelection(project modkit.Project) (string, modkit.ProviderSelections, bool) {
	selections, ok := project.Providers["ggg/database"]
	return "ggg/database", selections, ok
}

func envFileExists(root, environment string) bool {
	return fileExistsAt(root, remote.EnvironmentEnvFile(environment))
}

func fileExistsAt(root, relative string) bool {
	_, err := os.Stat(root + string(os.PathSeparator) + filepath.FromSlash(relative))
	return err == nil
}

// executeDoctorRuntime extends the offline health report with the live
// runtime checks.
func (c *Controller) executeDoctorRuntime(ctx context.Context) (Result, error) {
	project, err := c.loadProject()
	if err != nil {
		return Result{}, err
	}
	engine, err := c.engine(true)
	if err != nil {
		return Result{}, err
	}
	report, err := engine.Health(ctx, c.rootDir())
	if err != nil {
		return Result{}, runtimeError(err)
	}
	findings := c.runtimeFindings(ctx, project)
	report.Findings = append(report.Findings, findings...)
	for _, finding := range findings {
		if finding.Severity == "error" {
			report.Ok = false
		}
	}
	diagnostics := make([]modkit.Diagnostic, 0, len(report.Findings))
	fixable := make([]string, 0)
	for _, finding := range report.Findings {
		diagnostics = append(diagnostics, modkit.Diagnostic{
			Code: finding.Code, Severity: finding.Severity,
			Module: finding.Module, Path: finding.Path, Message: finding.Message,
		})
		if doctorRemediation(finding.Code) != "" {
			fixable = append(fixable, finding.Code)
		}
	}
	exit := exitOK
	if !report.Ok {
		exit = exitRefusal
	}
	env := normalizeEnvelope(modkit.Envelope{
		Command: "doctor", OK: report.Ok, RegistryCommit: report.RegistryCommit,
		Diagnostics: diagnostics, Exit: exit,
	})
	payload := map[string]any{"runtime": map[string]any{
		"checked_at": time.Now().UTC(), "findings": len(findings), "fixable": fixable,
	}}
	if exit != exitOK {
		return Result{Envelope: env, Payload: payload}, refusalError(fmt.Errorf("doctor: %d finding(s)", len(diagnostics)))
	}
	return Result{Envelope: env, Payload: payload}, nil
}

// doctorRemediation names the only remediation each finding code carries.
// A code without an entry here refuses --fix: a finding without a typed
// remediation is a human decision.
func doctorRemediation(code string) string {
	switch code {
	case "env_file_missing":
		return "create the missing .ggg/env/<environment>.env files at mode 0600"
	default:
		return ""
	}
}

// applyDoctorFix performs the remediation attached to one typed finding.
func (c *Controller) applyDoctorFix(ctx context.Context, plan Plan, mutation DoctorFixMutation) (Result, error) {
	if !mutation.Yes {
		return Result{}, refusalError(errors.New("doctor fix: noninteractive runs require --yes"))
	}
	if doctorRemediation(mutation.FindingCode) == "" {
		return Result{}, refusalError(fmt.Errorf(
			"finding %s carries no automated remediation: %s", mutation.FindingCode, remediationAdvice(mutation.FindingCode),
		))
	}
	switch mutation.FindingCode {
	case "env_file_missing":
		created := make([]string, 0)
		for _, environment := range []string{"development", "test"} {
			if envFileExists(c.rootDir(), environment) {
				continue
			}
			if err := remote.WriteEnvironmentEnvFile(c.rootDir(), environment, map[string]string{}); err != nil {
				return Result{}, runtimeError(err)
			}
			created = append(created, remote.EnvironmentEnvFile(environment))
		}
		env := normalizeEnvelope(modkit.Envelope{Command: "doctor fix", OK: true, Exit: exitOK})
		for _, path := range created {
			env.Changes = append(env.Changes, modkit.Change{
				Path: path, Module: "gggcli", Kind: modkit.ChangeCreate, Class: modkit.DestinationIntent,
			})
		}
		return Result{Envelope: env, Payload: map[string]any{"fixed": mutation.FindingCode}}, nil
	default:
		return Result{}, refusalError(fmt.Errorf("finding %s carries no automated remediation", mutation.FindingCode))
	}
}

func remediationAdvice(code string) string {
	switch code {
	case "provider_keys_missing":
		return "set the named keys with ggg provider configure or in the deployment environment"
	case "provider_unhealthy", "provider_not_configured":
		return "inspect the provider console, then re-run ggg provider test"
	case "deployment_unlinked":
		return "run ggg deployment set MODULE"
	case "backup_missing":
		return "run ggg db backup --destination PATH"
	case "env_file_missing":
		return "ggg doctor --fix --finding env_file_missing creates the file"
	default:
		return "resolve the finding manually"
	}
}
