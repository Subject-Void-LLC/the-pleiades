// Tests for what a job's failure reason says when its runbook cannot be
// prepared at fan-out (FAILURE_PATTERNS 1360): a runbook that does not
// compile is named with the builder's own message, and a runbook the source
// cannot read still gets the withheld sentence, with no storage detail.
package dispatch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	entjob "github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// brokenPinRunbook is pb-1 as a runbook that parses and does not build: its
// handler takes every device's lock together, which the builder refuses
// naming the handler.
const brokenPinRunbook = "id: pb-1\ntasks:\n  - name: step\n    noop: {}\n    notify: restart\nhandlers:\n  - name: restart\n    lock_acquisition: all_at_plan_time\n    noop: {}\n"

// failedAtFanOut fans one job of pb-1 out through the real worker over p and
// returns the failure reason the job recorded.
func failedAtFanOut(t *testing.T, p *pinSource) string {
	t.Helper()
	ctx := t.Context()
	store := newTestJobStore(t)
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{capableDevice("dev-1", "router", "10.0.0.9")}}
	bus := newCapturingBus()
	worker := dispatch.NewWorker(store, repo, p.src, bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	job, err := store.Lookup(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatal(err)
	}
	if job.State != string(entjob.StateFailed) {
		t.Fatalf("the job is %s (reason %q), want failed", job.State, job.FailureReason)
	}
	if bus.count() != 0 {
		t.Errorf("a job whose runbook could not be prepared published %d events", bus.count())
	}
	return job.FailureReason
}

// TestWorker_HandleJobRequested_ACompileErrorIsTheFailureReason: a runbook
// that stopped building between a job's launch and its fan-out fails the job
// with the builder's message, on one line, so its author can fix it.
func TestWorker_HandleJobRequested_ACompileErrorIsTheFailureReason(t *testing.T) {
	p := newPinSource(t)
	p.write(t, brokenPinRunbook)
	reason := failedAtFanOut(t, p)
	for _, want := range []string{`runbook "pb-1" does not compile`, `handler "restart"`, "lock_acquisition: all_at_plan_time"} {
		if !strings.Contains(reason, want) {
			t.Errorf("failure reason %q does not say %q", reason, want)
		}
	}
	if strings.Contains(reason, p.dir) || strings.ContainsAny(reason, "\n\r") {
		t.Errorf("failure reason %q names the runbook directory or spans lines", reason)
	}
}

// TestWorker_HandleJobRequested_AStorageErrorKeepsItsTextOutOfTheReason: a
// runbook the source cannot read is a storage error, not a compile error, so
// the reason is still the withheld sentence and names no path.
func TestWorker_HandleJobRequested_AStorageErrorKeepsItsTextOutOfTheReason(t *testing.T) {
	p := newPinSource(t)
	path := filepath.Join(p.dir, "pb-1.yaml")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("needs an unprivileged user: root reads a file whatever its mode")
	}
	if reason := failedAtFanOut(t, p); reason != `runbook "pb-1" could not be resolved` {
		t.Errorf("failure reason %q, want the withheld sentence alone", reason)
	}
}
