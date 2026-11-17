// Package journal_test: the run counter's lock is held around one tick alone,
// never across a run, and a tick that waits too long for it names its holder.
package journal_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestClockLock_ARunInProgressDoesNotHoldIt proves a run that has begun and
// not sealed leaves the counter's lock free: another tick takes it at once,
// with no wait at all, so a long run never holds other runs' ticks back.
func TestClockLock_ARunInProgressDoesNotHoldIt(t *testing.T) {
	root := t.TempDir()
	const run = "55555555-5555-4555-8555-555555555555"
	store, _ := startedRun(t, root, run)
	journal.SetClockLockWait(t, 0)
	release, err := journal.HoldClockLock(root)
	if err != nil {
		t.Fatalf("the counter's lock is held while a run is in progress: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.NextTick(root); err != nil {
		t.Fatalf("a tick while a run is in progress: %v", err)
	}
	if err := store.Seal(run, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// TestClockLock_AWaitThatRunsOutNamesTheHolder proves a tick that gives up
// says which process holds the lock, its command and how long it has held
// it, rather than only that it was held; that the holder's record goes when
// the lock does; and that a holder that left no record is said to be unknown.
func TestClockLock_AWaitThatRunsOutNamesTheHolder(t *testing.T) {
	root := t.TempDir()
	if _, err := journal.TickClock(root); err != nil {
		t.Fatal(err)
	}
	release := holdClockLock(t, root)
	journal.SetClockLockWait(t, 20*time.Millisecond)
	_, err := journal.TickClock(root)
	if err == nil {
		t.Fatal("a tick behind a held lock succeeded")
	}
	// The holder is this test binary, named as commandOf names any process.
	for _, want := range []string{"was held for over", "by process " + strconv.Itoa(os.Getpid()) + " (" + journal.CommandOf(os.Args) + ")", "has held it for", "nothing was run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say %q", err, want)
		}
	}
	holder := filepath.Join(root, ".pleiades", "clock.holder")
	if err := os.Remove(holder); err != nil {
		t.Fatalf("the holder left no record: %v", err)
	}
	if _, err := journal.TickClock(root); err == nil || !strings.Contains(err.Error(), "by a process that did not say which") {
		t.Errorf("a holder with no record: %v", err)
	}
	release()
	if _, err := os.Stat(holder); !os.IsNotExist(err) {
		t.Errorf("the holder's record outlived its lock: %v", err)
	}
	if _, err := journal.TickClock(root); err != nil {
		t.Errorf("a tick once the lock is free: %v", err)
	}
}

// TestClockHolder_NamesTheCommandAndNoArgument proves the holder record names
// the program and its command word and never an argument, which can hold a
// secret.
func TestClockHolder_NamesTheCommandAndNoArgument(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"/usr/local/bin/pleiades", "--dir", "/p", "adhoc", "r1", "net.ios.command", "password=hunter2"}, "pleiades adhoc"},
		{[]string{"pleiades.exe", "run", "runbooks/x.yaml"}, "pleiades.exe run"},
		{[]string{"/tmp/x/journal.test"}, "a process"},
		{[]string{"pleiades", "password=hunter2"}, "pleiades"},
		{nil, ""},
	} {
		if got := journal.CommandOf(tc.args); got != tc.want {
			t.Errorf("CommandOf(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
