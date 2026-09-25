// Command pleiades's forge subcommand family lives here: the authoring
// and migration tooling namespace, everything a user does before a
// runbook runs. This file only dispatches; every forge subcommand's real
// logic lives in its own file, and this file owns no business logic.
package main

import (
	"fmt"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clispec"
)

// forgeCommands maps each forge subcommand name to its handler, the same
// Command-pattern shape main.go's own commands map uses. Phases 31 through
// 37 populate it, each with one new file plus one entry here, never an
// edit to this file's own dispatch logic.
var forgeCommands = map[string]commandFunc{
	"new-device":     runForgeNewDevice,
	"new-collection": runForgeNewCollection,
	"new-plugin":     runForgeNewPlugin,
	"new-view":       runForgeNewView,
	"new-filter":     runForgeNewFilter,
	"new-external":   runForgeNewExternal,

	"migrate-playbook": runForgeMigratePlaybook,
}

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
	forge, _ := clispec.Find(clispec.Root, "forge")
	fmt.Fprint(os.Stderr, "usage: pleiades forge <command> [flags]\n\ncommands:\n")
	fmt.Fprint(os.Stderr, clispec.RenderList(forge.Subcommands))
	fmt.Fprintln(os.Stderr, "\nSee docs/ in the repository for the full Forge command surface.")
}
