// Command pleiades's `forge new-collection` subcommand lives here: thin
// flag parsing that delegates all real generation work to
// internal/forge/collectionscaffold, per Phase 30's "forge subcommand
// files own no business logic beyond their own flags" convention.
package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
)

// runForgeNewCollection emits a new namespaced Collection method package:
// a pkg/collection manifest registration and a stub built on
// pkg/sdk.RunbookContext. See internal/forge/collectionscaffold for the
// actual generation logic, and its generated package's own doc comment
// (repeated in this command's success message below) for exactly how a
// runbook task calling the new FQCN reaches the real dispatcher and is
// refused there, by design, until the method is really implemented.
func runForgeNewCollection(args []string) error {
	name, rest, err := splitPositional(args, map[string]bool{"requires-elevation": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades forge new-collection <namespace.method> [--capabilities Name1,Name2] [--transports ssh] [--requires-elevation] [--engine-version x.y.z] [--dir .]: %w", err)
	}

	fs := flag.NewFlagSet("forge new-collection", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository directory to write the generated package into")
	capabilitiesFlag := fs.String("capabilities", "", "comma-separated required capability names (e.g. AptCapable)")
	transportsFlag := fs.String("transports", "", "comma-separated supported transport names (e.g. ssh)")
	requiresElevation := fs.Bool("requires-elevation", false, "whether this method needs elevated privileges on the target device")
	engineVersion := fs.String("engine-version", "", "minimum core engine version constraint (unparsed, e.g. >=1.0.0)")

	if err := fs.Parse(rest); err != nil {
		return err
	}

	var transports []string
	if *transportsFlag != "" {
		transports = strings.Split(*transportsFlag, ",")
	}

	cfg := collectionscaffold.Config{
		Name:              name,
		Capabilities:      parseCapabilitiesFlag(*capabilitiesFlag),
		Transports:        transports,
		RequiresElevation: *requiresElevation,
		EngineVersion:     *engineVersion,
	}

	files, err := collectionscaffold.Generate(cfg)
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

	fmt.Printf("%q is registered and reachable through the real dispatcher, which refuses it with \"declared but not implemented\" until it is really implemented; see the generated package's own doc comment.\n", cfg.Name)
	return nil
}
