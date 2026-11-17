// Package journal_test: a rollback's list of runs passes over a run still
// live beside it, whose journal may be half written, and counts every run
// that has ended.
package journal_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestListRunsForRollback_PassesOverALiveRun proves a run in progress, its
// journal ending in half a line as a read caught mid-write sees it, is left
// out of the rollback's list and said to be live, where ListRuns refuses the
// whole project over it; and that once it has sealed it is listed again.
func TestListRunsForRollback_PassesOverALiveRun(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" && runtime.GOOS != "windows" {
		t.Skip("needs a lock that tells a live process from a dead one")
	}
	root := t.TempDir()
	const ended, live = "66666666-6666-4666-8666-666666666666", "77777777-7777-4777-8777-777777777777"
	endedStore, _ := startedRun(t, root, ended)
	if err := endedStore.Seal(ended, time.Now()); err != nil {
		t.Fatal(err)
	}
	liveStore, _ := startedRun(t, root, live)
	path := filepath.Join(root, ".pleiades", "journal", live+".jsonl")
	whole, err := os.ReadFile(path) // #nosec G304 -- this test's own journal
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"format":4,"run_id":"` + live + `","seq`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if !journal.RunLive(root, live) || journal.RunLive(root, ended) {
		t.Fatalf("RunLive: live %v, ended %v", journal.RunLive(root, live), journal.RunLive(root, ended))
	}
	if _, err := journal.ListRuns(root); err == nil {
		t.Fatal("ListRuns read a half-written line")
	}
	runs, err := journal.ListRunsForRollback(root)
	if err != nil {
		t.Fatalf("the rollback's list failed over a live run: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != ended {
		t.Fatalf("the rollback's list = %v, want only the ended run", runs)
	}

	// Once it has ended (its line finished and its seal written) it counts.
	if err := os.WriteFile(path, whole, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Seal(live, time.Now()); err != nil {
		t.Fatal(err)
	}
	if journal.RunLive(root, live) {
		t.Error("a sealed run is said to be live")
	}
	if runs, err := journal.ListRunsForRollback(root); err != nil || len(runs) != 2 {
		t.Errorf("after the seal: %d runs, %v; want both", len(runs), err)
	}
	if journal.RunLive(root, "not a run id") || journal.RunLive(root, "88888888-8888-4888-8888-888888888888") {
		t.Error("a run id with no journal is said to be live")
	}
}
