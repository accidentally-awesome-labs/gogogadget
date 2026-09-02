package docker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// runCommand executes one fixed argv inside the project directory, feeding
// stdin and capturing stdout. There is no shell anywhere: argv[0] is the
// binary and every later element is one literal argument.
func runCommand(ctx context.Context, root string, argv []string, stdin []byte) ([]byte, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty argv")
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = root
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stdout
	if err := command.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%s: %w", argv[0], err)
	}
	return stdout.Bytes(), nil
}
