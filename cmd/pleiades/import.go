// Command pleiades's import subcommand family lives here: reading what
// another automation platform exported and reporting what this one would
// do with it. This file only dispatches; every import subcommand's real
// logic lives in its own file.
package main

import (
	"fmt"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clispec"
)

// importCommands maps each import subcommand name to its handler, the same
// Command-pattern shape main.go's own commands map and forge.go's use.
var importCommands = map[string]commandFunc{
	"awx-credential-types": runImportAWXCredentialTypes,
}

// runImport is cmd/pleiades's import subcommand dispatcher. It mirrors
// runForge exactly, reusing main.go's commandFunc rather than defining a
// second one.
func runImport(args []string) error {
	if len(args) == 0 {
		printImportUsage()
		return errUnknownCommand
	}

	switch args[0] {
	case "-h", "--help", "help":
		printImportUsage()
		return nil
	}

	cmd, ok := importCommands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "pleiades import: unknown command %q\n", args[0])
		printImportUsage()
		return errUnknownCommand
	}

	return cmd(args[1:])
}

// printImportUsage prints the import namespace's own usage block to
// stderr, the same convention printUsage uses for the top-level command.
func printImportUsage() {
	imp, _ := clispec.Find(clispec.Root, "import")
	fmt.Fprint(os.Stderr, "usage: pleiades import <command> [flags]\n\ncommands:\n")
	fmt.Fprint(os.Stderr, clispec.RenderList(imp.Subcommands))
	fmt.Fprintln(os.Stderr, "\nSee docs/ in the repository for the migration guidance these commands support.")
}
