package gggcli

import (
	"fmt"
	"strings"
)

// renderCompletion derives a shell completion script from the command table.
// All three shells complete command names first and a named command's flags
// once its word is fixed. The scripts are generated text, not hand-maintained
// grammar: a command added to the table completes without further edits.
func renderCompletion(table []CommandSpec, shell string) string {
	switch shell {
	case "bash":
		return bashCompletion(table)
	case "zsh":
		return zshCompletion(table)
	case "fish":
		return fishCompletion(table)
	default:
		return fmt.Sprintf("unsupported shell %q; supported shells are bash, zsh, fish", shell)
	}
}

func bashCompletion(table []CommandSpec) string {
	var b strings.Builder
	b.WriteString(`# bash completion for ggg (derived from the ggg command table)
_ggg_completions() {
  local cur commands
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
		fmt.Fprintf(&b, "      %s) COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") ); return ;;\n",
			strings.Join(commandWords(spec), "|"), strings.Join(flags, " "))
	}
	b.WriteString(`    esac
  fi
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
  fi
}
complete -F _ggg_completions ggg
`)
	return b.String()
}

func commandWords(spec CommandSpec) []string {
	words := []string{spec.Name}
	words = append(words, strings.Fields(spec.Usage)...)
	return words
}

func zshCompletion(table []CommandSpec) string {
	var b strings.Builder
	b.WriteString("#compdef ggg\n# zsh completion for ggg (derived from the ggg command table)\n\n_ggg() {\n  local -a commands\n  commands=(\n")
	for _, spec := range table {
		fmt.Fprintf(&b, "    '%s:%s'\n", spec.Name, zshEscape(spec.Summary))
	}
	b.WriteString("  )\n")
	b.WriteString(`  if (( CURRENT == 2 )); then
    _describe -t commands 'ggg command' commands
  else
    local -a flags
    flags=( `)
	flags := zshFlagsFor(table, "")
	b.WriteString(flags)
	b.WriteString(`)
    compadd -a flags
  fi
}
_ggg "$@"
`)
	return b.String()
}

func zshFlagsFor(table []CommandSpec, _ string) string {
	var all []string
	for _, spec := range table {
		for _, flag := range spec.Flags {
			all = append(all, "'--"+flag.Name+"["+zshEscape(flag.Help)+"]'")
		}
	}
	return strings.Join(all, " ")
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
