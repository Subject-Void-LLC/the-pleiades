// This file is `controller facts inspect|discard|truncate`: looking at a
// device whose stored fact history no controller can serve, typically one a
// change of the facts key refused to re-key, and the two repairs that remove
// such a history (internal/facts/entstore's inspect.go has the design).
//
// It is an administrative command, run on the controller's host with the
// server's own key settings. It opens no NATS connection, starts no listener
// and joins no election, and unlike the other administrative commands it
// never creates, migrates or reclaims a database (facts_repair_open.go): it
// reads its flags and its facts keys first, then refuses a database that is
// missing, has no migration history, or is short of this build, and a master
// key the database's key registry does not record, warning of master key
// settings the registry says will leave some facts unopened. It reads
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
  controller facts inspect [--device <name> | --device-id <id>] [--all] [--json] [--show-fact-names]
  controller facts discard (--device <name> | --device-id <id>) [--confirm]
                           [--discard-verified-history] [--discard-unverifiable-history]
  controller facts truncate (--device <name> | --device-id <id>) [--confirm]
                            [--truncate-unverifiable-history]

--device takes the device's inventory name; --device-id takes its ID, which
is how the controller's log names a device.

inspect reads, and changes nothing. With a device it reports that device;
without, every device holding stored facts whose history cannot be served
(every one with --all). It names rows, key identities, times and failure
kinds, never a fact's name or value; --show-fact-names names the fact a
failing row holds, when its row opens. Exit 0 when nothing needs repair, 3
when a device does.

discard removes the device's stored facts, coverages and observations, so
its next gather starts a new history under the current key. It refuses a
history that verifies unless --discard-verified-history is given, and one
no key given can verify (a row does not open, or its rows name another
facts key) unless --discard-unverifiable-history is given.

truncate keeps the device's history before the gather that wrote the last
value that verifies, removes that gather and everything after it, removes
a failing volatile value alone, and re-keys what it keeps under the current
key when it was under the previous one. It is not offered when it would
keep nothing, and it refuses a history with a row that does not open unless
--truncate-unverifiable-history is given.

A row that does not open is never by itself proof of damage: a row sealed
under a master key, or a key version, this command was not given looks just
the same. Check the master key settings before repairing such a history.

Both repairs print what they would do and change nothing without --confirm,
though the preview holds the device as the repair does. What they remove
cannot be recovered except from a backup taken before, and each is recorded
in the activity stream.

Run it with the server's settings: the same database (DB_DSN or DB_PATH),
MASTER_ENCRYPTION_KEY and MASTER_ENCRYPTION_KEY_PREVIOUS with their
versions, and PLEIADES_FACTS_DIGEST_KEY, PLEIADES_FACTS_DIGEST_KEY_PREVIOUS,
or both. It refuses a database it would have to create or upgrade, and a
master key the database's key registry does not record, and it warns when
the registry records a newer master key, or another version for a key given.
`

// isFactsCommand reports whether args ask for this file's subcommand.
func isFactsCommand(args []string) bool {
	return len(args) > 0 && args[0] == factsCommand
}

// factsRequest is a parsed command line.
type factsRequest struct {
	action, device, deviceID                                                   string
	all, asJSON, showNames, confirm, discardGood, discardUnknown, truncUnknown bool
}

// parseFactsArgs reads args (the subcommand name first). A request for help
// is flag.ErrHelp, after the usage was written to stderr.
func parseFactsArgs(args []string, stderr io.Writer) (factsRequest, error) {
	if len(args) < 2 || (args[1] != "inspect" && args[1] != "discard" && args[1] != "truncate") {
		fmt.Fprint(stderr, factsUsage)
		if len(args) >= 2 && (args[1] == "-h" || args[1] == "--help" || args[1] == "help") {
			return factsRequest{}, flag.ErrHelp
		}
		return factsRequest{}, errors.New("name one of inspect, discard or truncate")
	}
	req := factsRequest{action: args[1]}
	fs := flag.NewFlagSet("controller facts "+req.action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, factsUsage) }
	fs.StringVar(&req.device, "device", "", "the device's inventory name")
	fs.StringVar(&req.deviceID, "device-id", "", "the device's ID, as the controller's log names it")
	if req.action == "inspect" {
		fs.BoolVar(&req.all, "all", false, "report every device read, healthy ones too")
		fs.BoolVar(&req.asJSON, "json", false, "print the report as JSON")
		fs.BoolVar(&req.showNames, "show-fact-names", false, "name the fact a failing row holds, when its row opens")
	} else {
		fs.BoolVar(&req.confirm, "confirm", false, "do the repair; without it, it is shown and nothing changes")
	}
	if req.action == "discard" {
		fs.BoolVar(&req.discardGood, "discard-verified-history", false, "discard even a history that verifies")
		fs.BoolVar(&req.discardUnknown, "discard-unverifiable-history", false, "discard even a history no key given can verify")
	}
	if req.action == "truncate" {
		fs.BoolVar(&req.truncUnknown, "truncate-unverifiable-history", false, "truncate even a history with a row that does not open")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return factsRequest{}, err
	}
	named := req.device != "" || req.deviceID != ""
	switch {
	case fs.NArg() != 0:
		return factsRequest{}, fmt.Errorf("unexpected argument %s", termsafe.Quote(fs.Arg(0)))
	case req.device != "" && req.deviceID != "":
		return factsRequest{}, errors.New("name the device by --device or by --device-id, not both")
	case req.action != "inspect" && !named:
		return factsRequest{}, fmt.Errorf("%s needs --device, the device's inventory name, or --device-id, its ID", req.action)
	case req.all && named:
		return factsRequest{}, errors.New("--all reports a fleet inspection; it does not go with a device")
	}
	return req, nil
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
	store, cleanup, err := openFactStore(ctx, current, previous, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "controller facts: %s\n", termsafe.Escape(err.Error()))
		return factsExitFailed
	}
	defer cleanup()
	if req.deviceID != "" {
		if req.device, err = store.NameOf(ctx, req.deviceID); err != nil {
			fmt.Fprintf(stderr, "controller facts %s: %s\n", req.action, termsafe.Escape(err.Error()))
			return factsExitFailed
		}
	}
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
	opts := entstore.RepairOptions{Confirm: req.confirm, DiscardVerified: req.discardGood, DiscardUnverifiable: req.discardUnknown,
		TruncateUnverifiable: req.truncUnknown, Actor: factsActor}
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
