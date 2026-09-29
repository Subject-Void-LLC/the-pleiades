// The run command, and the flags it shares with adhoc.
package main

import (
	"errors"
	"flag"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// maxForks is the most devices one run works on at once, the same bound
// the runbook launch kind's forks field sets on the Controller.
const maxForks = 1000

// runBoolFlags are the run flags that take no value in their bare form,
// for splitPositional.
var runBoolFlags = map[string]bool{"verbose": true, "v": true, "persist-connections": true, "json": true}

// runRunbook loads the inventory and a runbook, validates them, prints
// the resulting execution plan (the authored pretasks/tasks/posttasks
// tree and what each task targets), and then executes it with
// engine.Executor (Part 0 Phase W5), through the pipeline adhoc shares
// (run_pipeline.go).
//
// --mode check turns the run into a dry run (PLAN.md Section 34's check
// mode): every task that can say what it would change does so without
// changing anything, every task that cannot is named as unchecked, no
// journal is written, and the command ends non-zero if anything went
// unchecked. engine.WithMode carries the mode; see internal/engine's
// check.go for the rules it enforces.
//
// --json prints the run's report as one JSON document instead of text,
// with every task's output, and ends with the same exit status.
func runRunbook(args []string) error {
	// splitPositional rather than fs.Arg(0), for the same reason
	// add-host and the forge subcommands use it: Go's flag package stops
	// parsing at the first non-flag argument, so `run site.yaml
	// --verbose` would silently treat --verbose as a second positional
	// and fail with a usage error naming neither the flag nor why. The
	// runbook path is the thing a person types first.
	runbook, rest, err := splitPositional(args, runBoolFlags)
	if err != nil {
		return fmt.Errorf("usage: pleiades run <runbook.yaml> [--mode execute|check] [--tags a,b] [--skip-tags c] [--forks 5] [--persist-connections=false] [--extra-vars key=value|@file.yaml ...] [--verbose] [--json] [--dir .]: %w", err)
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	flags := addRunFlags(fs)
	selection := tagFlags(fs)
	extraVars := &extraVarsFlag{}
	fs.Var(extraVars, "extra-vars", "a variable the run starts with, as key=value, key:=yaml or @file.yaml (repeatable); a runbook reads it as vars.<name>")
	fs.Var(extraVars, "e", "shorthand for --extra-vars")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	opts, err := flags.options()
	if err != nil {
		return err
	}
	if opts.variables, err = extraVars.variables(); err != nil {
		return err
	}
	return runPipeline(opts, runSource{label: runbook, path: runbook, selection: *selection})
}

// runFlags are the flags run and adhoc share, as registered on a FlagSet.
type runFlags struct {
	dir, mode       *string
	verbose, asJSON *bool
	persist         *bool
	forks           *int
	allowUnchecked  map[string]bool
}

// addRunFlags registers the shared run flags on fs.
func addRunFlags(fs *flag.FlagSet) *runFlags {
	f := &runFlags{allowUnchecked: map[string]bool{}}
	f.dir = fs.String("dir", ".", "project directory")
	f.mode = fs.String("mode", string(collection.ModeExecute), "execute applies changes; check reports what each task would change and changes nothing")
	f.verbose = fs.Bool("verbose", false, "print each task's own output (stdout, exit status, diffs), not just whether it changed")
	fs.BoolVar(f.verbose, "v", false, "shorthand for --verbose")
	f.asJSON = fs.Bool("json", false, "print the run as one JSON document, with every task's output, instead of text")
	// allowUnchecked names methods whose tasks may go unchecked without
	// making the check incomplete: they are still listed, so a pipeline
	// accepts exactly the gaps it named and a new one still stops it.
	fs.Func("allow-unchecked", "a method whose tasks may go unchecked without making the check incomplete (repeatable)", func(v string) error {
		if v == "" {
			return errors.New("needs a method name")
		}
		f.allowUnchecked[v] = true
		return nil
	})
	f.persist = fs.Bool("persist-connections", true, "keep one SSH connection per device open between its tasks; =false logs in afresh for every task")
	f.forks = fs.Int("forks", engine.DefaultMaxConcurrency, "how many devices are worked on at once, 1 to 1000 (Ansible's forks, with the same default)")
	return f
}

// options checks the parsed flags and returns them as runOptions.
//
// The mode is parsed before anything is loaded, so a misspelled mode costs
// nothing and, above all, is never treated as execute: ParseMode refuses
// any value outside the closed set rather than defaulting.
func (f *runFlags) options() (runOptions, error) {
	mode, err := collection.ParseMode(*f.mode)
	if err != nil {
		return runOptions{}, fmt.Errorf("--mode: %w", err)
	}
	if *f.forks < 1 || *f.forks > maxForks {
		return runOptions{}, fmt.Errorf("--forks must be between 1 and %d, got %d", maxForks, *f.forks)
	}
	return runOptions{
		dir: *f.dir, mode: mode, forks: *f.forks, persist: *f.persist,
		allowUnchecked: f.allowUnchecked, verbose: *f.verbose, asJSON: *f.asJSON,
	}, nil
}
