package main

import (
	"fmt"
	"os"
	"strings"
)

// completion.go — `pincher completion <shell>` prints a shell
// completion script (#1710 v0.92). pincher has 18 subcommands;
// without completion, `pincher <TAB>` offers nothing. The script
// completes the subcommand at position 1 — the 80% of the value —
// for bash, zsh, and fish.
//
// pincherSubcommands is the single source of truth for the completion
// scripts. TestCompletion_ListsEveryDispatchedSubcommand keeps it in
// lockstep with what main() actually dispatches.

var pincherSubcommands = []string{
	"index", "doctor", "rebuild-fts", "self-test", "stats", "bench",
	"update", "web", "init", "setup", "project", "supervised",
	"vacuum", "health-check", "hook-check", "hook-stats", "verify",
	"completion",
}

// runCompletionCLI implements `pincher completion [bash|zsh|fish]`.
func runCompletionCLI(args []string) {
	usage := func() {
		fmt.Fprintln(os.Stderr, "usage: pincher completion <bash|zsh|fish>")
		fmt.Fprintln(os.Stderr, "  Prints a shell completion script for pincher's subcommands.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  Install (add to your shell rc):")
		fmt.Fprintln(os.Stderr, "    bash:  eval \"$(pincher completion bash)\"")
		fmt.Fprintln(os.Stderr, "    zsh:   eval \"$(pincher completion zsh)\"")
		fmt.Fprintln(os.Stderr, "    fish:  pincher completion fish | source")
	}
	if len(args) != 1 {
		usage()
		os.Exit(1)
	}
	script, ok := completionScript(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "pincher completion: unsupported shell %q (want bash, zsh, or fish)\n\n", args[0])
		usage()
		os.Exit(1)
	}
	fmt.Print(script)
}

// completionScript returns the completion script for shell, generated
// from pincherSubcommands. ok is false for an unrecognized shell.
func completionScript(shell string) (script string, ok bool) {
	subs := strings.Join(pincherSubcommands, " ")
	switch shell {
	case "bash":
		return "# pincher bash completion — eval \"$(pincher completion bash)\"\n" +
			"_pincher_complete() {\n" +
			"  local cur=\"${COMP_WORDS[COMP_CWORD]}\"\n" +
			"  if [ \"$COMP_CWORD\" -eq 1 ]; then\n" +
			"    COMPREPLY=( $(compgen -W \"" + subs + "\" -- \"$cur\") )\n" +
			"  fi\n" +
			"}\n" +
			"complete -F _pincher_complete pincher\n", true
	case "zsh":
		return "#compdef pincher\n" +
			"# pincher zsh completion — eval \"$(pincher completion zsh)\"\n" +
			"_pincher() {\n" +
			"  if (( CURRENT == 2 )); then\n" +
			"    compadd -- " + subs + "\n" +
			"  fi\n" +
			"}\n" +
			"compdef _pincher pincher\n", true
	case "fish":
		var b strings.Builder
		b.WriteString("# pincher fish completion — pincher completion fish | source\n")
		for _, s := range pincherSubcommands {
			fmt.Fprintf(&b, "complete -c pincher -n __fish_use_subcommand -a %s\n", s)
		}
		return b.String(), true
	}
	return "", false
}
