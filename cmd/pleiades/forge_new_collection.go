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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// runForgeNewCollection emits a new namespaced Collection method package:
// a pkg/collection manifest registration and a stub built on
// pkg/sdk.RunbookContext. See internal/forge/collectionscaffold for the
// actual generation logic, and its generated package's own doc comment
// (repeated in this command's success message below) for exactly how a
// runbook task calling the new FQCN reaches the real dispatcher and is
// refused there, by design, until the method is really implemented.
func runForgeNewCollection(args []string) error {
	name, rest, err := splitPositional(args, map[string]bool{"requires-elevation": true, "skip-existing": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades forge new-collection <namespace.method> [--capabilities Name1,Name2] [--transports ssh] [--requires-elevation] [--site target|controller|hybrid] [--device required|optional|none] [--engine-version x.y.z] [--doc-json '{...}'|@file.json] [--skip-existing] [--dir .]: %w", err)
	}

	fs := flag.NewFlagSet("forge new-collection", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository directory to write the generated package into")
	capabilitiesFlag := fs.String("capabilities", "", "comma-separated required capability names (e.g. AptCapable)")
	transportsFlag := fs.String("transports", "", "comma-separated supported transport names (e.g. ssh)")
	requiresElevation := fs.Bool("requires-elevation", false, "whether this method needs elevated privileges on the target device")
	site := fs.String("site", "target", "where the method's code runs: target (on or against the device), controller (in the host process) or hybrid")
	device := fs.String("device", "required", "whether the method acts on a device: required, optional (decided per call by a DeviceCall) or none")
	engineVersion := fs.String("engine-version", "", "minimum core engine version constraint (unparsed, e.g. >=1.0.0)")
	docJSON := fs.String("doc-json", "", "reference documentation as a JSON pkg/collection.Doc object, or @path to read it from a file")
	skipExisting := fs.Bool("skip-existing", false, "leave an already-written file alone instead of refusing, for regenerating a catalog in place")

	if err := fs.Parse(rest); err != nil {
		return err
	}

	var transports []string
	if *transportsFlag != "" {
		transports = strings.Split(*transportsFlag, ",")
	}

	doc, err := parseDocJSONFlag(*docJSON)
	if err != nil {
		return err
	}

	cfg := collectionscaffold.Config{
		Name:              name,
		Capabilities:      parseCapabilitiesFlag(*capabilitiesFlag),
		Transports:        transports,
		RequiresElevation: *requiresElevation,
		Site:              collection.Site(*site),
		Device:            collection.DeviceUse(*device),
		EngineVersion:     *engineVersion,
		Doc:               doc,
	}

	files, err := collectionscaffold.Generate(cfg)
	if err != nil {
		return err
	}

	if *skipExisting {
		relPaths := make([]string, len(files))
		for i, f := range files {
			relPaths[i] = f.Path
		}
		existing, err := firstExistingFile(*dir, relPaths)
		if err != nil {
			return err
		}
		if existing != "" {
			fmt.Printf("skipped %s (%s already exists)\n", cfg.Name, existing)
			return nil
		}
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
