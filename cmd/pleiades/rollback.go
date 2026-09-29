// Package main: `pleiades rollback`, which undoes a run from its journal.
//
// It reads the run's journal, plans the undo with internal/rollback (the
// same planner the Controller's rollback job uses), and carries the plan
// out as an ordinary runbook it writes in memory, through the pipeline
// `pleiades run` uses (run_pipeline.go): validated, locked, journaled and
// reported exactly as any run, with each journal entry naming the node of
// the undone run it undoes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// rollbackUsage is the command's usage line.
const rollbackUsage = "usage: pleiades rollback <run-id> [--dir DIR] [--mode check] [--runbook FILE] [--leave NODE]... [--allow-partial NODE]... [--allow-unknown NODE]... [--despite-run RUN]... [--json] [--verbose]"

// rollbackReport is a rollback's own part of the run report.
type rollbackReport struct {
	// Of is the run undone.
	Of string `json:"of"`
	// Levels are the undo's tasks in the order they run.
	Levels [][]rollback.Step `json:"levels,omitempty"`
	// Left are changes left in place, by --leave.
	Left []rollback.Gap `json:"left,omitempty"`
	// Unknown are effects nobody can say were changes: failed actions and
	// an unsealed journal.
	Unknown []rollback.Gap `json:"unknown,omitempty"`
	// UnacceptedUnknown counts Unknown not named with --allow-unknown.
	UnacceptedUnknown int `json:"unaccepted_unknown"`
	// AlreadyUndone are changes an earlier rollback of the run undid.
	AlreadyUndone []rollback.Gap `json:"already_undone,omitempty"`
	// Problems are why the rollback was refused, when it was.
	Problems []rollback.Problem `json:"problems,omitempty"`
}

// rollbackFlags are the flags only rollback takes.
type rollbackFlags struct {
	runbook                                    *string
	leave, allowPartial, allowUnknown, despite map[string]bool
}

// namedList registers a repeatable flag collecting names into set.
func namedList(fs *flag.FlagSet, name, usage string, set map[string]bool) {
	fs.Func(name, usage+" (repeatable)", func(v string) error {
		if v == "" {
			return errors.New("needs a name")
		}
		set[v] = true
		return nil
	})
}

// runRollback is `pleiades rollback <run-id>`.
func runRollback(args []string) error {
	positionals, rest := splitPositionals(args, runBoolFlags)
	if len(positionals) != 1 {
		return fmt.Errorf("%s: %w", rollbackUsage, errMissingPositional)
	}
	runID := positionals[0]
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	flags := addRunFlags(fs)
	rf := rollbackFlags{leave: map[string]bool{}, allowPartial: map[string]bool{}, allowUnknown: map[string]bool{}, despite: map[string]bool{}}
	rf.runbook = fs.String("runbook", "", "the runbook the run ran, when a task's undo is its rollback: list (found in DIR/runbooks when unset)")
	namedList(fs, "leave", "a node whose change to leave in place rather than undo", rf.leave)
	namedList(fs, "allow-partial", "a node whose partial undo to run anyway", rf.allowPartial)
	namedList(fs, "allow-unknown", "a node whose unknown effect to accept, or unsealed for a journal with no seal", rf.allowUnknown)
	namedList(fs, "despite-run", "a later run whose changes to the same devices to undo beneath", rf.despite)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	opts, err := flags.options()
	if err != nil {
		return err
	}
	label := "rollback of " + runID

	// Held for the whole rollback: the journals it reads to plan must not
	// move under it, and no run may change the devices it is undoing.
	if opts.mode != collection.ModeCheck {
		release, err := journal.LockRuns(opts.dir, true)
		if err != nil {
			return refuseRollback(opts, label, &rollbackReport{Of: runID}, err)
		}
		defer func() { _ = release() }()
		opts.holdsRunLock = true
	}

	plan, err := planRollback(opts.dir, runID, rf)
	report := &rollbackReport{Of: runID, Levels: plan.Levels, Left: plan.Left, Unknown: plan.Unknown,
		UnacceptedUnknown: plan.UnacceptedUnknown, AlreadyUndone: plan.AlreadyUndone}
	var refused *rollback.RefusedError
	switch {
	case errors.As(err, &refused):
		report.Problems = refused.Problems
		if !opts.asJSON {
			printRefusal(runID, refused.Problems)
		}
		return refuseRollback(opts, label, report, fmt.Errorf("rollback of %s refused: %d problem(s)", runID, len(refused.Problems)))
	case err != nil:
		return refuseRollback(opts, label, report, err)
	}
	if !opts.asJSON {
		printGaps(plan)
	}
	if len(plan.Levels) == 0 {
		return finishWithoutRun(opts, label, report)
	}

	payload, undoes, err := rollback.Runbook("rollback-"+runID, plan.Levels, true)
	if err != nil {
		return refuseRollback(opts, label, report, err)
	}
	opts.rollbackOf, opts.undoes, opts.rollback = runID, undoes, report
	return runPipeline(opts, runSource{label: label, yaml: payload})
}

// planRollback reads the journals and inventory of the project at dir and
// plans runID's undo.
func planRollback(dir, runID string, rf rollbackFlags) (rollback.Plan, error) {
	// External Collections register first, as a run's do, so the planner
	// knows every method a journal can name (and that a third party's undo
	// is not one it replays).
	if _, err := loadExternalCollections(context.Background(), dir); err != nil {
		return rollback.Plan{}, err
	}
	target, err := journal.ReadRun(dir, runID)
	if err != nil {
		return rollback.Plan{}, err
	}
	all, err := journal.ListRuns(dir)
	if err != nil {
		return rollback.Plan{}, fmt.Errorf("reading this project's other runs, which decide what a later run has changed since: %w", err)
	}
	var others []rollback.Run
	for _, r := range all {
		if r.ID != runID {
			others = append(others, rollback.Run{ID: r.ID, Entries: r.Entries, Sealed: r.Sealed})
		}
	}
	devices, err := inventoryNames(dir)
	if err != nil {
		return rollback.Plan{}, err
	}
	req := rollback.Request{
		Target: rollback.Run{ID: target.ID, Entries: target.Entries, Sealed: target.Sealed},
		Others: others, Devices: devices,
		Leave: rf.leave, AllowPartial: rf.allowPartial, AllowUnknown: rf.allowUnknown, DespiteRuns: rf.despite,
	}
	req.Authored = authoredFrom(dir, *rf.runbook, target.Entries)
	return rollback.Build(req)
}

// inventoryNames resolves a device id to its name in the project's
// inventory now. A name two devices share resolves neither, since a task
// targeting it could reach the wrong one.
func inventoryNames(dir string) (func(string) (string, bool), error) {
	items, err := loadInventory(dir)
	if err != nil {
		return nil, err
	}
	byID := map[string]string{}
	count := map[string]int{}
	for _, item := range items {
		byID[string(item.ID())] = item.Name()
		count[item.Name()]++
	}
	return func(id string) (string, bool) {
		name, ok := byID[id]
		return name, ok && count[name] == 1
	}, nil
}

// authoredFrom returns the planner's view of the runbook the run ran:
// explicit when named, else the one under dir/runbooks whose id and
// version match the journal's. A runbook whose version differs is not the
// one that ran, so its rollback: steps could undo tasks it no longer has.
// It is found once, on the first node that asks.
func authoredFrom(dir, explicit string, entries []engine.JournalEntry) func(string) ([]engine.Task, error) {
	var id, version string
	for _, e := range entries {
		if e.DAGVersion != "" {
			id, version = e.DAGID, e.DAGVersion
			break
		}
	}
	find := sync.OnceValues(func() (*engine.DAG, error) { return findRunbook(dir, explicit, id, version) })
	return func(node string) ([]engine.Task, error) {
		dag, err := find()
		if err != nil {
			return nil, err
		}
		task, ok := dag.Nodes[node]
		if !ok {
			return nil, fmt.Errorf("the runbook %s has no node %s", id, node)
		}
		return task.Rollback, nil
	}
}

// findRunbook compiles the runbook named explicit, or finds the one under
// dir/runbooks with the run's id and version.
func findRunbook(dir, explicit, id, version string) (*engine.DAG, error) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		return nil, err
	}
	builder := engine.NewBuilder(eval)
	if explicit != "" {
		dag, err := builder.BuildFromYAMLFile(explicit)
		if err != nil {
			return nil, fmt.Errorf("--runbook %s: %w", explicit, err)
		}
		if dag.ID != id || dag.Version != version {
			return nil, fmt.Errorf("--runbook %s is runbook %s at %s, and the run ran %s at %s; it has changed since, so its rollback: steps may not match what ran", explicit, dag.ID, dag.Version, id, version)
		}
		return dag, nil
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "runbooks", "*.yaml"))
	more, _ := filepath.Glob(filepath.Join(dir, "runbooks", "*.yml"))
	var found []*engine.DAG
	for _, path := range append(paths, more...) {
		// A runbook that does not compile is not the one that ran; others
		// in the directory are none of this command's business.
		if dag, err := builder.BuildFromYAMLFile(path); err == nil && dag.ID == id && dag.Version == version {
			found = append(found, dag)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return nil, fmt.Errorf("%w: no runbook in %s is %s as it ran; name it with --runbook",
			rollback.ErrNoRunbook, filepath.Join(dir, "runbooks"), id)
	default:
		return nil, fmt.Errorf("%d runbooks in %s are %s as it ran; name the one with --runbook", len(found), filepath.Join(dir, "runbooks"), id)
	}
}

// rollbackOutcome makes a rollback that ran every step it could, and left
// an effect nobody can vouch for, incomplete rather than complete: the
// device may not be as it was, and exit status is what a pipeline reads.
func rollbackOutcome(out runOutcome, report *rollbackReport) runOutcome {
	if out.Status != "complete" || report.UnacceptedUnknown == 0 {
		return out
	}
	return runOutcome{
		Status:   "incomplete",
		Message:  fmt.Sprintf("rollback done, but %d effect(s) nobody can say were changes are left as they were; name each with --allow-unknown to accept it", report.UnacceptedUnknown),
		ExitCode: exitIncomplete,
	}
}

// finishWithoutRun ends a rollback whose plan has nothing to run: every
// change was left in place by name. It still reports, and is incomplete
// when an unknown was not accepted.
func finishWithoutRun(opts runOptions, label string, report *rollbackReport) error {
	out := rollbackOutcome(runOutcome{Status: "complete", Message: "nothing to run: every change is left in place as named", ExitCode: 0}, report)
	if opts.asJSON {
		if err := writeJSON(os.Stdout, &runReport{Runbook: label, Mode: opts.mode, Rollback: report, Outcome: out}); err != nil {
			return err
		}
	} else {
		fmt.Println(out.Message)
	}
	return outcomeError(out)
}

// refuseRollback ends a rollback that ran nothing. With --json the refusal
// is still one report on standard output, carrying the problems.
func refuseRollback(opts runOptions, label string, report *rollbackReport, why error) error {
	if opts.asJSON {
		rep := &runReport{Runbook: label, Mode: opts.mode, Rollback: report,
			Outcome: runOutcome{Status: "invalid", Message: why.Error(), ExitCode: 1}}
		if err := writeJSON(os.Stdout, rep); err != nil {
			return err
		}
	}
	return why
}

// printRefusal prints every problem, with the flag that accepts it.
func printRefusal(runID string, problems []rollback.Problem) {
	fmt.Printf("rollback of run %s refused, before any device was contacted:\n", runID)
	for _, p := range problems {
		where := p.Node
		if p.DeviceName != "" {
			where += " on " + p.DeviceName
		}
		if where != "" {
			fmt.Printf("  %s: %s\n", termsafe.EscapeLine(where), termsafe.EscapeLine(p.Reason))
		} else {
			fmt.Printf("  %s\n", termsafe.EscapeLine(p.Reason))
		}
		if p.Accept != "" {
			fmt.Printf("    accept it with %s\n", p.Accept)
		}
	}
}

// printGaps prints what the plan leaves, before the run's own plan.
func printGaps(plan rollback.Plan) {
	for _, list := range []struct {
		title string
		gaps  []rollback.Gap
	}{{"left in place", plan.Left}, {"unknown", plan.Unknown}, {"already undone", plan.AlreadyUndone}} {
		for _, g := range list.gaps {
			where := g.Node
			if g.DeviceName != "" {
				where += " on " + g.DeviceName
			}
			fmt.Printf("%s: %s: %s\n", list.title, termsafe.EscapeLine(where), termsafe.EscapeLine(g.Reason))
		}
	}
	if len(plan.Unknown) > 0 && plan.UnacceptedUnknown > 0 {
		fmt.Println(strings.TrimSpace(`
Each unknown is left as it is; name it with --allow-unknown to accept that, or the rollback ends incomplete.`))
	}
}
