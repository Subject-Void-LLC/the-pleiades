// Command pleiades's `forge new-view` subcommand lives here: thin flag
// parsing that delegates all real generation work to
// internal/forge/viewscaffold, per Phase 30's "forge subcommand files own no
// business logic beyond their own flags" convention.
package main

import (
	"flag"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/viewscaffold"
)

// runForgeNewView emits a new web UI view resource package: a registration
// and a field declaration, registered declared so it refuses rather than
// pretends. See internal/forge/viewscaffold for the actual generation logic,
// and the generated package's own doc comment for what a human fills in and
// in what order.
func runForgeNewView(args []string) error {
	name, rest, err := splitPositional(args, nil)
	if err != nil {
		return fmt.Errorf("usage: pleiades forge new-view <name> --title <text> --summary <text> [--nav-label TEXT] [--nav-order 70] [--dir .]: %w", err)
	}

	fs := flag.NewFlagSet("forge new-view", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository directory to write the generated package into")
	title := fs.String("title", "", "page heading and document title for this view")
	summary := fs.String("summary", "", "one line rendered under the heading")
	navLabel := fs.String("nav-label", "", "sidebar text (defaults to the uppercased title)")
	// The built-in views occupy 10 through 60 in steps of ten, so 70 puts a
	// new view after them without renumbering anything.
	navOrder := fs.Int("nav-order", 70, "sidebar position; built-in views use 10 through 60")

	if err := fs.Parse(rest); err != nil {
		return err
	}

	cfg := viewscaffold.Config{
		Name:     name,
		Title:    *title,
		NavLabel: *navLabel,
		NavOrder: *navOrder,
		Summary:  *summary,
	}

	files, err := viewscaffold.Generate(cfg)
	if err != nil {
		return err
	}

	// No --skip-existing here, unlike the three catalog subcommands: a
	// view is not part of a generated table anything re-runs in bulk,
	// so there is no caller that wants a collision treated as a
	// non-event, and refusing stays the right answer.
	for _, f := range files {
		written, err := writeGeneratedFile(*dir, f.Path, f.Content)
		if err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", written)
	}

	// Printed last, and at length, because it is the one step no generator
	// can perform and the only one whose omission produces no error at all.
	fmt.Print(viewscaffold.Reminder(cfg))
	return nil
}
