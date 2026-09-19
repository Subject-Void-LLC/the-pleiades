// Package dispatch_test: what the fan-out does when the job record itself
// cannot be written.
//
// Every failure a fan-out can meet ends the same way: the job is recorded
// as failed and the delivery is acked, because BeginFanOut already moved
// the job out of pending and no redelivery will do any work. That leaves
// one question these cover and nothing else did: what happens when
// recording the failure ALSO fails. Answering it wrong leaves a job parked
// in fanning_out forever, or retries a delivery that can never succeed.
package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// errStoreDown is a store that cannot write, as one behind a database
// outage is.
var errStoreDown = errors.New("the database is not answering")

// brokenStore is a real store with one method replaced, so every other
// call a fan-out makes behaves exactly as it does in production.
type brokenStore struct {
	dispatch.JobStore

	// failBeginWith and failFailWith, when set, are what BeginFanOut and
	// Fail return instead of doing their work.
	failBeginWith error
	failFailWith  error
}

func (s *brokenStore) BeginFanOut(ctx context.Context, jobID string, staleAfter time.Duration) (bool, int64, error) {
	if s.failBeginWith != nil {
		return false, 0, s.failBeginWith
	}
	return s.JobStore.BeginFanOut(ctx, jobID, staleAfter)
}

func (s *brokenStore) Fail(ctx context.Context, jobID string, fence int64, reason string) error {
	if s.failFailWith != nil {
		return s.failFailWith
	}
	return s.JobStore.Fail(ctx, jobID, fence, reason)
}

// TestHandleJobRequested_RefusesAPayloadItCannotRead covers the inner
// decode: the envelope is this handler's own contract, and a payload that
// is not one is reported rather than treated as a job with an empty id,
// which would begin a fan-out for nothing.
func TestHandleJobRequested_RefusesAPayloadItCannotRead(t *testing.T) {
	store, _ := newTestStore(t)
	worker := dispatch.NewWorker(store, &fakeRepository{}, newTestRunbookSource(t), newCapturingBus(), nil)

	err := worker.HandleJobRequested(event.Event{ID: "e-1", Type: "job.requested", Data: []byte("{not json")})
	if err == nil || !strings.Contains(err.Error(), "decode job.requested payload") {
		t.Errorf("err = %v, want the decode failure", err)
	}
}

// TestHandleJobRequested_ReportsAClaimItCannotMake covers the first store
// call: a claim that errors is not the same as a claim somebody else
// holds, so it is returned for redelivery rather than acked. A job whose
// fan-out never began is still pending, and a later delivery can run it.
func TestHandleJobRequested_ReportsAClaimItCannotMake(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	evt := requestJob(t, ctx, store, "pb-1", "routers")
	worker := dispatch.NewWorker(&brokenStore{JobStore: store, failBeginWith: errStoreDown},
		&fakeRepository{}, newTestRunbookSource(t), newCapturingBus(), nil)

	err := worker.HandleJobRequested(evt)
	if !errors.Is(err, errStoreDown) {
		t.Fatalf("err = %v, want the store's own failure", err)
	}
	if !strings.Contains(err.Error(), "begin fan-out") {
		t.Errorf("err = %v, want it to say which step failed", err)
	}
}

// TestFanOut_WhenRecordingTheFailureAlsoFails covers each way a fan-out
// ends before reaching a device, with the store refusing to record it:
//
//   - a store that is simply down returns the failure, so the delivery is
//     retried and somebody is told;
//   - a store answering ErrFenced acks instead, because being fenced means
//     another claim owns this job now and recording anything against it
//     would be writing over that claim's work.
func TestFanOut_WhenRecordingTheFailureAlsoFails(t *testing.T) {
	fenced := fmt.Errorf("job whatever: %w", dispatch.ErrFenced)

	for _, tc := range []struct {
		name      string
		runbookID string
		kind      string
		failWith  error
		wantErr   error
	}{
		{name: "no definition source for the kind, store down", kind: "nosuchkind", runbookID: "pb-1", failWith: errStoreDown, wantErr: errStoreDown},
		{name: "no definition source for the kind, fenced", kind: "nosuchkind", runbookID: "pb-1", failWith: fenced},
		{name: "a definition that cannot be prepared, store down", runbookID: "pb-missing", failWith: errStoreDown, wantErr: errStoreDown},
		{name: "a definition that cannot be prepared, fenced", runbookID: "pb-missing", failWith: fenced},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			ctx := context.Background()
			job := &dispatch.Job{RunbookID: tc.runbookID, GroupName: "routers", Actor: "user@example.com", Kind: tc.kind}
			if err := store.Create(ctx, job); err != nil {
				t.Fatal(err)
			}
			evt := event.Event{ID: "e-1", Type: "job.requested", Data: []byte(`{"job_id":"` + job.JobID + `"}`)}

			worker := dispatch.NewWorker(&brokenStore{JobStore: store, failFailWith: tc.failWith},
				&fakeRepository{Devices: []pkginventory.InventoryItem{capableDevice("dev-1", "router", "10.0.0.1")}},
				newTestRunbookSource(t), newCapturingBus(), nil)

			err := worker.HandleJobRequested(evt)
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("err = %v, want the delivery acked: another claim owns this job", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestJob_ModeLabel covers what a job's mode reads as for a person: the
// mode itself when it is one, and a plain word when the record carries
// something no version of this platform wrote, rather than an empty
// string a reader would take for "execute".
func TestJob_ModeLabel(t *testing.T) {
	for _, tc := range []struct {
		fields map[string]any
		want   string
	}{
		{fields: nil, want: "execute"},
		{fields: map[string]any{"mode": "check"}, want: "check"},
		{fields: map[string]any{"mode": "rehearse"}, want: "unreadable"},
	} {
		job := &dispatch.Job{Fields: tc.fields}
		if got := job.ModeLabel(); got != tc.want {
			t.Errorf("ModeLabel for %v = %q, want %q", tc.fields, got, tc.want)
		}
	}
}
