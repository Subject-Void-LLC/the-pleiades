// Package main: `pleiades journal`, which lists a project's runs and shows
// one run's journal: what ran, where, how it ended, and what undo each
// change recorded. It is how an operator finds the run id `pleiades
// rollback` takes, and reads what a rollback would undo, without opening
// the JSON Lines files by hand.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// journalUsage is the command's usage.
const journalUsage = "usage: pleiades journal list [--since 2h | --since 2026-10-25T16:00:00Z] [--changed] [--dir DIR] [--json] | pleiades journal show <run-id> [--dir DIR] [--json]"

// runSummary is one run in `pleiades journal list`.
type runSummary struct {
	RunID      string    `json:"run_id"`
	Runbook    string    `json:"runbook"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Tasks      int       `json:"tasks"`
	Changed    int       `json:"changed"`
	Failed     int       `json:"failed"`
	Sealed     bool      `json:"sealed"`
	// RollbackOf is the run this one undoes, when it is a rollback.
	RollbackOf string `json:"rollback_of,omitempty"`
	// RolledBackBy are the rollbacks of this run.
	RolledBackBy []string `json:"rolled_back_by,omitempty"`
	// UndoneInRun counts the undo steps that ran inside this run itself: a
	// block's rollback_on_failure undid changes its own tasks made when one
	// of them failed. Those steps are among Tasks (and Changed, when they
	// changed something), so this says how many of them were undoing.
	UndoneInRun int `json:"undone_in_run,omitempty"`
}

// runShow is `pleiades journal show`'s model.
type runShow struct {
	RunID   string                `json:"run_id"`
	Sealed  bool                  `json:"sealed"`
	Entries []engine.JournalEntry `json:"entries"`
}

// runJournal is `pleiades journal <list|show>`.
func runJournal(args []string) error {
	if len(args) == 0 || asksHelp(args[:1]) {
		return fmt.Errorf("%s: %w", journalUsage, errMissingPositional)
	}
	positionals, rest := splitPositionals(args[1:], map[string]bool{"json": true, "changed": true})
	fs := flag.NewFlagSet("journal", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	since := fs.String("since", "", "list only runs that began this long ago or later (2h, 30m) or at or after this time (RFC 3339)")
	changed := fs.Bool("changed", false, "list only runs that changed something")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if args[0] != "list" && (*since != "" || *changed) {
		return errors.New("--since and --changed filter journal list")
	}
	switch args[0] {
	case "list":
		from, err := sinceTime(*since, time.Now())
		if err != nil {
			return err
		}
		return journalList(*dir, *asJSON, listFilter{since: from, changed: *changed})
	case "show":
		if len(positionals) != 1 {
			return fmt.Errorf("%s: %w", journalUsage, errMissingPositional)
		}
		return journalShow(*dir, positionals[0], *asJSON)
	default:
		return fmt.Errorf("%s: %w: %q", journalUsage, errUnknownCommand, args[0])
	}
}

// listFilter narrows journal list: runs begun at or after since (when it is
// not zero), and only runs that changed something.
type listFilter struct {
	since   time.Time
	changed bool
}

// keeps reports whether s passes the filter.
func (f listFilter) keeps(s runSummary) bool {
	if f.changed && s.Changed == 0 {
		return false
	}
	return f.since.IsZero() || !s.StartedAt.Before(f.since)
}

// sinceTime reads --since: a duration back from now, or a time.
func sinceTime(text string, now time.Time) (time.Time, error) {
	if text == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(text); err == nil {
		if d < 0 {
			return time.Time{}, fmt.Errorf("--since %s: a duration back from now is positive", text)
		}
		return now.Add(-d), nil
	}
	t, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since %q is neither a duration (2h, 30m) nor an RFC 3339 time (2026-10-25T16:00:00Z)", text)
	}
	return t, nil
}

// journalList prints every run in the project that filter keeps, newest
// last.
func journalList(dir string, asJSON bool, filter listFilter) error {
	runs, err := journal.ListRuns(dir)
	if err != nil {
		return err
	}
	rolledBack := map[string][]string{}
	summaries := make([]runSummary, 0, len(runs))
	for _, r := range runs {
		s := runSummary{RunID: r.ID, Sealed: r.Sealed, Tasks: len(r.Entries)}
		for _, e := range r.Entries {
			if s.Runbook == "" {
				s.Runbook = e.DAGID
				// An ad-hoc run is journaled under one id, so its method is
				// what tells one from another.
				if e.DAGID == adhocRunbookID && e.FQCN != "" {
					s.Runbook = adhocRunbookID + " " + e.FQCN
				}
			}
			if !e.StartedAt.IsZero() && (s.StartedAt.IsZero() || e.StartedAt.Before(s.StartedAt)) {
				s.StartedAt = e.StartedAt
			}
			if e.FinishedAt.After(s.FinishedAt) {
				s.FinishedAt = e.FinishedAt
			}
			if e.ActionChanged || e.Outcome == engine.OutcomeChanged {
				s.Changed++
			}
			if e.Outcome == engine.OutcomeFailed {
				s.Failed++
			}
			// An entry naming its own run is a step of a block's
			// rollback_on_failure, not a sign the run is a rollback.
			switch {
			case e.RollbackOf == s.RunID && e.UndoesNode != "" && (e.Outcome == engine.OutcomeChanged || e.Outcome == engine.OutcomeRan):
				// A step that ran, changed or not, is an undo done; the
				// planner reads the same two outcomes as done.
				s.UndoneInRun++
			case e.RollbackOf != "" && e.RollbackOf != s.RunID:
				s.RollbackOf = e.RollbackOf
			}
		}
		if s.RollbackOf != "" {
			rolledBack[s.RollbackOf] = append(rolledBack[s.RollbackOf], s.RunID)
		}
		summaries = append(summaries, s)
	}
	kept := summaries[:0]
	for i := range summaries {
		summaries[i].RolledBackBy = rolledBack[summaries[i].RunID]
		if filter.keeps(summaries[i]) {
			kept = append(kept, summaries[i])
		}
	}
	summaries = kept
	if asJSON {
		return writeJSON(os.Stdout, summaries)
	}
	if len(summaries) == 0 {
		if filter.changed || !filter.since.IsZero() {
			fmt.Println("no run recorded in this project matches")
		} else {
			fmt.Println("no runs recorded in this project")
		}
		return nil
	}
	for _, s := range summaries {
		notes := []string{fmt.Sprintf("%d task(s), %d changed, %d failed", s.Tasks, s.Changed, s.Failed)}
		if !s.Sealed {
			notes = append(notes, "unsealed")
		}
		if s.RollbackOf != "" {
			notes = append(notes, "rollback of "+s.RollbackOf)
		}
		if s.UndoneInRun > 0 {
			notes = append(notes, fmt.Sprintf("undid its own change in the run (%d undo step(s), rollback_on_failure)", s.UndoneInRun))
		}
		if len(s.RolledBackBy) > 0 {
			notes = append(notes, "rolled back by "+strings.Join(s.RolledBackBy, ", "))
		}
		fmt.Printf("%s  %s  %s  %s\n", s.RunID, s.StartedAt.Local().Format(time.DateTime), termsafe.EscapeLine(s.Runbook), strings.Join(notes, "; "))
	}
	return nil
}

// journalShow prints one run's journal.
func journalShow(dir, runID string, asJSON bool) error {
	run, err := journal.ReadRun(dir, runID)
	if err != nil {
		if errors.Is(err, journal.ErrNoSuchRun) {
			return fmt.Errorf("no run %s is recorded in %s; `pleiades journal list` lists the runs", runID, dir)
		}
		return err
	}
	entries := append([]engine.JournalEntry(nil), run.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Sequence < entries[j].Sequence })
	if asJSON {
		return writeJSON(os.Stdout, runShow{RunID: run.ID, Sealed: run.Sealed, Entries: entries})
	}
	names, _ := inventoryNames(dir)
	fmt.Printf("run %s", run.ID)
	if !run.Sealed {
		fmt.Print(" (unsealed: the process may have ended mid-level)")
	}
	fmt.Println()
	for _, e := range entries {
		device := e.DeviceID
		if names != nil {
			if name, ok := names(e.DeviceID); ok {
				device = name
			}
		}
		line := fmt.Sprintf("  %3d %-24s %-16s %-8s %s", e.Sequence, e.NodeID, termsafe.EscapeLine(device), e.Outcome, termsafe.EscapeLine(e.FQCN))
		if e.FailureStage != "" {
			line += " (failed at " + string(e.FailureStage) + ")"
		}
		fmt.Println(line)
		// An undo step (a rollback's, or one a block's rollback_on_failure
		// ran inside this run) says what it undid, and never the undo it
		// recorded in turn: a rollback does not undo an undo, so that line
		// would name a step nothing will ever run.
		if e.UndoesNode != "" {
			undid := "undoes: " + e.UndoesNode
			if e.RollbackOf == run.ID {
				undid += ", inside this run (rollback_on_failure)"
			} else {
				undid += " of run " + e.RollbackOf
			}
			fmt.Printf("        %s\n", termsafe.EscapeLine(undid))
			continue
		}
		if e.InverseFQCN != "" {
			fmt.Printf("        undo: %s%s\n", termsafe.EscapeLine(e.InverseFQCN), undoNote(e))
		}
		switch {
		case e.AuthoredRollbackEmpty:
			fmt.Println("        undo: nothing (the task's rollback: list is empty)")
		case e.AuthoredRollback:
			fmt.Println("        undo: the task's own rollback: list")
		}
	}
	return nil
}

// undoNote renders an entry's recorded undo parameters, and says when the
// undo cannot be replayed as recorded.
func undoNote(e engine.JournalEntry) string {
	var parts []string
	for _, p := range e.InverseParams {
		v, _ := p.Value()
		parts = append(parts, fmt.Sprintf("%s=%v", p.Key, v))
	}
	note := ""
	if len(parts) > 0 {
		note = " " + termsafe.EscapeLine(strings.Join(parts, " "))
	}
	switch {
	case e.InversePartial:
		note += " (partial)"
	case !e.InverseComplete:
		note += " (not recorded in full; a rollback needs a rollback: list or --leave)"
	}
	return note
}
