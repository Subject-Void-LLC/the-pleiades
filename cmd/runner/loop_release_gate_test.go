// The Walk tier's Release Gate for loops and variables (Phases 87 and 89):
// a Controller job runs a looped runbook with vars through a real Runner on a
// real sshd, each item is journaled as its own entry in the Controller's
// database, and a rollback undoes the items one by one, newest first.
package main_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/google/uuid"
)

// walkLoopRunbook makes a directory named by a runbook variable and copies
// one file into it per item of a loop.
const walkLoopRunbook = `id: loop-walk
vars:
  base: /tmp/walk-loop
tasks:
  - name: make the directory
    file.directory:
      path: "{{ vars.base }}"
  - name: one file per item
    file.copy:
      dest: "{{ vars.base }}/{{ item }}"
      content: "{{ item }}\n"
    loop: [a, b, c]
    register: copies
`

func TestLoopReleaseGate_ItemsAreJournaledAndUndoneOneByOne(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	files := map[string]string{"loop-walk.yaml": walkLoopRunbook}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, files)
	h.startRollbackAgent(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	dbPath := t.TempDir() + "/controller.db"
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + dbPath})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store := dispatch.NewEntJobStore(client)
	journalStore := journal.NewEntStore(client)

	device := &sshGateDevice{
		Stub: &inventorytest.Stub{
			StubID: "loop-walk-1", StubName: "loop-walk-1", StubState: pkginventory.StateActive,
			Caps:  []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
			Props: map[string]pkginventory.PropertyValue{"host": h.sshHost},
		},
		port: h.sshPort,
	}
	runbookDir := t.TempDir()
	writeRunbooks(t, runbookDir, files)
	runbooks, err := runbook.NewDirSource(runbookDir)
	if err != nil {
		t.Fatalf("runbook source: %v", err)
	}
	worker := dispatch.NewWorker(store, &gateRepository{devices: []pkginventory.InventoryItem{device}}, runbooks, h.bus, gateCredentials{})
	subCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	if err := dispatch.NewResultConsumer(store, quiet).Subscribe(subCtx, h.bus); err != nil {
		t.Fatalf("subscribe the result consumer: %v", err)
	}
	admission := journal.WithAdmission(func(ctx context.Context, jobID, deviceID string) (journal.Admission, error) {
		state, err := store.DispatchState(ctx, jobID, deviceID)
		switch {
		case err != nil:
			return journal.AdmitUnrecorded, err
		case state == dispatch.DispatchSent:
			return journal.AdmitDispatched, nil
		case state == dispatch.DispatchNotSent:
			return journal.AdmitNotDispatched, nil
		default:
			return journal.AdmitUnrecorded, nil
		}
	})
	if err := journal.NewSubscriber(journalStore, quiet, admission).Subscribe(subCtx, h.bus); err != nil {
		t.Fatalf("subscribe the journal consumer: %v", err)
	}
	dispatcher := api.NewDispatcher(runbooks, store, h.bus, api.WithRollback(journalStore,
		func(_ context.Context, ids []string) (map[string]string, error) {
			return map[string]string{"loop-walk-1": "loop-walk-1"}, nil
		}))

	fanOut := func(jobID string) {
		t.Helper()
		evt, err := event.WrapPayload(uuid.New().String(), "job.requested", map[string]string{"job_id": jobID})
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.HandleJobRequested(*evt); err != nil {
			t.Fatalf("HandleJobRequested: %v", err)
		}
		if !jobReaches(store, jobID, "completed", 2*time.Minute) {
			t.Fatalf("job %s never completed; devices:\n%s\nRunner events:\n%s", jobID, jobRows(store, jobID), jobEvents(t, h, jobID))
		}
	}
	journaled := func(jobID string, want int) {
		t.Helper()
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
			if entries, err := journalStore.AllForJob(ctx, jobID); err == nil && len(entries) >= want {
				return
			}
		}
		t.Fatalf("job %s never journaled %d entries", jobID, want)
	}

	h.onDevice(t, "rm -rf /tmp/walk-loop")
	job := &dispatch.Job{RunbookID: "loop-walk", GroupName: "gate", Actor: "release-gate", Kind: "runbook"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	fanOut(job.JobID)
	// The directory and one entry per item: four, not two.
	journaled(job.JobID, 4)
	if got := h.onDevice(t, "cat /tmp/walk-loop/a /tmp/walk-loop/b /tmp/walk-loop/c"); got != "a\nb\nc" {
		t.Fatalf("after the job the files hold %q", got)
	}
	entries, err := journalStore.AllForJob(ctx, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.NodeID)
	}
	if want := "tasks[0] tasks[1].item[0] tasks[1].item[1] tasks[1].item[2]"; strings.Join(ids, " ") != want {
		t.Fatalf("journal node ids %v, want %q", ids, want)
	}

	rollbackID, err := dispatcher.Rollback(ctx, "release-gate", job.JobID, api.RollbackRequest{Mode: collection.ModeExecute})
	if err != nil {
		var refused *api.RollbackRefusedError
		if errors.As(err, &refused) {
			t.Fatalf("rollback refused: %+v", refused.Problems)
		}
		t.Fatalf("rollback: %v", err)
	}
	fanOut(rollbackID)
	journaled(rollbackID, 4)
	if got := h.onDevice(t, "test -e /tmp/walk-loop && echo still-there || echo gone"); got != "gone" {
		t.Errorf("after the rollback the directory is %q", got)
	}

	// The stored rollback rows undo the items newest first, then the directory.
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rows, err := db.Query(`SELECT undoes_node, fqcn, outcome FROM journal_entries WHERE job_id = ? ORDER BY sequence`, rollbackID)
	if err != nil {
		t.Fatal(err)
	}
	var stored []string
	for rows.Next() {
		var node, fqcn, outcome string
		if err := rows.Scan(&node, &fqcn, &outcome); err != nil {
			t.Fatal(err)
		}
		stored = append(stored, fmt.Sprintf("%s %s %s", node, fqcn, outcome))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tasks[1].item[2] file.remove changed", "tasks[1].item[1] file.remove changed",
		"tasks[1].item[0] file.remove changed", "tasks[0] file.remove changed",
	}
	if strings.Join(stored, "|") != strings.Join(want, "|") {
		t.Errorf("stored rollback rows %q, want %q", stored, want)
	}
}
