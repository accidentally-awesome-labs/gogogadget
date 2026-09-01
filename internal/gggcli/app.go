package gggcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// App is the presentation front end: it parses argv against the command
// table, detects the terminal, and renders Results. All real work happens in
// the Controller the App carries.
type App struct {
	Out io.Writer
	Err io.Writer
	// In is the interactive input stream; nil means os.Stdin.
	In io.Reader
	// Version is the running ggg version.
	Version string
	// Root is the project directory. Empty means the process working directory.
	Root string
	// Engine overrides the engine the controller would otherwise build.
	Engine *modkit.Engine
	// WriteFile replaces direct file writes (test hook for rollback paths).
	WriteFile func(path string, data []byte, mode os.FileMode) error
	// Contributed carries the project-local commands from the generated
	// registry. Assembly refuses a contributed command colliding with a
	// reserved built-in name.
	Contributed []ContributedCommand
}

// Run executes the command described by args and returns the error whose exit
// code ExitCode reports.
func (a App) Run(ctx context.Context, args []string) error {
	out, errOut := a.writers()
	in := a.In
	if in == nil {
		in = os.Stdin
	}

	// --accessible (or GGG_ACCESSIBLE=1) switches guided forms to linear
	// accessible mode. It is a global flag: it may appear before the command
	// word, and it is stripped from argv before dispatch.
	accessible := accessibleFromEnv()
	for _, arg := range args {
		if arg == "--accessible" || arg == "-accessible" {
			accessible = true
		}
	}
	argv := stripAccessible(args)

	controller := NewController(ControllerOptions{Root: a.Root, Version: a.Version, Engine: a.Engine, WriteFile: a.WriteFile})
	// Assemble the full table once. A contributed command that collides with
	// a reserved name is skipped and reported; the remaining commands serve.
	table, nameConflicts := commandTable(a.Contributed)
	for _, name := range nameConflicts {
		fmt.Fprintf(errOut, "warning: contributed command %q collides with a reserved built-in name and was skipped\n", name)
	}
	interactive := IsTerminal(out) && IsTerminalReader(in)

	if len(argv) == 0 {
		// No arguments opens the interactive console, but only on a terminal.
		// Non-TTY callers get the declared usage failure, never a UI that
		// cannot read their keyboard.
		if !interactive {
			return usageError("interactive_terminal_required")
		}
		return a.launchUI(ctx, controller, CommandContext{
			Controller: controller, Table: table, Out: out, Err: errOut, Stdin: in,
			Version: a.Version, Interactive: true, Accessible: accessible,
		})
	}

	// `--help` / `-h` / `help` are derived from the table. A --help request
	// for an unknown command stays a usage failure.
	name := argv[0]
	if name == "--help" || name == "-h" {
		fmt.Fprint(out, renderHelp(table, ""))
		return nil
	}
	rest := argv[1:]
	for _, arg := range rest {
		if arg == "--help" || arg == "-h" {
			if _, known := lookupSpec(table, name); known {
				fmt.Fprint(out, renderHelp(table, name))
				return nil
			}
			return usageError(fmt.Sprintf("unknown command %q", name))
		}
	}

	if _, ok := lookupSpec(table, name); !ok {
		return usageError(fmt.Sprintf("unknown command %q", name))
	}

	asJSON := containsFlag(rest, "json")
	cc := CommandContext{
		Controller: controller, Table: table, Out: out, Err: errOut, Stdin: in,
		Version: a.Version, AsJSON: asJSON, Interactive: interactive, Accessible: accessible,
	}
	if asJSON {
		// JSON implies noninteractive: never prompt, never open a UI.
		cc.Interactive = false
	}

	handler := a.handlerFor(name)
	result, err := handler(ctx, cc, rest)
	if err != nil {
		// A failed command that produced an envelope emits it exactly once,
		// then the error carries the declared exit code to the caller. The
		// error's message is what stderr will print, so it passes through the
		// redactor too.
		if result.Envelope.Command != "" {
			if renderErr := a.render(cc, name, result); renderErr != nil {
				return renderErr
			}
		}
		return redactedError{cause: err, redactor: controller.Redactor()}
	}
	return a.render(cc, name, result)
}

// redactedError masks declared secret values in the error message the
// process prints to stderr, so diagnostics cannot leak what envelopes and
// prompts already hide.
type redactedError struct {
	cause    error
	redactor *Redactor
}

func (e redactedError) Error() string {
	if e.redactor == nil {
		return e.cause.Error()
	}
	return e.redactor.Apply(e.cause.Error())
}

func (e redactedError) Unwrap() error { return e.cause }

// handlerFor returns the execution path for a command: built-in handlers for
// the reserved names, the contributed handler for a contributed one.
func (a App) handlerFor(name string) CommandHandler {
	if handler, ok := builtInHandlers()[name]; ok {
		return handler
	}
	for _, command := range a.Contributed {
		if command.Spec.Name == name {
			return command.Handler
		}
	}
	return func(context.Context, CommandContext, []string) (Result, error) {
		return Result{}, usageError(fmt.Sprintf("unknown command %q", name))
	}
}

// launchUI starts the interactive console through the contributed `ui`
// command. A project that did not install a console has none to launch.
func (a App) launchUI(ctx context.Context, controller *Controller, cc CommandContext) error {
	for _, command := range a.Contributed {
		if command.Spec.Name == "ui" {
			_, err := command.Handler(ctx, cc, nil)
			return err
		}
	}
	return usageError("interactive_terminal_required")
}

// writers resolves the output streams.
func (a App) writers() (io.Writer, io.Writer) {
	out := io.Writer(io.Discard)
	if a.Out != nil {
		out = a.Out
	}
	errOut := io.Writer(io.Discard)
	if a.Err != nil {
		errOut = a.Err
	}
	return out, errOut
}

// render is the single render boundary. JSON output is the fixed envelope —
// or the envelope merged with the command payload — and human output renders
// the same fields, so the two can never disagree. Both pass through the
// secret redactor before anything leaves the process.
func (a App) render(cc CommandContext, command string, result Result) error {
	// A command with nothing to report — `identity link` succeeds silently —
	// renders nothing rather than an empty envelope.
	if result.Envelope.Command == "" && len(result.Payload) == 0 {
		return nil
	}
	redactor := cc.Controller.Redactor()
	if result.Envelope.Command == "" {
		result.Envelope.Command = command
	}
	redactor.ApplyEnvelope(&result.Envelope)
	redactor.ApplyPayload(result.Payload)
	asJSON := cc.AsJSON

	if asJSON {
		data, err := json.MarshalIndent(result.marshalShape(command), "", "  ")
		if err != nil {
			return runtimeError(err)
		}
		_, err = fmt.Fprintf(cc.Out, "%s\n", data)
		return err
	}

	switch command {
	case "version":
		_, err := fmt.Fprintf(cc.Out, "ggg %s\n", result.Payload["version"])
		return err
	case "help", "completion":
		_, err := fmt.Fprint(cc.Out, result.Payload["text"])
		return err
	case "catalog":
		return renderCatalog(cc.Out, result.Payload)
	case "info":
		return renderInfo(cc.Out, result.Payload)
	case "diff":
		return renderDiff(cc.Out, result.Payload)
	default:
		return renderHuman(cc.Out, result.Envelope)
	}
}

// marshalShape projects the result onto its JSON form: plan commands emit the
// fixed envelope; payload commands merge command data under the same fixed
// keys, so machine consumers parse one shape.
func (r Result) marshalShape(command string) any {
	if len(r.Payload) == 0 {
		return r.Envelope
	}
	envelope := map[string]any{
		"ok": r.Envelope.OK, "command": r.Envelope.Command, "run_id": r.Envelope.RunID,
		"registry_commit": r.Envelope.RegistryCommit,
		"resolved":        r.Envelope.Resolved, "changes": r.Envelope.Changes,
		"generated": r.Envelope.Generated, "conflicts": r.Envelope.Conflicts,
		"diagnostics": r.Envelope.Diagnostics, "exit": r.Envelope.Exit,
	}
	if r.Envelope.RunID == "" {
		envelope["run_id"] = envelopeRunID(r.Envelope)
	}
	for key, value := range r.Payload {
		envelope[key] = value
	}
	return envelope
}

func renderHuman(out io.Writer, env modkit.Envelope) error {
	if env.RegistryCommit != "" {
		if _, err := fmt.Fprintf(out, "registry %s\n", env.RegistryCommit); err != nil {
			return err
		}
	}
	for _, change := range env.Changes {
		if change.Kind == modkit.ChangeUnchanged {
			continue
		}
		if _, err := fmt.Fprintf(out, "  %-9s %-10s %s\n", change.Kind, change.Class, change.Path); err != nil {
			return err
		}
	}
	for _, conflict := range env.Conflicts {
		if _, err := fmt.Fprintf(out, "  conflict  %s %s\n", conflict.Module, conflict.Path); err != nil {
			return err
		}
	}
	for _, diagnostic := range env.Diagnostics {
		if _, err := fmt.Fprintf(out, "  %-8s %s %s\n", diagnostic.Severity, diagnostic.Code, diagnostic.Message); err != nil {
			return err
		}
	}
	if !env.OK {
		_, err := fmt.Fprintf(out, "failed (exit %d)\n", env.Exit)
		return err
	}
	return nil
}

func renderCatalog(out io.Writer, payload map[string]any) error {
	entries, _ := payload["modules"].([]catalogEntry)
	for _, entry := range entries {
		if _, err := fmt.Fprintf(out, "%-28s %-10s %s\n", entry.ID, entry.State, entry.Title); err != nil {
			return err
		}
	}
	return nil
}

func renderInfo(out io.Writer, payload map[string]any) error {
	module, _ := payload["module"].(*modkit.Manifest)
	if module == nil {
		return nil
	}
	state, _ := payload["state"].(string)
	fmt.Fprintf(out, "%s  %s\n", module.ID, module.Title)
	fmt.Fprintf(out, "state          %s\n", state)
	fmt.Fprintf(out, "revision       %d (contract %d)\n", module.Revision, module.Contract)
	fmt.Fprintf(out, "removal_policy %s\n", module.RemovalPolicy)
	if len(module.Requires) > 0 {
		requires := make([]string, 0, len(module.Requires))
		for _, requirement := range module.Requires {
			requires = append(requires, requirement.ID)
		}
		fmt.Fprintf(out, "requires       %s\n", strings.Join(requires, ", "))
	}
	for _, file := range module.Files {
		fmt.Fprintf(out, "  file %s\n", file.Target)
	}
	// The same derived facts the JSON envelope carries.
	links, _ := payload["links"].(map[string][]string)
	for _, key := range []string{"gallery", "scenario", "route"} {
		for _, link := range links[key] {
			fmt.Fprintf(out, "  %-8s %s\n", key, link)
		}
	}
	commands, _ := payload["verify"].([]string)
	for _, command := range commands {
		fmt.Fprintf(out, "  verify   %s\n", command)
	}
	return nil
}

func renderDiff(out io.Writer, payload map[string]any) error {
	entries, _ := payload["files"].([]DiffEntry)
	for _, entry := range entries {
		if _, err := fmt.Fprintf(out, "%-10s %-24s %s\n", entry.State, entry.Module, entry.Path); err != nil {
			return err
		}
	}
	return nil
}

// containsFlag reports whether the token set carries a boolean flag word.
func containsFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == "--"+name || arg == "-"+name || strings.HasPrefix(arg, "--"+name+"=") {
			return true
		}
	}
	return false
}

// accessibleFromEnv resolves the accessible-mode default from the environment.
func accessibleFromEnv() bool {
	value, ok := os.LookupEnv("GGG_ACCESSIBLE")
	if !ok {
		return false
	}
	value = strings.ToLower(strings.TrimSpace(value))
	return value != "" && value != "0" && value != "false" && value != "no"
}

// IsTerminal reports whether the stream is attached to a character device —
// the cheap, dependency-free test that separates a terminal from a pipe or a
// redirected file.
func IsTerminal(f io.Writer) bool {
	file, ok := f.(*os.File)
	if !ok {
		return false
	}
	return isCharDevice(file)
}

// IsTerminalReader is IsTerminal for the input stream.
func IsTerminalReader(f io.Reader) bool {
	file, ok := f.(*os.File)
	if !ok {
		return false
	}
	return isCharDevice(file)
}

func isCharDevice(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// readLine reads one interactive answer from the command context.
func readLine(cc CommandContext, prompt string) (string, error) {
	if !cc.Interactive {
		return "", usageError("interactive_terminal_required")
	}
	fmt.Fprint(cc.Out, prompt)
	reader := bufio.NewReader(cc.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// stripAccessible removes the global --accessible flag wherever it appears
// before the command word, returning the remaining argv.
func stripAccessible(args []string) []string {
	argv := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--accessible" || arg == "-accessible" {
			continue
		}
		argv = append(argv, arg)
	}
	return argv
}
