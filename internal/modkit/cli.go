package modkit

import (
	"context"
	"fmt"
	"io"
)

// CLI is the command-line interface for module registry operations.
type CLI struct {
	Out     io.Writer
	Version string
}

// Run executes the command described by args.
func (c CLI) Run(_ context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing command")
	}

	switch args[0] {
	case "version":
		if len(args) != 1 {
			return usageError("usage: ggg version")
		}
		out := c.Out
		if out == nil {
			out = io.Discard
		}
		version := c.Version
		if version == "" {
			version = "dev"
		}
		_, err := fmt.Fprintf(out, "ggg %s\n", version)
		return err
	default:
		return usageError(fmt.Sprintf("unknown command %q", args[0]))
	}
}

type usageError string

func (e usageError) Error() string {
	return string(e)
}

func (usageError) ExitCode() int {
	return 2
}
