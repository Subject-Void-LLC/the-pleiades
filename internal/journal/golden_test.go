// Package journal_test: the journal line format, pinned by golden files.
//
// The journal has no published schema, so its bytes are the contract: a
// rollback read by a later build plans from a file an earlier build wrote.
// Two files pin it. testdata/golden/entries.jsonl is what this build writes,
// and changes only when someone runs -update-golden on purpose.
// testdata/format-1/entries.jsonl is a frozen copy of the format-1 lines,
// never rewritten by any flag: every later build must still read it.
package journal_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/golden"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// goldenRunID is the one run every golden entry belongs to.
const goldenRunID = "0b6f6c1e-2c1d-4f4e-9a3b-6a7d8e9f0a1b"

// goldenEntries returns fixed entries that together set every field of
// engine.JournalEntry and of engine.InverseParam to a value other than its
// zero, with fixed UTC times, so the file they write is the same bytes on
// every machine.
func goldenEntries() []engine.JournalEntry {
	at := func(sec int) time.Time { return time.Date(2026, 11, 6, 12, 0, sec, 123456789, time.UTC) }
	text, number, yes := "/etc/motd", json.Number("4096"), true
	return []engine.JournalEntry{{
		// A changed task with a complete, recorded undo.
		JobID: "job-7", Attempt: 2, RunID: goldenRunID, Sequence: 1, NodeID: "tasks[0]",
		DAGID: "golden-runbook", DAGVersion: "sha256:" + hex64('a'),
		FQCN: "file.copy", ProviderProgram: "acme-files", ProviderDigest: "sha256:" + hex64('b'),
		TaskName: "copy the banner", Register: "banner", DeviceID: "dev-1",
		StartedAt: at(1), FinishedAt: at(2), RunTick: 41, LeaseRevision: 9, LeaseEpoch: "epoch-2026",
		Outcome: engine.OutcomeChanged, StatKeys: []string{"changed", "inverse"}, UndeclaredStatCount: 1,
		ParamKeys: []string{"content", "dest"}, UndeclaredParamCount: 2,
		InverseFQCN: "file.remove", InverseParamKeys: []string{"path"}, UndeclaredInverseParamCount: 3,
		InverseParams: []engine.InverseParam{
			{Key: "path", Text: &text},
			{Key: "size", Number: &number},
			{Key: "force", Bool: &yes},
			{Key: "names", Texts: []string{"a", "b"}},
		},
		InverseComplete: true, InversePartial: true, DiffRecorded: true, ActionChanged: true,
		AuthoredRollback: true, AuthoredRollbackEmpty: true, Become: "root",
	}, {
		// A failed task whose method name did not resolve, nor its undo's.
		RunID: goldenRunID, Sequence: 2, NodeID: "tasks[1].block[0]", DAGID: "golden-runbook",
		FQCN: engine.FQCNUnregistered, FQCNUnresolved: true, InverseFQCN: engine.FQCNUnregistered,
		InverseFQCNUnresolved: true, StartedAt: at(3), FinishedAt: at(4),
		Outcome: engine.OutcomeFailed, FailureStage: engine.FailureStageRegisterMask,
		StatKeys: []string{}, ParamKeys: []string{}, InverseParamKeys: []string{}, InverseParams: []engine.InverseParam{},
	}, {
		// A skipped step of a rollback of another run.
		RunID: goldenRunID, Sequence: 3, NodeID: "tasks[2].undo[1]", DAGID: "golden-runbook",
		StartedAt: at(5), FinishedAt: at(5), Outcome: engine.OutcomeSkipped,
		SkipKind: engine.SkipKindWhen, SkipOrdinal: 2, SkipTotal: 3,
		StatKeys: []string{}, ParamKeys: []string{}, InverseParamKeys: []string{}, InverseParams: []engine.InverseParam{},
		RollbackOf: "5d1e2f3a-0000-4000-8000-000000000001", UndoesNode: "tasks[0]", UndoesStep: 1,
	}}
}

// hex64 returns 64 copies of c, a digest-shaped hex string.
func hex64(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

// TestGoldenEntriesSetEveryField fails when engine.JournalEntry or
// engine.InverseParam gains a field the golden entries leave zero. A new
// omitempty field would otherwise never reach the golden file, and the
// format would change with no golden diff to review.
func TestGoldenEntriesSetEveryField(t *testing.T) {
	entries := goldenEntries()
	set := func(typ reflect.Type, values []reflect.Value) {
		for i := range typ.NumField() {
			found := false
			for _, v := range values {
				found = found || !v.Field(i).IsZero()
			}
			if !found {
				t.Errorf("no golden entry sets %s.%s: give it a value in goldenEntries and run -update-golden", typ.Name(), typ.Field(i).Name)
			}
		}
	}
	var ev, pv []reflect.Value
	for _, e := range entries {
		ev = append(ev, reflect.ValueOf(e))
		for _, p := range e.InverseParams {
			pv = append(pv, reflect.ValueOf(p))
		}
	}
	set(reflect.TypeFor[engine.JournalEntry](), ev)
	set(reflect.TypeFor[engine.InverseParam](), pv)
}

// TestJournalLinesMatchTheGoldenFormat records the golden entries through
// FileStore.Record and compares the file's bytes with the golden file.
func TestJournalLinesMatchTheGoldenFormat(t *testing.T) {
	store, root := newStore(t)
	if err := store.Record(context.Background(), goldenEntries()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, ".pleiades", "journal", goldenRunID+".jsonl")) // #nosec G304 -- under this test's TempDir
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "golden", "entries.jsonl"), got)
}

// TestJournalFormatOneStaysReadable is the contract: a format-1 journal
// written by an earlier build stays readable by every later one. The file is
// a frozen copy, so -update-golden never touches it. A field added to the
// entry after format 1 reads as its zero from this file; zero it in want
// before comparing rather than editing the file.
func TestJournalFormatOneStaysReadable(t *testing.T) {
	frozen, err := os.ReadFile(filepath.Join("testdata", "format-1", "entries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// The reader refuses a journal others may write, so the copy is made
	// private, inside a private directory, as the writer makes one.
	root := t.TempDir()
	dir := filepath.Join(root, ".pleiades", "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, goldenRunID+".jsonl"), frozen, 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := journal.ReadRun(root, goldenRunID)
	if err != nil {
		t.Fatalf("a format-1 journal no longer reads: %v", err)
	}
	if want := goldenEntries(); !reflect.DeepEqual(run.Entries, want) {
		gotJSON, _ := json.MarshalIndent(run.Entries, "", " ")
		wantJSON, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("a format-1 journal reads differently:\ngot  %s\nwant %s", gotJSON, wantJSON)
	}
}

// TestJournalFormatOneReadsEveryKind holds every later build to read a
// format-1 journal holding each failure stage, skip kind and outcome the
// engine writes, and each shape of node id, handlers' included. The fixture
// is frozen: a build that stops reading one of its lines has broken every
// journal that recorded it (engine.IsRecordedFailureStage's lists only grow).
func TestJournalFormatOneReadsEveryKind(t *testing.T) {
	frozen, err := os.ReadFile(filepath.Join("testdata", "format-1", "every-kind.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".pleiades", "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, goldenRunID+".jsonl"), frozen, 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := journal.ReadRun(root, goldenRunID)
	if err != nil {
		t.Fatalf("a format-1 journal holding every kind no longer reads: %v", err)
	}
	if len(run.Entries) != 28 {
		t.Errorf("read %d entries, want 28", len(run.Entries))
	}
}
