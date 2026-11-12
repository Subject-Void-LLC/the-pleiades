// The Walk tier's Release Gate for a templated rollback: step (Phase 87): a
// Controller job's step reads a variable a later set_fact changes, the Runner
// journals the step as it rendered it, the Controller stores it and plans the
// rollback from it, and a rollback-capable Runner holds the step to the
// runbook the job ran and runs it, removing what the job made and nothing the
// later value names. The path is the one TestRollbackReleaseGate_
// AControllerJobIsUndoneOnItsDevice proves real: the Worker, the result and
// journal consumers over the real ent store and migrations, the Dispatcher's
// planning, real NATS, a real Runner and a real sshd. The device is read with
// docker exec and the journal with this test's own SQL connection.
package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
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

// walkRenderedRunbook makes a directory a set_fact names, with a rollback:
// step removing the directory the same variable names, then changes it.
const walkRenderedRunbook = `id: rb-rendered
tasks:
  - name: pick
    set_fact:
      dest: /tmp/walk-rr-first
  - name: make it
    file.directory:
      path: "{{ vars.dest }}"
    rollback:
      - name: remove it
        file.remove:
          path: "{{ vars.dest }}"
  - name: pick again
    set_fact:
      dest: /tmp/walk-rr-second
`

func TestRollbackReleaseGate_ATemplatedStepIsUndoneAsTheJobRenderedIt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	files := map[string]string{"rb-rendered.yaml": walkRenderedRunbook}
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
			StubID: "rb-rr-1", StubName: "rb-rr-1", StubState: pkginventory.StateActive,
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
			return map[string]string{"rb-rr-1": "rb-rr-1"}, nil
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
	journaled := func(jobID string, want int) []engine.JournalEntry {
		t.Helper()
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
			if entries, err := journalStore.AllForJob(ctx, jobID); err == nil && len(entries) >= want {
				return entries
			}
		}
		t.Fatalf("job %s never journaled %d entries", jobID, want)
		return nil
	}
	const footprint = "for d in /tmp/walk-rr-first /tmp/walk-rr-second; do test -e $d && echo $d; done; true"

	// The forward job, then a decoy at the later value, which a step
	// rendered again would remove.
	h.onDevice(t, "rm -rf /tmp/walk-rr-first /tmp/walk-rr-second")
	job := &dispatch.Job{RunbookID: "rb-rendered", GroupName: "gate", Actor: "release-gate", Kind: "runbook"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	fanOut(job.JobID)
	journaled(job.JobID, 3)
	h.onDevice(t, "mkdir -p /tmp/walk-rr-second")
	if got := h.onDevice(t, footprint); got != "/tmp/walk-rr-first\n/tmp/walk-rr-second" {
		t.Fatalf("after the job and the decoy the device holds %q", got)
	}

	// The Runner journaled the step as it rendered it, and the Controller
	// stored that, read back with a connection of this test's own.
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var raw sql.NullString
	if err := db.QueryRow(`SELECT authored_steps FROM journal_entries WHERE job_id = ? AND node_id = 'tasks[1]'`, job.JobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var steps []engine.AuthoredStep
	if err := json.Unmarshal([]byte(raw.String), &steps); err != nil || len(steps) != 1 || !steps[0].Complete || len(steps[0].Params) != 1 {
		t.Fatalf("stored authored_steps %q (%v), want the one step complete", raw.String, err)
	}
	if v, _ := steps[0].Params[0].Value(); steps[0].Params[0].Key != "path" || v != "/tmp/walk-rr-first" {
		t.Errorf("stored step value %s=%v, want path=/tmp/walk-rr-first", steps[0].Params[0].Key, v)
	}

	rollbackID, err := dispatcher.Rollback(ctx, "release-gate", job.JobID, api.RollbackRequest{Mode: collection.ModeExecute})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	planned, err := store.Lookup(ctx, rollbackID)
	if err != nil || planned.Rollback == nil || len(planned.Rollback.Devices) != 1 || len(planned.Rollback.Devices[0].Steps) != 1 {
		t.Fatalf("the rollback's plan: %+v %v", planned, err)
	}
	if s := planned.Rollback.Devices[0].Steps[0]; s.Params["path"] != "/tmp/walk-rr-first" || len(s.Rendered) != 1 || s.Rendered[0] != "path" {
		t.Errorf("the planned step %+v does not carry the journaled path as rendered", s)
	}
	fanOut(rollbackID)
	journaled(rollbackID, 1)
	if _, tasks, err := store.Get(ctx, rollbackID); err != nil {
		t.Fatal(err)
	} else if len(tasks) != 1 || tasks[0].Result != dispatch.ResultSucceeded {
		t.Errorf("the rollback reported %s; want the Runner to have run the step", jobRows(store, rollbackID))
	}
	if got := h.onDevice(t, footprint); got != "/tmp/walk-rr-second" {
		t.Errorf("after the rollback the device holds %q, want only the decoy", got)
	}
}
