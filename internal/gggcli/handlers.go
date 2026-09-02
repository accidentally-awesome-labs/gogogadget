package gggcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/gogogadget/gogogadget/internal/identity"
	identityclerk "github.com/gogogadget/gogogadget/internal/identity/clerk"
	identitydev "github.com/gogogadget/gogogadget/internal/identity/devadapter"
	identitysession "github.com/gogogadget/gogogadget/internal/identity/session"
	"github.com/gogogadget/gogogadget/internal/modkit"
)

// parsedArgs is the outcome of parsing one command's operands against its
// CommandSpec: declared flags by name, repeatable flags collected in order,
// and the positional words.
type parsedArgs struct {
	flags      map[string]string
	repeatable map[string][]string
	positional []string
}

// String returns a flag's value or its declared default.
func (p parsedArgs) value(name, def string) string {
	if v, ok := p.flags[name]; ok && v != "" {
		return v
	}
	return def
}

// Bool reports whether a boolean flag was present.
func (p parsedArgs) Bool(name string) bool {
	return p.flags[name] == "true"
}

// List returns every occurrence of a repeatable flag.
func (p parsedArgs) List(name string) []string {
	return p.repeatable[name]
}

// parseArgv parses flags that may appear before, after, or between positional
// arguments, against the spec's declared flags. Go's flag package stops at the
// first non-flag token, which would silently drop a trailing --json; this
// parser does not. Undeclared flags are usage failures, exactly as before.
func parseArgv(spec CommandSpec, args []string) (parsedArgs, error) {
	parsed := parsedArgs{
		flags:      map[string]string{},
		repeatable: map[string][]string{},
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			parsed.positional = append(parsed.positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			name := strings.TrimLeft(arg, "-")
			value := ""
			hasValue := false
			if eq := strings.Index(name, "="); eq >= 0 {
				value, hasValue = name[eq+1:], true
				name = name[:eq]
			}
			flag, ok := spec.spec(name)
			if !ok {
				return parsed, usageError(fmt.Sprintf("flag provided but not defined: -%s", name))
			}
			if flag.Value {
				if !hasValue {
					if i+1 >= len(args) {
						return parsed, usageError(fmt.Sprintf("flag needs an argument: -%s", name))
					}
					i++
					value = args[i]
				}
				if flag.Repeatable {
					parsed.repeatable[name] = append(parsed.repeatable[name], value)
				} else {
					parsed.flags[name] = value
				}
				continue
			}
			if hasValue {
				return parsed, usageError(fmt.Sprintf("flag -%s takes no value", name))
			}
			parsed.flags[name] = "true"
			continue
		}
		parsed.positional = append(parsed.positional, arg)
	}
	return parsed, nil
}

// builtInHandlers binds every reserved command name to its handler. The
// handlers parse operands into typed requests and mutations, hand them to the
// controller, and return the boundary result; the App renders.
func builtInHandlers() map[string]CommandHandler {
	return map[string]CommandHandler{
		"version":    runVersion,
		"new":        runNew,
		"create":     runCreate,
		"setup":      runSetup,
		"generate":   runGenerate,
		"services":   runServices,
		"dev":        runDev,
		"db":         runDB,
		"check":      runCheck,
		"test":       runTest,
		"build":      runBuild,
		"init":       runInit,
		"catalog":    runCatalog,
		"info":       runInfo,
		"add":        runGraphMutation(modkit.OpAdd),
		"remove":     runGraphMutation(modkit.OpRemove),
		"update":     runGraphMutation(modkit.OpUpdate),
		"sync":       runSync,
		"diff":       runDiff,
		"resolve":    runResolve,
		"identity":   runIdentity,
		"doctor":     runDoctor,
		"migrate":    runMigrate,
		"cache":      runCache,
		"registry":   runRegistry,
		"provider":   runProvider,
		"deployment": runDeployment,
		"deploy":     runDeploy,
		"help":       runHelp,
		"completion": runCompletion,
	}
}

func runVersion(_ context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "version")
	if len(args) != 0 {
		return Result{}, usageError(spec.Usage)
	}
	version := cc.Version
	if version == "" {
		version = "dev"
	}
	return Result{Payload: map[string]any{"version": version}}, nil
}

func runHelp(_ context.Context, cc CommandContext, args []string) (Result, error) {
	// Help is derived from the full table — built-ins plus contributed
	// commands — so a contributed command is documented the moment it
	// installs.
	return Result{Payload: map[string]any{"text": renderHelp(cc.Table, strings.Join(args, " "))}}, nil
}

func runCompletion(_ context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "completion")
	if len(args) != 1 {
		return Result{}, usageError(spec.Usage)
	}
	script, err := renderCompletion(cc.Table, args[0])
	if err != nil {
		return Result{}, err
	}
	return Result{Payload: map[string]any{"text": script}}, nil
}

func runCatalog(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "catalog")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 0 {
		return Result{}, usageError(spec.Usage)
	}
	return cc.Controller.Execute(ctx, CatalogRequest{
		Installed: parsed.Bool("installed"),
		Kind:      parsed.value("kind", ""),
		Latest:    parsed.Bool("latest"),
	})
}

func runInfo(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "info")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 1 {
		return Result{}, usageError(spec.Usage)
	}
	return cc.Controller.Execute(ctx, InfoRequest{ModuleID: parsed.positional[0]})
}

func runDiff(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "diff")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	return cc.Controller.Execute(ctx, DiffRequest{Modules: parsed.positional, Upstream: parsed.Bool("upstream")})
}

func runDoctor(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "doctor")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 0 {
		return Result{}, usageError(spec.Usage)
	}
	if parsed.Bool("fix") {
		mutation := DoctorFixMutation{
			FindingCode: parsed.value("finding", ""),
			Yes:         parsed.Bool("yes"),
		}
		if mutation.FindingCode == "" {
			return Result{}, usageError("doctor --fix requires --finding CODE naming the finding to remediate")
		}
		if !mutation.Yes {
			return Result{}, refusalError(errors.New("doctor fix: noninteractive runs require --yes"))
		}
		plan, err := cc.Controller.Preview(ctx, mutation)
		if err != nil {
			return Result{}, err
		}
		result, err := cc.Controller.Apply(ctx, plan)
		if err != nil {
			if result.Envelope.Command == "" {
				result.Envelope.Command = "doctor fix"
			}
			return Result{Envelope: normalizeEnvelope(result.Envelope), Payload: result.Payload}, err
		}
		return Result{Envelope: normalizeEnvelope(result.Envelope), Payload: result.Payload}, nil
	}
	return cc.Controller.Execute(ctx, DoctorRequest{Runtime: parsed.Bool("runtime")})
}

// runGraphMutation drives add/remove/update. All three edit intent and
// converge on the same reconciler, so they share one code path.
func runGraphMutation(kind modkit.OperationKind) CommandHandler {
	return func(ctx context.Context, cc CommandContext, args []string) (Result, error) {
		spec, ok := lookupSpec(builtInCommands(), string(kind))
		if !ok {
			return Result{}, usageError(fmt.Sprintf("unknown command %q", kind))
		}
		parsed, err := parseArgv(spec, args)
		if err != nil {
			return Result{}, err
		}
		purge := parsed.Bool("purge-data")
		ref := parsed.value("ref", "")
		if kind != modkit.OpRemove && purge {
			return Result{}, usageError("--purge-data is only valid for `ggg remove`")
		}
		if kind != modkit.OpUpdate && strings.TrimSpace(ref) != "" {
			return Result{}, usageError("--ref is only valid for `ggg update`")
		}

		switch kind {
		case modkit.OpAdd, modkit.OpRemove:
			if len(parsed.positional) == 0 {
				return Result{}, usageError(spec.Usage)
			}
			for _, id := range parsed.positional {
				if err := modkit.ValidateInstallableModuleID(id); err != nil {
					return Result{}, usageError(fmt.Sprintf("%s: %v", id, err))
				}
			}
		case modkit.OpUpdate:
			// Two exact forms: named modules advance, or exactly one
			// registry's ref moves. The planner refuses any mixed intent.
			targetedRegistry := parsed.value("registry", "")
			if targetedRegistry != "" && ref == "" {
				return Result{}, usageError("--registry requires --ref")
			}
			if targetedRegistry != "" && len(parsed.positional) != 0 {
				return Result{}, usageError("update accepts either module operands or --registry with --ref, not both")
			}
			update := GraphMutation{
				Kind: kind, Modules: parsed.positional, Ref: ref, Registry: targetedRegistry,
				DryRun: parsed.Bool("dry-run"), PurgeData: purge,
			}
			return drivePlanMutation(ctx, cc, spec.Name, update, update.DryRun)
		}

		mutation := GraphMutation{
			Kind:      kind,
			Modules:   parsed.positional,
			Ref:       ref,
			DryRun:    parsed.Bool("dry-run"),
			PurgeData: purge,
		}
		return drivePlanMutation(ctx, cc, spec.Name, mutation, mutation.DryRun)
	}
}

func runSync(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "sync")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 0 {
		return Result{}, usageError(spec.Usage)
	}
	claims := parsed.List("claim")
	check := parsed.Bool("check")
	if check && len(claims) > 0 {
		return Result{}, usageError("--claim mutates the lock and cannot be combined with --check")
	}
	mutation := SyncMutation{Check: check, Offline: parsed.Bool("offline"), Claims: claims}
	return drivePlanMutation(ctx, cc, "sync", mutation, check)
}

// drivePlanMutation is the one path from a mutation to rendered output: flag
// callers, guided forms, and the TUI all reach it through Preview and Apply,
// so a dry run never writes and an apply is the previewed plan.
func drivePlanMutation(ctx context.Context, cc CommandContext, command string, mutation Mutation, readOnly bool) (Result, error) {
	plan, err := cc.Controller.Preview(ctx, mutation)
	if err != nil {
		var planned plannerFailure
		if errors.As(err, &planned) {
			result, cause := failureEnvelope(command, err)
			return result, cause
		}
		return Result{}, err
	}
	if readOnly {
		// Check/dry-run: report the plan, never write. Drift in planner output
		// or generated aggregates is the declared conflict exit.
		if plan.Local == nil {
			return Result{Envelope: normalizeEnvelope(modkit.Envelope{Command: command, OK: true, Exit: exitOK})}, nil
		}
		drift := countDrift(*plan.Local)
		exit := exitOK
		if drift > 0 || len(plan.Diagnostics) > 0 {
			exit = exitConflict
		}
		env := planEnvelope(*plan.Local, command, exit)
		if exit != exitOK {
			return Result{Envelope: env}, conflictExit(fmt.Errorf("%s: %d pending change(s), %d generated drift(s)",
				command, drift, len(plan.Diagnostics)))
		}
		return Result{Envelope: env}, nil
	}

	result, err := cc.Controller.Apply(ctx, plan)
	if err != nil {
		// The apply failure already carries the rollback envelope; emit it and
		// propagate the coded error.
		if result.Envelope.Command == "" {
			result.Envelope.Command = command
		}
		result.Envelope = normalizeEnvelope(result.Envelope)
		return result, err
	}
	env := result.Envelope
	env.Command = command
	if len(env.Conflicts) > 0 && command != "resolve" {
		return Result{Envelope: normalizeEnvelope(env), Payload: result.Payload},
			conflictExit(fmt.Errorf("%s: %d staged conflict(s) remain; run `ggg resolve`", command, len(env.Conflicts)))
	}
	return Result{Envelope: normalizeEnvelope(env), Payload: result.Payload}, nil
}

func runInit(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "init")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 0 {
		return Result{}, usageError(spec.Usage)
	}
	if _, statErr := os.Stat(filepath.Join(cc.Controller.Root(), "go.mod")); os.IsNotExist(statErr) {
		modulePath := parsed.value("module", "")
		if modulePath == "" && cc.Interactive && !cc.AsJSON {
			modulePath, err = readLine(cc, "Go module path: ")
			if err != nil {
				return Result{}, ErrCancelled
			}
		}
		if modulePath == "" {
			return Result{}, usageError("ggg init without go.mod requires --module")
		}
		registry := "github:" + parsed.value("repository", DefaultRegistryRepository)
		if _, registryErr := os.Stat(filepath.Join(cc.Controller.Root(), "registry.json")); registryErr == nil {
			registry = "directory:."
		}
		mutation := NewMutation{
			Dir: cc.Controller.Root(), Name: filepath.Base(filepath.Clean(cc.Controller.Root())),
			ModulePath: modulePath, Profile: "ggg/profile/minimal",
			Providers: map[string]modkit.ProviderSelections{}, Deployment: "",
			Registry: registry, Ref: parsed.value("ref", "main"), InPlace: true,
		}
		return drivePlanMutation(ctx, cc, "init", mutation, false)
	} else if statErr != nil {
		return Result{}, runtimeError(statErr)
	}
	mutation := InitMutation{
		Ref:        parsed.value("ref", "main"),
		Repository: parsed.value("repository", DefaultRegistryRepository),
		PublicKey:  parsed.value("public-key", ""),
		Adopt:      parsed.Bool("adopt"),
		Offline:    parsed.Bool("offline"),
		Claims:     parsed.List("claim"),
	}
	return cc.Controller.applyInit(ctx, mutation)
}

func runResolve(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "resolve")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 1 {
		return Result{}, usageError(spec.Usage)
	}
	id := parsed.positional[0]
	if err := modkit.ValidateInstallableModuleID(id); err != nil {
		return Result{}, usageError(fmt.Sprintf("%s: %v", id, err))
	}
	path := parsed.value("path", "")
	if strings.TrimSpace(path) == "" {
		return Result{}, usageError("resolve requires --path PATH")
	}

	chosen := 0
	mode := modkit.ResolutionKeepLocal
	if parsed.Bool("accept-upstream") {
		chosen, mode = chosen+1, modkit.ResolutionAcceptUpstream
	}
	if parsed.Bool("keep-local") {
		chosen, mode = chosen+1, modkit.ResolutionKeepLocal
	}
	if parsed.Bool("merged") {
		chosen, mode = chosen+1, modkit.ResolutionMerged
	}
	// Missing operands prompt only on a terminal, through the same mutation
	// the flags would have built. JSON implies noninteractive and falls
	// through to the usage failure.
	if chosen != 1 {
		if cc.Interactive {
			mode, err = promptResolution(ctx, cc, id, path)
			if err != nil {
				return Result{}, err
			}
			if mode == "" {
				// The operator dismissed the prompt before any plan existed.
				return Result{}, ErrCancelled
			}
		} else {
			return Result{}, usageError("resolve requires exactly one of --accept-upstream, --keep-local, or --merged")
		}
	}

	plan, err := cc.Controller.Preview(ctx, ResolveMutation{ModuleID: id, Path: path, Mode: mode})
	if err != nil {
		var planned plannerFailure
		if errors.As(err, &planned) {
			r, e := failureEnvelope("resolve", err)
			return r, e
		}
		return Result{}, err
	}
	result, err := cc.Controller.Apply(ctx, plan)
	if err != nil {
		result.Envelope.Command = "resolve"
		result.Envelope = normalizeEnvelope(result.Envelope)
		return result, err
	}
	env := result.Envelope
	env.Command = "resolve"
	return Result{Envelope: normalizeEnvelope(env)}, nil
}

func runIdentity(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	identitySpec, _ := lookupSpec(builtInCommands(), "identity")
	if len(args) == 0 || args[0] != "link" {
		return Result{}, usageError(identitySpec.Usage)
	}
	linkArgs := args[1:]
	var environment, provider, subject, userID, orgID string
	values := map[string]*string{
		"environment": &environment, "provider": &provider, "subject": &subject,
		"user": &userID, "org": &orgID,
	}
	for i := 0; i < len(linkArgs); i++ {
		name := strings.TrimLeft(linkArgs[i], "-")
		target, ok := values[name]
		if !ok || !strings.HasPrefix(linkArgs[i], "-") {
			return Result{}, usageError(identitySpec.Usage)
		}
		if i+1 >= len(linkArgs) {
			return Result{}, usageError(identitySpec.Usage)
		}
		i++
		*target = linkArgs[i]
	}
	mutation := TaskMutation{
		Task: "identity-link", Environment: environment, Provider: provider,
		Subject: subject, UserID: userID, OrgID: orgID,
	}
	if _, err := cc.Controller.Preview(ctx, mutation); err != nil {
		return Result{}, err
	}
	return cc.Controller.applyIdentityLink(ctx, mutation)
}

func runMigrate(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "migrate")
	parsed, err := parseArgv(spec, args)
	if err != nil {
		return Result{}, err
	}
	if len(parsed.positional) != 1 || (parsed.positional[0] != "schema-1" && parsed.positional[0] != "schema1") {
		return Result{}, usageError(spec.Usage)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return cc.Controller.applyMigrateSchema1()
}

func runCache(_ context.Context, cc CommandContext, args []string) (Result, error) {
	cacheSpec, _ := lookupSpec(builtInCommands(), "cache")
	if len(args) != 1 || args[0] != "prune" {
		return Result{}, usageError(cacheSpec.Usage)
	}
	if err := cc.Controller.previewTask(TaskMutation{Task: "cache-prune"}); err != nil {
		return Result{}, err
	}
	return cc.Controller.applyCachePrune()
}

func runRegistry(ctx context.Context, cc CommandContext, args []string) (Result, error) {
	spec, _ := lookupSpec(builtInCommands(), "registry")
	if len(args) == 0 {
		return Result{}, usageError(spec.Usage)
	}
	subcommand, rest := args[0], args[1:]
	parsed, err := parseArgv(spec, rest)
	if err != nil {
		return Result{}, err
	}
	switch subcommand {
	case "build":
		if len(parsed.positional) != 0 {
			return Result{}, usageError("ggg registry build")
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		return cc.Controller.applyRegistryBuild()
	case "validate":
		if len(parsed.positional) != 0 {
			return Result{}, usageError("ggg registry validate")
		}
		// Validation is a read: it exercises the example closures but
		// mutates nothing in the project. Progress goes to the human stream
		// even under --json... suppressed for JSON, which reads the
		// envelope instead.
		if cc.AsJSON {
			ctx = WithProgressSink(ctx, io.Discard)
		} else {
			ctx = WithProgressSink(ctx, cc.Out)
		}
		return cc.Controller.Execute(ctx, RegistryReadRequest{Validate: true})
	case "init":
		return runRegistryInit(cc, parsed)
	case "keygen":
		return runRegistryKeygen(parsed)
	case "sign":
		return runRegistrySign(cc, parsed)
	case "verify":
		return runRegistryVerify(cc, parsed)
	case "rotate":
		return runRegistryRotate(cc, parsed)
	case "add", "remove", "update":
		return runRegistrySet(ctx, cc, subcommand, parsed)
	default:
		return Result{}, usageError(fmt.Sprintf("unknown registry subcommand %q", subcommand))
	}
}

// applyIdentityLink resolves the selected identity adapter for the requested
// environment, verifies the subject through it, and writes the audited
// provider-subject mapping. It is the Task-5 link command, unchanged in
// behavior, expressed as a TaskMutation through the controller.
func (c *Controller) applyIdentityLink(ctx context.Context, mutation TaskMutation) (Result, error) {
	project, err := c.loadProject()
	if err != nil {
		return Result{}, err
	}
	choices, ok := project.Providers["ggg/identity"]
	if !ok {
		return Result{}, refusalError(errors.New("identity provider slot is not selected"))
	}
	choice := choices.Development
	switch mutation.Environment {
	case "test":
		choice = choices.Test
	case "production":
		choice = choices.Production
	}
	if choice.Adapter == "" || choice.Target == "" {
		return Result{}, refusalError(fmt.Errorf("identity provider selection is incomplete for %s", mutation.Environment))
	}
	if (strings.HasSuffix(choice.Adapter, "/identity-dev") && mutation.Provider != "dev") ||
		(strings.HasSuffix(choice.Adapter, "/identity-clerk") && mutation.Provider != "clerk") {
		return Result{}, refusalError(fmt.Errorf("provider %q does not match selected adapter %q", mutation.Provider, choice.Adapter))
	}
	lookup := func(key string) string {
		if key == "APP_ENV" {
			return mutation.Environment
		}
		return os.Getenv(key)
	}
	cfg, err := config.LoadFrom(lookup)
	if err != nil {
		return Result{}, refusalError(err)
	}
	h := apphost.Map(nil, time.Now(), c.version)
	var verifier identity.Verifier
	switch {
	case strings.HasSuffix(choice.Adapter, "/identity-dev"):
		adapter, createErr := identitydev.NewModule(ctx, h, identitydev.Deps{Config: &cfg})
		if createErr != nil {
			return Result{}, runtimeError(createErr)
		}
		verifier = adapter.Verifier
	case strings.HasSuffix(choice.Adapter, "/identity-clerk"):
		adapter, createErr := identityclerk.NewModule(ctx, h, identityclerk.Deps{Config: &cfg})
		if createErr != nil {
			return Result{}, runtimeError(createErr)
		}
		verifier = adapter.Verifier
	default:
		return Result{}, refusalError(fmt.Errorf("unsupported identity adapter %q", choice.Adapter))
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return Result{}, runtimeError(err)
	}
	defer pool.Close()
	linker := &identitysession.Linker{Pool: pool, Verify: verifier}
	argsForLink := []string{"--environment", mutation.Environment, "--provider", mutation.Provider, "--subject", mutation.Subject}
	if mutation.UserID != "" {
		argsForLink = append(argsForLink, "--user", mutation.UserID)
	} else {
		argsForLink = append(argsForLink, "--org", mutation.OrgID)
	}
	if err := identitysession.RunLinkCommand(ctx, linker, argsForLink); err != nil {
		return Result{}, runtimeError(err)
	}
	return Result{}, nil
}

// applyMigrateSchema1 rewrites schema-1 project and lock metadata in one
// journalled transaction, rolling the project back if the lock write fails.
func (c *Controller) applyMigrateSchema1() (Result, error) {
	root := c.rootDir()
	projectPath := filepath.Join(root, modkit.ProjectFileName)
	lockPath := filepath.Join(root, modkit.LockFileName)
	project, err := os.ReadFile(projectPath)
	if err != nil {
		return Result{}, runtimeError(err)
	}
	lock, err := os.ReadFile(lockPath)
	if err != nil {
		return Result{}, runtimeError(err)
	}
	migratedProject, err := modkit.MigrateSchema1Project(project)
	if err != nil {
		return Result{}, refusalError(err)
	}
	migratedLock, err := modkit.MigrateSchema1Lock(lock)
	if err != nil {
		return Result{}, refusalError(err)
	}
	if _, err := modkit.ParseProject(migratedProject); err != nil {
		return Result{}, refusalError(fmt.Errorf("migrated project validation: %w", err))
	}
	if _, err := modkit.ParseLock(migratedLock); err != nil {
		return Result{}, refusalError(fmt.Errorf("migrated lock validation: %w", err))
	}
	// Both files are journalled as one transaction. Never leave a schema-2
	// intent paired with a schema-1 lock if the second write fails.
	if err := c.Write(projectPath, migratedProject, 0o644); err != nil {
		return Result{}, runtimeError(err)
	}
	if err := c.Write(lockPath, migratedLock, 0o644); err != nil {
		_ = c.Write(projectPath, project, 0o644)
		return Result{}, rollbackError(fmt.Errorf("migration rollback: %w", err))
	}
	changes := []modkit.Change{
		{Path: modkit.ProjectFileName, Kind: modkit.ChangeUpdate, Class: modkit.DestinationIntent},
		{Path: modkit.LockFileName, Kind: modkit.ChangeUpdate, Class: modkit.DestinationLock},
	}
	return Result{Envelope: normalizeEnvelope(modkit.Envelope{Command: "migrate", OK: true, Exit: exitOK, Changes: changes})}, nil
}

// applyCachePrune removes unreferenced registry cache entries.
func (c *Controller) applyCachePrune() (Result, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return Result{}, runtimeError(err)
	}
	cacheDir := filepath.Join(cacheRoot, "ggg", "registry")
	data, err := os.ReadFile(filepath.Join(c.rootDir(), modkit.LockFileName))
	if err != nil {
		return Result{}, refusalError(fmt.Errorf("cache prune requires a valid lock: %w", err))
	}
	lock, err := modkit.ParseLock(data)
	if err != nil {
		return Result{}, refusalError(fmt.Errorf("cache prune requires a valid lock: %w", err))
	}
	referenced := make([]string, 0, len(lock.Snapshots))
	for _, snapshot := range lock.Snapshots {
		referenced = append(referenced, snapshot.CacheKey)
	}
	removed, err := modkit.PruneRegistryCache(cacheDir, referenced)
	if err != nil {
		return Result{}, runtimeError(err)
	}
	suffix := "ies"
	if removed == 1 {
		suffix = "y"
	}
	return Result{Payload: map[string]any{
		"text": fmt.Sprintf("removed %d registry cache entr%s\n", removed, suffix),
	}}, nil
}
