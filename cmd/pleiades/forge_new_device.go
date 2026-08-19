// Command pleiades's `forge new-device` subcommand lives here: thin flag
// parsing that delegates all real generation work to
// internal/inventory/devicescaffold, per Phase 30's "forge subcommand
// files own no business logic beyond their own flags" convention.
package main

import (
	"flag"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devicescaffold"
)

// runForgeNewDevice emits a new vendor device-type package mirroring
// internal/inventory/devices/cisco and devices/linux. See
// internal/inventory/devicescaffold for the actual generation logic, and
// its generated package's own doc comment (repeated in this command's
// success message below) for the composition-root caveat: a generated
// type is not reachable from the stock binary until a human adds a blank
// import of it to internal/inventory/builtins.go.
func runForgeNewDevice(args []string) error {
	vendor, rest, err := splitPositional(args, map[string]bool{"skip-existing": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades forge new-device <vendor> --type <type_key> [--capabilities Name1,Name2] [--skip-existing] [--dir .]: %w", err)
	}

	fs := flag.NewFlagSet("forge new-device", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository directory to write the generated package into")
	typeKey := fs.String("type", "", "the full record.RegisterType key (e.g. cisco_router)")
	capabilitiesFlag := fs.String("capabilities", "", "comma-separated vendor baseline capability names (e.g. AptCapable,SSHTransportCapable)")
	skipExisting := fs.Bool("skip-existing", false, "leave an already-written file alone instead of refusing, for regenerating a catalog in place")

	if err := fs.Parse(rest); err != nil {
		return err
	}

	if *typeKey == "" {
		return fmt.Errorf("--type is required")
	}

	cfg := devicescaffold.Config{
		Vendor:       vendor,
		TypeKey:      *typeKey,
		Capabilities: parseCapabilitiesFlag(*capabilitiesFlag),
	}

	files, err := devicescaffold.Generate(cfg)
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
			fmt.Printf("skipped %s.%s (%s already exists)\n", cfg.Vendor, cfg.StructName(), existing)
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

	fmt.Printf("%s.%s is not yet reachable from the stock binary: add a blank import of it to internal/inventory/builtins.go (or your own composition root) first.\n", cfg.Vendor, cfg.StructName())
	return nil
}
