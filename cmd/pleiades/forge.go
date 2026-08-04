// Command pleiades's forge subcommand family lives here. The Forge of
// Hephaestus (docs/hephaestus.md) is the authoring and migration tooling
// namespace: everything a user does before a runbook runs. This file only
// dispatches; every forge subcommand's real logic lives in its own file
// and, per Phase 30 (.SPECIFICATION/IMPLEMENTATION.md Part VII), owns no
// business logic here.
package main

import (
	"fmt"
	"os"
)

// forgeCommands maps each forge subcommand name to its handler, the same
// Command-pattern shape main.go's own commands map uses. It is empty
// today: Phases 31 through 37 populate it, each with one new file plus
// one entry here, never an edit to this file's own dispatch logic.
var forgeCommands = map[string]commandFunc{}

// runForge is cmd/pleiades's forge subcommand dispatcher. It mirrors
// run()'s own structure exactly (help interception, then a map lookup,
// then either dispatch or a same-shaped unknown-command failure), reusing
// main.go's commandFunc type rather than defining a second one.
func runForge(args []string) error {
	if len(args) == 0 {
		printForgeUsage()
		return errUnknownCommand
	}

	switch args[0] {
	case "-h", "--help", "help":
		printForgeUsage()
		return nil
	}

	cmd, ok := forgeCommands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "pleiades forge: unknown command %q\n", args[0])
		printForgeUsage()
		return errUnknownCommand
	}

	return cmd(args[1:])
}

// printForgeUsage prints the forge namespace's own usage block to stderr,
// the same convention printUsage() uses for the top-level command.
func printForgeUsage() {
	fmt.Fprintln(os.Stderr, `usage: pleiades forge <command> [flags]

commands:
  (none registered yet; see docs/hephaestus.md for the planned Forge
  command surface, and .SPECIFICATION/IMPLEMENTATION.md Part VII for the
  phases that populate this namespace)`)
}
