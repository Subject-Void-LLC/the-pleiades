// Tests for what a rollback reads: the Walk tier's two journal reads, the
// Crawl tier's run lock, and the reader's remaining refusals.
package journal_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestRollbackReads_ReturnWhatAPlanNeedsAndRefuseWhatIsTooMuch covers
// AllForJob and OnDevicesSince over a real SQLite store: every entry of
// the job in order, other jobs' entries on its devices since it began and
// nothing else, device lists split into several queries, and a read past
// its bound refused rather than cut short.
func TestRollbackReads_ReturnWhatAPlanNeedsAndRefuseWhatIsTooMuch(t *testing.T) {
	store, _ := newEntStore(t)
	ctx := context.Background()
	at := func(e engine.JournalEntry, minutes int) engine.JournalEntry {
		e.StartedAt = time.Date(2026, 9, 28, 12, minutes, 0, 0, time.UTC)
		e.FinishedAt = e.StartedAt.Add(time.Second)
		return e
	}
	entries := []engine.JournalEntry{
		at(walkEntry("job-a", "d2", 1, 2, "tasks[1]"), 2),
		at(walkEntry("job-a", "d1", 1, 1, "tasks[0]"), 1),
		at(walkEntry("job-a", "d1", 2, 1, "tasks[0]"), 3),
		at(walkEntry("job-b", "d1", 1, 1, "tasks[0]"), 10), // later, on d1
		at(walkEntry("job-b", "d3", 1, 2, "tasks[1]"), 11), // later, elsewhere
		at(walkEntry("job-c", "d2", 1, 1, "tasks[0]"), 0),  // before job-a began
	}
	for i := range entries {
		entries[i].RunID = fmt.Sprintf("run-%d", i)
	}
	if _, err := store.Save(ctx, entries); err != nil {
		t.Fatalf("Save: %v", err)
	}

	all, err := store.AllForJob(ctx, "job-a")
	if err != nil || len(all) != 3 {
		t.Fatalf("AllForJob = %d entries, %v", len(all), err)
	}
	if all[0].DeviceID != "d1" || all[0].Attempt != 1 || all[1].Attempt != 2 || all[2].DeviceID != "d2" {
		t.Errorf("AllForJob is not in device, attempt, sequence order: %+v", all)
	}
	if _, err := journal.AllForJobWithin(store, ctx, "job-a", 2); !errors.Is(err, journal.ErrTooMuchToPlan) {
		t.Errorf("a job past the bound: %v, want ErrTooMuchToPlan", err)
	}

	since := time.Date(2026, 9, 28, 12, 1, 0, 0, time.UTC)
	others, err := store.OnDevicesSince(ctx, "job-a", []string{"d1", "d2"}, since)
	if err != nil || len(others) != 1 || others[0].JobID != "job-b" || others[0].DeviceID != "d1" {
		t.Fatalf("OnDevicesSince = %+v, %v, want only job-b's entry on d1", others, err)
	}
	// One device per query reads the same.
	if chunked, err := journal.OnDevicesSinceWithin(store, ctx, "job-a", []string{"d1", "d2", "d3"}, since, 10, 1); err != nil || len(chunked) != 2 {
		t.Errorf("chunked one device per query: %d entries, %v, want job-b's two", len(chunked), err)
	}
	if _, err := journal.OnDevicesSinceWithin(store, ctx, "job-a", []string{"d1", "d3"}, since, 1, 1); !errors.Is(err, journal.ErrTooMuchToPlan) {
		t.Errorf("other jobs past the bound: %v, want ErrTooMuchToPlan", err)
	}
	if none, err := store.OnDevicesSince(ctx, "job-a", nil, since); err != nil || len(none) != 0 {
		t.Errorf("no devices: %v, %v", none, err)
	}
}

// TestLockRuns_ARollbackExcludesRunsAndRunsExcludeARollback covers the run
// lock both ways, and that neither waits.
func TestLockRuns_ARollbackExcludesRunsAndRunsExcludeARollback(t *testing.T) {
	root := t.TempDir()
	first, err := journal.LockRuns(root, false)
	if err != nil {
		t.Fatalf("a first run: %v", err)
	}
	second, err := journal.LockRuns(root, false)
	if err != nil {
		t.Fatalf("a second run beside it: %v", err)
	}
	if _, err := journal.LockRuns(root, true); !errors.Is(err, journal.ErrRunsBusy) || !strings.Contains(err.Error(), "a run is in progress") {
		t.Errorf("a rollback while runs hold the lock: %v", err)
	}
	for _, release := range []func() error{first, second} {
		if err := release(); err != nil {
			t.Fatal(err)
		}
	}
	rollback, err := journal.LockRuns(root, true)
	if err != nil {
		t.Fatalf("a rollback once the runs ended: %v", err)
	}
	if _, err := journal.LockRuns(root, false); !errors.Is(err, journal.ErrRunsBusy) || !strings.Contains(err.Error(), "a rollback is in progress") {
		t.Errorf("a run during a rollback: %v", err)
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if release, err := journal.LockRuns(root, false); err != nil {
		t.Errorf("a run after the rollback: %v", err)
	} else {
		_ = release()
	}

	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		readOnly := t.TempDir()
		if err := os.Chmod(readOnly, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
		if _, err := journal.LockRuns(readOnly, false); err == nil || errors.Is(err, journal.ErrRunsBusy) {
			t.Errorf("a project the lock cannot be made in: %v", err)
		}
	}
}

// TestReadRun_RefusesTheRestOfWhatNoRunWrote covers the fields the
// earlier table leaves out, and ListRuns over nothing and over a bad file.
func TestReadRun_RefusesTheRestOfWhatNoRunWrote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes and owners")
	}
	valid := `{"run_id":"` + readerRun + `","sequence":1,"node_id":"tasks[0]","dag_version":"` + digest + `","outcome":"changed","failure_stage":"","skip_kind":""}`
	for _, tc := range []struct{ name, line, want string }{
		{"a made-up undone node", strings.Replace(valid, `"sequence":1`, `"sequence":1,"undoes_node":"x;y"`, 1), "undoes node"},
		{"a made-up failure stage", strings.Replace(valid, `"failure_stage":""`, `"failure_stage":"nowhere"`, 1), "failure stage"},
		{"a made-up skip kind", strings.Replace(valid, `"skip_kind":""`, `"skip_kind":"because"`, 1), "skip kind"},
		{"a made-up undone run", strings.Replace(valid, `"sequence":1`, `"sequence":1,"rollback_of":"../x"`, 1), "undoes run"},
		{"two values on a line", valid + " {}", "more than one value"},
		{"not JSON", "}", "not a journal entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writtenRun(t)
			if err := os.WriteFile(journalFile(root), []byte(tc.line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := journal.ReadRun(root, readerRun); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ReadRun = %v, want a refusal mentioning %q", err, tc.want)
			}
			if _, err := journal.ListRuns(root); err == nil {
				t.Error("ListRuns read past a journal no run wrote")
			}
		})
	}

	root := writtenRun(t)
	if err := os.WriteFile(filepath.Join(root, ".pleiades", "journal", readerRun+".end"), []byte(`{"run_id":"another"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ReadRun(root, readerRun); err == nil || !strings.Contains(err.Error(), "seal") {
		t.Errorf("a seal naming another run: %v", err)
	}
	if _, err := journal.ReadRun(root, "../../etc/passwd"); err == nil {
		t.Error("a run id naming another file was read")
	}
	if runs, err := journal.ListRuns(t.TempDir()); err != nil || runs != nil {
		t.Errorf("a project with no journal: %v, %v", runs, err)
	}
	bad := writtenRun(t)
	if err := os.Chmod(filepath.Join(bad, ".pleiades", "journal"), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ListRuns(bad); err == nil {
		t.Error("ListRuns read a journal directory anyone may write")
	}
}

// TestListRuns_OrdersRunsByWhenTheyBegan covers the order journal list
// and the planner read runs in, whatever order the directory holds them.
func TestListRuns_OrdersRunsByWhenTheyBegan(t *testing.T) {
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"cccccccc-0000-4000-8000-000000000000", "aaaaaaaa-0000-4000-8000-000000000000", "bbbbbbbb-0000-4000-8000-000000000000"}
	for i, id := range ids {
		began := time.Date(2026, 9, 28, 12, i, 0, 0, time.UTC)
		if err := store.Record(context.Background(), []engine.JournalEntry{{RunID: id, Sequence: 1, NodeID: "tasks[0]", Outcome: engine.OutcomeRan, StartedAt: began, FinishedAt: began}}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := journal.ListRuns(root)
	if err != nil || len(runs) != 3 {
		t.Fatalf("ListRuns = %d, %v", len(runs), err)
	}
	for i, id := range ids {
		if runs[i].ID != id {
			t.Errorf("run %d is %s, want %s: runs are listed by when they began", i, runs[i].ID, id)
		}
	}
}

// TestReadRun_RefusesAFileNoRunCouldHaveWritten covers the reader's file
// checks: a directory where the journal should be, a journal larger than
// any run writes, a journal directory that is a file, and a seal others
// may read.
func TestReadRun_RefusesAFileNoRunCouldHaveWritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes and owners")
	}
	dirInstead := writtenRun(t)
	if err := os.Remove(journalFile(dirInstead)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(journalFile(dirInstead), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ReadRun(dirInstead, readerRun); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a directory in the journal's place: %v", err)
	}

	huge := writtenRun(t)
	if err := os.Truncate(journalFile(huge), 64<<20+1); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ReadRun(huge, readerRun); err == nil || !strings.Contains(err.Error(), "more than any run writes") {
		t.Errorf("a journal past the size bound: %v", err)
	}

	fileInstead := t.TempDir()
	if err := os.Mkdir(filepath.Join(fileInstead, ".pleiades"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fileInstead, ".pleiades", "journal"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ReadRun(fileInstead, readerRun); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("a journal directory that is a file: %v", err)
	}

	openSeal := writtenRun(t)
	if err := os.Chmod(filepath.Join(openSeal, ".pleiades", "journal", readerRun+".end"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ReadRun(openSeal, readerRun); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Errorf("a seal others may read: %v", err)
	}
}

// TestSeal_ReportsASealItCannotMake covers a journal directory the seal
// cannot be created in.
func TestSeal_ReportsASealItCannotMake(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions that bind this user")
	}
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".pleiades", "journal")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := store.Seal(readerRun, time.Now()); err == nil || !strings.Contains(err.Error(), "failed to create the seal") {
		t.Errorf("Seal in a directory it cannot write: %v", err)
	}
}

// TestRollbackReads_ReportAStoreThatCannotBeRead covers the reads' own
// failures and a project with no journal at all.
func TestRollbackReads_ReportAStoreThatCannotBeRead(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:"+filepath.Join(t.TempDir(), "closed.db")+"?_fk=1")
	store := journal.NewEntStore(client)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.AllForJob(ctx, "job-a"); err == nil || errors.Is(err, journal.ErrTooMuchToPlan) {
		t.Errorf("AllForJob on a closed store: %v", err)
	}
	if _, err := store.OnDevicesSince(ctx, "job-a", []string{"d1"}, time.Now()); err == nil || errors.Is(err, journal.ErrTooMuchToPlan) {
		t.Errorf("OnDevicesSince on a closed store: %v", err)
	}
	if _, err := journal.ReadRun(t.TempDir(), readerRun); err == nil || !strings.Contains(err.Error(), "journal directory") {
		t.Errorf("a project with no journal: %v", err)
	}
}
