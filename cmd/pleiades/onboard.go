// Command pleiades's `onboard` subcommand: probe a generic device over its
// protocol and record what it proved. It parses flags and delegates;
// onboarding itself is internal/inventory/onboard.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// runOnboard probes one device and prints what onboarding did. It ends
// non-zero when the probe failed, after printing the result, which then
// carries the reason.
func runOnboard(args []string) error {
	name, rest, err := splitPositional(args, map[string]bool{"json": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades onboard <name> [--json] [--timeout 30s]: %w", err)
	}
	fs := flag.NewFlagSet("onboard", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	timeout := fs.Duration("timeout", 30*time.Second, "how long the probe may take")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be positive")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	repo := inv.NewFileRepository(filepath.Join(*dir, inv.DefaultInventoryFilename), inv.NewItemFactory())
	store := credential.NewLazyFileStore(*dir)
	secrets := onboard.SecretsFrom(store)
	// A device reached through a route of SSH hops (a cluster behind a
	// bastion) is probed through it, resolved as a run resolves it.
	routes := onboard.WithRoutes(func(ctx context.Context, device inventory.InventoryItem) (sdk.RouteDial, error) {
		dial, _, err := engine.RouteDialerFor(ctx, repo, store, device)
		return dial, err
	})

	// A container host with no host (this machine) is probed through this
	// machine's own engine socket, which run allowed for every command
	// (containerengine.AllowLocalCarrier): the process that onboards here is
	// the process that runs this project's tasks.
	res, onboardErr := onboard.Onboard(ctx, repo, name, secrets, time.Now, routes)
	// Then each loaded program's probe, whatever the device's own probe
	// found (a program may reach it another way, an API rather than SSH):
	// what a program defines is proved by that program. A failed probe of
	// the device's own is still the command's answer.
	if onboardErr == nil || errors.Is(onboardErr, onboard.ErrNotOnboarded) || errors.Is(onboardErr, onboard.ErrProbe) {
		grants, err := runProgramProbes(*dir, name)
		if err != nil {
			return err
		}
		if len(grants) > 0 {
			res.Grants = grants
			if errors.Is(onboardErr, onboard.ErrNotOnboarded) {
				onboardErr = nil
				res.Device = name
			}
		}
	}
	if *asJSON {
		if err := printOnboardJSON(os.Stdout, res); err != nil {
			return err
		}
	} else {
		if res.Type != "" {
			printOnboard(os.Stdout, res)
		}
		printProbedGrants(os.Stdout, res)
	}
	return onboardErr
}

// printOnboardJSON prints res as indented JSON. Text in it came from the
// device, so it goes through writeJSON, which escapes what JSON's own
// escaping leaves for a terminal to act on.
func printOnboardJSON(w io.Writer, res onboard.Result) error {
	return writeJSON(w, res)
}

// printOnboard prints res for a person. Every value that came from the
// device is escaped, since a device can answer with terminal control
// sequences.
func printOnboard(w io.Writer, res onboard.Result) {
	fmt.Fprintf(w, "%s (%s) over %s: %s -> %s\n", termsafe.EscapeLine(res.Device), res.Type, res.Protocol, res.PreviousState, res.State)
	for _, warning := range res.Warnings {
		fmt.Fprintf(w, "  WARNING: %s\n", termsafe.EscapeLine(warning))
	}
	if res.Error != "" {
		fmt.Fprintf(w, "  failed: %s\n", termsafe.EscapeLine(res.Error))
		return
	}
	fmt.Fprintf(w, "  capabilities: %s\n", joinOrNone(res.Capabilities))
	if len(res.Added)+len(res.Removed) > 0 {
		fmt.Fprintf(w, "  added: %s\n  removed: %s\n", joinOrNone(res.Added), joinOrNone(res.Removed))
	}
	keys := make([]string, 0, len(res.Facts))
	for k := range res.Facts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "  %s: %s\n", k, termsafe.EscapeLine(factString(res.Facts[k])))
	}
	if !res.Changed {
		fmt.Fprintln(w, "  unchanged")
	}
}

// printProbedGrants prints what each program's probe did.
func printProbedGrants(w io.Writer, res onboard.Result) {
	for _, g := range res.Grants {
		switch {
		case g.Error != "":
			fmt.Fprintf(w, "  %s (%s): not granted: %s\n", g.Capability, g.Program, termsafe.EscapeLine(g.Error))
		case g.Granted:
			fmt.Fprintf(w, "  %s (%s): granted by its probe %s\n", g.Capability, g.Program, g.Probe)
		case g.Withdrawn:
			fmt.Fprintf(w, "  %s (%s): withdrawn, the device no longer answers its probe %s\n", g.Capability, g.Program, g.Probe)
		default:
			fmt.Fprintf(w, "  %s (%s): not granted, the device does not answer its probe %s\n", g.Capability, g.Program, g.Probe)
		}
	}
}

// joinOrNone joins names, or says none.
func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// factString renders one fact: text as is, a list comma-separated.
func factString(v any) string {
	list, ok := v.([]any)
	if !ok {
		return fmt.Sprint(v)
	}
	parts := make([]string, 0, len(list))
	for _, item := range list {
		parts = append(parts, fmt.Sprint(item))
	}
	return strings.Join(parts, ", ")
}
