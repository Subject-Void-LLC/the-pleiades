package main

import (
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
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runbook := filepath.Join(*dir, inventory.DefaultRunbookDir, inventory.DefaultSampleRunbook)
	if fs.NArg() == 1 {
		runbook = fs.Arg(0)
	} else if fs.NArg() > 1 {
		return fmt.Errorf("usage: pleiades validate [runbook.yaml]")
	}

	items, dag, err := loadWorld(*dir, runbook)
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
