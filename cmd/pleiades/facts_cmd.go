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
	"path/filepath"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// factsUsage is the command's usage.
const factsUsage = "usage: pleiades facts <host> [<key>] [--dir DIR] [--json]"

// factsReport is `pleiades facts`' model: the host, and either its current
// facts or one key's history.
type factsReport struct {
	Host     string        `json:"host"`
	DeviceID string        `json:"device_id"`
	Key      string        `json:"key,omitempty"`
	Facts    []facts.Entry `json:"facts"`
}

// runFacts is `pleiades facts <host> [<key>]`.
func runFacts(args []string) error {
	if len(args) == 0 || asksHelp(args[:1]) {
		return fmt.Errorf("%s: %w", factsUsage, errMissingPositional)
	}
	positionals, rest := splitPositionals(args, map[string]bool{"json": true})
	fs := flag.NewFlagSet("facts", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if len(positionals) < 1 || len(positionals) > 2 {
		return fmt.Errorf("%s: %w", factsUsage, errMissingPositional)
	}
	ctx := context.Background()
	repo := inv.NewFileRepository(filepath.Join(*dir, inv.DefaultInventoryFilename), inv.NewItemFactory())
	item, err := repo.GetByName(ctx, positionals[0])
	if err != nil {
		return fmt.Errorf("host %q: %w", positionals[0], err)
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
		fmt.Printf("%s has no fact %s\n", termsafe.Escape(rep.Host), termsafe.Escape(rep.Key))
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
		fmt.Printf("%s = %s  (observed %s, last seen %s)\n", termsafe.Escape(e.Key), termsafe.Escape(string(value)),
			e.ObservedAt.Local().Format(time.RFC3339), e.LastSeenAt.Local().Format(time.RFC3339))
	}
}
