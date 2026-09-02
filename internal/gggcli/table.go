package gggcli

import (
	"fmt"
	"strings"
)

// CommandTable returns the built-in command table in canonical name order.
// Help, completions, dispatch, and reserved-name checks all read it, so a
// command cannot exist in one place and be invisible in another.
func CommandTable() []CommandSpec {
	return builtInCommands()
}

// commandTable composes built-ins with a contributed set. A contributed
// command colliding with a reserved built-in name is a diagnostic, not a
// crash: the colliding command is skipped and the conflict is reported so the
// console still serves every other command.
func commandTable(contributed []ContributedCommand) ([]CommandSpec, []string) {
	table := append([]CommandSpec{}, builtInCommands()...)
	reserved := reservedCommandNames(table)
	conflicts := make([]string, 0)
	for _, command := range contributed {
		if _, clash := reserved[command.Spec.Name]; clash {
			conflicts = append(conflicts, command.Spec.Name)
			continue
		}
		table = append(table, command.Spec)
	}
	sortSpecs(table)
	return table, conflicts
}

// IsReservedName reports whether a command name is a built-in and therefore
// reserved against contributed commands.
func IsReservedName(name string) bool {
	_, ok := lookupSpec(builtInCommands(), name)
	return ok
}

func sortSpecs(table []CommandSpec) {
	for i := 1; i < len(table); i++ {
		for j := i; j > 0 && table[j].Name < table[j-1].Name; j-- {
			table[j], table[j-1] = table[j-1], table[j]
		}
	}
}

// builtInCommands is the reserved command table. Usage strings here are the
// single source of truth for help output and completions.
func builtInCommands() []CommandSpec {
	return []CommandSpec{
		{Name: "add", Summary: "Install one or more modules into the project", Usage: "ggg add KIND/NAME... [--dry-run] [--json]", Flags: []FlagSpec{
			{Name: "dry-run", Help: "resolve and report without writing"}, {Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "build", Summary: "Build the server binary", Usage: "ggg build [--json]", Flags: []FlagSpec{{Name: "json", Help: "emit the machine envelope"}}},
		{Name: "cache", Summary: "Manage the registry cache", Usage: "ggg cache prune"},
		{Name: "catalog", Summary: "List the modules the registry publishes", Usage: "ggg catalog [--installed] [--kind KIND] [--latest] [--json]", Flags: []FlagSpec{
			{Name: "installed", Help: "list only installed modules"}, {Name: "kind", Help: "restrict to one module kind", Value: true},
			{Name: "latest", Help: "resolve the registry ref instead of the locked commit"}, {Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "check", Summary: "Run generation, drift, vet, test, and build gates", Usage: "ggg check [--json]", Flags: []FlagSpec{{Name: "json", Help: "emit the machine envelope"}}},
		{Name: "completion", Summary: "Emit a shell completion script (bash, zsh, fish)", Usage: "ggg completion bash|zsh|fish"},
		{Name: "create", Summary: "Create source in the mutable project registry", Usage: "ggg create module|resource|page|workflow|job|migration|component|provider NAME [FLAGS]", Flags: []FlagSpec{
			{Name: "scope", Help: "resource/page scope", Value: true}, {Name: "table", Help: "resource table", Value: true},
			{Name: "route", Help: "resource route prefix", Value: true}, {Name: "api", Help: "include API transport"},
			{Name: "admin", Help: "include admin surface"}, {Name: "search", Help: "include search integration"},
			{Name: "no-ui", Help: "omit UI surface"}, {Name: "family", Help: "component family", Value: true},
			{Name: "owner", Help: "migration owner module", Value: true}, {Name: "kind", Help: "migration kind", Value: true},
			{Name: "schedulable", Help: "make the job schedulable"}, {Name: "max-attempts", Help: "job attempt limit", Value: true},
			{Name: "slot", Help: "provider slot", Value: true}, {Name: "package", Help: "provider package", Value: true},
			{Name: "constructor", Help: "provider constructor", Value: true}, {Name: "definition", Help: "provider definition JSON", Value: true},
			{Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "db", Summary: "Run database lifecycle tasks", Usage: "ggg db migrate|status|seed|reset|backup|restore|restore-drill [--environment development|test] [--destination PATH] [--backup ID] [--to-env KEY] [--yes] [--json]", Flags: []FlagSpec{
			{Name: "environment", Help: "select the local environment", Value: true, Default: "development"},
			{Name: "destination", Help: "backup destination path", Value: true},
			{Name: "backup", Help: "recorded backup id", Value: true},
			{Name: "to-env", Help: "environment key addressing the restored database", Value: true},
			{Name: "yes", Help: "confirm a destructive reset or a restore"}, {Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "deploy", Summary: "Deploy the project to its selected target", Usage: "ggg deploy plan|apply|status|logs|rollback|secrets --environment development|test|production [--key KEY]... [--yes] [--resume RUN_ID] [--json]", Flags: []FlagSpec{
			{Name: "environment", Help: "deployment environment", Value: true, Default: "production"},
			{Name: "key", Help: "secret key for deploy secrets (repeatable)", Value: true, Repeatable: true},
			{Name: "yes", Help: "confirm the remote mutation"}, {Name: "resume", Help: "resume a persisted run", Value: true},
			{Name: "follow", Help: "stream logs until cancelled"}, {Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "deployment", Summary: "Set the project's deployment module", Usage: "ggg deployment set MODULE [--json]", Flags: []FlagSpec{
			{Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "dev", Summary: "Run the supervised development loop", Usage: "ggg dev [--json]", Flags: []FlagSpec{{Name: "json", Help: "emit the machine envelope"}}},
		{Name: "diff", Summary: "Compare installed module files with the lock", Usage: "ggg diff [MODULE]... [--upstream] [--json]", Flags: []FlagSpec{
			{Name: "upstream", Help: "show the staged upstream candidate diff"}, {Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "doctor", Summary: "Report project health and drifted state", Usage: "ggg doctor [--runtime] [--fix --finding CODE --yes] [--json]", Flags: []FlagSpec{
			{Name: "runtime", Help: "add live checks: provider keys, provider health, deployment linkage, backup policy"},
			{Name: "fix", Help: "perform the remediation attached to one typed finding"},
			{Name: "finding", Help: "the finding code to fix", Value: true},
			{Name: "yes", Help: "confirm the remediation"}, {Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "provider", Summary: "Manage provider slot selections and provisioning", Usage: "ggg provider list|set|configure|provision|test [--json]", Flags: []FlagSpec{
			{Name: "slot", Help: "provider slot", Value: true}, {Name: "environment", Help: "target environment", Value: true},
			{Name: "provider", Help: "selection SLOT:ENV=ADAPTER@TARGET (repeatable)", Value: true, Repeatable: true},
			{Name: "set", Help: "declared input value KEY=VALUE (repeatable)", Value: true, Repeatable: true},
			{Name: "adapter", Help: "adapter to verify", Value: true}, {Name: "target", Help: "target to verify", Value: true},
			{Name: "yes", Help: "confirm the remote mutation"}, {Name: "resume", Help: "resume a persisted run", Value: true},
			{Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "generate", Summary: "Refresh mutable registries and generate the project", Usage: "ggg generate [--json]", Flags: []FlagSpec{{Name: "json", Help: "emit the machine envelope"}}},
		{Name: "help", Summary: "Show help for ggg or one command", Usage: "ggg help [COMMAND]"},
		{Name: "identity", Summary: "Identity provider operations", Usage: "ggg identity link --environment ENV --provider PROVIDER --subject SUBJECT (--user USER_ID|--org ORG_ID)"},
		{Name: "info", Summary: "Report one module's contract", Usage: "ggg info KIND/NAME [--json]", Flags: []FlagSpec{{Name: "json", Help: "emit the machine envelope"}}},
		{Name: "init", Summary: "Initialize or adopt the current directory", Usage: "ggg init [--module MODULE] [--ref REF] [--repository REPO] [--public-key BASE64] [--adopt] [--claim PATH]... [--offline] [--json]", Flags: []FlagSpec{
			{Name: "module", Help: "Go module path for a directory without go.mod", Value: true},
			{Name: "ref", Help: "registry ref to pin", Value: true, Default: "main"},
			{Name: "repository", Help: "registry repository", Value: true, Default: DefaultRegistryRepository},
			{Name: "public-key", Help: "base64 raw Ed25519 registry public key", Value: true},
			{Name: "adopt", Help: "produce the initial lock from what is already installed"}, {Name: "offline", Help: "resolve only from a self-hosted or cached registry"},
			{Name: "claim", Help: "adopt a pre-existing divergent file as a recorded modification (repeatable)", Value: true, Repeatable: true},
			{Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "migrate", Summary: "Migrate project metadata between schema versions", Usage: "ggg migrate schema-1 [--json]", Flags: []FlagSpec{{Name: "json", Help: "emit the machine envelope"}}},
		{Name: "new", Summary: "Create a new project", Usage: "ggg new DIR --module MODULE --profile PROFILE --provider SLOT:ENV=ADAPTER@TARGET... --deployment MODULE --registry github:OWNER/REPO|directory:PATH [--ref REF] [--answers FILE] [--non-interactive] [--json]", Flags: []FlagSpec{
			{Name: "module", Help: "Go module path", Value: true}, {Name: "profile", Help: "project profile", Value: true},
			{Name: "provider", Help: "provider selection (repeatable)", Value: true, Repeatable: true},
			{Name: "deployment", Help: "deployment module", Value: true}, {Name: "registry", Help: "core registry source", Value: true},
			{Name: "ref", Help: "GitHub registry ref", Value: true}, {Name: "answers", Help: "JSON answers file", Value: true},
			{Name: "non-interactive", Help: "refuse missing answers instead of prompting"}, {Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "registry", Summary: "Author and verify the self-hosting registry", Usage: "ggg registry build|validate [--json]", Flags: []FlagSpec{{Name: "json", Help: "emit the machine envelope"}}},
		{Name: "remove", Summary: "Remove modules from the project", Usage: "ggg remove KIND/NAME... [--purge-data] [--dry-run] [--json]", Flags: []FlagSpec{
			{Name: "purge-data", Help: "run the module's reviewed teardown migration"}, {Name: "dry-run", Help: "resolve and report without writing"}, {Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "resolve", Summary: "Resolve one staged conflict", Usage: "ggg resolve KIND/NAME --path PATH (--accept-upstream|--keep-local|--merged) [--json]", Flags: []FlagSpec{
			{Name: "path", Help: "the conflicted file to resolve", Value: true}, {Name: "accept-upstream", Help: "replace local bytes with the staged candidate"},
			{Name: "keep-local", Help: "keep local bytes and clear the conflict"}, {Name: "merged", Help: "accept the already-merged local bytes"},
			{Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "services", Summary: "Manage selected local provider services", Usage: "ggg services up|down|status|logs [--environment development|test] [--volumes] [--json]", Flags: []FlagSpec{
			{Name: "environment", Help: "select compose environment", Value: true, Default: "development"},
			{Name: "volumes", Help: "remove named volumes with services down"}, {Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "setup", Summary: "Install verified tools and build bin/ggg", Usage: "ggg setup [--json]", Flags: []FlagSpec{{Name: "json", Help: "emit the machine envelope"}}},
		{Name: "sync", Summary: "Reconcile the tree with the project intent", Usage: "ggg sync [--check] [--offline] [--claim PATH]... [--json]", Flags: []FlagSpec{
			{Name: "check", Help: "fail on drift without writing"}, {Name: "offline", Help: "resolve only from the local registry cache"},
			{Name: "claim", Help: "adopt a pre-existing divergent file as a recorded modification (repeatable)", Value: true, Repeatable: true},
			{Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "test", Summary: "Run one test layer", Usage: "ggg test unit|integration|e2e|visual|smoke|all [--json]", Flags: []FlagSpec{{Name: "json", Help: "emit the machine envelope"}}},
		{Name: "update", Summary: "Advance the project to newer module revisions", Usage: "ggg update [--ref REF] [--dry-run] [--json]", Flags: []FlagSpec{
			{Name: "ref", Help: "registry ref to advance to", Value: true}, {Name: "dry-run", Help: "resolve and report without writing"}, {Name: "json", Help: "emit the machine envelope"},
		}},
		{Name: "version", Summary: "Print the ggg version", Usage: "ggg version"},
	}
}

// renderHelp derives help text from the command table. With no command name it
// lists every command; with one it renders that command's usage and flags.
func renderHelp(table []CommandSpec, command string) string {
	var b strings.Builder
	if command == "" {
		b.WriteString("ggg is the module registry command line for GoGoGadget projects.\n\n")
		b.WriteString("Usage:\n  ggg COMMAND [flags]\n\nCommands:\n")
		for _, spec := range table {
			fmt.Fprintf(&b, "  %-12s %s\n", spec.Name, spec.Summary)
		}
		b.WriteString("\nRun `ggg help COMMAND` for a command's flags. `ggg` with no arguments\nopens the interactive console when attached to a terminal.\n")
		return b.String()
	}
	spec, ok := lookupSpec(table, command)
	if !ok {
		return fmt.Sprintf("unknown command %q\n\n%s", command, renderHelp(table, ""))
	}
	fmt.Fprintf(&b, "%s - %s\n\nUsage:\n  %s\n", spec.Name, spec.Summary, spec.Usage)
	if len(spec.Flags) > 0 {
		b.WriteString("\nFlags:\n")
		for _, flag := range spec.Flags {
			form := "--" + flag.Name
			if flag.Value {
				form += " " + strings.ToUpper(flag.Name)
			}
			line := "  " + form
			if flag.Default != "" {
				line += " (default " + flag.Default + ")"
			}
			line += "\n      " + flag.Help + "\n"
			b.WriteString(line)
		}
	}
	if spec.SourceModule != "" {
		fmt.Fprintf(&b, "\nContributed by %s.\n", spec.SourceModule)
	}
	return b.String()
}

func lookupSpec(table []CommandSpec, name string) (CommandSpec, bool) {
	for _, spec := range table {
		if spec.Name == name {
			return spec, true
		}
	}
	return CommandSpec{}, false
}
