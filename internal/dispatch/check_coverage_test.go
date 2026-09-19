// Package dispatch_test: whether a check job covered everything it
// targeted, from the results its Runners report.
package dispatch_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// runningCheck is runningJob for a check: the job records mode check,
// dispatched lists the devices handed off, and skipped the ones fan-out
// passed over.
func runningCheck(t *testing.T, store dispatch.JobStore, mode string, dispatched []string, skipped ...string) string {
	t.Helper()
	ctx := t.Context()
	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher", Fields: launch.Fields{launch.ModeField: mode}}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}
	for _, id := range dispatched {
		if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{DeviceID: id, DeviceName: id, Outcome: dispatch.OutcomeDispatched}); err != nil {
			t.Fatalf("RecordTask(%s): %v", id, err)
		}
	}
	for _, id := range skipped {
		if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{DeviceID: id, DeviceName: id, Outcome: dispatch.OutcomeSkipped, Reason: "not active"}); err != nil {
			t.Fatalf("RecordTask(%s): %v", id, err)
		}
	}
	if err := store.SettleRunning(ctx, job.JobID, fence, len(dispatched), len(skipped), 0); err != nil {
		t.Fatalf("SettleRunning: %v", err)
	}
	return job.JobID
}

// checkResult is the event a Runner publishes for one device of a check.
func checkResult(t *testing.T, jobID, deviceID, outcome, reason string, unchecked int) event.Event {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"id": jobID + ":" + deviceID, "job_id": jobID, "device_id": deviceID,
		"outcome": outcome, "reason": reason, "unchecked": unchecked,
	})
	if err != nil {
		t.Fatal(err)
	}
	return event.Event{ID: jobID + ":" + deviceID, Data: body}
}

// TestCheckCoverage_FromTheRunnersResults drives the real result consumer
// over the real store and reads coverage off the stored job: a check is
// complete only when every device it targeted was checked, successfully,
// with nothing left unchecked; it is undecided while running; and a real
// run has no coverage to report whatever its results say.
func TestCheckCoverage_FromTheRunnersResults(t *testing.T) {
	type report struct {
		device, outcome string
		unchecked       int
	}
	for _, tc := range []struct {
		name          string
		mode          string
		skipped       []string
		reports       []report
		wantDecided   bool
		wantComplete  bool
		wantUnchecked int
	}{
		{"every task checked on every device", "check", nil, []report{{"dev-1", "completed", 0}, {"dev-2", "completed", 0}}, true, true, 0},
		{"one device left three tasks unchecked", "check", nil, []report{{"dev-1", "completed", 0}, {"dev-2", "completed", 3}}, true, false, 3},
		{"one device's check failed", "check", nil, []report{{"dev-1", "completed", 0}, {"dev-2", "failed", 0}}, true, false, 0},
		{"a device was skipped as not active", "check", []string{"dev-3"}, []report{{"dev-1", "completed", 0}, {"dev-2", "completed", 0}}, true, false, 0},
		{"still running", "check", nil, []report{{"dev-1", "completed", 1}}, false, false, 1},
		{"a real run", "execute", nil, []report{{"dev-1", "completed", 0}, {"dev-2", "completed", 0}}, false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			jobID := runningCheck(t, store, tc.mode, []string{"dev-1", "dev-2"}, tc.skipped...)
			consumer := dispatch.NewResultConsumer(store, quietLogger())
			for _, r := range tc.reports {
				reason := ""
				if r.unchecked > 0 {
					reason = "check incomplete"
				}
				if err := consumer.Handle(checkResult(t, jobID, r.device, r.outcome, reason, r.unchecked)); err != nil {
					t.Fatalf("Handle(%s): %v", r.device, err)
				}
			}
			job, tasks, err := store.Get(t.Context(), jobID)
			if err != nil {
				t.Fatal(err)
			}
			complete, decided, unchecked := job.CheckCoverage(tasks)
			if complete != tc.wantComplete || decided != tc.wantDecided || unchecked != tc.wantUnchecked {
				t.Errorf("CheckCoverage = complete %v, decided %v, unchecked %d; want %v, %v, %d (job state %s)",
					complete, decided, unchecked, tc.wantComplete, tc.wantDecided, tc.wantUnchecked, job.State)
			}
		})
	}
}

// TestResultConsumer_DropsANegativeUncheckedCount covers the one new
// field a Runner sends: a count below zero is malformed, acknowledged and
// dropped like an unknown outcome, and records nothing.
func TestResultConsumer_DropsANegativeUncheckedCount(t *testing.T) {
	store, _ := newTestStore(t)
	jobID := runningCheck(t, store, "check", []string{"dev-1"})
	consumer := dispatch.NewResultConsumer(store, quietLogger())
	if err := consumer.Handle(checkResult(t, jobID, "dev-1", "completed", "", -1)); err != nil {
		t.Fatalf("Handle = %v, want it acknowledged", err)
	}
	_, tasks, err := store.Get(t.Context(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].Result != "" {
		t.Errorf("a result with a negative count was recorded: %+v", tasks[0])
	}
}
