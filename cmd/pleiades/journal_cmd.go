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
const journalUsage = "usage: pleiades journal list [--dir DIR] [--json] | pleiades journal show <run-id> [--dir DIR] [--json]"

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
}

// runShow is `pleiades journal show`'s model.
type runShow struct {
	RunID   string                `json:"run_id"`
	Sealed  bool                  `json:"sealed"`
	Entries []engine.JournalEntry `json:"entries"`
}

// runJournal is `pleiades journal <list|show>`.
func runJournal(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s: %w", journalUsage, errMissingPositional)
	}
	positionals, rest := splitPositionals(args[1:], map[string]bool{"json": true})
	fs := flag.NewFlagSet("journal", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return journalList(*dir, *asJSON)
	case "show":
		if len(positionals) != 1 {
			return fmt.Errorf("%s: %w", journalUsage, errMissingPositional)
		}
		return journalShow(*dir, positionals[0], *asJSON)
	default:
		return fmt.Errorf("%s: %w: %q", journalUsage, errUnknownCommand, args[0])
	}
}

// journalList prints every run in the project, newest last.
func journalList(dir string, asJSON bool) error {
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
			if e.RollbackOf != "" {
				s.RollbackOf = e.RollbackOf
			}
		}
		if s.RollbackOf != "" {
			rolledBack[s.RollbackOf] = append(rolledBack[s.RollbackOf], s.RunID)
		}
		summaries = append(summaries, s)
	}
	for i := range summaries {
		summaries[i].RolledBackBy = rolledBack[summaries[i].RunID]
	}
	if asJSON {
		return writeJSON(os.Stdout, summaries)
	}
	if len(summaries) == 0 {
		fmt.Println("no runs recorded in this project")
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
		if e.InverseFQCN != "" {
			fmt.Printf("        undo: %s%s\n", termsafe.EscapeLine(e.InverseFQCN), undoNote(e))
		}
		if e.AuthoredRollback {
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
