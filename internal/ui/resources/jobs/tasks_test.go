// This file covers the Tasks section's projection: what each column says,
// and the two things the section has to be honest about.
//
// The projection is tested directly rather than through the router because
// the conformance harness wires no journal at all, and the section that
// matters here is the one that does not exist without one. What the harness
// DOES prove is the other half, that a deployment with no journal reader
// draws no Tasks tab.
package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// stubJournal is a JournalReader over a fixed answer.
type stubJournal struct {
	entries   []engine.JournalEntry
	truncated bool
	err       error
}

func (s stubJournal) ForJob(context.Context, string, int) ([]engine.JournalEntry, bool, error) {
	return s.entries, s.truncated, s.err
}

// entry builds a journal entry with the fields this projection reads.
func entry(device, node string, seq int, outcome engine.Outcome) engine.JournalEntry {
	return engine.JournalEntry{
		JobID: "job-1", DeviceID: device, NodeID: node, Sequence: seq,
		FQCN: "svc.systemd.restart", TaskName: "restart the thing",
		Outcome:    outcome,
		StartedAt:  time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 9, 12, 10, 0, 2, 500*int(time.Millisecond), time.UTC),
	}
}

func TestTasksSection_ProjectsAnEntryOntoItsColumns(t *testing.T) {
	section := tasksSection(stubJournal{entries: []engine.JournalEntry{
		entry("dev-1", "tasks[0]", 1, engine.OutcomeChanged),
	}}, nil)

	rows, err := section.Rows(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Rows returned %d rows, want 1", len(rows))
	}

	want := view.Cells{
		"step": "1", "device": "dev-1", "task": "restart the thing",
		"action": "svc.systemd.restart", "outcome": "changed", "took": "2.5s", "detail": "",
	}
	for name, expected := range want {
		if got := rows[0].Cells[name]; got != expected {
			t.Errorf("cell %q = %q, want %q", name, got, expected)
		}
	}
	// The device links by id even though the cell shows a name, which is
	// what makes the column walkable rather than something to copy out.
	if rows[0].Refs["device"] != "dev-1" {
		t.Errorf("the device ref is %q, want the device id", rows[0].Refs["device"])
	}
}

// TestTasksSection_NamesTheDeviceRatherThanItsID is the join this section
// takes a second port to do.
//
// A journal entry records a device id and nothing else about the device, so
// without the job's own task rows the DEVICE column renders an opaque
// identifier -- which is the "a list that prints ORGANIZATION: 1 has moved
// the join into the reader's head" failure this UI has a rule about.
func TestTasksSection_NamesTheDeviceRatherThanItsID(t *testing.T) {
	jobs := &stubJobStore{tasks: []dispatch.JobTask{
		{DeviceID: "dev-1", DeviceName: "edge-mad-07"},
	}}
	section := tasksSection(stubJournal{entries: []engine.JournalEntry{
		entry("dev-1", "tasks[0]", 1, engine.OutcomeChanged),
		// A device the job has no task row for, which a redelivered
		// dispatch can produce. It falls back to the id rather than
		// rendering an empty cell.
		entry("dev-ghost", "tasks[0]", 1, engine.OutcomeFailed),
	}}, jobs)

	rows, err := section.Rows(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if rows[0].Cells["device"] != "edge-mad-07" {
		t.Errorf("the device cell is %q, want the name the job dispatched under", rows[0].Cells["device"])
	}
	if rows[1].Cells["device"] != "dev-ghost" {
		t.Errorf("an unknown device rendered %q, want its id rather than a blank cell", rows[1].Cells["device"])
	}
}

// TestTasksSection_SaysWhenItIsShowingLessThanTheWholeRun is the honesty
// property the Note hook was added for.
//
// A table capped at the limit shows a partial record of what happened while
// looking exactly like a complete one, and on an audit trail that is the
// worst available outcome.
func TestTasksSection_SaysWhenItIsShowingLessThanTheWholeRun(t *testing.T) {
	entries := []engine.JournalEntry{entry("dev-1", "tasks[0]", 1, engine.OutcomeRan)}

	full := tasksSection(stubJournal{entries: entries}, nil)
	if note := full.Note(context.Background(), "job-1"); note != "" {
		t.Errorf("a complete run carries the note %q, want none", note)
	}

	capped := tasksSection(stubJournal{entries: entries, truncated: true}, nil)
	note := capped.Note(context.Background(), "job-1")
	if note == "" {
		t.Fatal("a truncated read carries no note, so the page shows a partial run as if it were whole")
	}
	if !strings.Contains(note, "not the whole record") {
		t.Errorf("the note does not say the record is partial: %q", note)
	}
}

// TestTasksSection_AReadFailureIsAnErrorAndNotAnEmptyTable matters because
// the two render identically and mean opposite things: "this job ran
// nothing" and "the journal could not be read".
func TestTasksSection_AReadFailureIsAnErrorAndNotAnEmptyTable(t *testing.T) {
	boom := errors.New("the database is unreachable")
	section := tasksSection(stubJournal{err: boom}, nil)

	if _, err := section.Rows(context.Background(), "job-1"); !errors.Is(err, boom) {
		t.Fatalf("Rows on a failed read returned %v, want the read error", err)
	}
	// And the note stays silent rather than adding a second voice for one
	// failure the section already reports.
	if note := section.Note(context.Background(), "job-1"); note != "" {
		t.Errorf("a failed read also produced the note %q", note)
	}
}

// TestTasksSection_IsAbsentWithoutAJournalReader is the difference between
// an absent tab and an empty one.
//
// Having no journal is a property of the INSTALLATION, so drawing the tab
// would promise something that deployment cannot do for any job. A playbook
// job's blank table is a property of the RECORD, and that one belongs on
// the page, which is what Empty says.
func TestTasksSection_IsAbsentWithoutAJournalReader(t *testing.T) {
	without := sections(nil, nil)
	for _, s := range without {
		if s.Title == tasksTitle {
			t.Fatal("a deployment with no journal reader still draws a Tasks tab")
		}
	}

	with := sections(nil, stubJournal{})
	var found bool
	for _, s := range with {
		if s.Title == tasksTitle {
			found = true
		}
	}
	if !found {
		t.Fatal("a deployment with a journal reader draws no Tasks tab, so the check above proves nothing")
	}
}

// TestTasksSection_EmptyStateExplainsAPlaybookJob pins the sentence, not
// its wording, but the fact it has to carry.
//
// Only the native adapter writes the journal, so a playbook job's table is
// permanently empty. An empty state reading "no tasks" would say the job
// did nothing, which is false and is exactly the ambiguity Section.Empty
// exists to remove.
func TestTasksSection_EmptyStateExplainsAPlaybookJob(t *testing.T) {
	section := tasksSection(stubJournal{}, nil)
	if !strings.Contains(section.Empty, "playbook") {
		t.Errorf("the empty state does not mention a playbook job, so its permanently blank table reads as a job that did nothing: %q", section.Empty)
	}
	if !strings.Contains(section.Empty, "Live output") {
		t.Errorf("the empty state does not send the reader to where a playbook's per-task detail actually is: %q", section.Empty)
	}
}

func TestTasksSection_DetailAndDuration(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*engine.JournalEntry)
		wantDetail   string
		wantDuration string
	}{
		{
			name:         "a failure names the stage it reached",
			mutate:       func(e *engine.JournalEntry) { e.FailureStage = engine.FailureStage("connect") },
			wantDetail:   "failed at connect",
			wantDuration: "2.5s",
		},
		{
			name: "a skip in a loop says which of how many",
			mutate: func(e *engine.JournalEntry) {
				e.SkipKind, e.SkipOrdinal, e.SkipTotal = engine.SkipKind("when"), 2, 5
			},
			wantDetail:   "skipped by when (2 of 5)",
			wantDuration: "2.5s",
		},
		{
			name:         "a skip outside a loop omits the count",
			mutate:       func(e *engine.JournalEntry) { e.SkipKind = engine.SkipKind("when") },
			wantDetail:   "skipped by when",
			wantDuration: "2.5s",
		},
		{
			// A node that never started and one that took no measurable
			// time are different facts, and "0s" is the wrong way to say
			// the first.
			name:         "a node that never started shows no duration",
			mutate:       func(e *engine.JournalEntry) { e.StartedAt, e.FinishedAt = time.Time{}, time.Time{} },
			wantDetail:   "",
			wantDuration: "",
		},
		{
			name: "clock skew is not this column's to adjudicate",
			mutate: func(e *engine.JournalEntry) {
				e.FinishedAt = e.StartedAt.Add(-time.Second)
			},
			wantDetail:   "",
			wantDuration: "",
		},
		{
			name: "a sub-second node reads in milliseconds",
			mutate: func(e *engine.JournalEntry) {
				e.FinishedAt = e.StartedAt.Add(120 * time.Millisecond)
			},
			wantDetail:   "",
			wantDuration: "120ms",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := entry("dev-1", "tasks[0]", 1, engine.OutcomeFailed)
			tc.mutate(&e)
			section := tasksSection(stubJournal{entries: []engine.JournalEntry{e}}, nil)

			rows, err := section.Rows(context.Background(), "job-1")
			if err != nil {
				t.Fatalf("Rows: %v", err)
			}
			if got := rows[0].Cells["detail"]; got != tc.wantDetail {
				t.Errorf("detail = %q, want %q", got, tc.wantDetail)
			}
			if got := rows[0].Cells["took"]; got != tc.wantDuration {
				t.Errorf("took = %q, want %q", got, tc.wantDuration)
			}
		})
	}
}

// TestTasksSection_LabelsFallBackHonestly covers the two columns that have
// something worse than a blank cell to fall back to.
func TestTasksSection_LabelsFallBackHonestly(t *testing.T) {
	e := entry("dev-1", "tasks[3]", 1, engine.OutcomeRan)
	e.TaskName = ""
	e.FQCNUnresolved = true

	section := tasksSection(stubJournal{entries: []engine.JournalEntry{e}}, nil)
	rows, err := section.Rows(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}

	// An unnamed task falls back to its position in the document, which is
	// a worse answer than a name and a much better one than nothing on the
	// column a reader scans first.
	if rows[0].Cells["task"] != "tasks[3]" {
		t.Errorf("an unnamed task rendered %q, want its node id", rows[0].Cells["task"])
	}
	// An unresolved FQCN is marked, because "ran svc.systemd.restart" and
	// "named something this deployment does not have" look identical in a
	// bare column.
	if !strings.Contains(rows[0].Cells["action"], "unresolved") {
		t.Errorf("an unresolved action rendered %q, want it marked", rows[0].Cells["action"])
	}
}

// TestTasksSection_TheCollectionPageShowsNothing guards the parentless
// case: a journal belongs to a job, so the section on the list page must
// not read every entry in the system.
func TestTasksSection_TheCollectionPageShowsNothing(t *testing.T) {
	section := tasksSection(stubJournal{entries: []engine.JournalEntry{
		entry("dev-1", "tasks[0]", 1, engine.OutcomeRan),
	}}, nil)

	rows, err := section.Rows(context.Background(), "")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("the collection page rendered %d journal rows", len(rows))
	}
	if note := section.Note(context.Background(), ""); note != "" {
		t.Errorf("the collection page carries the note %q", note)
	}
}

// stubJobStore answers only Get, which is all deviceNames uses.
type stubJobStore struct {
	dispatch.JobStore
	tasks []dispatch.JobTask
	err   error
}

func (s *stubJobStore) Get(context.Context, string) (*dispatch.Job, []dispatch.JobTask, error) {
	if s.err != nil {
		return nil, nil, s.err
	}
	return &dispatch.Job{}, s.tasks, nil
}

// TestTasksSection_AFailedNameLookupStillRendersTheTable proves the join is
// best effort.
//
// A job whose task rows cannot be read still has a journal worth showing.
// Failing the whole page for the sake of one column would trade everything
// the reader came for against a nicety.
func TestTasksSection_AFailedNameLookupStillRendersTheTable(t *testing.T) {
	jobs := &stubJobStore{err: errors.New("task rows unavailable")}
	section := tasksSection(stubJournal{entries: []engine.JournalEntry{
		entry("dev-1", "tasks[0]", 1, engine.OutcomeRan),
	}}, jobs)

	rows, err := section.Rows(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("Rows failed because the name lookup did: %v", err)
	}
	if len(rows) != 1 || rows[0].Cells["device"] != "dev-1" {
		t.Errorf("rows = %+v, want one row falling back to the device id", rows)
	}
}
