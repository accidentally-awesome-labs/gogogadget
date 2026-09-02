package gggcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

func runNew(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "new")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 1 {
		return Result{}, usageError(spec.Usage)
	}
	individual := false
	for _, name := range []string{"module", "profile", "provider", "deployment", "registry", "ref"} {
		if _, ok := parsed.flags[name]; ok || len(parsed.repeatable[name]) > 0 {
			individual = true
		}
	}
	var answers NewProjectAnswers
	if answersPath := parsed.value("answers", ""); answersPath != "" {
		if individual {
			return Result{}, usageError("--answers is mutually exclusive with individual answer flags")
		}
		file, openErr := os.Open(answersPath)
		if openErr != nil {
			return Result{}, usageError(openErr.Error())
		}
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		decodeErr := decoder.Decode(&answers)
		if decodeErr == nil {
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				decodeErr = fmt.Errorf("answers file must contain one JSON object")
			}
		}
		closeErr := file.Close()
		if decodeErr != nil {
			return Result{}, usageError(fmt.Sprintf("parse --answers: %v", decodeErr))
		}
		if closeErr != nil {
			return Result{}, runtimeError(closeErr)
		}
	} else {
		answers = NewProjectAnswers{
			Name: filepath.Base(filepath.Clean(parsed.positional[0])), Module: parsed.value("module", ""),
			Profile: parsed.value("profile", ""), Deployment: parsed.value("deployment", ""),
			Registry: parsed.value("registry", ""), Ref: parsed.value("ref", ""),
			Providers: map[string]modkit.ProviderSelections{},
		}
		for _, value := range parsed.List("provider") {
			slot, environment, selection, parseErr := parseProviderAnswer(value)
			if parseErr != nil {
				return Result{}, usageError(parseErr.Error())
			}
			selections := answers.Providers[slot]
			switch environment {
			case "development":
				selections.Development = selection
			case "test":
				selections.Test = selection
			case "production":
				selections.Production = selection
			}
			answers.Providers[slot] = selections
		}
	}
	if answers.Name == "" {
		answers.Name = filepath.Base(filepath.Clean(parsed.positional[0]))
	}
	if answers.Providers == nil {
		answers.Providers = map[string]modkit.ProviderSelections{}
	}
	if !parsed.Bool("non-interactive") && !cc.AsJSON && cc.Interactive {
		if answers.Module == "" {
			answers.Module, err = readLine(cc, "Go module path: ")
		}
		if err == nil && answers.Profile == "" {
			answers.Profile, err = readLine(cc, "Profile (minimal, web, api, saas, full): ")
		}
		if err == nil && answers.Registry == "" {
			answers.Registry, err = readLine(cc, "Registry (github:OWNER/REPO or directory:PATH): ")
		}
		if err == nil && answers.Deployment == "" {
			answers.Deployment, err = readLine(cc, "Deployment module: ")
		}
		if err != nil {
			return Result{}, ErrCancelled
		}
	}
	if answers.Module == "" || answers.Profile == "" || answers.Registry == "" {
		missing := "module, profile, and registry"
		if answers.Registry == "" {
			missing = "registry"
		}
		return Result{}, usageError("noninteractive new requires " + missing + " answers")
	}
	if strings.HasPrefix(answers.Registry, "github:") && answers.Ref == "" {
		if strings.HasPrefix(cc.Version, "v") {
			answers.Ref = cc.Version
		} else {
			return Result{}, usageError("development builds require an explicit --ref for a GitHub registry")
		}
	}
	mutation := NewMutation{
		Dir: parsed.positional[0], Name: answers.Name, ModulePath: answers.Module, Profile: scopedProfileID(answers.Profile),
		Providers: answers.Providers, Deployment: answers.Deployment, Registry: answers.Registry, Ref: answers.Ref,
	}
	return drivePlanMutation(ctx, cc, "new", mutation, false)
}

func scopedProfileID(profile string) string {
	if strings.Contains(profile, "/") {
		return profile
	}
	return "ggg/profile/" + profile
}

func runCreate(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "create")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 2 {
		return Result{}, usageError(spec.Usage)
	}
	maxAttempts := 0
	if value := parsed.value("max-attempts", ""); value != "" {
		maxAttempts, err = strconv.Atoi(value)
		if err != nil || maxAttempts < 1 {
			return Result{}, usageError("--max-attempts must be a positive integer")
		}
	}
	mutation := CreateMutation{
		Kind: parsed.positional[0], Name: parsed.positional[1], Scope: parsed.value("scope", ""),
		Table: parsed.value("table", ""), Route: parsed.value("route", ""), API: parsed.Bool("api"),
		Admin: parsed.Bool("admin"), Search: parsed.Bool("search"), NoUI: parsed.Bool("no-ui"),
		Family: parsed.value("family", ""), Owner: parsed.value("owner", ""), Migration: parsed.value("kind", ""),
		Schedulable: parsed.Bool("schedulable"), MaxAttempts: maxAttempts, Slot: parsed.value("slot", ""),
		Package: parsed.value("package", ""), Constructor: parsed.value("constructor", ""), Definition: parsed.value("definition", ""),
	}
	if mutation.Kind == "provider" && mutation.Definition == "" && !cc.Interactive {
		return Result{}, usageError("create provider requires --definition off-TTY")
	}
	return drivePlanMutation(ctx, cc, "create", mutation, false)
}

func runSetup(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	return runSimpleTask(ctx, cc, "setup", "", args)
}
func runGenerate(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	return runSimpleTask(ctx, cc, "generate", "", args)
}
func runDev(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	return runSimpleTask(ctx, cc, "dev", "", args)
}
func runCheck(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	return runSimpleTask(ctx, cc, "check", "", args)
}
func runBuild(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	return runSimpleTask(ctx, cc, "build", "", args)
}

func runSimpleTask(ctx context.Context, cc CommandContext, task, action string, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), task)
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 0 {
		return Result{}, usageError(spec.Usage)
	}
	return drivePlanMutation(ctx, cc, task, TaskMutation{Task: task, Action: action}, false)
}

func runServices(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "services")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 1 {
		return Result{}, usageError(spec.Usage)
	}
	mutation := TaskMutation{Task: "services", Action: parsed.positional[0], Environment: parsed.value("environment", "development"), Volumes: parsed.Bool("volumes")}
	return drivePlanMutation(ctx, cc, "services", mutation, false)
}

func runDB(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "db")
	if len(args) == 0 {
		return Result{}, usageError(spec.Usage)
	}
	switch args[0] {
	case "backup", "restore", "restore-drill":
		return runDBOps(ctx, cc, spec, args)
	default:
		return runDBTask(ctx, cc, spec, args)
	}
}

// runDBTask is the trusted-task path: migrate, status, seed, reset.
func runDBTask(ctx context.Context, cc CommandContext, spec CommandSpec, args []string) (Result, error) {
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 1 {
		return Result{}, usageError(spec.Usage)
	}
	action := parsed.positional[0]
	if action != "migrate" && action != "status" && action != "seed" && action != "reset" {
		return Result{}, usageError(spec.Usage)
	}
	mutation := TaskMutation{
		Task: "db", Action: action,
		Environment: parsed.value("environment", "development"),
		Yes:         parsed.Bool("yes"),
	}
	return drivePlanMutation(ctx, cc, "db", mutation, false)
}

// runDBOps is the database-operator path: backup, restore, restore-drill.
// Restore and drill are container mutations, so they follow the remote
// plan/confirm contract; restore never overwrites the active database.
func runDBOps(ctx context.Context, cc CommandContext, spec CommandSpec, args []string) (Result, error) {
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	cc.AsJSON = cc.AsJSON || parsed.Bool("json")
	yes := parsed.Bool("yes")
	mutation := DatabaseOpsMutation{
		Action:      parsed.positional[0],
		Environment: parsed.value("environment", "development"),
		Destination: parsed.value("destination", ""),
		BackupID:    parsed.value("backup", ""),
		DestURLKey:  parsed.value("to-env", ""),
		Yes:         yes,
	}
	if mutation.Action == "backup" && mutation.Destination == "" {
		return Result{}, usageError("db backup requires --destination PATH")
	}
	if mutation.Action == "restore" {
		if err := requireRemoteConfirm(cc, yes, "db restore"); err != nil {
			return Result{}, err
		}
	}
	if mutation.Action == "restore-drill" {
		if err := requireRemoteConfirm(cc, yes, "db restore-drill"); err != nil {
			return Result{}, err
		}
	}
	confirm := mutation.Action != "backup"
	return driveRemoteMutation(ctx, cc, "db "+mutation.Action, mutation, "db "+mutation.Action, confirm && !yes)
}

func runTest(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "test")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 1 {
		return Result{}, usageError(spec.Usage)
	}
	return drivePlanMutation(ctx, cc, "test", TaskMutation{Task: "test", Action: parsed.positional[0]}, false)
}
