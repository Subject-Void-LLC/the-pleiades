// Package dispatch_test: the one path a cancel takes.
package dispatch_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
)

// recordingPublisher captures the cancel signals it was asked to send.
type recordingPublisher struct {
	jobs []string
	err  error
}

func (p *recordingPublisher) PublishCancel(_ context.Context, jobID string) error {
	p.jobs = append(p.jobs, jobID)
	return p.err
}

// TestCanceller_SettlesTheRecordAndSignalsTheRunner is the regression
// guard for a defect this type exists because of.
//
// Cancelling has two halves, and they were done in two different places:
// the JSON API settled the record and published the signal, while the
// browser's own Cancel button called the store directly and published
// nothing. So cancelling through the UI stopped the job on paper and left
// the runbook running on the device, which is the worse way round of the
// two, since the browser is what an operator actually reaches for. Both
// callers take this path now, and this asserts both halves happen on it.
func TestCanceller_SettlesTheRecordAndSignalsTheRunner(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	signals := &recordingPublisher{}
	canceller := dispatch.NewCanceller(store, signals, quietLogger())

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := store.BeginFanOut(ctx, job.JobID, time.Hour); err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}

	if err := canceller.Cancel(ctx, job.JobID, "operator"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "canceled" {
		t.Errorf("State = %q, want %q", got.State, "canceled")
	}
	if got.CanceledBy != "operator" {
		t.Errorf("CanceledBy = %q, want %q", got.CanceledBy, "operator")
	}
	if len(signals.jobs) != 1 || signals.jobs[0] != job.JobID {
		t.Errorf("published signals = %v, want exactly one for %q: a cancel that settles the record without signalling leaves the runbook running on the device", signals.jobs, job.JobID)
	}
}

// TestCanceller_DoesNotSignalAJobItCouldNotStop pins the ordering.
//
// The record is what makes a cancel true and the signal only carries that
// decision outward, so a refused write must not produce a signal. Doing it
// the other way round would let a job that is still running have its
// execution stopped while the record kept saying it was running, which is
// the one inconsistency ordering alone can rule out.
func TestCanceller_DoesNotSignalAJobItCouldNotStop(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	signals := &recordingPublisher{}
	canceller := dispatch.NewCanceller(store, signals, quietLogger())

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}
	if err := store.Complete(ctx, job.JobID, fence, 1, 0, 0); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	err = canceller.Cancel(ctx, job.JobID, "operator")
	if !errors.Is(err, dispatch.ErrNotCancelable) {
		t.Fatalf("Cancel of a finished job = %v, want a wrapped ErrNotCancelable passed through unchanged", err)
	}
	if len(signals.jobs) != 0 {
		t.Errorf("published %d signal(s) for a job it did not cancel, want 0", len(signals.jobs))
	}
}

// TestCanceller_SurvivesAFailedSignal proves the best-effort half cannot
// fail the half that was actually promised.
//
// Returning an error here would invite a retry that would then be refused
// as not cancelable, while the job stayed canceled: the caller would read
// a failure for something that had already succeeded.
func TestCanceller_SurvivesAFailedSignal(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	signals := &recordingPublisher{err: errors.New("the broker is unreachable")}
	canceller := dispatch.NewCanceller(store, signals, quietLogger())

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := canceller.Cancel(ctx, job.JobID, "operator"); err != nil {
		t.Fatalf("Cancel returned %v, want nil: a failed signal must not fail a cancel that already landed", err)
	}
	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "canceled" {
		t.Errorf("State = %q, want %q", got.State, "canceled")
	}
}

// TestCanceller_WorksWithNoPublisherWired covers the deployment that wires
// no control channel: the durable half still happens.
func TestCanceller_WorksWithNoPublisherWired(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	canceller := dispatch.NewCanceller(store, nil, quietLogger())

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := canceller.Cancel(ctx, job.JobID, "operator"); err != nil {
		t.Fatalf("Cancel with no publisher wired: %v", err)
	}
	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "canceled" {
		t.Errorf("State = %q, want %q", got.State, "canceled")
	}
}
