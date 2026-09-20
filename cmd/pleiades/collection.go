// Package main: the `pleiades collection` commands, which approve, revoke and
// list the builds of external Collection programs allowed to run.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/loader"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// collectionCommands is the `pleiades collection` family: the approval
// list that decides which build of which external Collection program may
// run (internal/loader's ApprovalFile).
var collectionCommands = map[string]commandFunc{
	"approve": runCollectionApprove,
	"revoke":  runCollectionRevoke,
	"list":    runCollectionList,
}

// approvalPrompt is where runCollectionApprove reads its answer. A test
// replaces it.
var approvalPrompt io.Reader = os.Stdin

// runCollection dispatches the collection subcommand family.
func runCollection(args []string) error {
	if len(args) == 0 {
		printCollectionUsage()
		return errUnknownCommand
	}
	switch args[0] {
	case "-h", "--help", "help":
		printCollectionUsage()
		return nil
	}
	cmd, ok := collectionCommands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "pleiades collection: unknown command %q\n", args[0])
		printCollectionUsage()
		return errUnknownCommand
	}
	return cmd(args[1:])
}

// printCollectionUsage prints the collection namespace's usage block.
func printCollectionUsage() {
	spec, _ := clispec.Find(clispec.Root, "collection")
	fmt.Fprint(os.Stderr, "usage: pleiades collection <command> [flags]\n\ncommands:\n")
	fmt.Fprint(os.Stderr, clispec.RenderList(spec.Subcommands))
}

// collectionsDir is the directory the collection commands act on: the
// one PLEIADES_COLLECTIONS_DIR names, the same one run, validate and doc
// load from, so an approval always lands where it will be read.
func collectionsDir() (string, error) {
	dir := os.Getenv(collectionsDirEnv)
	if dir == "" {
		return "", fmt.Errorf("%s is not set: name the directory that holds your external Collection programs", collectionsDirEnv)
	}
	return dir, nil
}

// runCollectionApprove approves a build of one program. Without --digest
// it first runs the program's describe, confined, and shows its digest
// and every method it says it provides, then asks, unless --yes. With
// --digest it records that build without running anything, for an image
// build that installs the program later, or a rolling upgrade that
// approves the next build before it arrives.
func runCollectionApprove(args []string) error {
	program, rest, err := splitPositional(args, map[string]bool{"yes": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades collection approve <program> [--digest sha256:<hex>] [--yes]: %w", err)
	}
	fs := flag.NewFlagSet("collection approve", flag.ContinueOnError)
	digest := fs.String("digest", "", "approve this build (sha256:<hex>) without inspecting the program")
	yes := fs.Bool("yes", false, "approve without asking")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	dir, err := collectionsDir()
	if err != nil {
		return err
	}

	if *digest == "" {
		found, desc, err := loader.Inspect(context.Background(), dir, program, loader.Options{
			EngineVersion: version,
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, redact.Shared().HandlerOptions(slog.LevelWarn))),
		})
		if err != nil {
			return err
		}
		printDescription(program, found, desc)
		if !*yes && !confirm("Approve this build to run, with the credential each task is given? [y/N] ") {
			return errors.New("not approved")
		}
		*digest = found
	}

	if err := loader.Approve(dir, loader.Approval{
		Program:    program,
		Digest:     *digest,
		ApprovedBy: approver(),
		ApprovedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return err
	}
	fmt.Printf("approved %s (%s)\n", program, *digest)
	return nil
}

// printDescription shows what a program says it provides, one method at
// a time: its name, what it needs, and whether it can be checked.
//
// Everything here except the digest is the program's own text, shown to a
// person at the moment they decide whether to trust it, and before the
// loader has validated any of it. So every field is escaped onto one line
// (termsafe.EscapeLine): an escape sequence, a carriage return or a
// newline in a summary could otherwise draw a fake method list, or a fake
// "approved" line, over the real one.
func printDescription(program, digest string, desc external.Description) {
	fmt.Printf("program: %s\ndigest:  %s\nmethods:\n", termsafe.EscapeLine(program), digest)
	for _, m := range desc.Methods {
		fmt.Printf("  %s\n", termsafe.EscapeLine(m.Name))
		if m.Manifest.Doc.Summary != "" {
			fmt.Printf("    %s\n", termsafe.EscapeLine(m.Manifest.Doc.Summary))
		}
		caps := make([]string, len(m.Manifest.RequiredCapabilities))
		for i, c := range m.Manifest.RequiredCapabilities {
			caps[i] = termsafe.EscapeLine(string(c))
		}
		transports := make([]string, len(m.Manifest.SupportedTransports))
		for i, tr := range m.Manifest.SupportedTransports {
			transports[i] = termsafe.EscapeLine(tr)
		}
		fmt.Printf("    needs:      %s\n", orNone(strings.Join(caps, ", ")))
		fmt.Printf("    transports: %s\n", orNone(strings.Join(transports, ", ")))
		check := "not supported"
		if m.Manifest.SupportsCheck {
			check = "supported"
		}
		fmt.Printf("    check mode: %s\n", check)
		if !m.Manifest.Reversibility.Reversible {
			fmt.Printf("    reversible: no (%s)\n", termsafe.EscapeLine(m.Manifest.Reversibility.Notes))
		} else {
			fmt.Println("    reversible: yes")
		}
	}
}

// orNone is s, or "none" when s is empty.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// confirm prints question and reports whether the answer read from
// approvalPrompt is yes. No answer is no.
func confirm(question string) bool {
	fmt.Print(question)
	answer, _ := bufio.NewReader(approvalPrompt).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// approver is the local account recorded as having approved a build.
func approver() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	return fmt.Sprintf("uid %d", os.Getuid())
}

// runCollectionRevoke withdraws a program's approvals: every build, or
// the one --digest names.
func runCollectionRevoke(args []string) error {
	program, rest, err := splitPositional(args, nil)
	if err != nil {
		return fmt.Errorf("usage: pleiades collection revoke <program> [--digest sha256:<hex>]: %w", err)
	}
	fs := flag.NewFlagSet("collection revoke", flag.ContinueOnError)
	digest := fs.String("digest", "", "withdraw only this build's approval")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	dir, err := collectionsDir()
	if err != nil {
		return err
	}
	n, err := loader.Revoke(dir, program, *digest)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no approval of %s matched, so nothing was revoked", program)
	}
	fmt.Printf("revoked %d approval(s) of %s\n", n, program)
	return nil
}

// runCollectionList prints every approved build.
func runCollectionList(args []string) error {
	fs := flag.NewFlagSet("collection list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir, err := collectionsDir()
	if err != nil {
		return err
	}
	approvals, err := loader.ReadApprovals(dir)
	if err != nil {
		return err
	}
	if len(approvals) == 0 {
		fmt.Println("no approved builds")
		return nil
	}
	for _, a := range approvals {
		// The account and time are whatever the list holds, and anyone who
		// can write the list can write anything there.
		fmt.Printf("%s  %s  approved by %s at %s\n", a.Program, a.Digest, termsafe.EscapeLine(a.ApprovedBy), termsafe.EscapeLine(a.ApprovedAt))
	}
	return nil
}
