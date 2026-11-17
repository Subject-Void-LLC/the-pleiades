// Tests that an entry's answer that its call only read is held to what the
// engine writes: kept on a first-party built-in's failed call, refused by the
// strict reader and cleared by the Walk tier's consumer on any combination no
// build writes (another's method, an engine action, a change).
package journal_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// failedRead is a call of fqcn that failed during its action and that its
// method says only read.
func failedRead(e engine.JournalEntry, fqcn string) engine.JournalEntry {
	e.FQCN, e.Outcome, e.FailureStage, e.ReadOnlyCall = fqcn, engine.OutcomeFailed, engine.FailureStageAction, true
	return e
}

// TestReadRun_RefusesAReadOnlyAnswerNoBuildWrites: a built-in's failed read
// reads; the same answer on an external program's method, an engine action,
// an unregistered or unknown method, a changed task and an action that
// changed the device is refused, naming why.
func TestReadRun_RefusesAReadOnlyAnswerNoBuildWrites(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*engine.JournalEntry)
		wantErr string
	}{
		{"a built-in's failed read", func(*engine.JournalEntry) {}, ""},
		{"an external program's method", func(e *engine.JournalEntry) { e.ProviderProgram = "/c/acme" }, "it names an external Collection program"},
		{"a changed task", func(e *engine.JournalEntry) { e.Outcome, e.FailureStage = engine.OutcomeChanged, "" }, "it changed the device"},
		{"an action that changed the device", func(e *engine.JournalEntry) { e.ActionChanged = true }, "it changed the device"},
		{"an engine action", func(e *engine.JournalEntry) { e.FQCN = "ssh_exec" }, "its method is an engine action"},
		{"an unregistered method", func(e *engine.JournalEntry) { e.FQCN, e.FQCNUnresolved = engine.FQCNUnregistered, true }, "no first-party Collection's"},
		{"another namespace's method", func(e *engine.JournalEntry) { e.FQCN = "acme.show" }, "no first-party Collection's"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, root := newStore(t)
			e := failedRead(entry("run-a", 1, "tasks[0]"), "net.ios.command")
			tc.mutate(&e)
			if err := store.Record(context.Background(), []engine.JournalEntry{e}); err != nil {
				t.Fatalf("Record: %v", err)
			}
			_, err := journal.ReadRun(root, "run-a")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("error %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// TestSubscriber_HoldsAReadOnlyAnswerToWhatTheEngineWrites: a Runner's row
// keeps its answer on a built-in's failed read its node ran, and loses it,
// said at warning, on a change, an engine action, and a row claiming a
// built-in for a node that ran another method.
func TestSubscriber_HoldsAReadOnlyAnswerToWhatTheEngineWrites(t *testing.T) {
	store, _ := newEntStore(t)
	var logged bytes.Buffer
	nodes := &stubNodes{answers: map[string]journal.NodeMethod{
		"tasks[0]": {Resolved: true, Method: "net.ios.command"},
		"tasks[1]": {Resolved: true, Method: "net.ios.command"},
		"tasks[2]": {Resolved: true, Method: "ssh_exec"},
		"tasks[3]": {Resolved: true, Method: "acme.inventory.write"},
	}}
	sub := journal.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)), journal.WithNodeResolver(nodes.resolve))
	honest := failedRead(walkEntry("job-1", "device-1", 1, 1, "tasks[0]"), "net.ios.command")
	changed := failedRead(walkEntry("job-1", "device-1", 1, 2, "tasks[1]"), "net.ios.command")
	changed.Outcome, changed.FailureStage, changed.ActionChanged = engine.OutcomeChanged, "", true
	action := failedRead(walkEntry("job-1", "device-1", 1, 3, "tasks[2]"), "ssh_exec")
	forged := failedRead(walkEntry("job-1", "device-1", 1, 4, "tasks[3]"), "net.ios.command")
	batch := journal.Batch{JobID: "job-1", DeviceID: "device-1", Attempt: 1, Entries: []engine.JournalEntry{honest, changed, action, forged}}
	if err := sub.Handle(batchEvent(t, batch)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	stored, _, err := store.ForJob(context.Background(), "job-1", 0)
	if err != nil || len(stored) != 4 {
		t.Fatalf("stored %d entries, %v", len(stored), err)
	}
	for _, e := range stored {
		if want := e.NodeID == "tasks[0]"; e.ReadOnlyCall != want {
			t.Errorf("%s stored read_only_call %v, want %v", e.NodeID, e.ReadOnlyCall, want)
		}
	}
	if !strings.Contains(logged.String(), "read-only answer") {
		t.Errorf("the cleared answers were not said:\n%s", logged.String())
	}
}

// TestFrozenFormatFour_PinsOnlyAReadABuildWrites: the frozen format-4 file
// holds a read-only answer only where a build writes one, a first-party
// built-in's call that failed during its action, so it pins no combination
// the reader refuses.
func TestFrozenFormatFour_PinsOnlyAReadABuildWrites(t *testing.T) {
	frozen, err := os.ReadFile(filepath.Join("testdata", "format-4", "entries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(frozen), `"read_only_call":true`); n != 1 {
		t.Fatalf("the frozen file holds %d read-only answers, want one", n)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(frozen)), "\n") {
		if strings.Contains(line, `"read_only_call":true`) &&
			(!strings.Contains(line, `"outcome":"failed"`) || !strings.Contains(line, `"failure_stage":"action"`) || !strings.Contains(line, `"provider_program":""`)) {
			t.Errorf("the frozen read-only answer is not a built-in's failed read:\n%s", line)
		}
	}
}
