package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitCode(err))
	}
}

func run(args []string) error {
	return (modkit.CLI{Out: os.Stdout, Err: os.Stderr, Version: version}).Run(context.Background(), args)
}

func exitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return 1
}
