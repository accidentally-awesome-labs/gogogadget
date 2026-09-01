package gggcli

import (
	"fmt"
	"strings"
)

// renderCompletion derives a shell completion script from the command table.
// All three shells complete command names first and a named command's own
// flags once its word is fixed. The scripts are generated text, not
// hand-maintained grammar: a command added to the table completes without
// further edits. An unsupported shell is a usage failure, never an empty
// success.
func renderCompletion(table []CommandSpec, shell string) (string, error) {
	switch shell {
	case "bash":
		return bashCompletion(table), nil
	case "zsh":
		return zshCompletion(table), nil
	case "fish":
		return fishCompletion(table), nil
	default:
		return "", usageError(fmt.Sprintf("unsupported shell %q; supported shells are bash, zsh, fish", shell))
	}
}

func bashCompletion(table []CommandSpec) string {
	var b strings.Builder
	b.WriteString(`# bash completion for ggg (derived from the ggg command table)
_ggg_completions() {
  local cur commands flags
  cur="${COMP_WORDS[COMP_CWORD]}"
`)
	b.WriteString("  commands=\"")
	for i, spec := range table {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(spec.Name)
	}
	b.WriteString("\"\n")
	b.WriteString(`  if [[ "$cur" == -* && "$COMP_CWORD" -ge 2 ]]; then
    local prev="${COMP_WORDS[COMP_CWORD-1]}"
    case "$prev" in
`)
	for _, spec := range table {
		if len(spec.Flags) == 0 {
			continue
		}
		flags := make([]string, 0, len(spec.Flags))
		for _, flag := range spec.Flags {
			flags = append(flags, "--"+flag.Name)
		}
		fmt.Fprintf(&b, "      %s) flags=\"%s\" ;;\n", spec.Name, strings.Join(flags, " "))
	}
	b.WriteString("      *) flags=\"\" ;;\n")
	b.WriteString(`    esac
    COMPREPLY=( $(compgen -W "$flags" -- "$cur") )
    return
  fi
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
  fi
}
complete -F _ggg_completions ggg
`)
	return b.String()
}

func zshCompletion(table []CommandSpec) string {
	var b strings.Builder
	b.WriteString("#compdef ggg\n")
	b.WriteString("# zsh completion for ggg (derived from the ggg command table)\n\n")
	b.WriteString("_ggg() {\n  local -a commands\n  commands=(\n")
	for _, spec := range table {
		fmt.Fprintf(&b, "    '%s:%s'\n", spec.Name, zshEscape(spec.Summary))
	}
	b.WriteString("  )\n")
	b.WriteString("  if (( CURRENT == 2 )); then\n")
	b.WriteString("    _describe -t commands 'ggg command' commands\n    return\n  fi\n")
	b.WriteString("  local -a flags\n")
	b.WriteString("  case \"$words[2]\" in\n")
	for _, spec := range table {
		if len(spec.Flags) == 0 {
			continue
		}
		fmt.Fprintf(&b, "    %s) flags=( ", spec.Name)
		for _, flag := range spec.Flags {
			fmt.Fprintf(&b, "'--%s[%s]' ", flag.Name, zshEscape(flag.Help))
		}
		b.WriteString(") ;;\n")
	}
	b.WriteString("  esac\n  compadd -a flags\n}\n_ggg \"$@\"\n")
	return b.String()
}

func zshEscape(s string) string {
	return strings.ReplaceAll(s, "'", "")
}

func fishCompletion(table []CommandSpec) string {
	var b strings.Builder
	b.WriteString("# fish completion for ggg (derived from the ggg command table)\n")
	for _, spec := range table {
		fmt.Fprintf(&b, "complete -c ggg -n '__fish_use_subcommand' -a '%s' -d '%s'\n", spec.Name, zshEscape(spec.Summary))
		for _, flag := range spec.Flags {
			fmt.Fprintf(&b, "complete -c ggg -n '__fish_seen_subcommand_from %s' -l %s -d '%s'\n", spec.Name, flag.Name, zshEscape(flag.Help))
		}
	}
	return b.String()
}
