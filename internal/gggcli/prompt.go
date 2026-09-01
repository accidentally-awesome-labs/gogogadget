package gggcli

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// promptResolution asks the operator to choose a conflict resolution on a
// terminal, when the flags left the choice open. It is a linear prompt by
// design: the nonvisual package carries no UI toolkit, and the same choice is
// rendered by the Huh forms of the interactive console when the full TUI is
// installed. Returning an empty mode means the operator dismissed the prompt
// before any plan existed, which cancels with exit 0.
func promptResolution(_ context.Context, cc CommandContext, moduleID, path string) (mode modkit.ResolutionMode, err error) {
	fmt.Fprintf(cc.Out, "Resolve %s at %s:\n", moduleID, path)
	fmt.Fprintf(cc.Out, "  1. keep-local      keep local bytes and clear the conflict\n")
	fmt.Fprintf(cc.Out, "  2. accept-upstream replace local bytes with the staged candidate\n")
	fmt.Fprintf(cc.Out, "  3. merged          accept the already-merged local bytes\n")
	for range 3 {
		line, readErr := readLine(cc, "Choose 1-3 (Esc to cancel): ")
		if readErr != nil {
			return "", readErr
		}
		switch line {
		case "1", "keep-local", "--keep-local":
			return modkit.ResolutionKeepLocal, nil
		case "2", "accept-upstream", "--accept-upstream":
			return modkit.ResolutionAcceptUpstream, nil
		case "3", "merged", "--merged":
			return modkit.ResolutionMerged, nil
		case "esc", "escape", "cancel", "q":
			return "", nil
		}
		fmt.Fprintln(cc.Out, "Choose 1-3, or Esc to cancel.")
	}
	return "", nil
}
