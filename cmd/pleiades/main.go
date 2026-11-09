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
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/kubeapi"
)

// commandFunc is one subcommand's entry point. It receives the arguments
// after the subcommand name and returns an error to report on stderr.
type commandFunc func(args []string) error

// commands maps each subcommand name to its handler (Command pattern).
// Adding a subcommand means adding an entry here, never branching inside
// an existing one.
var commands = map[string]commandFunc{
	"init":                       runInit,
	"add-host":                   runAddHost,
	"set-host":                   runSetHost,
	"grant-capability":           runGrantCapability,
	"revoke-capability":          runRevokeCapability,
	"add-credential":             runAddCredential,
	"seal-key":                   runSealKey,
	"seal":                       runSeal,
	"add-registry-credential":    runAddRegistryCredential,
	"add-database-connection":    runAddDatabaseConnection,
	"list-database-connections":  runListDatabaseConnections,
	"remove-database-connection": runRemoveDatabaseConnection,
	"add-secret":                 runAddSecret,
	"list-secrets":               runListSecrets,
	"remove-secret":              runRemoveSecret,
	"remove-host":                runRemoveHost,
	"remove-credential":          runRemoveCredential,
	"import-containerlab":        runImportContainerlab,
	"import-cml":                 runImportCML,
	"import-gns3":                runImportGNS3,
	"import-kubeconfig":          runImportKubeconfig,
	"onboard":                    runOnboard,
	"trust-host":                 runTrustHost,
	"validate":                   runValidate,
	"run":                        runRunbook,
	"adhoc":                      runAdhoc,
	"rollback":                   runRollback,
	"facts":                      runFacts,
	"journal":                    runJournal,
	"forge":                      runForge,
	"import":                     runImport,
	"inventory":                  runInventory,
	"doc":                        runDoc,
	"collection":                 runCollection,
	"ai":                         runAI,
	"version":                    runVersion,
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
	// client-go's own log, from a Kubernetes task or onboarding a cluster,
	// through the CLI's masking (pkg/kubeapi.LogTo).
	kubeapi.LogTo(redact.Shared().Writer(os.Stderr), redact.Shared().Literals())
	// The command line runs every task in its own process, on the machine
	// whose engine onboarding found, so it alone may reach an engine
	// through this machine's own socket (containerengine.AllowLocalCarrier).
	containerengine.AllowLocalCarrier()
	args, err := applyGlobalOptions(args)
	if err != nil {
		printError(err)
		return 2
	}
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
		// Asked for help, a command that stops on its usage has answered:
		// the flag package printed its flags, the usage line is the
		// error's text, or both, and asking is not a failure.
		if asksHelp(args[1:]) && (errors.Is(err, flag.ErrHelp) || errors.Is(err, errMissingPositional) || strings.HasPrefix(err.Error(), "usage: ")) {
			if usage := helpUsage(err); usage != "" {
				fmt.Fprintln(os.Stderr, termsafe.Escape(usage))
			}
			return 0
		}
		// A nested dispatcher (e.g. forge.go) has already printed its own
		// unknown-command message and usage block; do not print a second,
		// redundant error line for it, and report the same exit code this
		// file's own unknown-command case above uses.
		if errors.Is(err, errUnknownCommand) {
			return 2
		}
		printError(err)
		var coded exitCoder
		if errors.As(err, &coded) {
			return coded.ExitCode()
		}
		return 1
	}
	return 0
}

// asksHelp reports whether args ask for help: -h, -help or --help, before
// any "--" that ends a command's flags.
func asksHelp(args []string) bool {
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "-h", "-help", "--help", "--h":
			return true
		}
	}
	return false
}

// printError writes a command's error to stderr. An error can carry text
// another system wrote (a device name a sync source sent, a remote
// command's message), so every character a terminal would act on is
// escaped visibly; plain text, tabs and newlines print as they are, so an
// error of several lines still reads as lines.
func printError(err error) {
	fmt.Fprintf(os.Stderr, "pleiades: %s\n", termsafe.Escape(err.Error()))
}

// helpUsage is the usage line a command's error carries when it was asked
// for help: the error's text without the missing positional or the flag
// package's help request it ends with. It is "" for the flag package's
// bare help request, which carries no usage line.
func helpUsage(err error) string {
	msg := err.Error()
	for _, asked := range []error{errMissingPositional, flag.ErrHelp} {
		msg = strings.TrimSuffix(msg, ": "+asked.Error())
	}
	if msg == flag.ErrHelp.Error() {
		return ""
	}
	return msg
}

// exitCoder is an error that carries the status the command ends with,
// for an outcome that is neither a success (0), a failure (1) nor a usage
// error (2). The one today is an incomplete check (exitIncomplete).
type exitCoder interface {
	ExitCode() int
}

func printUsage() {
	fmt.Fprint(os.Stderr, "usage: pleiades [--known-hosts FILE] <command> [flags]\n\ncommands:\n")
	fmt.Fprint(os.Stderr, clispec.RenderList(clispec.Root.Subcommands))
	fmt.Fprintln(os.Stderr, "\nCrawl tier: no server, no database, no broker. See docs/ in the repository\nfor the full documentation.")
}
