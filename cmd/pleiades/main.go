// Command pleiades is the Walk-tier composition root (PLAN.md Section 7).
// It links the inventory, engine, and validation packages directly and
// does not dial a Controller: at Walk there is no Controller to dial.
// This file and its subcommand siblings only parse arguments and delegate;
// every subcommand's real logic lives in the internal package that already
// owns it.
package main

import (
	"fmt"
	"os"
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
	"validate":       runValidate,
	"run":            runRunbook,
}

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
		fmt.Fprintf(os.Stderr, "pleiades: %v\n", err)
		return 1
	}
	return 0
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `usage: pleiades <command> [flags]

commands:
  init            scaffold a new project in the current directory
  add-host        add a host to the static inventory
  add-credential  store an encrypted SSH credential for a device
  validate        check a runbook against the inventory
  run             build, validate, and print the plan for a runbook

Walk tier: no server, no database, no broker. See PLAN.md Section 7.`)
}
