package gggcli

import (
	"context"
	"encoding/json"
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

type recordingTaskRunner struct{ argv []string }

func (r *recordingTaskRunner) Run(_ context.Context, _ string, argv []string) error {
	r.argv = append([]string(nil), argv...)
	return nil
}
