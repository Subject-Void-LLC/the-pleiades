// This file is `controller facts inspect|discard|truncate`: looking at a
// device whose stored fact history no controller can serve, typically one a
// change of the facts key refused to re-key, and the two repairs that remove
// such a history (internal/facts/entstore's inspect.go has the design).
//
// It is an administrative command, run on the controller's host, and opens
// the database the way the others do (openAdminDepsWith: no NATS, no
// listener, no election, the database never upgraded), with one difference
// in order: its flags and its facts keys are read first, so a usage error or
// an unusable key stops it before the database is opened. It reads
// PLEIADES_FACTS_DIGEST_KEY, PLEIADES_FACTS_DIGEST_KEY_PREVIOUS, or both,
// unlike the server, which refuses a previous key with no current one: a
// history can be inspected under the old key alone. Its exit code is its
// verdict, as migrate --plan's is.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// factsCommand is the subcommand this file implements.
const factsCommand = "facts"

// factsActor is who the activity stream records a repair as: somebody with
// shell access to the controller, as every administrative command is.
const factsActor = "controller-cli"

// Exit codes of `controller facts`.
const (
	// factsExitOK: every device inspected can be served, or a repair was
	// done or shown.
	factsExitOK = 0
	// factsExitFailed: the command could not run, or a repair was refused.
	factsExitFailed = 1
	// factsExitUsage: the command line was wrong.
	factsExitUsage = 2
	// factsExitNeedsRepair: an inspection found a device that needs one.
	factsExitNeedsRepair = 3
)

// factsUsage is the command's help.
const factsUsage = `Usage:
  controller facts inspect [--device <name>] [--all] [--json] [--show-fact-names]
  controller facts discard --device <name> [--confirm] [--discard-verified-history]
  controller facts truncate --device <name> [--confirm]

inspect reads, and changes nothing. With --device it reports that device;
without, every device holding stored facts whose history cannot be served
(every one with --all). It names rows, key identities, times and failure
kinds, never a fact's name or value; --show-fact-names names the fact a
failing row holds, when its row opens. Exit 0 when nothing needs repair, 3
when a device does.

discard removes the device's stored facts, coverages and observations, so
its next gather starts a new history under the current key. It refuses a
device whose history verifies unless --discard-verified-history is given.

truncate keeps the device's history before the gather that wrote its first
failing value, removes the rest, and re-keys what it keeps under the current
key when it was under the previous one.

Both repairs print what they would do and change nothing without --confirm.
What they remove cannot be recovered except from a backup taken before, and
each is recorded in the activity stream. They read the same database and
MASTER_ENCRYPTION_KEY the server does, and PLEIADES_FACTS_DIGEST_KEY,
PLEIADES_FACTS_DIGEST_KEY_PREVIOUS, or both.
`

// isFactsCommand reports whether args ask for this file's subcommand.
func isFactsCommand(args []string) bool {
	return len(args) > 0 && args[0] == factsCommand
}

// factsRequest is a parsed command line.
type factsRequest struct {
	action                                       string
	device                                       string
	all, asJSON, showNames, confirm, discardGood bool
}

// parseFactsArgs reads args (the subcommand name first). A request for help
// is flag.ErrHelp, after the usage was written to stderr.
func parseFactsArgs(args []string, stderr io.Writer) (factsRequest, error) {
	if len(args) < 2 || (args[1] != "inspect" && args[1] != "discard" && args[1] != "truncate") {
		if len(args) >= 2 && (args[1] == "-h" || args[1] == "--help" || args[1] == "help") {
			fmt.Fprint(stderr, factsUsage)
			return factsRequest{}, flag.ErrHelp
		}
		fmt.Fprint(stderr, factsUsage)
		return factsRequest{}, errors.New("name one of inspect, discard or truncate")
	}
	req := factsRequest{action: args[1]}
	fs := flag.NewFlagSet("controller facts "+req.action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, factsUsage) }
	fs.StringVar(&req.device, "device", "", "the device's inventory name")
	if req.action == "inspect" {
		fs.BoolVar(&req.all, "all", false, "report every device read, healthy ones too")
		fs.BoolVar(&req.asJSON, "json", false, "print the report as JSON")
		fs.BoolVar(&req.showNames, "show-fact-names", false, "name the fact a failing row holds, when its row opens")
	} else {
		fs.BoolVar(&req.confirm, "confirm", false, "do the repair; without it, it is shown and nothing changes")
	}
	if req.action == "discard" {
		fs.BoolVar(&req.discardGood, "discard-verified-history", false, "discard even a history that verifies")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return factsRequest{}, err
	}
	switch {
	case fs.NArg() != 0:
		return factsRequest{}, fmt.Errorf("unexpected argument %s", termsafe.Quote(fs.Arg(0)))
	case req.action != "inspect" && req.device == "":
		return factsRequest{}, fmt.Errorf("%s needs --device, the device's inventory name", req.action)
	case req.all && req.device != "":
		return factsRequest{}, errors.New("--all reports a fleet inspection; it does not go with --device")
	}
	return req, nil
}

// loadFactsCommandKeys reads the facts keys the command checks with: the
// current, the previous, or both, at least one. A key that does not decode,
// or a previous key that is the current one, is refused by name, as the
// server refuses it.
func loadFactsCommandKeys() (*facts.Keys, *facts.Keys, error) {
	var currentSecret []byte
	var current, previous *facts.Keys
	if os.Getenv(factDigestKeyEnv) != "" {
		secret, err := decodeEnvelopeKey(factDigestKeyEnv)
		if err != nil {
			return nil, nil, err
		}
		if current, err = entstore.AtRestKeys(secret); err != nil {
			return nil, nil, err
		}
		currentSecret = secret
	}
	previousSecret, err := previousFactSecret(currentSecret)
	if err != nil {
		return nil, nil, err
	}
	if previousSecret != nil {
		if previous, err = entstore.AtRestKeys(previousSecret); err != nil {
			return nil, nil, err
		}
	}
	if current == nil && previous == nil {
		return nil, nil, fmt.Errorf("set %s, %s, or both: stored facts are checked under the facts keys", factDigestKeyEnv, factDigestKeyPreviousEnv)
	}
	return current, previous, nil
}

// openFactStore opens the database and returns the fact store the command
// works through, holding current and previous, and its cleanup.
func openFactStore(ctx context.Context, current, previous *facts.Keys) (*entstore.Store, func(), error) {
	dsn, err := resolveDatabaseDSN()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve database configuration: %w", err)
	}
	envelopeSvc, err := loadEnvelopeService()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to init envelope encryption: %w", err)
	}
	deps, cleanup, err := openAdminDepsWith(ctx, dsn, envelopeSvc)
	if err != nil {
		return nil, nil, err
	}
	return entstore.New(deps.client, current, envelopeSvc, entstore.WithPreviousKeys(previous)), cleanup, nil
}

// runFacts runs `controller facts ...` and returns its exit code.
func runFacts(args []string, stdout, stderr io.Writer) int {
	req, err := parseFactsArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return factsExitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "controller facts: %v\n", err)
		return factsExitUsage
	}
	// Before the database is opened: a key that cannot be used stops the
	// command having changed nothing.
	current, previous, err := loadFactsCommandKeys()
	if err != nil {
		fmt.Fprintf(stderr, "controller facts: %v\n", err)
		return factsExitFailed
	}
	ctx := context.Background()
	store, cleanup, err := openFactStore(ctx, current, previous)
	if err != nil {
		fmt.Fprintf(stderr, "controller facts: %s\n", termsafe.Escape(err.Error()))
		return factsExitFailed
	}
	defer cleanup()
	if req.action == "inspect" {
		return runFactsInspect(ctx, store, req, stdout, stderr)
	}
	return runFactsRepair(ctx, store, req, stdout, stderr)
}

// runFactsInspect inspects one device or the fleet and prints the report.
func runFactsInspect(ctx context.Context, store *entstore.Store, req factsRequest, stdout, stderr io.Writer) int {
	opts := entstore.InspectOptions{ShowFactNames: req.showNames, All: req.all}
	var report factsreport.Report
	if req.device != "" {
		found, err := store.InspectDevice(ctx, req.device, opts)
		if err != nil {
			fmt.Fprintf(stderr, "controller facts inspect: %s\n", termsafe.Escape(err.Error()))
			return factsExitFailed
		}
		report = factsreport.Report{SchemaVersion: factsreport.Version, Keys: store.Keys(), Scope: factsreport.ScopeDevice,
			Scanned: 1, Devices: []factsreport.Device{found}}
		if found.State.NeedsRepair() {
			report.NeedsRepair = 1
		}
	} else {
		var err error
		if report, err = store.InspectAll(ctx, opts); err != nil {
			fmt.Fprintf(stderr, "controller facts inspect: %s\n", termsafe.Escape(err.Error()))
			return factsExitFailed
		}
	}
	if req.asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "controller facts inspect: writing the report: %v\n", err)
			return factsExitFailed
		}
	} else {
		writeInspectText(stdout, report)
	}
	if report.NeedsRepair > 0 {
		return factsExitNeedsRepair
	}
	return factsExitOK
}

// runFactsRepair runs a discard or a truncate and prints what it did, or
// without --confirm what it would do.
func runFactsRepair(ctx context.Context, store *entstore.Store, req factsRequest, stdout, stderr io.Writer) int {
	opts := entstore.RepairOptions{Confirm: req.confirm, DiscardVerified: req.discardGood, Actor: factsActor}
	repair := store.Discard
	if req.action == "truncate" {
		repair = store.Truncate
	}
	done, err := repair(ctx, req.device, opts)
	if err != nil {
		fmt.Fprintf(stderr, "controller facts %s: %s\n", req.action, termsafe.Escape(err.Error()))
		return factsExitFailed
	}
	writeRepairText(stdout, done)
	return factsExitOK
}

// runFactsMain is the route's entry point, bound to the process's streams.
func runFactsMain(args []string) int {
	return runFacts(args, os.Stdout, os.Stderr)
}
