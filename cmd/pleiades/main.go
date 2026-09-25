// Command pleiades is the Crawl-tier composition root: the offline,
// single-binary entry point with no server, database, or broker. It
// links the inventory, engine, and validation packages directly and
// does not dial a Controller: at Crawl there is no Controller to dial.
// This file and its subcommand siblings only parse arguments and delegate;
// every subcommand's real logic lives in the internal package that already
// owns it.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clispec"
)

// commandFunc is one subcommand's entry point. It receives the arguments
// after the subcommand name and returns an error to report on stderr.
type commandFunc func(args []string) error

// commands maps each subcommand name to its handler (Command pattern).
// Adding a subcommand means adding an entry here, never branching inside
// an existing one.
var commands = map[string]commandFunc{
	"init":           runInit,
	"add-host":       runAddHost,
	"add-credential": runAddCredential,
	"onboard":        runOnboard,
	"validate":       runValidate,
	"run":            runRunbook,
	"forge":          runForge,
	"import":         runImport,
	"inventory":      runInventory,
	"doc":            runDoc,
	"collection":     runCollection,
	"version":        runVersion,
}

// errUnknownCommand signals that a dispatch table (this file's own
// commands, or a nested one like forge.go's forgeCommands) found no entry
// for the requested name. A nested dispatcher that returns it gets mapped
// to the same exit code as this file's own top-level unknown-command
// case, so an unrecognized name reports the same shape everywhere it can
// occur, not just at the outermost dispatch.
var errUnknownCommand = errors.New("unknown command")

func main() {
	os.Exit(run(os.Args[1:]))
}

// run contains everything main would otherwise do inline, so tests can
// drive it without calling os.Exit.
func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage()
		return 0
	}

	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "pleiades: unknown command %q\n", args[0])
		printUsage()
		return 2
	}

	if err := cmd(args[1:]); err != nil {
		// A nested dispatcher (e.g. forge.go) has already printed its own
		// unknown-command message and usage block; do not print a second,
		// redundant error line for it, and report the same exit code this
		// file's own unknown-command case above uses.
		if errors.Is(err, errUnknownCommand) {
			return 2
		}
		fmt.Fprintf(os.Stderr, "pleiades: %v\n", err)
		var coded exitCoder
		if errors.As(err, &coded) {
			return coded.ExitCode()
		}
		return 1
	}
	return 0
}

// exitCoder is an error that carries the status the command ends with,
// for an outcome that is neither a success (0), a failure (1) nor a usage
// error (2). The one today is an incomplete check (exitIncomplete).
type exitCoder interface {
	ExitCode() int
}

func printUsage() {
	fmt.Fprint(os.Stderr, "usage: pleiades <command> [flags]\n\ncommands:\n")
	fmt.Fprint(os.Stderr, clispec.RenderList(clispec.Root.Subcommands))
	fmt.Fprintln(os.Stderr, "\nCrawl tier: no server, no database, no broker. See docs/ in the repository\nfor the full documentation.")
}
