// Package main: `pleiades facts baseline` and `pleiades facts drift` (Phase
// 41d). A baseline is a named, immutable set of a host's stable facts, taken
// from its own current facts or another host's (--from, a golden host), and
// sealed under the project's master key like the facts themselves. Drift is
// each host's current stable facts against its baseline, computed on read.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The subcommands' usage lines.
const (
	factsBaselineUsage = "usage: pleiades facts baseline <hosts> --name NAME --reason TEXT [--from HOST] [--keys KEYS] [--dir DIR]"
	factsDriftUsage    = "usage: pleiades facts drift [<hosts>] [--baseline NAME] [--dir DIR] [--json]"
)

// exitDrifted is the status of a drift that found a host drifted: neither
// success (0) nor failure (1), the same "neither" an incomplete check ends
// with, so a pipeline can tell a difference found from a command that failed.
const exitDrifted = 3

// driftedError ends a drift that found a drifted host, carrying exitDrifted.
type driftedError struct{ msg string }

// Error implements error.
func (e *driftedError) Error() string { return e.msg }

// ExitCode implements exitCoder.
func (e *driftedError) ExitCode() int { return exitDrifted }

// runFactsBaseline is `pleiades facts baseline <hosts>`.
func runFactsBaseline(args []string) error {
	if len(args) == 0 || asksHelp(args[:1]) {
		return fmt.Errorf("%s: %w", factsBaselineUsage, errMissingPositional)
	}
	fs := flag.NewFlagSet("facts baseline", flag.ContinueOnError)
	addInventoryFlag(fs)
	dir := fs.String("dir", ".", "project directory")
	name := fs.String("name", "", "the baseline's name, unique per host (letters, digits, '.', '_' and '-')")
	reason := fs.String("reason", "", "why the baseline is taken, recorded with it")
	from := fs.String("from", "", "a host to take the facts from (a golden host), hashed again for each host")
	keysFlag := fs.String("keys", "", "the facts to take, comma-separated: a key, or a prefix ending in * (package.*); every stable fact when unset")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("%s: %w", factsBaselineUsage, errMissingPositional)
	}
	if *name == "" || strings.TrimSpace(*reason) == "" {
		return errors.New("facts baseline needs --name and --reason: a baseline is recorded with why it was taken")
	}
	sel, err := facts.ParseKeySelector(*keysFlag)
	if err != nil {
		return err
	}
	// Refused before anything is read or written: a baseline is sealed under
	// the master key, and a project with none has no facts to take one of.
	keys := factKeysToRead(*dir)
	if err := keys.Ready(); err != nil {
		return err
	}
	items, err := loadInventory(*dir)
	if err != nil {
		return err
	}
	hosts, err := resolveFactHosts(items, positionals[0])
	if err != nil {
		return err
	}
	var source pkginventory.InventoryItem
	if *from != "" {
		if source = itemNamed(items, *from); source == nil {
			return fmt.Errorf("--from %q is not a host in the inventory", *from)
		}
		for _, h := range hosts {
			if h.ID() == source.ID() {
				return fmt.Errorf("--from %q is also a host the baseline is taken of: a host cannot be its own golden source; leave it out of the hosts, or drop --from to take its own facts", source.Name())
			}
		}
	}
	store, err := facts.OpenFileStore(factsDir(*dir), keys)
	if errors.Is(err, facts.ErrNoStore) {
		return errors.New("no host's facts have been gathered in this project yet: run facts.gather first")
	}
	if err != nil {
		return err
	}
	ctx := context.Background()
	if source != nil {
		if current, err := store.Current(ctx, string(source.ID())); err != nil {
			return fmt.Errorf("--from %q: %w", source.Name(), err)
		} else if len(current) == 0 {
			return fmt.Errorf("--from %q: its facts were never gathered: run facts.gather on it first", source.Name())
		}
	}
	req := facts.BaselineRequest{Name: *name, Reason: *reason, By: osUser(), Keys: sel, At: time.Now()}
	if source != nil {
		req.From, req.FromName = string(source.ID()), source.Name()
	}
	// Every host's baseline is made, and every refusal storing it could make
	// is made, before any is written, so one host's refusal (a name it
	// already has, a host never gathered, a key pattern matching none of its
	// facts, nothing comparable, too large) leaves every host as it was.
	plans := make([]facts.PlannedBaseline, 0, len(hosts))
	for _, h := range hosts {
		p, err := store.PlanBaseline(ctx, string(h.ID()), req)
		if err != nil {
			return baselineRefusal(h, source, err)
		}
		plans = append(plans, p)
	}
	for i, p := range plans {
		// Only another taker writing the same name since the plan can
		// refuse a write now; say which hosts were written before it.
		if err := store.StoreBaseline(ctx, p); err != nil {
			return fmt.Errorf("%w (written before it: %d of %d host(s))", baselineRefusal(hosts[i], source, err), i, len(plans))
		}
		printBaselineTaken(hosts[i].Name(), p.Baseline)
	}
	return nil
}

// baselineRefusal is err, a refusal to take h's baseline, as the command
// says it: a host or source never gathered named with what to do, anything
// else with the host it refused.
func baselineRefusal(h, source pkginventory.InventoryItem, err error) error {
	switch {
	case errors.Is(err, facts.ErrSourceNeverGathered) && source != nil:
		return fmt.Errorf("--from %q: its facts were never gathered: run facts.gather on it first", source.Name())
	case errors.Is(err, facts.ErrNeverGathered):
		return fmt.Errorf("host %q: its facts were never gathered: run facts.gather on it first", h.Name())
	}
	return fmt.Errorf("host %q: %w", h.Name(), err)
}

// runFactsDrift is `pleiades facts drift [<hosts>]`.
func runFactsDrift(args []string) error {
	if len(args) > 0 && asksHelp(args[:1]) {
		return fmt.Errorf("%s: %w", factsDriftUsage, errMissingPositional)
	}
	fs := flag.NewFlagSet("facts drift", flag.ContinueOnError)
	addInventoryFlag(fs)
	dir := fs.String("dir", ".", "project directory")
	baseline := fs.String("baseline", "", "the baseline to compare against, by name; each host's newest when unset")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(positionals) > 1 {
		return fmt.Errorf("%s: %w", factsDriftUsage, errExtraPositional)
	}
	keys := factKeysToRead(*dir)
	if err := keys.Ready(); err != nil {
		return err
	}
	items, err := loadInventory(*dir)
	if err != nil {
		return err
	}
	hosts, named := items, len(positionals) == 1
	if named {
		if hosts, err = resolveFactHosts(items, positionals[0]); err != nil {
			return err
		}
	}
	store, err := facts.OpenFileStore(factsDir(*dir), keys)
	if err != nil && !errors.Is(err, facts.ErrNoStore) {
		return err
	}
	doc := clireport.FactsDriftReport{SchemaVersion: clireport.FactsDriftVersion, At: time.Now().UTC(), Baseline: *baseline, Devices: []facts.DriftReport{}}
	for _, h := range hosts {
		rep := facts.NoBaseline(string(h.ID()), "no host's facts have been gathered in this project yet")
		if store != nil {
			// A host whose files do not open is its own unknown report;
			// only a store with no keys at all, which no host's report could
			// change, stops the command.
			if rep, err = store.DriftOf(context.Background(), string(h.ID()), *baseline, doc.At); err != nil {
				return fmt.Errorf("host %q: %w", h.Name(), err)
			}
		}
		rep.Host = h.Name()
		doc.Devices = append(doc.Devices, shownDrift(rep))
	}
	if *asJSON {
		if err := writeJSON(os.Stdout, doc); err != nil {
			return err
		}
	} else {
		printDrift(doc)
	}
	return driftOutcome(doc, named)
}

// driftOutcome is how a drift ends: exitDrifted when any host drifted; a
// failure when none did and a host could not be compared (unknown), or a
// host named on the command line has no baseline; else success. A host the
// command was not asked about by name and that has no baseline is listed
// and does not fail it.
func driftOutcome(doc clireport.FactsDriftReport, named bool) error {
	var drifted, uncompared []string
	for _, d := range doc.Devices {
		switch {
		case d.State == facts.DriftDrifted:
			drifted = append(drifted, d.Host)
		case d.State == facts.DriftUnknown, d.State == facts.DriftNoBaseline && named:
			uncompared = append(uncompared, d.Host)
		}
	}
	switch {
	case len(drifted) > 0:
		return &driftedError{msg: fmt.Sprintf("%d host(s) drifted from their baseline: %s", len(drifted), strings.Join(drifted, ", "))}
	case len(uncompared) > 0:
		return fmt.Errorf("%d host(s) could not be compared with a baseline: %s", len(uncompared), strings.Join(uncompared, ", "))
	}
	return nil
}

// shownDrift is rep with every key as a person reads it (facts.ShownKey), as
// the facts view names keys.
func shownDrift(rep facts.DriftReport) facts.DriftReport {
	for i := range rep.Changes {
		rep.Changes[i].Key = facts.ShownKey(rep.Changes[i].Key)
	}
	for i := range rep.Unconfirmed {
		rep.Unconfirmed[i].Key = facts.ShownKey(rep.Unconfirmed[i].Key)
	}
	for _, list := range [][]string{rep.NotComparable, rep.KindChanged} {
		for i := range list {
			list[i] = facts.ShownKey(list[i])
		}
	}
	return rep
}

// resolveFactHosts is the inventory hosts target names, a host, a tag, or a
// comma-separated list of either, as a runbook's hosts: resolves them; a
// name that matches nothing is refused.
func resolveFactHosts(items []pkginventory.InventoryItem, target string) ([]pkginventory.InventoryItem, error) {
	hosts, missing := engine.ResolveTargets(validate.WorldView{Items: items}, target)
	if len(missing) > 0 {
		return nil, fmt.Errorf("not a host or tag in the inventory: %s", strings.Join(missing, ", "))
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%q names no host", target)
	}
	return hosts, nil
}

// itemNamed is the inventory host named name, or nil.
func itemNamed(items []pkginventory.InventoryItem, name string) pkginventory.InventoryItem {
	for _, it := range items {
		if it.Name() == name {
			return it
		}
	}
	return nil
}

// osUser is who takes a baseline: the operating system's user, or its
// environment's when the system cannot say.
func osUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	for _, v := range []string{"USER", "USERNAME", "LOGNAME"} {
		if s := os.Getenv(v); s != "" {
			return s
		}
	}
	return "unknown"
}
