package gggcli

// CommandSpec is one entry in the ggg command table. Help text, shell
// completions, and flag parsing all derive from the table, so a command
// cannot drift from its own usage.
type CommandSpec struct {
	// Name is the command word. Built-in names are reserved: a contributed
	// command colliding with one is refused when the table is assembled.
	Name string
	// Summary is the one-line description help and completions render.
	Summary string
	// Usage is the canonical argument shape shown by `ggg help NAME`.
	Usage string
	// Flags declares the command's flags. Parsing, help, and completions
	// derive from this list.
	Flags []FlagSpec
	// SourceModule names the installed module that contributed the command.
	// Empty for built-ins.
	SourceModule string
}

// FlagSpec declares one flag in a command's spec.
type FlagSpec struct {
	// Name is the flag word without dashes: "check" for --check.
	Name string
	// Help is the flag's help line.
	Help string
	// Value reports that the flag takes a value (--ref REF). Booleans omit it.
	Value bool
	// Default is the declared default rendered by help.
	Default string
	// Repeatable collects repeated occurrences instead of overwriting.
	Repeatable bool
}

// spec looks up one declared flag.
func (c CommandSpec) spec(name string) (FlagSpec, bool) {
	for _, flag := range c.Flags {
		if flag.Name == name {
			return flag, true
		}
	}
	return FlagSpec{}, false
}

// reservedCommandNames returns the built-in command names. The list is derived
// from the table itself, so a built-in can never collide with a contributed
// command silently.
func reservedCommandNames(table []CommandSpec) map[string]struct{} {
	reserved := make(map[string]struct{}, len(table))
	for _, spec := range table {
		reserved[spec.Name] = struct{}{}
	}
	return reserved
}
