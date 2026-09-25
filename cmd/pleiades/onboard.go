// Command pleiades's `onboard` subcommand: probe a generic device over its
// protocol and record what it proved. It parses flags and delegates;
// onboarding itself is internal/inventory/onboard.
package main

import (
	"context"
	"encoding/json"
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
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
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
	secrets := onboard.SecretsFrom(credential.NewLazyFileStore(*dir))

	res, onboardErr := onboard.Onboard(ctx, repo, name, secrets, time.Now)
	if *asJSON {
		if err := printOnboardJSON(os.Stdout, res); err != nil {
			return err
		}
	} else if res.Type != "" {
		printOnboard(os.Stdout, res)
	}
	return onboardErr
}

// printOnboardJSON prints res as indented JSON. Text in it came from the
// device, and JSON's own escaping is what keeps it inert.
func printOnboardJSON(w io.Writer, res onboard.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// printOnboard prints res for a person. Every value that came from the
// device is escaped, since a device can answer with terminal control
// sequences.
func printOnboard(w io.Writer, res onboard.Result) {
	fmt.Fprintf(w, "%s (%s) over %s: %s -> %s\n", termsafe.EscapeLine(res.Device), res.Type, res.Protocol, res.PreviousState, res.State)
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
