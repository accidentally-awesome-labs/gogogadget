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
)

// TaskRunner is the narrow execution seam for trusted commands. Handlers choose
// every argv element; manifests can declare artifacts and containers but never
// executable shell text.
type TaskRunner interface {
	Run(context.Context, string, []string) error
}

type osTaskRunner struct {
	out io.Writer
	err io.Writer
}

func (r osTaskRunner) Run(ctx context.Context, root string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty task argv")
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = root
	command.Stdout = r.out
	command.Stderr = r.err
	command.Stdin = os.Stdin
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
	return nil
}

func (c *Controller) applyTrustedTask(ctx context.Context, mutation TaskMutation) (Result, error) {
	root := c.rootDir()
	runner := c.runner()
	run := func(dir string, argv ...string) error {
		if err := runner.Run(ctx, dir, argv); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
		}
		return nil
	}
	environment := mutation.Environment
	if environment == "" {
		environment = "development"
	}
	compose := "compose.yaml"
	if environment == "test" {
		compose = "compose.test.yaml"
	}
	// Compose parses env_file for every subcommand, so the per-environment
	// file must exist before anything invokes compose. It is created empty and
	// mode 0600: `ggg provider configure` fills it, and it is gitignored.
	switch mutation.Task {
	case "services", "dev", "db", "setup":
		if mutation.Task != "setup" || mutation.Action == "" {
			for _, name := range []string{"development", "test"} {
				if mutation.Task != "setup" && name != environment {
					continue
				}
				path := filepath.Join(root, ".ggg", "env", name+".env")
				if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						return Result{}, runtimeError(err)
					}
					if err := os.WriteFile(path, nil, 0o600); err != nil {
						return Result{}, runtimeError(err)
					}
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
			err = c.installDeclaredTools(ctx)
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
		switch mutation.Action {
		case "migrate":
			err = run(root, "go", "tool", "goose", "-dir", "internal/db/migrations", "postgres", os.Getenv("DATABASE_URL"), "up")
		case "status":
			err = run(root, "go", "tool", "goose", "-dir", "internal/db/migrations", "postgres", os.Getenv("DATABASE_URL"), "status")
		case "seed":
			err = run(root, "go", "run", "./cmd/seed", "-registry", "dev")
		case "reset":
			if err = run(root, "docker", "compose", "-f", compose, "down", "--volumes"); err == nil {
				err = run(root, "docker", "compose", "-f", compose, "up", "-d", "--wait")
			}
			if err == nil {
				err = run(root, "go", "run", "./cmd/seed", "-reset", "-registry", "dev")
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
		return Result{}, runtimeError(err)
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
	run := func(argv ...string) error { return runner.Run(ctx, root, argv) }
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
	for _, argv := range [][]string{
		selfArgv("sync", "--offline"),
		{"go", "tool", "templ", "generate"},
		{"go", "tool", "sqlc", "generate"},
		{filepath.Join("bin", "tailwindcss"), "-i", "input.css", "-o", "static/app.css", "--minify"},
	} {
		if err := run(argv...); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
		}
	}
	return nil
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
func (c *Controller) installDeclaredTools(ctx context.Context) error {
	root := c.rootDir()
	data, err := os.ReadFile(filepath.Join(root, modkit.LockFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil // no lock: no installed modules, no declared tools
	}
	if err != nil {
		return err
	}
	lock, err := modkit.ParseLock(data)
	if err != nil {
		return err
	}
	catalog, _, _, err := c.readCatalog(ctx, false)
	if err != nil {
		return err
	}
	byID := make(map[string]modkit.Manifest, len(catalog.Modules))
	for _, module := range catalog.Modules {
		byID[module.ID] = module
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
