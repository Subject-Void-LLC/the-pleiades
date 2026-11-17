// Gate for adhoc reads and a rollback in one project, through the real binary
// against a scripted IOS router over real SSH: an adhoc call that only reads
// takes no run lock, so it runs while a rollback holds the lock and never
// refuses one, while a call that changes the device is still refused; and a
// rollback plans past a read still running beside it, whose journal may be
// half written. In the 2026-11-17 CML window two adhoc pollers of reads made
// every rollback refuse "another run or rollback holds this project's run
// lock".
package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestCLI_AdhocReadsAndARollbackNeverRefuseEachOther is the gate.
func TestCLI_AdhocReadsAndARollbackNeverRefuseEachOther(t *testing.T) {
	srv := readsRouter(t)
	dir := t.TempDir()
	home := readsHome(t, srv, srv.HostKey)
	for _, args := range [][]string{
		{"init"},
		{"add-host", "r1", "--type", "cisco_router", "--set", "host=" + srv.Host, "--set", "port=" + strconv.Itoa(srv.Port)},
	} {
		if out, err := runPleiades(t, dir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := runPleiadesWithStdin(t, dir, srv.Password+"\n", "add-credential", "r1", "--username", srv.Username, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	// The run a rollback will plan for.
	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", readsRunbook(t, dir, "clear", "  - name: clear\n    net.ios.command:\n      command: clear counters\n      prompts:\n        - match: 'confirm\\]'\n          answer: \"y\"\n    rollback: []\n"))
	if code != 0 {
		t.Fatalf("the run to roll back: exit %d\n%s", code, raw)
	}
	target := rep.RunID

	// While a rollback holds the lock, a read runs, and a change is refused.
	release, err := journal.LockRuns(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "r1", "net.ios.command", "command=show version"); code != 0 {
		t.Errorf("a read while a rollback holds the lock: exit %d\n%s", code, raw)
	}
	if _, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "r1", "net.ios.command", "command=clear counters"); code == 0 || !strings.Contains(raw, "a rollback is in progress") {
		t.Errorf("a change while a rollback holds the lock: exit %d\n%s", code, raw)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}

	// A read still running beside the rollback, caught mid-write, as a poller
	// of reads is: a run that took no lock, its journal ending in half a line.
	reading := beginLiveRead(t, dir)
	if rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "rollback", target); code != 0 || rep.Outcome.Status != "complete" || strings.Contains(raw, reading) {
		t.Errorf("a rollback beside a live read: exit %d\n%s", code, raw)
	}
	if _, raw, code := runPleiadesJSONWithHome(t, dir, home, "rollback", reading); code == 0 || !strings.Contains(raw, "is still running") {
		t.Errorf("a rollback of the live read itself: exit %d\n%s", code, raw)
	}
}

// beginLiveRead starts, in this process, a run that only read and has not
// ended: begun on the project's counter (so its marker is held), one read
// journaled, and half of a second line written. It returns the run's id.
func beginLiveRead(t *testing.T, dir string) string {
	t.Helper()
	const runID = "99999999-9999-4999-8999-999999999999"
	store, err := journal.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginRun(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Seal(runID, time.Now()) })
	now := time.Now().UTC()
	entry := engine.JournalEntry{RunID: runID, Sequence: 1, NodeID: "tasks[0]", FQCN: "net.ios.command", DeviceID: "r1",
		Outcome: engine.OutcomeRan, ReadOnlyCall: true, StartedAt: now, FinishedAt: now}
	if err := store.Record(context.Background(), []engine.JournalEntry{entry}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".pleiades", "journal", runID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0) // #nosec G304 -- this test's own journal
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(`{"format":4,"run_id":"` + runID + `","seq`); err != nil {
		t.Fatal(err)
	}
	return runID
}
