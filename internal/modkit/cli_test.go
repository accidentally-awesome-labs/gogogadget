package modkit

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestCLIVersionReportsConfiguredBuildVersion(t *testing.T) {
	var out bytes.Buffer
	cli := CLI{Out: &out, Version: "v1.2.3"}

	if err := cli.Run(context.Background(), []string{"version"}); err != nil {
		t.Fatalf("Run(version): %v", err)
	}
	if got, want := out.String(), "ggg v1.2.3\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestCLIVersionDefaultsToDev(t *testing.T) {
	var out bytes.Buffer

	if err := (CLI{Out: &out}).Run(context.Background(), []string{"version"}); err != nil {
		t.Fatalf("Run(version): %v", err)
	}
	if got, want := out.String(), "ggg dev\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestCLIVersionAllowsNilOutput(t *testing.T) {
	if err := (CLI{Version: "test"}).Run(context.Background(), []string{"version"}); err != nil {
		t.Fatalf("Run(version): %v", err)
	}
}

func TestCLIUsageFailuresCarryExitCodeTwo(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing command"},
		{name: "unknown command", args: []string{"unknown"}},
		{name: "version arguments", args: []string{"version", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := (CLI{Out: &out, Version: "test"}).Run(context.Background(), tt.args)
			if err == nil {
				t.Fatal("Run returned nil error")
			}
			var coded interface{ ExitCode() int }
			if !errors.As(err, &coded) {
				t.Fatalf("error %T does not expose ExitCode", err)
			}
			if got, want := coded.ExitCode(), 2; got != want {
				t.Fatalf("exit code = %d, want %d", got, want)
			}
		})
	}
}
