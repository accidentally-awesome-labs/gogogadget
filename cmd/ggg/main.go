package main

import (
	"context"
	"fmt"
	"os"

	"github.com/gogogadget/gogogadget/internal/gggcli"
	"github.com/gogogadget/gogogadget/internal/gggcli/commands"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitCode(err))
	}
}

func run(args []string) error {
	app := gggcli.App{
		Out:         os.Stdout,
		Err:         os.Stderr,
		Version:     version,
		Contributed: commands.CLICommands(),
		Remote:      commands.RemoteRegistries(),
	}
	return app.Run(context.Background(), args)
}

func exitCode(err error) int {
	return gggcli.ExitCode(err)
}
