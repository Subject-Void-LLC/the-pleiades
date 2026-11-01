// The Release Gate for serial: on the Controller (Phase 91): a job running a
// runbook with serial: 2 over four devices dispatches them two at a time,
// the second batch only once both devices of the first have reported, and a
// batch with a failure stops the rollout.
//
// Everything on the path is real, as in the forks window's gate beside it:
// the Controller's fan-out Worker over the real ent store and migrations,
// real NATS JetStream, a real Runner executing each dispatch against a real
// sshd, and the real ResultConsumer pumping the next batch as results land.
// The order is measured on the device: each task appends S as it starts and
// E as it ends to one file, so two batches of two leave S S E E S S E E, and
// anything that let a later batch start early would put an S among the
// first batch's.
package main_test

import (
	"context"
	"fmt"
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

// serialRunbook appends S to /tmp/order-<tag> as each device's task starts
// and E three seconds later as it ends; with fail set, the first task to find
// /tmp/fail-<tag> removes it and fails instead of ending.
func serialRunbook(tag string) string {
	cmd := fmt.Sprintf("echo S >> /tmp/order-%[1]s; sleep 3; if rm /tmp/fail-%[1]s 2>/dev/null; then exit 1; fi; echo E >> /tmp/order-%[1]s", tag)
	return fmt.Sprintf("id: serial-%s\nserial: 2\ntasks:\n  - name: start, hold, end\n    exec.shell:\n      cmd: %q\n", tag, cmd)
}

// TestSerialReleaseGate_TwoAtATimeAndAFailedBatchStops.
func TestSerialReleaseGate_TwoAtATimeAndAFailedBatchStops(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	files := map[string]string{"serial-ok.yaml": serialRunbook("ok"), "serial-fail.yaml": serialRunbook("fail")}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, files)
	ctx := context.Background()

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + t.TempDir() + "/controller.db"})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store := dispatch.NewEntJobStore(client)
	repo := &gateRepository{}
	for _, name := range []string{"serial-d", "serial-c", "serial-b", "serial-a"} {
		repo.devices = append(repo.devices, &sshGateDevice{
			Stub: &inventorytest.Stub{
				StubID: pkginventory.DeviceID(name), StubName: name, StubState: pkginventory.StateActive,
				Caps:  []capability.Name{capability.NameSSHTransport, capability.NameShellExec},
				Props: map[string]pkginventory.PropertyValue{"host": h.sshHost},
			},
			port: h.sshPort,
		})
	}
	runbookDir := t.TempDir()
	writeRunbooks(t, runbookDir, files)
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
	launch := func(tag string) string {
		t.Helper()
		job := &dispatch.Job{RunbookID: "serial-" + tag, GroupName: "gate", Actor: "release-gate"}
		if err := store.Create(ctx, job); err != nil {
			t.Fatalf("%s: Create: %v", tag, err)
		}
		evt, err := event.WrapPayload(uuid.New().String(), "job.requested", map[string]string{"job_id": job.JobID})
		if err != nil {
			t.Fatalf("%s: wrap: %v", tag, err)
		}
		if err := worker.HandleJobRequested(*evt); err != nil {
			t.Fatalf("%s: HandleJobRequested: %v", tag, err)
		}
		return job.JobID
	}

	// Two batches of two, each finishing before the next starts.
	okJob := launch("ok")
	if !jobReaches(store, okJob, "completed", 2*time.Minute) {
		t.Fatalf("ok: job never completed; devices:\n%s\nRunner events:\n%s", jobRows(store, okJob), jobEvents(t, h, okJob))
	}
	if got := deviceFile(t, h, "/tmp/order-ok"); got != "S S E E S S E E" {
		t.Errorf("ok: the device recorded %q, want two batches of two, S S E E S S E E", got)
	}

	// A failure in the first batch: that batch finishes, the second never
	// starts, and its devices are skipped naming the batch.
	// Owned by the login the tasks run as: /tmp is sticky, so a task could
	// not remove a root-owned marker, and no task would fail.
	for _, argv := range [][]string{{"touch", "/tmp/fail-fail"}, {"chown", releaseGateSSHUser, "/tmp/fail-fail"}} {
		if code, _, err := h.sshd.Exec(ctx, argv, tcexec.Multiplexed()); err != nil || code != 0 {
			t.Fatalf("arming the failure (%v): exit %d, %v", argv, code, err)
		}
	}
	failJob := launch("fail")
	if !jobSettles(store, failJob, 2*time.Minute) {
		t.Fatalf("fail: job never settled; devices:\n%s", jobRows(store, failJob))
	}
	if got := deviceFile(t, h, "/tmp/order-fail"); got != "S S E" {
		t.Errorf("fail: the device recorded %q, want the first batch only (S S E: one ended, one failed)", got)
	}
	_, tasks, err := store.Get(ctx, failJob)
	if err != nil {
		t.Fatal(err)
	}
	skipped := 0
	for _, task := range tasks {
		if task.Outcome == dispatch.OutcomeSkipped {
			skipped++
			if !strings.Contains(task.Reason, "serial: batch 1 had a failure") || (task.DeviceName != "serial-c" && task.DeviceName != "serial-d") {
				t.Errorf("fail: %s skipped with %q", task.DeviceName, task.Reason)
			}
		}
	}
	if skipped != 2 {
		t.Errorf("fail: %d devices skipped, want the second batch's 2:\n%s", skipped, jobRows(store, failJob))
	}
}

// jobSettles polls until the job leaves pending and running, reporting
// whether it did before the deadline.
func jobSettles(store dispatch.JobStore, jobID string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if job, err := store.Lookup(context.Background(), jobID); err == nil && job.State != "pending" && job.State != "running" {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// deviceFile reads path on the harness's sshd, its lines joined by spaces.
func deviceFile(t *testing.T, h *releaseGateHarness, path string) string {
	t.Helper()
	code, r, err := h.sshd.Exec(context.Background(), []string{"cat", path}, tcexec.Multiplexed())
	if err != nil || code != 0 {
		t.Fatalf("reading %s on the device: exit %d, %v", path, code, err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(raw)), " ")
}
