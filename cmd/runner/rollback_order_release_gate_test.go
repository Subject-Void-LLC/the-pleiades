// The Walk tier's Release Gate for ordering jobs without a clock (Phase
// 40a): two Runners, each with its own NATS lock manager on the one bucket,
// as cmd/runner's main has it, run two jobs on one device in turn. The later
// job's journal is then made to read as a Runner whose clock ran a day behind
// would have written it, and the Controller still refuses to undo the first
// job beneath the second, because it orders them by their device leases'
// fencing revisions, not by their times.
//
// Everything on the path is real: real NATS JetStream and its KV lock
// bucket, two real Runners (Agents over the real native adapter) running in
// real per-task children against a real sshd, the Controller's Worker,
// ResultConsumer and journal consumer over the real ent store through the
// real migrations, and the Dispatcher's rollback planning. The journal is
// read, and skewed, with the test's own SQL connection.
package main_test

import (
	"context"
	"database/sql"
	"errors"
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
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/google/uuid"
)

// The two jobs: the first makes a directory, the second puts a file in it.
const (
	orderFirstRunbook = `id: order-first
tasks:
  - name: make a directory
    file.directory:
      path: /tmp/walk-order
`
	orderSecondRunbook = `id: order-second
tasks:
  - name: put a file in it
    file.copy:
      dest: /tmp/walk-order/later
      content: "later\n"
`
)

// startGateRunner starts a Runner pulling the harness's dispatches with a
// NATS lock manager of its own, and returns the function that stops it and
// waits until it has.
func startGateRunner(t *testing.T, h *releaseGateHarness) func() {
	t.Helper()
	locks, err := lock.NewNatsLockManager(context.Background(), h.natsURL, nil, topology.StreamProvisioner)
	if err != nil {
		t.Fatalf("lock manager: %v", err)
	}
	agent := runner.NewAgent(h.dispatches, h.adapter, h.js, locks, topology.MaxDeliverDefault, nil, nil,
		runner.WithResultReporting(h.bus))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = agent.Run(ctx)
	}()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		<-done
		_ = locks.Close()
	}
	t.Cleanup(stop)
	return stop
}

func TestRollbackReleaseGate_ALaterJobFromASkewedRunnerStillRefuses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	files := map[string]string{"order-first.yaml": orderFirstRunbook, "order-second.yaml": orderSecondRunbook}
	h := newReleaseGateHarnessBare(t, knownHostsInEnvironment, files)
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
			StubID: "order-1", StubName: "order-1", StubState: pkginventory.StateActive,
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
			return map[string]string{"order-1": "order-1"}, nil
		}))

	runJob := func(runbookID string) string {
		t.Helper()
		job := &dispatch.Job{RunbookID: runbookID, GroupName: "gate", Actor: "release-gate", Kind: "runbook"}
		if err := store.Create(ctx, job); err != nil {
			t.Fatal(err)
		}
		evt, err := event.WrapPayload(uuid.New().String(), "job.requested", map[string]string{"job_id": job.JobID})
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.HandleJobRequested(*evt); err != nil {
			t.Fatalf("HandleJobRequested: %v", err)
		}
		if !jobReaches(store, job.JobID, "completed", 2*time.Minute) {
			t.Fatalf("job %s never completed; devices:\n%s\nRunner events:\n%s", job.JobID, jobRows(store, job.JobID), jobEvents(t, h, job.JobID))
		}
		for deadline := time.Now().Add(time.Minute); ; time.Sleep(200 * time.Millisecond) {
			if entries, err := journalStore.AllForJob(ctx, job.JobID); err == nil && len(entries) >= 1 {
				return job.JobID
			}
			if time.Now().After(deadline) {
				t.Fatalf("job %s never journaled", job.JobID)
			}
		}
	}

	h.onDevice(t, "rm -rf /tmp/walk-order")
	// Runner A runs the first job, and stops; Runner B runs the second.
	stopA := startGateRunner(t, h)
	first := runJob("order-first")
	stopA()
	startGateRunner(t, h)
	second := runJob("order-second")
	if got := h.onDevice(t, "cat /tmp/walk-order/later"); got != "later" {
		t.Fatalf("after both jobs the device holds %q", got)
	}

	// Runner B's clock ran a day behind: its rows read as written before the
	// first job began, which is what such a Runner would have recorded.
	db, err := sql.Open("sqlite3", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	skewJournal(t, db, second, -24*time.Hour)
	firstLow, _, firstStart, _ := jobOrder(t, db, first)
	_, secondHigh, _, secondFinish := jobOrder(t, db, second)
	if firstLow == 0 || secondHigh <= firstLow {
		t.Fatalf("lease revisions: first job %d, second %d; want both stamped and the second above the first", firstLow, secondHigh)
	}
	if !secondFinish.Before(firstStart) {
		t.Fatalf("the skew did not take: the second job finished at %s, the first began at %s", secondFinish, firstStart)
	}

	_, err = dispatcher.Rollback(ctx, "release-gate", first, api.RollbackRequest{Mode: collection.ModeExecute})
	var refused *api.RollbackRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("undoing the first job beneath the second was not refused: %v", err)
	}
	named := false
	for _, p := range refused.Problems {
		if strings.Contains(p.Reason, second) && p.Field == "despite_job" && p.Value == second {
			named = true
		}
		if strings.Contains(p.Reason, "clock") {
			t.Errorf("a refusal between two jobs that both carry lease revisions spoke of clocks: %q", p.Reason)
		}
	}
	if !named {
		t.Errorf("the refusal did not name the later job %s with despite_job: %+v", second, refused.Problems)
	}
	if got := h.onDevice(t, "cat /tmp/walk-order/later"); got != "later" {
		t.Errorf("a refused rollback changed the device: %q", got)
	}
}

// skewJournal moves every time job's stored rows hold by d, as a Runner whose
// clock was off by d would have written them.
func skewJournal(t *testing.T, db *sql.DB, job string, d time.Duration) {
	t.Helper()
	rows, err := db.Query(`SELECT id, started_at, finished_at FROM journal_entries WHERE job_id = ?`, job)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id              int64
		started, finish time.Time
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.started, &r.finish); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if len(all) == 0 {
		t.Fatalf("job %s has no rows to skew", job)
	}
	for _, r := range all {
		if _, err := db.Exec(`UPDATE journal_entries SET started_at = ?, finished_at = ? WHERE id = ?`, r.started.Add(d), r.finish.Add(d), r.id); err != nil {
			t.Fatal(err)
		}
	}
}

// jobOrder returns what job's stored rows record of its order: the lowest
// and highest lease revision, the earliest start and the latest finish.
func jobOrder(t *testing.T, db *sql.DB, job string) (low, high uint64, firstStart, lastFinish time.Time) {
	t.Helper()
	rows, err := db.Query(`SELECT lease_revision, started_at, finished_at FROM journal_entries WHERE job_id = ?`, job)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var revision uint64
		var started, finished time.Time
		if err := rows.Scan(&revision, &started, &finished); err != nil {
			t.Fatal(err)
		}
		if low == 0 || revision < low {
			low = revision
		}
		high = max(high, revision)
		if firstStart.IsZero() || started.Before(firstStart) {
			firstStart = started
		}
		if finished.After(lastFinish) {
			lastFinish = finished
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return low, high, firstStart, lastFinish
}
