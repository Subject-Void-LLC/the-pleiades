// Package main: `pleiades facts`, which shows what this project's real runs
// observed about a host (Phase 41): each fact's current value with when it
// was first observed and last confirmed, or one fact's history, read from
// .pleiades/facts. A fact is what a method emitted (sdk.EmitFact) during a
// run that changed or read the host; a check keeps none.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// factsUsage is the command's usage.
const factsUsage = "usage: pleiades facts <host> [<key>] [--verify] [--dir DIR] [--json]"

// factsReport is `pleiades facts`' model: the host, and either its current
// facts or one key's history.
type factsReport struct {
	Host     string        `json:"host"`
	DeviceID string        `json:"device_id"`
	Key      string        `json:"key,omitempty"`
	Facts    []facts.Entry `json:"facts"`
}

// factChainReport is `pleiades facts <host> --verify`'s model: what checking
// the host's fact chain found.
type factChainReport struct {
	Host     string `json:"host"`
	DeviceID string `json:"device_id"`
	facts.Verdict
}

// errFactChainBroken ends a --verify that found a break, so a script can
// tell an intact chain from a broken one by the exit status alone.
var errFactChainBroken = errors.New("the host's fact chain is broken")

// runFacts is `pleiades facts <host> [<key>]`.
func runFacts(args []string) error {
	if len(args) == 0 || asksHelp(args[:1]) {
		return fmt.Errorf("%s: %w", factsUsage, errMissingPositional)
	}
	fs := flag.NewFlagSet("facts", flag.ContinueOnError)
	addInventoryFlag(fs)
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	verify := fs.Bool("verify", false, "check the host's fact chain instead of showing its facts")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(positionals) < 1 || len(positionals) > 2 || *verify && len(positionals) != 1 {
		return fmt.Errorf("%s: %w", factsUsage, errMissingPositional)
	}
	ctx := context.Background()
	repo := inv.NewFileRepository(inventoryFile(*dir), inv.NewItemFactory())
	item, err := repo.GetByName(ctx, positionals[0])
	if err != nil {
		return fmt.Errorf("host %q: %w", positionals[0], err)
	}
	if *verify {
		return verifyFacts(ctx, *dir, item.Name(), string(item.ID()), *asJSON)
	}
	rep := factsReport{Host: item.Name(), DeviceID: string(item.ID())}
	if len(positionals) == 2 {
		rep.Key = positionals[1]
	}
	// A read creates nothing: a project no run has stored a fact in yet has
	// no fact directory, and its hosts have no facts.
	store, err := facts.OpenFileStore(factsDir(*dir))
	switch {
	case errors.Is(err, facts.ErrNoStore):
	case err != nil:
		return err
	case rep.Key != "":
		rep.Facts, err = store.History(ctx, rep.DeviceID, rep.Key)
	default:
		rep.Facts, err = store.Current(ctx, rep.DeviceID)
	}
	if err != nil && !errors.Is(err, facts.ErrNoStore) {
		return err
	}
	if rep.Facts == nil {
		rep.Facts = []facts.Entry{}
	}
	// Every key as a person reads it (facts.ShownKey), in both views: the
	// reader's rule reads keys a new fact could not take, which a hand edit
	// or a build of another toolchain wrote, and one holding a character that
	// would not show as itself is named with that character escaped.
	rep.Key = facts.ShownKey(rep.Key)
	for i := range rep.Facts {
		rep.Facts[i].Key = facts.ShownKey(rep.Facts[i].Key)
	}
	if *asJSON {
		// Through the CLI's JSON writer, which escapes what a terminal would
		// act on: a fact value is the device's own text.
		return writeJSON(os.Stdout, rep)
	}
	printFacts(rep)
	return nil
}

// printFacts is the text view of rep: one line per value, its device text
// made safe for a terminal.
func printFacts(rep factsReport) {
	switch {
	case len(rep.Facts) == 0 && rep.Key != "":
		fmt.Printf("%s has no fact %s\n", termsafe.Escape(rep.Host), termsafe.EscapeLine(rep.Key))
		return
	case len(rep.Facts) == 0:
		fmt.Printf("%s has no facts: a real run of a method that gathers them (facts.gather, net.ios.facts) records them\n", termsafe.Escape(rep.Host))
		return
	}
	for _, e := range rep.Facts {
		value, err := json.Marshal(e.Value)
		if err != nil {
			value = []byte("?")
		}
		fmt.Printf("%s = %s  (%s; observed %s, last seen %s%s)%s\n", termsafe.EscapeLine(e.Key), termsafe.Escape(string(value)), e.Kind,
			e.ObservedAt.Local().Format(time.RFC3339), e.LastSeenAt.Local().Format(time.RFC3339), byRun(e.LastBy), clockNote(e))
	}
}

// byRun names the run that last confirmed a value, when it is known.
func byRun(p facts.Provenance) string {
	if p.Run == "" {
		return ""
	}
	return " by run " + termsafe.Escape(p.Run)
}

// clockNote says a history entry was placed by the clock rather than by
// run order.
func clockNote(e facts.Entry) string {
	if !e.OrderedByClock {
		return ""
	}
	return "  [placed after the previous value by the clock: the two runs' orders do not compare]"
}

// verifyFacts is --verify: the host's fact chain, checked.
func verifyFacts(ctx context.Context, dir, host, deviceID string, asJSON bool) error {
	rep := factChainReport{Host: host, DeviceID: deviceID}
	store, err := facts.OpenFileStore(factsDir(dir))
	switch {
	case errors.Is(err, facts.ErrNoStore):
	case err != nil:
		return err
	default:
		if rep.Verdict, err = store.Verify(ctx, deviceID); err != nil {
			return err
		}
		if rep.Break != nil {
			// The key as a person reads it, as the facts view names it.
			rep.Break.Key = facts.ShownKey(rep.Break.Key)
		}
	}
	if asJSON {
		if err := writeJSON(os.Stdout, rep); err != nil {
			return err
		}
	} else {
		printVerdict(rep)
	}
	if rep.Break != nil {
		return errFactChainBroken
	}
	return nil
}

// printVerdict is the text view of a chain check.
func printVerdict(rep factChainReport) {
	host := termsafe.Escape(rep.Host)
	if rep.Break != nil {
		// The reason quotes the start of a stored chain hash, which a hand
		// edit can fill with an escape sequence, so it is escaped too.
		fmt.Printf("%s: the fact chain breaks at value %d (%s): %s\n", host, rep.Break.Index, termsafe.EscapeLine(rep.Break.Key), termsafe.EscapeLine(rep.Break.Reason))
		return
	}
	fmt.Printf("%s: %d stored value(s) chained, the chain unbroken; head %s\n", host, rep.Values, rep.Head)
	fmt.Println("  tamper-evident only: values cut off the end leave a shorter chain that still verifies; keep the head elsewhere to notice that")
}
