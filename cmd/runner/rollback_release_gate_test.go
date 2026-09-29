// The Walk tier's Release Gate for rollback (Phase 40): a job changes a
// real device through a real Runner, the Controller plans its rollback
// from the journal that Runner published, and a rollback job run by a
// rollback-capable Runner puts the device back.
//
// Everything on the path is real: the Controller's Worker, ResultConsumer
// and journal consumer (with its dispatch admission) over the real ent
// store through the real migrations, the Dispatcher's rollback planning
// over the stored journal and the real runbook source, real NATS
// JetStream, and a real Runner running the dispatch and the rollback in
// real per-task children against a real sshd. What the gate claims about
// the device it reads from the device (docker exec), and what it claims
// about the journal it reads from the database file with its own SQL
// connection.
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

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/google/uuid"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// walkRollbackRunbook makes a directory, copies a file into it, and edits
// a file whose undo the journal withholds.
const walkRollbackRunbook = `id: rb-walk
tasks:
  - name: make a directory
    file.directory:
      path: /tmp/walk-rb
  - name: copy a file in
    file.copy:
      dest: /tmp/walk-rb/new
      content: "new\n"
  - name: edit a file
    file.line.set:
      path: /tmp/walk-rb.conf
      regexp: "^setting"
      line: "setting = new"
`

// startRollbackAgent adds the rollback pull loop a rollback-capable Runner
// runs beside its others (cmd/runner's main): the rollback consumer, and
// an Agent over the same adapter through routing.RollbackOnly.
func (h *releaseGateHarness) startRollbackAgent(t *testing.T) {
	t.Helper()
	consumer, err := h.js.CreateOrUpdateConsumer(context.Background(), topology.StreamName, topology.RollbackConsumerConfig())
	if err != nil {
		t.Fatalf("rollback consumer: %v", err)
	}
	agent := runner.NewAgent(consumer, routing.RollbackOnly(h.adapter), h.js, lock.NewInProcessManager(), topology.MaxDeliverDefault, nil, nil,
		runner.WithResultReporting(h.bus))
	agentCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = agent.Run(agentCtx) }()
}

// onDevice runs script in the sshd container and returns its output.
func (h *releaseGateHarness) onDevice(t *testing.T, script string) string {
	t.Helper()
	code, r, err := h.sshd.Exec(context.Background(), []string{"sh", "-c", script}, tcexec.Multiplexed())
	if err != nil {
		t.Fatalf("exec %q: %v", script, err)
	}
	out, _ := io.ReadAll(r)
	if code != 0 {
		t.Fatalf("exec %q: exit %d\n%s", script, code, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRollbackReleaseGate_AControllerJobIsUndoneOnItsDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	files := map[string]string{"rb-walk.yaml": walkRollbackRunbook}
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
			StubID: "rb-walk-1", StubName: "rb-walk-1", StubState: pkginventory.StateActive,
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
			return map[string]string{"rb-walk-1": "rb-walk-1"}, nil
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
	const footprint = "cat /tmp/walk-rb.conf; ls -A /tmp/walk-rb 2>/dev/null || echo no-directory"

	// The forward job.
	// Made as the login the Runner uses, which /tmp's sticky bit would
	// otherwise stop replacing the file.
	h.onDevice(t, "rm -rf /tmp/walk-rb /tmp/walk-rb.conf && su -s /bin/sh "+releaseGateSSHUser+" -c \"printf 'setting = old\\n' > /tmp/walk-rb.conf\"")
	job := &dispatch.Job{RunbookID: "rb-walk", GroupName: "gate", Actor: "release-gate", Kind: "runbook"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	fanOut(job.JobID)
	journaled(job.JobID, 3)
	if got := h.onDevice(t, footprint); got != "setting = new\nnew" {
		t.Fatalf("after the job the device holds %q", got)
	}

	// Refused whole while the edit's undo is withheld, and nothing made.
	jobsBefore := client.Job.Query().CountX(ctx)
	_, err = dispatcher.Rollback(ctx, "release-gate", job.JobID, api.RollbackRequest{Mode: collection.ModeExecute})
	var refused *api.RollbackRefusedError
	if !errors.As(err, &refused) || len(refused.Problems) != 1 || refused.Problems[0].Field != "leave" || refused.Problems[0].Value != "tasks[2]" {
		t.Fatalf("the rollback was not refused naming tasks[2]: %v %+v", err, refused)
	}
	if client.Job.Query().CountX(ctx) != jobsBefore {
		t.Fatal("a refused rollback created a job")
	}

	// A check of the rollback changes nothing and journals nothing.
	checkID, err := dispatcher.Rollback(ctx, "release-gate", job.JobID, api.RollbackRequest{Mode: collection.ModeCheck, Leave: []string{"tasks[2]"}})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	fanOut(checkID)
	if got := h.onDevice(t, footprint); got != "setting = new\nnew" {
		t.Errorf("a checked rollback changed the device: %q", got)
	}

	// The rollback.
	rollbackID, err := dispatcher.Rollback(ctx, "release-gate", job.JobID, api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[2]"}})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	fanOut(rollbackID)
	journaled(rollbackID, 2)
	if got := h.onDevice(t, footprint); got != "setting = new\nno-directory" {
		t.Errorf("after the rollback the device holds %q, want the copy and directory gone and the edit left", got)
	}

	// The journal as stored, read with a connection of this test's own.
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rows, err := db.Query(`SELECT job_id, rollback_of, undoes_node, fqcn, outcome FROM journal_entries WHERE job_id IN (?, ?) ORDER BY job_id, sequence`, rollbackID, checkID)
	if err != nil {
		t.Fatal(err)
	}
	var stored []string
	for rows.Next() {
		var jobID, of, node, fqcn, outcome string
		if err := rows.Scan(&jobID, &of, &node, &fqcn, &outcome); err != nil {
			t.Fatal(err)
		}
		if jobID != rollbackID || of != job.JobID {
			t.Errorf("a stored row of job %s undoes %q", jobID, of)
		}
		stored = append(stored, fmt.Sprintf("%s %s %s", node, fqcn, outcome))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"tasks[1] file.remove changed", "tasks[0] file.remove changed"}; strings.Join(stored, "|") != strings.Join(want, "|") {
		t.Errorf("stored rollback rows %q, want %q (and none for the check)", stored, want)
	}

	// Undone in full: a second rollback has nothing left to do.
	_, err = dispatcher.Rollback(ctx, "release-gate", job.JobID, api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[2]"}})
	if !errors.As(err, &refused) || !strings.Contains(refused.Problems[0].Reason, "already been undone") {
		t.Errorf("a second rollback: %v", err)
	}
}
