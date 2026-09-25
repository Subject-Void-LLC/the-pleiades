package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// runValidate loads the inventory and a runbook, then runs the shared
// validation core (internal/validate) against them. All of the actual
// rule logic lives there; this function only wires the CLI surface.
func runValidate(args []string) error {
	// The runbook is optional and may come before or after the flags, so
	// it is pulled out first (splitPositional); every validate flag takes
	// a value.
	runbookArg, rest, err := splitPositional(args, nil)
	if err != nil && !errors.Is(err, errMissingPositional) {
		return fmt.Errorf("usage: pleiades validate [runbook.yaml] [--tags a,b] [--skip-tags c] [--dir .]: %w", err)
	}

	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	selection := tagFlags(fs)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: pleiades validate [runbook.yaml]")
	}

	runbook := filepath.Join(*dir, inventory.DefaultRunbookDir, inventory.DefaultSampleRunbook)
	if runbookArg != "" {
		runbook = runbookArg
	}

	// External Collections register first, so a runbook calling one of
	// their methods validates exactly as one calling a built-in does.
	if _, err := loadExternalCollections(context.Background(), *dir); err != nil {
		return err
	}

	items, dag, err := loadWorld(*dir, runbook, *selection)
	if err != nil {
		return err
	}

	report := validate.Validate(validate.WorldView{Items: items, DAG: dag})
	fmt.Print(report.String())
	if report.HasErrors() {
		return fmt.Errorf("validation failed")
	}
	return nil
}
