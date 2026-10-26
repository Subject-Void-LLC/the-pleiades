// Command pleiades: validate, which checks one or more runbooks against
// the inventory without touching any device.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// runbookCheck is one path validate was given, and what became of it:
// passed over (skip), unbuildable (err), or checked (report).
type runbookCheck struct {
	// path is the path as the user named it, or as the runbooks
	// directory listing produced it.
	path string

	// dag is the built runbook, nil when the path was skipped or failed.
	dag *engine.DAG

	// skip says why path is not a runbook to check, empty when it is.
	skip string

	// err is why path could not be built or selected from, and stage
	// names which of the two failed ("build DAG", "select tasks").
	err   error
	stage string

	// report holds the rule findings once dag has been checked.
	report validate.Report

	// warnings is the advice about the file itself (its header comment),
	// added to report once it is checked; it never fails the run.
	warnings []validate.Finding
}

// failed reports whether this path counts against the run: it could not
// be built, or a rule found something wrong with it.
func (c runbookCheck) failed() bool {
	return c.err != nil || c.report.HasErrors()
}

// runValidate loads the inventory and the runbooks named, then runs the
// shared validation core (internal/validate) against each. All of the
// actual rule logic lives there; this function only wires the CLI surface.
//
// With no runbook named it checks every entry in the project's runbooks
// directory, which is what `pleiades validate runbooks/*` would name.
// Either way a directory, a file that is not YAML, and an import_tasks
// file (engine.ErrTaskList) are passed over with a note rather than
// failed, since a shell glob hands all three over unasked. Every runbook
// is checked even after one fails, and the command fails when any did or
// when nothing was left to check.
func runValidate(args []string) error {
	// Runbooks may come before, after or between the flags, so they are
	// pulled out first; every validate flag takes a value.
	paths, rest := splitPositionals(args, nil)

	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	selection := tagFlags(fs)
	// The same flag, and the same parser, as run's, so a runbook is
	// validated against exactly the variables a run would render.
	extraVars := addExtraVarsFlag(fs)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: pleiades validate [runbook.yaml ...] [--tags a,b] [--skip-tags c] [--extra-vars key=value] [--dir .]")
	}
	variables, err := extraVars.variables()
	if err != nil {
		return err
	}

	if len(paths) == 0 {
		if paths, err = projectRunbooks(*dir); err != nil {
			return err
		}
	}

	// External Collections register first, so a runbook calling one of
	// their methods validates exactly as one calling a built-in does.
	if _, err := loadExternalCollections(context.Background(), *dir); err != nil {
		return err
	}

	items, err := loadInventory(*dir)
	if err != nil {
		return err
	}
	builder, err := newRunbookBuilder()
	if err != nil {
		return err
	}

	// Every runbook is built before any is selected from, because a tag
	// filter is checked against all of them together: a name is a typo
	// only when none of them carries it.
	checks := make([]runbookCheck, len(paths))
	var built []*engine.DAG
	for i, path := range paths {
		checks[i] = buildCheck(builder, path)
		if checks[i].dag != nil {
			built = append(built, checks[i].dag)
		}
	}
	if !selection.IsZero() && len(built) > 0 {
		sel, err := engine.NewTagSelection(*selection, built...)
		if err != nil {
			return err
		}
		for i := range checks {
			if checks[i].dag == nil {
				continue
			}
			if checks[i].dag, err = sel.Select(checks[i].dag); err != nil {
				checks[i].dag, checks[i].err, checks[i].stage = nil, err, "select tasks"
			}
		}
	}
	for i := range checks {
		if checks[i].dag != nil {
			// Validated as run would validate it: --extra-vars are every
			// variable the run would have, so a template reading one none
			// of them gives is reported here.
			checks[i].report = validate.Validate(validate.WorldView{Items: items, DAG: checks[i].dag, Variables: variables, VariablesKnown: true, VariablesFlag: extraVarsFlagName})
			checks[i].report.Warnings = checks[i].warnings
		}
	}

	if len(checks) == 1 {
		return reportOne(checks[0])
	}
	return reportMany(checks)
}

// projectRunbooks lists what a bare `pleiades validate` checks: every
// entry directly in dir's runbooks directory, in name order, leaving out
// hidden entries as the shell's runbooks/* does.
func projectRunbooks(dir string) ([]string, error) {
	runbookDir := filepath.Join(dir, inventory.DefaultRunbookDir)
	entries, err := os.ReadDir(runbookDir)
	if err != nil {
		return nil, fmt.Errorf("no runbook named, and the runbooks directory cannot be read: %w; name one: pleiades validate <runbook.yaml>", err)
	}
	var paths []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		paths = append(paths, filepath.Join(runbookDir, entry.Name()))
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no runbook named, and %s is empty; name one: pleiades validate <runbook.yaml>", runbookDir)
	}
	return paths, nil
}

// buildCheck decides what path is and builds it when it is a runbook.
// A directory, a file without a .yaml or .yml extension, and a list of
// tasks come back skipped; any other build failure comes back as err.
func buildCheck(builder *engine.Builder, path string) runbookCheck {
	check := runbookCheck{path: path}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		check.skip = "a directory"
		return check
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".yaml" && ext != ".yml" {
		check.skip = "not a .yaml or .yml file"
		return check
	}
	// path is a CLI argument the invoking user supplies to their own
	// process, or an entry of their own project's runbooks directory; see
	// loadWorld for why that crosses no trust boundary.
	dag, err := builder.BuildFromYAMLFile(path)
	switch {
	case errors.Is(err, engine.ErrTaskList):
		check.skip = "a list of tasks, checked through the runbook that imports it"
	case err != nil:
		check.err, check.stage = err, "build DAG"
	default:
		source, err := os.ReadFile(path) // #nosec G304 -- the same path BuildFromYAMLFile just read, a CLI argument or an entry of the user's own runbooks directory
		if err != nil {
			check.err, check.stage = err, "read"
			return check
		}
		check.dag, check.warnings = dag, append(validate.HeaderWarnings(source), validate.TaskFormWarnings(source)...)
		check.warnings = append(check.warnings, validate.LiteralSecretWarnings(dag)...)
	}
	return check
}

// reportOne prints the result for a single runbook exactly as validate
// always has: the report alone, and any failure as the command's error.
func reportOne(check runbookCheck) error {
	switch {
	case check.skip != "":
		fmt.Printf("%s: skipped, %s\n", check.path, check.skip)
		return fmt.Errorf("no runbook to validate")
	case check.err != nil:
		return fmt.Errorf("failed to %s from %s: %w", check.stage, check.path, check.err)
	}
	fmt.Print(check.report.String())
	if check.report.HasErrors() {
		return fmt.Errorf("validation failed")
	}
	return nil
}

// reportMany prints one entry per path, in the order given, then a
// summary line, and fails when any runbook failed or none was checked.
func reportMany(checks []runbookCheck) error {
	checked, failed, skipped, warned := 0, 0, 0, 0
	for _, check := range checks {
		switch {
		case check.skip != "":
			skipped++
			fmt.Printf("%s: skipped, %s\n", check.path, check.skip)
		case check.err != nil:
			checked++
			failed++
			fmt.Printf("%s: failed to %s: %v\n", check.path, check.stage, check.err)
		case check.report.HasErrors():
			checked++
			failed++
			// The findings sit under their runbook, indented, so each
			// one reads as belonging to the path above it.
			fmt.Printf("%s:\n", check.path)
			for _, line := range strings.Split(strings.TrimSuffix(check.report.String(), "\n"), "\n") {
				fmt.Printf("  %s\n", line)
			}
		default:
			checked++
			if len(check.report.Warnings) == 0 {
				fmt.Printf("%s: no issues found\n", check.path)
				continue
			}
			warned++
			fmt.Printf("%s: no issues found, %d %s\n", check.path, len(check.report.Warnings), plural(len(check.report.Warnings), "warning", "warnings"))
			for _, w := range check.report.Warnings {
				fmt.Printf("  [warning] %s: %s\n", w.RuleName, w.Message)
			}
		}
	}

	summary := fmt.Sprintf("validate: %d %s checked", checked, plural(checked, "runbook", "runbooks"))
	if skipped > 0 {
		summary += fmt.Sprintf(", %d skipped", skipped)
	}
	if warned > 0 {
		summary += fmt.Sprintf(", %d with warnings", warned)
	}
	switch {
	case checked == 0:
		fmt.Println(summary)
		return fmt.Errorf("no runbook to validate")
	case failed > 0:
		fmt.Printf("%s, %d with problems\n", summary, failed)
		return fmt.Errorf("validation failed: %d of %d %s", failed, checked, plural(checked, "runbook", "runbooks"))
	}
	fmt.Printf("%s, no issues found\n", summary)
	return nil
}

// plural returns one when n is 1 and many otherwise.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
