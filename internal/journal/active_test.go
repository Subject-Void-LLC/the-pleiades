// Package journal_test: the record of runs changing devices now, and the
// journal read that finds another run's change during one.
package journal_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// activeDir is where a project's records live.
func activeDir(root string) string { return filepath.Join(root, ".pleiades", "runs", "active") }

// TestActive_AnotherRunSeesALiveRunOnItsDevices: a held record is seen by
// a run sharing a device, by name and runbook, and not by one sharing
// none or by itself; once it ends it is gone, files and all.
func TestActive_AnotherRunSeesALiveRunOnItsDevices(t *testing.T) {
	root := t.TempDir()
	a, err := journal.BeginActive(root, journal.ActiveRun{Runbook: "runbooks/change.yaml", Devices: []journal.ActiveDevice{{ID: "d1", Name: "r1"}, {ID: "d2", Name: "r2"}}})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(activeDir(root)); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("the record directory: %v, %v", info, err)
		}
	}
	got, err := journal.OtherActive(root, "someone-else", []string{"d2", "d9"})
	if err != nil || len(got) != 1 {
		t.Fatalf("OtherActive = %v, %v; want the one live run", got, err)
	}
	if got[0].Runbook != "runbooks/change.yaml" || got[0].PID != os.Getpid() || got[0].Devices[1].Name != "r2" || got[0].ID != a.ID || got[0].StartedAt.IsZero() {
		t.Errorf("record %+v", got[0])
	}
	if got, _ := journal.OtherActive(root, "someone-else", []string{"d9"}); len(got) != 0 {
		t.Errorf("a run on other devices saw %v", got)
	}
	if got, _ := journal.OtherActive(root, a.ID, []string{"d1"}); len(got) != 0 {
		t.Errorf("a run saw itself: %v", got)
	}
	if err := a.End(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(activeDir(root)); len(entries) != 0 {
		t.Errorf("after End the directory holds %v", entries)
	}
	if got, _ := journal.OtherActive(root, "x", []string{"d1"}); len(got) != 0 {
		t.Errorf("an ended run is still seen: %v", got)
	}
	if err := (*journal.Active)(nil).End(); err != nil {
		t.Errorf("ending nothing: %v", err)
	}
	if got, err := journal.OtherActive(t.TempDir(), "x", []string{"d1"}); err != nil || got != nil {
		t.Errorf("a project with no records: %v, %v", got, err)
	}
}

// TestActive_ARecordNobodyHoldsIsADeadRun: a record whose lock nobody
// holds is not a live run; when old it is removed with its lock file, and
// when new (a run between locking and writing) it is left. An old lock
// file with no record is removed; a record that is a link, writable by
// others or not a record is skipped.
func TestActive_ARecordNobodyHoldsIsADeadRun(t *testing.T) {
	root := t.TempDir()
	a, err := journal.BeginActive(root, journal.ActiveRun{Devices: []journal.ActiveDevice{{ID: "d1", Name: "r1"}}})
	if err != nil {
		t.Fatal(err)
	}
	dir := activeDir(root)
	body, err := os.ReadFile(filepath.Join(dir, a.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.End(); err != nil {
		t.Fatal(err)
	}
	// What a killed run leaves: its record and an unlocked lock file.
	old := time.Now().Add(-time.Hour)
	write := func(name string, data []byte, mode os.FileMode, at time.Time) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write(a.ID+".json", body, 0o600, old)
	write(a.ID+".lock", nil, 0o600, old)
	if got, _ := journal.OtherActive(root, "x", []string{"d1"}); len(got) != 0 {
		t.Errorf("a dead run is seen: %v", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a dead run's files were left: %v", entries)
	}

	write(a.ID+".json", body, 0o600, time.Now())
	write(a.ID+".lock", nil, 0o600, time.Now())
	write("orphan.lock", nil, 0o600, old)
	if got, _ := journal.OtherActive(root, "x", []string{"d1"}); len(got) != 0 {
		t.Errorf("a new unheld record is seen: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, a.ID+".json")); err != nil {
		t.Errorf("a new unheld record was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "orphan.lock")); !os.IsNotExist(err) {
		t.Errorf("an old lock file with no record was left: %v", err)
	}

	// A live run's lock, with a record that cannot be trusted beside it.
	live, err := journal.BeginActive(root, journal.ActiveRun{Devices: []journal.ActiveDevice{{ID: "d1", Name: "r1"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.End() }()
	recordPath := filepath.Join(dir, live.ID+".json")
	if runtime.GOOS != "windows" {
		if err := os.Chmod(recordPath, 0o666); err != nil {
			t.Fatal(err)
		}
		if got, _ := journal.OtherActive(root, "x", []string{"d1"}); len(got) != 0 {
			t.Errorf("a record others may write was read: %v", got)
		}
		if err := os.Chmod(recordPath, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(recordPath, []byte(`{"id":"`+live.ID+`","extra":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := journal.OtherActive(root, "x", []string{"d1"}); len(got) != 0 {
		t.Errorf("a record with an unknown key was read: %v", got)
	}
}

// TestChangesDuring_FindsAnotherRunsChangeToTheSameDevices: a change by
// another run to a shared device inside the window is found, with its run,
// runbook and devices; one outside the window, on another device, a task
// that changed nothing, and this run's own are not; and a journal that
// cannot be read is named rather than failing the rest.
func TestChangesDuring_FindsAnotherRunsChangeToTheSameDevices(t *testing.T) {
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	in, before := start.Add(time.Second), start.Add(-time.Minute)
	const (
		self  = "00000000-0000-4000-8000-000000000001"
		other = "00000000-0000-4000-8000-000000000002"
		older = "00000000-0000-4000-8000-000000000003"
	)
	entry := func(run string, seq int, device string, outcome engine.Outcome, at time.Time) engine.JournalEntry {
		return engine.JournalEntry{RunID: run, DAGID: "inject", Sequence: seq, NodeID: "tasks[0]", DAGVersion: digest,
			DeviceID: device, Outcome: outcome, StartedAt: at, FinishedAt: at}
	}
	if err := store.Record(context.Background(), []engine.JournalEntry{
		entry(self, 1, "d1", engine.OutcomeChanged, in),
		entry(other, 1, "d1", engine.OutcomeChanged, in),
		entry(other, 2, "d9", engine.OutcomeChanged, in),
		entry(other, 3, "d2", engine.OutcomeRan, in),
		entry(older, 1, "d1", engine.OutcomeChanged, before),
	}); err != nil {
		t.Fatal(err)
	}
	changes, unread, err := journal.ChangesDuring(root, self, []string{"d1", "d2"}, start, start.Add(time.Minute))
	if err != nil || len(unread) != 0 || len(changes) != 1 {
		t.Fatalf("ChangesDuring = %+v, %v, %v; want the one other run", changes, unread, err)
	}
	if c := changes[0]; c.RunID != other || c.Runbook != "inject" || c.Changes != 1 || len(c.DeviceIDs) != 1 || c.DeviceIDs[0] != "d1" {
		t.Errorf("change %+v", c)
	}

	broken := "00000000-0000-4000-8000-000000000004"
	if err := os.WriteFile(filepath.Join(root, ".pleiades", "journal", broken+".jsonl"), []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changes, unread, err := journal.ChangesDuring(root, self, []string{"d1"}, start, start.Add(time.Minute)); err != nil || len(changes) != 1 || len(unread) != 1 || unread[0] != broken {
		t.Errorf("with an unreadable journal: %+v, %v, %v", changes, unread, err)
	}
	if changes, unread, err := journal.ChangesDuring(t.TempDir(), self, []string{"d1"}, start, start); err != nil || changes != nil || unread != nil {
		t.Errorf("a project with no journal: %v, %v, %v", changes, unread, err)
	}
}

// TestActive_FailuresSayWhatFailed: a project directory that is a file
// cannot hold a record, a record directory that is a file cannot be
// listed, and a journal directory that is a file cannot be read; each is
// an error, never an empty answer. A record whose files are already gone
// ends cleanly, and a lock file that is a directory is no live run's.
func TestActive_FailuresSayWhatFailed(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.BeginActive(file, journal.ActiveRun{}); err == nil {
		t.Error("a project that is a file held a record")
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".pleiades", "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeDir(root), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.OtherActive(root, "x", []string{"d1"}); err == nil {
		t.Error("a record directory that is a file was listed")
	}
	if _, err := journal.BeginActive(root, journal.ActiveRun{}); err == nil {
		t.Error("a record directory that is a file held a record")
	}
	if err := os.MkdirAll(filepath.Join(root, ".pleiades"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".pleiades", "journal"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.ChangesDuring(root, "x", []string{"d1"}, time.Now(), time.Now()); err == nil {
		t.Error("a journal directory that is a file was read")
	}

	root = t.TempDir()
	a, err := journal.BeginActive(root, journal.ActiveRun{Devices: []journal.ActiveDevice{{ID: "d1", Name: "r1"}}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(activeDir(root), a.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(activeDir(root), a.ID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := a.End(); err != nil {
		t.Errorf("ending a run whose record was already gone: %v", err)
	}
	// A record beside a lock "file" that is a directory: nobody's lock.
	if err := os.WriteFile(filepath.Join(activeDir(root), a.ID+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(activeDir(root), a.ID+".lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, _ := journal.OtherActive(root, "x", []string{"d1"}); len(got) != 0 {
		t.Errorf("a record with no lockable lock file was read as live: %v", got)
	}
}

// TestActive_SkipsWhatIsNotARecord: a directory named like a record, and a
// record whose lock file cannot be opened, are not live runs.
func TestActive_SkipsWhatIsNotARecord(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("Unix modes, as an account they bind")
	}
	root := t.TempDir()
	a, err := journal.BeginActive(root, journal.ActiveRun{Devices: []journal.ActiveDevice{{ID: "d1", Name: "r1"}}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(activeDir(root), a.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.End(); err != nil {
		t.Fatal(err)
	}
	dir := activeDir(root)
	if err := os.Mkdir(filepath.Join(dir, "folder.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, a.ID+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, a.ID+".lock"), nil, 0o000); err != nil {
		t.Fatal(err)
	}
	if got, err := journal.OtherActive(root, "x", []string{"d1"}); err != nil || len(got) != 0 {
		t.Errorf("OtherActive = %v, %v; want nothing live", got, err)
	}
}
