// The Release Gate for async on a Runner (Phase 90): a job whose runbook runs
// a background task and collects it, and one whose task runs past its async
// limit, dispatched through real NATS JetStream to a real Runner, whose
// per-task child runs each method against a real sshd.
//
// The point is the process boundary: an async task's SSH commands run
// detached on the device (pkg/remoteexec's detach.go), which is a context
// value in the Runner and must be carried to the child on the request
// (wire.ChildRequest.Detach). The gate reads the device: the job's directory
// while it runs, and after the limit, that what the task started is stopped
// and its directory removed.
package main_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/google/uuid"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// asyncRunbooks are the gate's two runbooks: a background job collected by
// async_status, and a task that starts a child and outlives its limit.
var asyncRunbooks = map[string]string{
	"async-background.yaml": "id: async-background\ntasks:\n" +
		"  - name: slow write in the background\n    exec.shell:\n      cmd: sleep 3; echo built >> /tmp/async-order\n    async: 60\n    poll: 0\n    register: build\n" +
		"  - name: meanwhile\n    exec.shell:\n      cmd: echo meanwhile >> /tmp/async-order\n" +
		"  - name: collect\n    async_status:\n      jid: \"{{ result.build.ansible_job_id }}\"\n",
	"async-limit.yaml": "id: async-limit\ntasks:\n" +
		"  - name: too slow\n    exec.shell:\n      cmd: sleep 60 & echo $! > /tmp/async-child; wait; touch /tmp/async-after\n    async: 3\n    poll: 1\n",
}

// TestAsyncReleaseGate_DetachedOnARunner.
func TestAsyncReleaseGate_DetachedOnARunner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, asyncRunbooks)
	ctx := context.Background()

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + t.TempDir() + "/controller.db"})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store := dispatch.NewEntJobStore(client)
	repo := &gateRepository{devices: []pkginventory.InventoryItem{&sshGateDevice{
		Stub: &inventorytest.Stub{
			StubID: "async-a", StubName: "async-a", StubState: pkginventory.StateActive,
			Caps:  []capability.Name{capability.NameSSHTransport, capability.NameShellExec},
			Props: map[string]pkginventory.PropertyValue{"host": h.sshHost},
		},
		port: h.sshPort,
	}}}
	runbookDir := t.TempDir()
	writeRunbooks(t, runbookDir, asyncRunbooks)
	runbooks, err := runbook.NewDirSource(runbookDir)
	if err != nil {
		t.Fatalf("runbook source: %v", err)
	}
	worker := dispatch.NewWorker(store, repo, runbooks, h.bus, gateCredentials{})
	consumer := dispatch.NewResultConsumer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), dispatch.WithResultPump(worker.Pump))
	subCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	if err := consumer.Subscribe(subCtx, h.bus); err != nil {
		t.Fatalf("subscribe the result consumer: %v", err)
	}
	launch := func(id string) string {
		t.Helper()
		job := &dispatch.Job{RunbookID: id, GroupName: "gate", Actor: "release-gate"}
		if err := store.Create(ctx, job); err != nil {
			t.Fatalf("%s: Create: %v", id, err)
		}
		evt, err := event.WrapPayload(uuid.New().String(), "job.requested", map[string]string{"job_id": job.JobID})
		if err != nil {
			t.Fatalf("%s: wrap: %v", id, err)
		}
		if err := worker.HandleJobRequested(*evt); err != nil {
			t.Fatalf("%s: HandleJobRequested: %v", id, err)
		}
		return job.JobID
	}
	sh := func(script string) (int, string) {
		t.Helper()
		code, r, err := h.sshd.Exec(ctx, []string{"sh", "-c", script}, tcexec.Multiplexed())
		if err != nil {
			t.Fatalf("on the device, %q: %v", script, err)
		}
		raw, _ := io.ReadAll(r)
		return code, strings.TrimSpace(string(raw))
	}
	leftovers := "ls -d /root/.pleiades-async-*/job.* /home/*/.pleiades-async-*/job.* /config/.pleiades-async-*/job.* 2>/dev/null"

	// A background job runs while the next task does, and is collected.
	bg := launch("async-background")
	if !jobReaches(store, bg, "completed", 2*time.Minute) {
		t.Fatalf("background: job never completed; devices:\n%s\nRunner events:\n%s", jobRows(store, bg), jobEvents(t, h, bg))
	}
	if got := deviceFile(t, h, "/tmp/async-order"); got != "meanwhile built" {
		t.Errorf("background: the device recorded %q, want meanwhile then built", got)
	}
	if _, left := sh(leftovers); left != "" {
		t.Errorf("background: job directories left on the device: %s", left)
	}

	// Past its limit the task fails, and what it started is stopped on the
	// device: only a detached run in the child can do that.
	limit := launch("async-limit")
	// The command runs detached in the Runner's child, which only the request
	// carrying Detach makes it do: its job directory appears on the device
	// while it runs. (This image's sshd delivers a signal, so a plain command
	// would be stopped at the limit too; the directory is what tells them
	// apart.)
	sawJob := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && !sawJob; time.Sleep(100 * time.Millisecond) {
		_, left := sh(leftovers)
		sawJob = left != ""
	}
	if !sawJob {
		t.Error("limit: no job directory appeared on the device, so the command did not run detached in the Runner's child")
	}
	if !jobSettles(store, limit, 2*time.Minute) {
		t.Fatalf("limit: job never settled; devices:\n%s", jobRows(store, limit))
	}
	// The job finishes; its one task failed, naming the limit.
	if rows := jobRows(store, limit); !strings.Contains(rows, `result="failed"`) || !strings.Contains(rows, "its async limit of 3s ran out") {
		t.Errorf("limit: the task did not fail at its async limit:\n%s", rows)
	}
	stopped := `p=$(cat /tmp/async-child) && { [ ! -d /proc/$p ] || grep -q ') Z ' /proc/$p/stat; }`
	if code, _ := sh(stopped); code != 0 {
		_, ps := sh("ps -ef")
		t.Errorf("limit: the sleep the task started is still running on the device:\n%s\nRunner events:\n%s", ps, jobEvents(t, h, limit))
	}
	if code, _ := sh("test -e /tmp/async-after"); code == 0 {
		t.Error("limit: the stopped task ran on to its end")
	}
	if _, left := sh(leftovers); left != "" {
		t.Errorf("limit: job directories left on the device: %s", left)
	}
}
