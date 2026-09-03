package gggcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

func TestTask8CommandsAreReserved(t *testing.T) {
	for _, name := range []string{"new", "create", "setup", "generate", "services", "dev", "db", "check", "test", "build"} {
		if !IsReservedName(name) {
			t.Fatalf("%q is not a reserved built-in command", name)
		}
	}
}

func TestNewAnswersAreMutuallyExclusiveWithIndividualFlags(t *testing.T) {
	root := t.TempDir()
	answers := filepath.Join(root, "answers.json")
	data, err := json.Marshal(NewProjectAnswers{Name: "demo", Module: "example.com/demo", Profile: "ggg/profile/full", Registry: "directory:."})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(answers, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, runErr := runApp(t, root, nil, "new", filepath.Join(root, "demo"), "--answers", answers, "--module", "example.com/other", "--non-interactive")
	if runErr == nil || exitOf(t, runErr) != exitUsage || !strings.Contains(runErr.Error(), "mutually exclusive") {
		t.Fatalf("new error = %v, want mutually-exclusive usage error", runErr)
	}
}

func TestNewNonInteractiveRequiresCompleteAnswers(t *testing.T) {
	root := t.TempDir()
	_, _, err := runApp(t, root, nil, "new", filepath.Join(root, "demo"), "--module", "example.com/demo", "--profile", "ggg/profile/full", "--non-interactive")
	if err == nil || exitOf(t, err) != exitUsage || !strings.Contains(err.Error(), "registry") {
		t.Fatalf("new error = %v, want missing registry usage error", err)
	}
}

func TestParseProviderAnswer(t *testing.T) {
	slot, environment, selection, err := parseProviderAnswer("ggg/mail:production=ggg/system/mail-smtp@smtp")
	if err != nil {
		t.Fatal(err)
	}
	if slot != "ggg/mail" || environment != "production" || selection.Adapter != "ggg/system/mail-smtp" || selection.Target != "smtp" {
		t.Fatalf("parsed provider = %q %q %#v", slot, environment, selection)
	}
}

func TestComposeGenerationSelectsEnvironmentAndRefusesPortCollision(t *testing.T) {
	graph := []modkit.Manifest{
		composeAdapter("ggg/system/db", "ggg/database", "postgres", "postgres@sha256:"+strings.Repeat("a", 64), 5432),
		composeAdapter("ggg/system/mail", "ggg/mail", "mailpit", "mailpit@sha256:"+strings.Repeat("b", 64), 8025),
	}
	lock := modkit.Lock{Providers: map[string]modkit.ProviderSelections{
		"ggg/database": {Development: modkit.ProviderSelection{Adapter: "ggg/system/db", Target: "postgres"}},
		"ggg/mail":     {Development: modkit.ProviderSelection{Adapter: "ggg/system/mail", Target: "mailpit"}},
	}}
	files, err := modkit.GenerateComposeFiles(lock, graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || !strings.Contains(files[0].Content+files[1].Content, "ggg-system-db-postgres") {
		t.Fatalf("compose files = %#v", files)
	}

	graph[1].Runtime.System.Adapter.Targets[0].LocalService.Ports[0].DefaultHost = 5432
	if _, err := modkit.GenerateComposeFiles(lock, graph); err == nil || !strings.Contains(err.Error(), "host port 5432") {
		t.Fatalf("collision error = %v", err)
	}
}

func composeAdapter(id, slot, target, image string, port int) modkit.Manifest {
	targets := []modkit.ServiceTarget{{
		ID: target, Mode: "self-hosted", Environments: []string{"development"},
		LocalService: &modkit.LocalService{
			Container:   image,
			Ports:       []modkit.LocalServicePort{{Name: "service", Container: port, DefaultHost: port}},
			Environment: []modkit.LocalServiceEnv{}, Volumes: []modkit.LocalServiceVolume{},
			Health: modkit.LocalServiceHealth{Kind: "tcp", Port: port},
		},
	}}
	return modkit.Manifest{
		ID: id,
		Dependencies: modkit.Dependencies{
			Go: []modkit.GoDependency{}, Tools: []modkit.ToolArtifact{},
			Containers: []modkit.ContainerDependency{{Name: target, Image: image}},
		},
		Runtime: modkit.RuntimeContributions{
			System: &modkit.SystemContribution{
				Adapter: &modkit.AdapterContribution{Slot: slot, Targets: targets},
			},
		},
	}
}

// The two generated stacks must be able to run at the same time on one host
// out of the box: a project that cannot run its own test stack while its
// development stack is up has no working test story. Development publishes
// what the target declares and test publishes the same port shifted, and the
// app service publishes nothing in test — nothing reaches it over a host port
// (`ggg test e2e` runs the server on the host at :18080, CI's e2e job uses a
// service container and no compose at all), and publishing it would both take
// the development port and land on 18080, the exact port Playwright's
// webServer reuses instead of the server it builds.
func TestComposePublishesDerivedTestPortsAndLeavesTheTestAppUnpublished(t *testing.T) {
	graph := []modkit.Manifest{
		composeAdapter("ggg/system/db", "ggg/database", "postgres", "postgres@sha256:"+strings.Repeat("a", 64), 5432),
	}
	both := modkit.ProviderSelections{
		Development: modkit.ProviderSelection{Adapter: "ggg/system/db", Target: "postgres"},
		Test:        modkit.ProviderSelection{Adapter: "ggg/system/db", Target: "postgres"},
	}
	files, err := modkit.GenerateComposeFiles(modkit.Lock{Providers: map[string]modkit.ProviderSelections{"ggg/database": both}}, graph)
	if err != nil {
		t.Fatal(err)
	}
	development, test := composeFile(t, files, "compose.yaml"), composeFile(t, files, "compose.test.yaml")
	for _, want := range []string{"- 8080:8080", "- 5432:5432", "APP_URL: http://localhost:8080"} {
		if !strings.Contains(development, want) {
			t.Fatalf("compose.yaml does not publish %q:\n%s", want, development)
		}
	}
	if !strings.Contains(test, "- 15432:5432") {
		t.Fatalf("compose.test.yaml does not publish the derived database port:\n%s", test)
	}
	// The database is the only service with a ports block, so the app has none.
	if strings.Count(test, "ports:") != 1 || strings.Contains(test, "8080:8080") {
		t.Fatalf("compose.test.yaml publishes the app port:\n%s", test)
	}
	if !strings.Contains(test, "APP_URL: http://app:8080") {
		t.Fatalf("unpublished test app does not report its in-network origin:\n%s", test)
	}
	// A project that declares no override has no `ports` key at all, so the
	// generator sees a nil map. It must be the same input as an empty one: an
	// absent override is no override, not a missing one.
	withEmpty, err := modkit.GenerateComposeFiles(modkit.Lock{
		Providers: map[string]modkit.ProviderSelections{"ggg/database": both},
		Ports:     map[string]modkit.PortOverrides{},
	}, graph)
	if err != nil {
		t.Fatal(err)
	}
	for i := range files {
		if files[i].Path != withEmpty[i].Path || files[i].Content != withEmpty[i].Content {
			t.Fatalf("absent ports generated %s differently from an empty map", files[i].Path)
		}
	}
}

// An operator on a busy host must be able to move a published port without
// editing a generated file, and APP_URL has to follow: a redirect built from a
// port nothing listens on is broken exactly when the override was needed.
func TestComposePortOverrideMovesThePublishedPortAndAppURL(t *testing.T) {
	graph := []modkit.Manifest{
		composeAdapter("ggg/system/db", "ggg/database", "postgres", "postgres@sha256:"+strings.Repeat("a", 64), 5432),
	}
	lock := modkit.Lock{
		Providers: map[string]modkit.ProviderSelections{"ggg/database": {
			Development: modkit.ProviderSelection{Adapter: "ggg/system/db", Target: "postgres"},
			Test:        modkit.ProviderSelection{Adapter: "ggg/system/db", Target: "postgres"},
		}},
		Ports: map[string]modkit.PortOverrides{
			"app/http":                       {Development: 8081, Test: 18081},
			"ggg/system/db@postgres/service": {Development: 5433},
		},
	}
	files, err := modkit.GenerateComposeFiles(lock, graph)
	if err != nil {
		t.Fatal(err)
	}
	development, test := composeFile(t, files, "compose.yaml"), composeFile(t, files, "compose.test.yaml")
	for _, want := range []string{"- 8081:8080", "- 5433:5432", "APP_URL: http://localhost:8081"} {
		if !strings.Contains(development, want) {
			t.Fatalf("override did not move %q:\n%s", want, development)
		}
	}
	// The test app is published only because the project asked for it, and the
	// database keeps its derived port because only development was overridden.
	for _, want := range []string{"- 18081:8080", "- 15432:5432", "APP_URL: http://localhost:18081"} {
		if !strings.Contains(test, want) {
			t.Fatalf("override did not move %q:\n%s", want, test)
		}
	}
}

// An override that names nothing is a committed decision the generator would
// otherwise drop on the floor, leaving the service on the port the operator
// moved it off.
func TestComposeRefusesAnOverrideNamingNoDeclaredPort(t *testing.T) {
	graph := []modkit.Manifest{
		composeAdapter("ggg/system/db", "ggg/database", "postgres", "postgres@sha256:"+strings.Repeat("a", 64), 5432),
	}
	for _, override := range []string{"ggg/system/db@postgres/http", "ggg/system/other@postgres/service", "app/admin"} {
		lock := modkit.Lock{
			Providers: map[string]modkit.ProviderSelections{"ggg/database": {
				Development: modkit.ProviderSelection{Adapter: "ggg/system/db", Target: "postgres"},
			}},
			Ports: map[string]modkit.PortOverrides{override: {Development: 5433}},
		}
		_, err := modkit.GenerateComposeFiles(lock, graph)
		if err == nil || !strings.Contains(err.Error(), "names no port the development stack declares") {
			t.Fatalf("override %q error = %v", override, err)
		}
	}
}

// Each file is its own Compose project, so only the generated set as a whole
// can see this: a development service already sitting on the port test derives
// for its database. AGENTS.md promises host-port collisions refuse generation,
// and across environments is where the collision that bit actually lived.
func TestComposeRefusesACrossEnvironmentPortCollision(t *testing.T) {
	graph := []modkit.Manifest{
		composeAdapter("ggg/system/db", "ggg/database", "postgres", "postgres@sha256:"+strings.Repeat("a", 64), 5432),
		composeAdapter("ggg/system/cache", "ggg/cache", "valkey", "valkey@sha256:"+strings.Repeat("c", 64), 15432),
	}
	lock := modkit.Lock{Providers: map[string]modkit.ProviderSelections{
		"ggg/database": {
			Development: modkit.ProviderSelection{Adapter: "ggg/system/db", Target: "postgres"},
			Test:        modkit.ProviderSelection{Adapter: "ggg/system/db", Target: "postgres"},
		},
		"ggg/cache": {Development: modkit.ProviderSelection{Adapter: "ggg/system/cache", Target: "valkey"}},
	}}
	_, err := modkit.GenerateComposeFiles(lock, graph)
	if err == nil {
		t.Fatal("cross-environment collision generated both files")
	}
	for _, want := range []string{"host port 15432", "development (ggg/system/cache@valkey)", "test (ggg/system/db@postgres)"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("collision error %q does not name %q", err, want)
		}
	}
}

func composeFile(t *testing.T, files []modkit.GeneratedFile, path string) string {
	t.Helper()
	for _, file := range files {
		if file.Path == path {
			return file.Content
		}
	}
	t.Fatalf("generated set has no %s: %#v", path, files)
	return ""
}

func TestTrustedTaskUsesFixedArgv(t *testing.T) {
	runner := &recordingTaskRunner{}
	root := t.TempDir()
	controller := NewController(ControllerOptions{Root: root, TaskRunner: runner})
	plan, err := controller.Preview(context.Background(), TaskMutation{Task: "build"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.argv, " "); got != "go build ./cmd/server" {
		t.Fatalf("argv = %q", got)
	}
}

// The visual gate is only meaningful through scripts/visual.sh: it seeds the
// e2e database, starts the host server and runs the specs in the pinned
// container. A bare `npx playwright test` passes locally (node_modules present,
// server already up) and fails in CI, so the argv is pinned by a test.
func TestVisualTaskRunsContainerHarness(t *testing.T) {
	runner := &recordingTaskRunner{}
	controller := NewController(ControllerOptions{Root: t.TempDir(), TaskRunner: runner})
	plan, err := controller.Preview(context.Background(), TaskMutation{Task: "test", Action: "visual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.argv, " "); got != filepath.Join("scripts", "visual.sh") {
		t.Fatalf("argv = %q", got)
	}
}

// A failed trusted task must carry the same fixed envelope as any other
// failure. It returned a zero-value envelope, so the renderer printed
// "failed (exit 0)" while the process exited 1 — an envelope that contradicts
// the process status, which is what automation branches on.
//
// The second case is the regression the first fix nearly introduced: a child
// process's status must never become a declared exit code. *exec.ExitError
// satisfies interface{ ExitCode() int } through *os.ProcessState, and the
// statuses are real — smoke.sh under `set -euo pipefail` exits 7 on a bare
// curl, docker compose reports daemon errors as 125, and a child exiting 5
// would make ggg claim the rolled-back-tree code over a tree it never
// touched.
func TestFailedTrustedTaskReportsTheFailureEnvelope(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		cause error
	}{
		{"uncoded failure", os.ErrInvalid},
		{"child process status", childStatusError(7)},
		{"child process status claiming the rollback code", childStatusError(exitRollback)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &recordingRunner{fail: map[string]error{"go build ./cmd/server": testCase.cause}}
			controller := NewController(ControllerOptions{Root: t.TempDir(), TaskRunner: runner})
			plan, err := controller.Preview(context.Background(), TaskMutation{Task: "build"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := controller.Apply(context.Background(), plan)
			if err == nil {
				t.Fatal("a failed task reported success")
			}
			if result.Envelope.Exit != exitRuntime || result.Envelope.OK {
				t.Fatalf("envelope = {ok:%v exit:%d}, want {ok:false exit:%d}", result.Envelope.OK, result.Envelope.Exit, exitRuntime)
			}
			if got := exitOf(t, err); got != exitRuntime {
				t.Fatalf("process exit = %d, want %d: a subprocess status reached the public contract", got, exitRuntime)
			}
			if result.Envelope.Exit != exitOf(t, err) {
				t.Fatalf("envelope exit %d contradicts the process exit %d", result.Envelope.Exit, exitOf(t, err))
			}
			if result.Envelope.Command != "build" {
				t.Fatalf("envelope command = %q, want %q", result.Envelope.Command, "build")
			}
		})
	}
}

// childStatusError mimics what osTaskRunner returns for a child that exited
// nonzero: an error carrying ExitCode() structurally, wrapped in the argv
// prefix the task runner adds.
type childStatusError int

func (e childStatusError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e childStatusError) ExitCode() int { return int(e) }

type recordingTaskRunner struct {
	argv []string
	env  map[string]string
}

func (r *recordingTaskRunner) Run(_ context.Context, _ string, argv []string, env map[string]string) error {
	r.argv = append([]string(nil), argv...)
	r.env = env
	return nil
}
