package gggcli

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/gogogadget/gogogadget/internal/remote"
)

// TaskRunner is the narrow execution seam for trusted commands. Handlers
// choose every argv element and every injected environment value; manifests
// can declare artifacts and containers but never executable shell text.
//
// env carries values a child reads from its environment rather than from
// argv. A connection string is the case that forced it: the plan forbids
// putting one on a command line, where the process list would publish it, and
// `go run ./cmd/seed` reads its configuration from the environment by design.
// Nil means the child inherits the CLI's environment unchanged.
type TaskRunner interface {
	Run(ctx context.Context, root string, argv []string, env map[string]string) error
}

type osTaskRunner struct {
	out io.Writer
	err io.Writer
}

func (r osTaskRunner) Run(ctx context.Context, root string, argv []string, env map[string]string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty task argv")
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = root
	command.Stdout = r.out
	command.Stderr = r.err
	command.Stdin = os.Stdin
	if len(env) > 0 {
		// Injected values are appended after the inherited environment, and
		// os/exec keeps the last occurrence of a duplicate key, so the
		// resolved value is what the child sees. Sorted so an argv/env pair
		// is reproducible in a test and in a log.
		command.Env = os.Environ()
		for _, key := range slices.Sorted(maps.Keys(env)) {
			command.Env = append(command.Env, key+"="+env[key])
		}
	}
	return command.Run()
}

func (c *Controller) runner() TaskRunner {
	if c.taskRunner != nil {
		return c.taskRunner
	}
	return osTaskRunner{out: os.Stdout, err: os.Stderr}
}

func (c *Controller) previewTrustedTask(mutation TaskMutation) error {
	actions := map[string]map[string]bool{
		"setup": {"": true}, "generate": {"": true}, "dev": {"": true}, "check": {"": true}, "build": {"": true},
		"services": {"up": true, "down": true, "status": true, "logs": true},
		"db":       {"migrate": true, "status": true, "seed": true, "reset": true},
		"test":     {"unit": true, "integration": true, "e2e": true, "visual": true, "smoke": true, "all": true},
	}
	allowed, ok := actions[mutation.Task]
	if !ok || !allowed[mutation.Action] {
		return usageError("unsupported trusted task")
	}
	if mutation.Environment != "" && mutation.Environment != "development" && mutation.Environment != "test" {
		return usageError("task environment must be development or test")
	}
	if mutation.Task == "db" && mutation.Action == "reset" && !mutation.Yes {
		return refusalError(fmt.Errorf("db reset requires destructive confirmation (--yes in noninteractive mode)"))
	}
	// Every trusted task either re-runs sync or operates on state sync
	// produced, and apply creates the per-environment env files before it
	// reaches the first lock read. Refusing here keeps the preview contract
	// intact: a stale engine writes nothing at all.
	return c.refuseStaleEngine()
}

// refuseStaleEngine reports the lock's engine-contract refusal for commands
// that would otherwise write before reading the lock. A project with no lock
// has nothing to be stale against — that is a fresh genesis, where `ggg setup`
// is exactly the right command.
func (c *Controller) refuseStaleEngine() error {
	data, err := os.ReadFile(filepath.Join(c.rootDir(), modkit.LockFileName))
	if err != nil {
		return nil
	}
	if err := modkit.EngineContractRefusal(modkit.LockEngineContract(data)); err != nil {
		return refusalError(err)
	}
	return nil
}

// envProvenance records where a resolved value came from, so a command can
// decide whether to trust it with a mutation.
type envProvenance int

const (
	// envConfigured: the operator supplied it — process environment, the
	// CLI-managed .ggg/env/<environment>.env, or the legacy .env.
	envConfigured envProvenance = iota
	// envDeclaredDefault: nobody supplied it and the owning manifest declares
	// a fallback. Fine to read, refused for anything that mutates.
	envDeclaredDefault
)

// taskEnvValue resolves one value a host-side task needs through the
// documented precedence: the process environment wins, then the CLI-managed
// .ggg/env/<environment>.env, then the legacy .env in development only, and
// never a file in production. remote.LookupEnv already implements exactly
// that order and is what `ggg provider` and `ggg deploy` resolve through, so
// this is one contract with one implementation.
//
// It refuses an unresolved value rather than returning empty, and that is the
// point. An empty connection string is not an error to libpq — it falls back
// to its own defaults and connects to a local socket — so passing one on is
// how a command reaches the wrong server. The refusal names the key, the
// environment, and the file the CLI writes.
//
// The declared default is returned as a distinct provenance rather than
// silently blended in. DATABASE_URL's default is
// postgres://postgres:postgres@localhost:5432/gogogadget, which is a live
// address on any machine that has ever run Postgres locally — so a caller
// that is about to mutate a database must be able to tell "the operator told
// me this" from "nobody told me anything and this is the documented guess".
func (c *Controller) taskEnvValue(environment, key string) (string, envProvenance, error) {
	root := c.rootDir()
	if value, ok := remote.LookupEnv(root, environment)(key); ok {
		return value, envConfigured, nil
	}
	if value, ok := c.declaredEnvDefault(key); ok {
		return value, envDeclaredDefault, nil
	}
	return "", envConfigured, refusalError(fmt.Errorf(
		"%s is not set for the %s environment; export it, or run `ggg provider configure` to write it to %s",
		key, environment, remote.EnvironmentEnvFile(environment)))
}

// declaredEnvDefault reports the default the owning manifest declares for one
// key. It comes from the lock's embedded manifests — the same records the
// generated config parser is rendered from — so the CLI and the runtime agree
// on what the fallback is without the CLI reading generated Go.
func (c *Controller) declaredEnvDefault(key string) (string, bool) {
	lock, ok, err := readProjectLock(c.rootDir())
	if err != nil || !ok {
		return "", false
	}
	for _, locked := range lock.Modules {
		for _, declaration := range locked.Manifest.Environment {
			if declaration.Key == key && declaration.Default != "" {
				return declaration.Default, true
			}
		}
	}
	return "", false
}

// refuseDeclaredDefault is the refusal a mutating command emits when the only
// value available is the manifest's declared default. `go run ./cmd/seed`
// outside the CLI resolves that default and would migrate and seed whatever
// answers at that address; a ggg command that mutates must be told which
// database it is mutating.
func refuseDeclaredDefault(action, environment, key, value string) error {
	return refusalError(fmt.Errorf(
		"db %s refuses to mutate a database it was not told about: %s is only supplied by its declared default (%s) for the %s environment; "+
			"export it, or run `ggg provider configure` to write it to %s. `ggg db status` reads the default.",
		action, key, value, environment, remote.EnvironmentEnvFile(environment)))
}

func (c *Controller) applyTrustedTask(ctx context.Context, mutation TaskMutation) (Result, error) {
	root := c.rootDir()
	runner := c.runner()
	runEnv := func(dir string, env map[string]string, argv ...string) error {
		if err := runner.Run(ctx, dir, argv, env); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
		}
		return nil
	}
	run := func(dir string, argv ...string) error { return runEnv(dir, nil, argv...) }
	environment := mutation.Environment
	if environment == "" {
		environment = "development"
	}
	compose := "compose.yaml"
	if environment == "test" {
		compose = "compose.test.yaml"
	}
	// Compose parses env_file for every subcommand, so the per-environment
	// file must exist before anything invokes compose.
	switch mutation.Task {
	case "services", "dev", "db", "setup":
		if mutation.Task != "setup" || mutation.Action == "" {
			for _, name := range []string{"development", "test"} {
				if mutation.Task != "setup" && name != environment {
					continue
				}
				if err := ensureEnvironmentFile(root, name); err != nil {
					return Result{}, runtimeError(err)
				}
			}
		}
	}

	var err error
	switch mutation.Task {
	case "setup":
		// Ordering matters: sums first (download all works before any package
		// loads), then declared tools (generate needs bin/tailwindcss), then
		// generation (sqlc output lets every package load), then tidy (the
		// readonly build requires the indirect require graph), and only then
		// the bin/ggg build.
		if err = run(root, "go", "mod", "download", "all"); err == nil {
			err = installDeclaredTools(ctx, root)
		}
		if err == nil {
			// `tidy -e` completes the require graph even though generated
			// packages (sqlc output) do not exist yet, which is what lets
			// readonly `go tool` run before generation.
			err = run(root, "go", "mod", "tidy", "-e")
		}
		if err == nil {
			err = c.runGenerate(ctx, runner)
		}
		if err == nil {
			err = run(root, "go", "mod", "tidy")
		}
		if err == nil {
			err = run(root, "go", "build", "-o", "bin/ggg", "./cmd/ggg")
		}
	case "generate":
		err = c.runGenerate(ctx, runner)
	case "services":
		argv := []string{"docker", "compose", "-f", compose}
		switch mutation.Action {
		case "up":
			argv = append(argv, "up", "-d", "--wait")
		case "down":
			argv = append(argv, "down")
			if mutation.Volumes {
				argv = append(argv, "--volumes")
			}
		case "status":
			argv = append(argv, "ps")
		case "logs":
			argv = append(argv, "logs", "--follow")
		}
		err = run(root, argv...)
	case "dev":
		if err = c.runGenerate(ctx, runner); err == nil {
			err = run(root, "docker", "compose", "-f", "compose.yaml", "up", "-d", "--wait")
		}
		if err == nil {
			err = superviseDev(ctx, root, os.Stdout, os.Stderr)
		}
	case "db":
		// Every db form needs the connection string, and it is resolved once,
		// through the documented precedence, before any tool is invoked. The
		// old code read os.Getenv("DATABASE_URL") straight into goose's argv,
		// so a project whose value lives only where the CLI writes it —
		// .ggg/env/<environment>.env — handed goose an EMPTY DSN. That is not
		// a failure to libpq: it falls back to its own defaults and connects
		// to a local socket, so the documented first migration of a generated
		// project could aim at a server the project has nothing to do with.
		//
		// The value goes in the child's environment, never in argv, because a
		// DSN carries a password and the process list is public. goose reads
		// GOOSE_DRIVER/GOOSE_DBSTRING; cmd/seed reads DATABASE_URL.
		databaseURL, provenance, resolveErr := c.taskEnvValue(environment, "DATABASE_URL")
		if resolveErr != nil {
			return failureEnvelope("db"+taskActionSuffix(mutation.Action), resolveErr)
		}
		// `db status` reads; the rest mutate. A default-sourced value is a
		// documented guess at a live local address, so reading through it is
		// a diagnostic and migrating or seeding through it is how a command
		// reaches a database nobody pointed it at.
		if provenance == envDeclaredDefault && mutation.Action != "status" {
			return failureEnvelope("db"+taskActionSuffix(mutation.Action),
				refuseDeclaredDefault(mutation.Action, environment, "DATABASE_URL", databaseURL))
		}
		goose := map[string]string{"GOOSE_DRIVER": "postgres", "GOOSE_DBSTRING": databaseURL}
		seed := map[string]string{"DATABASE_URL": databaseURL}
		switch mutation.Action {
		case "migrate":
			err = runEnv(root, goose, "go", "tool", "goose", "-dir", "internal/db/migrations", "up")
		case "status":
			err = runEnv(root, goose, "go", "tool", "goose", "-dir", "internal/db/migrations", "status")
		case "seed":
			err = runEnv(root, seed, "go", "run", "./cmd/seed", "-registry", "dev")
		case "reset":
			if err = run(root, "docker", "compose", "-f", compose, "down", "--volumes"); err == nil {
				err = run(root, "docker", "compose", "-f", compose, "up", "-d", "--wait")
			}
			if err == nil {
				err = runEnv(root, seed, "go", "run", "./cmd/seed", "-reset", "-registry", "dev")
			}
		}
	case "check":
		for _, argv := range [][]string{selfArgv("generate"), selfArgv("sync", "--check", "--offline"), {"go", "vet", "./..."}, {"go", "test", "./..."}, {"go", "build", "./..."}} {
			if err = run(root, argv...); err != nil {
				break
			}
		}
	case "test":
		err = runTestTask(run, root, mutation.Action)
	case "build":
		err = run(root, "go", "build", "./cmd/server")
	}
	if err != nil {
		// A failed trusted task reports the same fixed envelope as any other
		// failure. Returning a zero-value envelope here made the renderer
		// print "failed (exit 0)" while the process exited nonzero — the same
		// success-over-failure mismatch the success path below guards against.
		//
		// hasDeclaredExit deliberately does not test the structural
		// interface{ ExitCode() int }, because a subprocess status would
		// satisfy it and become a declared code. Everything reachable here is
		// a step failure — a wrapped *exec.ExitError, or the argv-prefixed
		// error the run closure builds — so it all becomes a runtime failure,
		// and the envelope's exit is read off the same error the process
		// status comes from, so the two cannot disagree.
		if !hasDeclaredExit(err) {
			err = runtimeError(err)
		}
		return failureEnvelope(mutation.Task+taskActionSuffix(mutation.Action), err)
	}
	// Trusted tasks report the fixed envelope like every other command: a
	// zero-value envelope must never reach the renderer, or a successful task
	// prints "failed (exit 0)".
	env := normalizeEnvelope(modkit.Envelope{OK: true, Exit: exitOK})
	env.Command = mutation.Task + taskActionSuffix(mutation.Action)
	return Result{Envelope: env, Payload: map[string]any{"text": mutation.Task + taskActionSuffix(mutation.Action) + " complete\n"}}, nil
}

func (c *Controller) runGenerate(ctx context.Context, runner TaskRunner) error {
	root := c.rootDir()
	run := func(argv ...string) error { return runner.Run(ctx, root, argv, nil) }
	// Mutable directory registries are refreshed deliberately; remote registries
	// are immutable snapshots and are never rewritten to absorb local edits.
	project, err := c.loadProject()
	if err != nil {
		return err
	}
	for _, registry := range project.Registries {
		if registry.Source != "directory" {
			continue
		}
		// Mirror DirectorySource.Resolve: a registry path whose directory
		// carries its own registry.json is a self-contained registry root;
		// otherwise the project root itself is the registry root (the
		// self-hosting layout). Refresh only mutable directory registries;
		// remote registries are immutable snapshots.
		refreshRoot := root
		if registry.Path != "" && registry.Path != "." {
			candidate := filepath.Join(root, filepath.FromSlash(registry.Path))
			if _, statErr := os.Stat(filepath.Join(candidate, "registry.json")); statErr == nil {
				refreshRoot = candidate
			}
		}
		if _, err := modkit.RefreshManifestDigests(refreshRoot); err != nil {
			return err
		}
		if _, _, err := modkit.BuildRegistryIndexes(refreshRoot); err != nil {
			return err
		}
	}
	lock, _, err := readProjectLock(root)
	if err != nil {
		return err
	}
	for _, argv := range append([][]string{selfArgv("sync", "--offline")}, generationSteps(lock)...) {
		if err := run(argv...); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
		}
	}
	return nil
}

// The three generators this framework runs. templ and sqlc are Go tool
// directives; Tailwind is a pinned standalone binary installed under bin/.
const (
	templGoTool         = "github.com/a-h/templ/cmd/templ"
	sqlcGoTool          = "github.com/sqlc-dev/sqlc/cmd/sqlc"
	tailwindInstallPath = "bin/tailwindcss"
)

// generationSteps is the ordered generator argv the installed lock declares.
// Each step is gated on the declaration that produces it, so the pipeline is
// the project's own — a project that installs neither templ nor sqlc runs
// neither instead of failing on a tool it never asked for. templ runs before
// sqlc only because both write inputs to compilation and this is the order
// `make generate` has always used; neither reads the other's output.
func generationSteps(lock modkit.Lock) [][]string {
	steps := make([][]string, 0, 3)
	if slices.Contains(lock.GoTools, templGoTool) {
		steps = append(steps, []string{"go", "tool", "templ", "generate"})
	}
	if slices.Contains(lock.GoTools, sqlcGoTool) {
		steps = append(steps, []string{"go", "tool", "sqlc", "generate"})
	}
	if declaresToolInstallPath(lock, tailwindInstallPath) {
		steps = append(steps, []string{filepath.FromSlash(tailwindInstallPath), "-i", "input.css", "-o", "static/app.css", "--minify"})
	}
	return steps
}

func declaresToolInstallPath(lock modkit.Lock, path string) bool {
	for _, locked := range lock.Modules {
		for _, tool := range locked.Manifest.Dependencies.Tools {
			if tool.InstallPath == path {
				return true
			}
		}
	}
	return false
}

// readProjectLock reads the installed lock. A missing lock is not an error:
// a project with no lock has no installed modules, and so declares no tools
// and no generators.
func readProjectLock(root string) (modkit.Lock, bool, error) {
	data, err := os.ReadFile(filepath.Join(root, modkit.LockFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return modkit.Lock{}, false, nil
	}
	if err != nil {
		return modkit.Lock{}, false, err
	}
	lock, err := modkit.ParseLock(data)
	if err != nil {
		return modkit.Lock{}, false, err
	}
	return lock, true, nil
}

// ensureEnvironmentFile creates .ggg/env/<environment>.env carrying the
// declared development posture, and leaves a file that already has content
// completely alone: `ggg provider configure` owns it after creation and an
// operator's values must survive every re-run.
//
// The file is what the stack actually reads — compose names it as the app
// service's env_file, and the CLI reads it after the process environment.
// .env.example is generated reference and nothing loads it, which is how a
// created project ended up outside the zero-account posture its own docs
// describe: `/dev/login` was unreachable because DEV_AUTH_BYPASS was unset.
//
// Values come from the manifests' own declarations, never from this file:
// non-secret keys whose module states an example that differs from its
// default. Mode stays 0600 because configured credentials land here later.
//
// The rule that production is never written to disk is enforced here, at the
// only place that writes: a value provider that merely declines to produce
// lines would still have created the file.
func ensureEnvironmentFile(root, environment string) error {
	if environment != "development" && environment != "test" {
		return fmt.Errorf("environment %q is never written to disk", environment)
	}
	path := filepath.Join(root, ".ggg", "env", environment+".env")
	if info, err := os.Stat(path); err == nil {
		if info.Size() > 0 {
			return nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	body := []byte(nil)
	if lock, ok, err := readProjectLock(root); err != nil {
		return err
	} else if ok {
		graph := make([]modkit.Manifest, 0, len(lock.Modules))
		for _, locked := range lock.Modules {
			graph = append(graph, locked.Manifest)
		}
		lines, postureErr := modkit.DeclaredEnvironmentPosture(lock, graph, environment)
		if postureErr != nil {
			return postureErr
		}
		if len(lines) > 0 {
			body = []byte(strings.Join(lines, "\n") + "\n")
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

// selfArgv re-invokes the running ggg binary. `go run ./cmd/ggg` would force
// the go tool to recompile the CLI from a tree whose go.mod is not yet
// complete (a fresh genesis), so subprocess steps ride the already-built
// executable instead.
func selfArgv(args ...string) []string {
	if self, err := os.Executable(); err == nil {
		return append([]string{self}, args...)
	}
	return append([]string{"go", "run", "./cmd/ggg"}, args...)
}

func runTestTask(run func(string, ...string) error, root, mode string) error {
	switch mode {
	case "unit", "integration":
		return run(root, "go", "test", "./...")
	case "e2e":
		if err := run(root, "docker", "compose", "-f", "compose.test.yaml", "up", "-d", "--wait"); err != nil {
			return err
		}
		return run(filepath.Join(root, "e2e"), "npx", "playwright", "test")
	case "visual":
		// Baselines only reproduce inside the pinned Playwright container, and
		// the suite needs a seeded database plus a host server. scripts/visual.sh
		// owns all three; a bare `npx playwright test` here runs on the host with
		// no server and no e2e/node_modules.
		return run(root, filepath.Join("scripts", "visual.sh"))
	case "smoke":
		return run(root, filepath.Join("scripts", "smoke.sh"))
	case "all":
		for _, item := range []string{"unit", "integration", "e2e", "visual", "smoke"} {
			if err := runTestTask(run, root, item); err != nil {
				return err
			}
		}
		return nil
	default:
		return usageError("unknown test mode")
	}
}

func taskActionSuffix(action string) string {
	if action == "" {
		return ""
	}
	return " " + action
}

// installDeclaredTools downloads every tool artifact the installed modules
// declare for this platform into its project-relative install path. Artifact
// bytes are digest-verified before anything is written; an existing verified
// install is left untouched.
//
// The declaration set comes from the lock's own embedded manifests, not from a
// re-resolved catalog: the lock records exactly what is installed, and a
// freshly created project must be able to install its tools before it can
// resolve anything itself. root is explicit for the same reason — genesis
// installs into the tree it just created, not into the caller's project.
func installDeclaredTools(ctx context.Context, root string) error {
	lock, ok, err := readProjectLock(root)
	if err != nil || !ok {
		return err // no lock: no installed modules, no declared tools
	}
	byID := make(map[string]modkit.Manifest, len(lock.Modules))
	for _, locked := range lock.Modules {
		byID[locked.Manifest.ID] = locked.Manifest
	}
	// One logical tool may declare one artifact per platform; artifacts that
	// share an install path AND platform must agree byte for byte.
	declared := make(map[string]modkit.ToolArtifact)
	for _, id := range lock.Order {
		module, ok := byID[id]
		if !ok {
			continue
		}
		for _, tool := range module.Dependencies.Tools {
			key := tool.InstallPath + "\x00" + tool.OS + "/" + tool.Arch
			if prior, seen := declared[key]; seen && prior != tool {
				return fmt.Errorf("conflicting tool artifact %q", tool.InstallPath)
			}
			declared[key] = tool
		}
	}
	platform := make([]string, 0)
	matched := make([]modkit.ToolArtifact, 0)
	for _, path := range sortedToolPaths(declared) {
		tool := declared[path]
		label := tool.OS + "/" + tool.Arch
		if !slices.Contains(platform, label) {
			platform = append(platform, label)
		}
		if tool.OS == runtime.GOOS && tool.Arch == runtime.GOARCH {
			matched = append(matched, tool)
		}
	}
	if len(declared) > 0 && len(matched) == 0 {
		return fmt.Errorf("declared tools cover %s; this host is %s/%s",
			strings.Join(platform, ", "), runtime.GOOS, runtime.GOARCH)
	}
	for _, tool := range matched {
		if err := installToolArtifact(ctx, root, tool); err != nil {
			return err
		}
	}
	return nil
}

func sortedToolPaths(declared map[string]modkit.ToolArtifact) []string {
	paths := make([]string, 0, len(declared))
	for path := range declared {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func installToolArtifact(ctx context.Context, root string, tool modkit.ToolArtifact) error {
	dest := filepath.Join(root, filepath.FromSlash(tool.InstallPath))
	if info, err := os.Lstat(dest); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("tool install path %s is a symlink", tool.InstallPath)
		}
		if info.Mode().IsRegular() && tool.Format == "raw" {
			if existing, readErr := os.ReadFile(dest); readErr == nil {
				sum := sha256.Sum256(existing)
				if hex.EncodeToString(sum[:]) == tool.SHA256 {
					return nil // already installed and verified
				}
			}
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, tool.URL, nil)
	if err != nil {
		return fmt.Errorf("tool %s: %w", tool.InstallPath, err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("tool %s: %w", tool.InstallPath, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("tool %s: download %s: %s", tool.InstallPath, tool.URL, response.Status)
	}
	archive, err := io.ReadAll(io.LimitReader(response.Body, maxToolArtifactBytes))
	if err != nil {
		return fmt.Errorf("tool %s: %w", tool.InstallPath, err)
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != tool.SHA256 {
		return fmt.Errorf("tool %s: digest mismatch for %s", tool.InstallPath, tool.URL)
	}
	var payload []byte
	switch tool.Format {
	case "raw":
		payload = archive
	case "zip":
		payload, err = extractZipExecutable(archive, tool.BinaryPath)
	case "tar.gz":
		payload, err = extractTarExecutable(archive, tool.BinaryPath)
	default:
		err = fmt.Errorf("tool %s: unsupported format %q", tool.InstallPath, tool.Format)
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dest, payload, 0o755); err != nil {
		return err
	}
	return nil
}

// maxToolArtifactBytes bounds one tool download; declared tools are pinned
// compilers and proxies, never datasets.
const maxToolArtifactBytes = 512 << 20

// extractZipExecutable returns the named entry and refuses symlinks or any
// undeclared executable bit, so a tampered archive cannot smuggle a second
// binary onto PATH.
func extractZipExecutable(archive []byte, binaryPath string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, entry := range reader.File {
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("archive entry %s is a symlink", entry.Name)
		}
		if entry.Name == binaryPath {
			if mode.IsDir() {
				return nil, fmt.Errorf("archive entry %s is a directory", entry.Name)
			}
			file, err := entry.Open()
			if err != nil {
				return nil, err
			}
			defer file.Close()
			data, err := io.ReadAll(io.LimitReader(file, maxToolArtifactBytes))
			if err != nil {
				return nil, err
			}
			return data, nil
		}
		if mode.IsRegular() && mode&0o111 != 0 {
			return nil, fmt.Errorf("archive declares undeclared executable %s", entry.Name)
		}
	}
	return nil, fmt.Errorf("archive is missing declared binary %s", binaryPath)
}

func extractTarExecutable(archive []byte, binaryPath string) ([]byte, error) {
	stream, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	reader := tar.NewReader(stream)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(header.Name, "./")
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			return nil, fmt.Errorf("archive entry %s is a link", name)
		}
		if name == binaryPath && header.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(io.LimitReader(reader, maxToolArtifactBytes))
			if err != nil {
				return nil, err
			}
			return data, nil
		}
		if header.Typeflag == tar.TypeReg && header.Mode&0o111 != 0 {
			return nil, fmt.Errorf("archive declares undeclared executable %s", name)
		}
	}
	return nil, fmt.Errorf("archive is missing declared binary %s", binaryPath)
}

func superviseDev(ctx context.Context, root string, out, errOut io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type child struct {
		name string
		argv []string
	}
	children := []child{
		{name: "templ", argv: []string{"go", "tool", "templ", "generate", "--watch"}},
		{name: "tailwind", argv: []string{filepath.Join("bin", "tailwindcss"), "-i", "input.css", "-o", "static/app.css", "--watch"}},
		{name: "air", argv: []string{"go", "tool", "air"}},
	}
	type running struct {
		child child
		cmd   *exec.Cmd
	}
	runningChildren := make([]running, 0, len(children))
	results := make(chan error, len(children))
	var output sync.WaitGroup
	for _, spec := range children {
		cmd := exec.CommandContext(ctx, spec.argv[0], spec.argv[1:]...)
		cmd.Dir = root
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		stdout, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			cancel()
			return pipeErr
		}
		stderr, pipeErr := cmd.StderrPipe()
		if pipeErr != nil {
			cancel()
			return pipeErr
		}
		if startErr := cmd.Start(); startErr != nil {
			cancel()
			return startErr
		}
		runningChildren = append(runningChildren, running{child: spec, cmd: cmd})
		output.Add(2)
		go prefixProcessOutput(&output, out, spec.name, stdout)
		go prefixProcessOutput(&output, errOut, spec.name, stderr)
		go func(name string, command *exec.Cmd) {
			waitErr := command.Wait()
			if waitErr != nil && !errors.Is(ctx.Err(), context.Canceled) {
				results <- fmt.Errorf("%s: %w", name, waitErr)
				return
			}
			results <- nil
		}(spec.name, cmd)
	}
	first := <-results
	cancel()
	for _, child := range runningChildren {
		if child.cmd.Process != nil {
			_ = syscall.Kill(-child.cmd.Process.Pid, syscall.SIGTERM)
		}
	}
	for range runningChildren[1:] {
		next := <-results
		if first == nil && next != nil {
			first = next
		}
	}
	output.Wait()
	if first == nil && ctx.Err() != nil {
		return nil
	}
	return first
}

func prefixProcessOutput(group *sync.WaitGroup, destination io.Writer, name string, source io.Reader) {
	defer group.Done()
	scanner := bufio.NewScanner(source)
	for scanner.Scan() {
		fmt.Fprintf(destination, "[%s] %s\n", name, scanner.Text())
	}
}

var _ = runtime.GOOS
