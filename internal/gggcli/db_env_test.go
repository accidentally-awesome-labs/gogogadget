package gggcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `ggg db` handed goose `os.Getenv("DATABASE_URL")` straight in its argv, so a
// project whose value lives only where the CLI writes it —
// .ggg/env/<environment>.env — passed an EMPTY DSN. That is not an error to
// libpq: it falls back to its own defaults and connects to a local socket, so
// a generated project's documented first migration could aim at a server the
// project has nothing to do with. These tests pin the precedence, the refusal,
// and that no tool is ever invoked with an empty connection string.

const (
	fileDSN = "postgres://postgres:postgres@localhost:55432/gogogadget?sslmode=disable"
	envDSN  = "postgres://postgres:postgres@localhost:6000/from-process-env?sslmode=disable"
	dotDSN  = "postgres://postgres:postgres@localhost:6001/from-legacy-dotenv?sslmode=disable"
)

// dbProject writes a project root carrying the CLI-managed env files named by
// contents, keyed by environment.
func dbProject(t *testing.T, contents map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for environment, body := range contents {
		writeTestFile(t, root, filepath.Join(".ggg", "env", environment+".env"), []byte(body))
	}
	return root
}

// driveDBTask drives one `ggg db ACTION` through the controller's own
// preview/apply boundary and returns the recorded argv and injected env.
func driveDBTask(t *testing.T, root, action, environment string) (*recordingRunner, error) {
	t.Helper()
	runner := &recordingRunner{}
	controller := NewController(ControllerOptions{Root: root, Version: "v1.2.3", TaskRunner: runner})
	plan, err := controller.Preview(context.Background(),
		TaskMutation{Task: "db", Action: action, Environment: environment})
	if err != nil {
		return runner, err
	}
	_, err = controller.Apply(context.Background(), plan)
	return runner, err
}

// injectedFor returns the environment injected with the argv containing want.
func injectedFor(t *testing.T, runner *recordingRunner, want string) map[string]string {
	t.Helper()
	for i, call := range runner.calls {
		if strings.Contains(call, want) {
			return runner.envs[i]
		}
	}
	t.Fatalf("no task ran %q; ran %v", want, runner.calls)
	return nil
}

// The failure that shipped: the value is only in the CLI-managed file.
func TestDBTasksResolveTheDSNFromTheCLIManagedFile(t *testing.T) {
	root := dbProject(t, map[string]string{"development": "DATABASE_URL=" + fileDSN + "\n"})
	for _, testCase := range []struct {
		action string
		argv   string
		key    string
	}{
		{"migrate", "goose", "GOOSE_DBSTRING"},
		{"status", "goose", "GOOSE_DBSTRING"},
		{"seed", "cmd/seed", "DATABASE_URL"},
	} {
		t.Run(testCase.action, func(t *testing.T) {
			runner, err := driveDBTask(t, root, testCase.action, "")
			if err != nil {
				t.Fatalf("db %s: %v", testCase.action, err)
			}
			injected := injectedFor(t, runner, testCase.argv)
			if injected[testCase.key] != fileDSN {
				t.Fatalf("injected %s = %q, want the file's value %q", testCase.key, injected[testCase.key], fileDSN)
			}
			// The DSN carries a password; the process list is public. It must
			// never reach argv, and no argument may be empty.
			for _, call := range runner.calls {
				if strings.Contains(call, fileDSN) {
					t.Fatalf("the connection string reached argv: %q", call)
				}
			}
			for i, call := range runner.calls {
				for _, arg := range strings.Split(call, " ") {
					if arg == "" {
						t.Fatalf("task %d was invoked with an empty argument: %q", i, call)
					}
				}
			}
		})
	}
}

// Process environment wins over the file, and the file wins over legacy .env.
func TestDBTaskDSNPrecedence(t *testing.T) {
	root := dbProject(t, map[string]string{"development": "DATABASE_URL=" + fileDSN + "\n"})
	writeTestFile(t, root, ".env", []byte("DATABASE_URL="+dotDSN+"\n"))

	t.Setenv("DATABASE_URL", envDSN)
	runner, err := driveDBTask(t, root, "migrate", "")
	if err != nil {
		t.Fatalf("db migrate: %v", err)
	}
	if got := injectedFor(t, runner, "goose")["GOOSE_DBSTRING"]; got != envDSN {
		t.Fatalf("injected %q, want the process environment to win with %q", got, envDSN)
	}

	// With the process value absent, the CLI-managed file outranks .env.
	t.Setenv("DATABASE_URL", "")
	runner, err = driveDBTask(t, root, "migrate", "")
	if err != nil {
		t.Fatalf("db migrate without a process value: %v", err)
	}
	if got := injectedFor(t, runner, "goose")["GOOSE_DBSTRING"]; got != fileDSN {
		t.Fatalf("injected %q, want the CLI-managed file's %q to outrank .env", got, fileDSN)
	}
}

// `--environment test` reads the test file, and test never reads .env: a
// legacy dotenv is a development convenience and pointing the test database at
// it would be the same wrong-server hazard.
func TestDBTaskEnvironmentSelectsTheFileAndTestIgnoresDotEnv(t *testing.T) {
	root := dbProject(t, map[string]string{
		"development": "DATABASE_URL=" + fileDSN + "\n",
		"test":        "DATABASE_URL=" + envDSN + "\n",
	})
	writeTestFile(t, root, ".env", []byte("DATABASE_URL="+dotDSN+"\n"))
	t.Setenv("DATABASE_URL", "")

	runner, err := driveDBTask(t, root, "status", "test")
	if err != nil {
		t.Fatalf("db status --environment test: %v", err)
	}
	if got := injectedFor(t, runner, "goose")["GOOSE_DBSTRING"]; got != envDSN {
		t.Fatalf("injected %q, want the test file's %q", got, envDSN)
	}

	// A test environment with no test file must refuse rather than fall
	// through to the development file or to .env.
	bare := t.TempDir()
	writeTestFile(t, bare, ".env", []byte("DATABASE_URL="+dotDSN+"\n"))
	writeTestFile(t, bare, filepath.Join(".ggg", "env", "development.env"), []byte("DATABASE_URL="+fileDSN+"\n"))
	if _, err := driveDBTask(t, bare, "status", "test"); err == nil {
		t.Fatal("db status --environment test resolved a value from a development-only source")
	}
}

// The dangerous case: nothing supplies the value. It must refuse, and no tool
// may be invoked at all — passing empty is how a command reaches the wrong
// server.
func TestDBTasksRefuseAnUnresolvedDSNWithoutInvokingATool(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, action := range []string{"migrate", "status", "seed"} {
		t.Run(action, func(t *testing.T) {
			runner, err := driveDBTask(t, dbProject(t, nil), action, "")
			if err == nil {
				t.Fatalf("db %s succeeded with no connection string anywhere", action)
			}
			if exitOf(t, err) != exitRefusal {
				t.Fatalf("db %s exit = %d, want %d (refusal)", action, exitOf(t, err), exitRefusal)
			}
			for _, want := range []string{"DATABASE_URL", "development", filepath.ToSlash(filepath.Join(".ggg", "env", "development.env"))} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not name %q", err, want)
				}
			}
			if len(runner.calls) != 0 {
				t.Fatalf("a tool ran despite the unresolved value: %v", runner.calls)
			}
		})
	}
}

// Production configuration comes from the deployment environment. A file
// someone left at .ggg/env/production.env must not be read, so the absence of
// the file is not what the rule rests on.
func TestProductionReadsNoEnvironmentFile(t *testing.T) {
	root := dbProject(t, map[string]string{"production": "DATABASE_URL=" + fileDSN + "\n"})
	t.Setenv("DATABASE_URL", "")
	controller := NewController(ControllerOptions{Root: root, Version: "v1.2.3", TaskRunner: &recordingRunner{}})
	if _, err := controller.taskEnvValue("production", "DATABASE_URL"); err == nil {
		t.Fatal("production resolved a value from .ggg/env/production.env")
	}
	if _, err := os.Stat(filepath.Join(root, ".ggg", "env", "production.env")); err != nil {
		t.Fatalf("the fixture must actually have the file, or this proves nothing: %v", err)
	}
}
