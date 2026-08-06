// Command pleiades's `forge new-plugin` subcommand lives here: thin flag
// parsing that delegates all real generation work to
// internal/forge/pluginscaffold, per Phase 30's "forge subcommand files own
// no business logic beyond their own flags" convention.
package main

import (
	"flag"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/pluginscaffold"
)

// runForgeNewPlugin emits a new inventory sync plugin package: a syncplugin
// registration and a four-method skeleton that refuses rather than pretends.
// See internal/forge/pluginscaffold for the actual generation logic, and the
// generated package's own doc comment for what a human fills in and in what
// order.
func runForgeNewPlugin(args []string) error {
	name, rest, err := splitPositional(args, map[string]bool{"read-only": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades forge new-plugin <name> --description <text> [--endpoint https://host] [--read-only] [--dir .]: %w", err)
	}

	fs := flag.NewFlagSet("forge new-plugin", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository directory to write the generated package into")
	description := fs.String("description", "", "one-line help text describing the upstream system this plugin reads")
	endpoint := fs.String("endpoint", "", "default upstream base URL (e.g. https://sandboxdnac.cisco.com)")
	readOnly := fs.Bool("read-only", false, "declare the upstream authoritative and never written back")

	if err := fs.Parse(rest); err != nil {
		return err
	}

	cfg := pluginscaffold.Config{
		Name:        name,
		Description: *description,
		Endpoint:    *endpoint,
		ReadOnly:    *readOnly,
	}

	files, err := pluginscaffold.Generate(cfg)
	if err != nil {
		return err
	}

	for _, f := range files {
		written, err := writeGeneratedFile(*dir, f.Path, f.Content)
		if err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", written)
	}

	// The same reachability caveat new-device prints, for the same reason:
	// an init() only runs if something imports the package, so a generated
	// plugin is invisible to the real binary until the composition root
	// names it (FAILURE_PATTERNS.md #52).
	fmt.Printf("%q is not yet reachable from the stock binary: add a blank import of %s to internal/inventory/plugins/builtins.go first.\n",
		cfg.Name, cfg.ImportPath())
	fmt.Printf("%q is registered with status declared: every method refuses until a human implements it against the real upstream system.\n", cfg.Name)
	return nil
}
