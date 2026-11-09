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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
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
	// What the check reports: success, as the real rollback below has. The
	// copy inside the directory is still there when the directory's undo
	// asks, its removal only predicted; the first undo's prediction reaches
	// the second's check in the Runner's child (wire.ChildRequest.Predicted,
	// Phase 46a), so the directory is predicted empty rather than refused as
	// not empty, which is what this check reported before.
	if _, tasks, err := store.Get(ctx, checkID); err != nil {
		t.Fatal(err)
	} else if len(tasks) != 1 || tasks[0].Result != dispatch.ResultSucceeded {
		t.Errorf("the checked rollback reported %s; want it to predict the rollback succeeds", jobRows(store, checkID))
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

	// Each forward row names the Collection its method came from (Phase
	// 136b), as the Runner's engine projected it and the Controller stored
	// it: the first-party group at this build's version with the digest the
	// registry this binary links computes, and a complete undo the digest of
	// its declaration.
	assertStoredCollectionIdentity(t, db, job.JobID)

	// And the rollback's steps, which its dispatch carries to the Runner,
	// carry the identity and undo digest each forward row recorded.
	forward, err := journalStore.AllForJob(ctx, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	byNode := map[string]engine.JournalEntry{}
	for _, e := range forward {
		byNode[e.NodeID] = e
	}
	undone, err := store.Lookup(ctx, rollbackID)
	if err != nil || undone.Rollback == nil || len(undone.Rollback.Devices) != 1 || len(undone.Rollback.Devices[0].Steps) == 0 {
		t.Fatalf("the rollback job's plan: %+v %v", undone, err)
	}
	for _, step := range undone.Rollback.Devices[0].Steps {
		e := byNode[step.Node]
		if step.Collection.IsZero() || step.Collection != e.Collection || step.UndoDigest == "" || step.UndoDigest != e.InverseSpecDigest {
			t.Errorf("step %s carries %+v and %q, want its forward row's %+v and %q", step.Node, step.Collection, step.UndoDigest, e.Collection, e.InverseSpecDigest)
		}
	}

	// Undone in full: a second rollback has nothing left to do.
	_, err = dispatcher.Rollback(ctx, "release-gate", job.JobID, api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[2]"}})
	if !errors.As(err, &refused) || !strings.Contains(refused.Problems[0].Reason, "already been undone") {
		t.Errorf("a second rollback: %v", err)
	}

	// A skew accepted by name, end to end (Phase 136b): the job again, its
	// rows then rewritten as a Runner of an older build with other
	// declarations would have published them, which this process, holding
	// one build, cannot run. The Controller refuses the rollback naming
	// allow_skew, plans it once both nodes are named, and the Runner, whose
	// declarations differ from the recorded ones just as the Controller's
	// do, runs each step the plan marked and puts the device back.
	again := &dispatch.Job{RunbookID: "rb-walk", GroupName: "gate", Actor: "release-gate", Kind: "runbook"}
	if err := store.Create(ctx, again); err != nil {
		t.Fatal(err)
	}
	fanOut(again.JobID)
	journaled(again.JobID, 3)
	if got := h.onDevice(t, footprint); got != "setting = new\nnew" {
		t.Fatalf("after the job again the device holds %q", got)
	}
	recordedElsewhere(t, dbPath, again.JobID, "tasks[0]", "tasks[1]")
	skewed := api.RollbackRequest{Mode: collection.ModeExecute}
	_, err = dispatcher.Rollback(ctx, "release-gate", again.JobID, skewed)
	if !errors.As(err, &refused) || len(refused.Problems) != 2 {
		t.Fatalf("a rollback of rows recorded under other declarations: %v %+v, want two problems", err, refused)
	}
	for _, p := range refused.Problems {
		if p.Field != "allow_skew" || p.Value != p.Node || !strings.Contains(p.Reason, `"0.3.9"`) {
			t.Errorf("problem %+v, want it accepted by allow_skew naming its node and the recorded version", p)
		}
	}
	skewed.AllowSkew = []string{"tasks[0]", "tasks[1]"}
	skewID, err := dispatcher.Rollback(ctx, "release-gate", again.JobID, skewed)
	if err != nil {
		t.Fatalf("the rollback with allow_skew: %v", err)
	}
	planned, err := store.Lookup(ctx, skewID)
	if err != nil || planned.Rollback == nil || len(planned.Rollback.Devices) != 1 || len(planned.Rollback.Devices[0].Steps) != 2 {
		t.Fatalf("the skewed rollback's plan: %+v %v", planned, err)
	}
	for _, step := range planned.Rollback.Devices[0].Steps {
		if !step.SkewAccepted || step.Collection.Version != "0.3.9" {
			t.Errorf("step %s carries %+v, want it marked accepted under the recorded identity", step.Node, step)
		}
	}
	fanOut(skewID)
	journaled(skewID, 2)
	if got := h.onDevice(t, footprint); got != "setting = new\nno-directory" {
		t.Errorf("after the skewed rollback the device holds %q, want the copy and directory gone", got)
	}
	if _, tasks, err := store.Get(ctx, skewID); err != nil {
		t.Fatal(err)
	} else if len(tasks) != 1 || tasks[0].Result != dispatch.ResultSucceeded {
		t.Errorf("the skewed rollback reported %s; want the Runner to have run the accepted steps", jobRows(store, skewID))
	}
	// And what the Runner accepted is in the job's log, where an operator
	// reads it: a task.warning for each step, naming the recorded digest.
	logged := jobEvents(t, h, skewID)
	for _, node := range []string{"tasks[0]", "tasks[1]"} {
		if !strings.Contains(logged, "task.warning ok WARNING: node "+node+": the undo of") || !strings.Contains(logged, "sha256:"+strings.Repeat("b", 64)) {
			t.Errorf("the skewed rollback's log does not warn of %s's accepted skew:\n%s", node, logged)
		}
	}
}

// recordedElsewhere rewrites the stored rows of jobID's nodes as an older
// build of other declarations would have recorded them: another version,
// and a Collection and an undo digest no build of this source computes.
// It writes through a connection of its own, as the gate reads.
func recordedElsewhere(t *testing.T, dbPath, jobID string, nodes ...string) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, node := range nodes {
		res, err := db.Exec(`UPDATE journal_entries SET collection_version = ?, collection_manifests = ?, inverse_spec_digest = ? WHERE job_id = ? AND node_id = ?`,
			"0.3.9", "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64), jobID, node)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			t.Fatalf("rewrote %d rows of job %s node %s, want 1", n, jobID, node)
		}
	}
}

// assertStoredCollectionIdentity holds every stored row of jobID to the
// identity of its method's first-party group, as this test binary's
// registry and build compute it, and each complete undo to a digest.
func assertStoredCollectionIdentity(t *testing.T, db *sql.DB, jobID string) {
	t.Helper()
	rows, err := db.Query(`SELECT node_id, fqcn, collection_group, collection_version, collection_program, collection_manifests, inverse_complete, inverse_spec_digest
		FROM journal_entries WHERE job_id = ? ORDER BY sequence`, jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	digestShape := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	seen, undos := 0, 0
	for rows.Next() {
		var node, fqcn string
		var group, version, manifests, undo sql.NullString
		var program, complete bool
		if err := rows.Scan(&node, &fqcn, &group, &version, &program, &manifests, &complete, &undo); err != nil {
			t.Fatal(err)
		}
		seen++
		want, ok := collection.FirstPartyGroupOf(fqcn)
		if !ok {
			t.Fatalf("row %s ran %s, which is in no first-party group", node, fqcn)
		}
		digest, err := collection.GroupDigest(want.Name)
		if err != nil {
			t.Fatal(err)
		}
		if group.String != want.Name || program || version.String != buildinfo.Version() || manifests.String != digest {
			t.Errorf("row %s (%s) stored collection %q %q program=%v %q, want %s at %s with digest %s",
				node, fqcn, group.String, version.String, program, manifests.String, want.Name, buildinfo.Version(), digest)
		}
		if complete {
			undos++
			if !digestShape.MatchString(undo.String) {
				t.Errorf("row %s's complete undo stored inverse_spec_digest %q", node, undo.String)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen == 0 || undos == 0 {
		t.Fatalf("job %s stored %d rows, %d with a complete undo, so the identity was not checked", jobID, seen, undos)
	}
}
