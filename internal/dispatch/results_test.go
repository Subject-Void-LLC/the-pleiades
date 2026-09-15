// Package dispatch_test: the Controller-side result consumer.
//
// Every case drives the real Handle against the real ent-backed store, and
// the end-to-end case publishes through a real in-process event.Bus so the
// wire shape is the one a Runner actually produces rather than a struct
// built by hand on this side of the mesh.
package dispatch_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// quietLogger keeps the expected error lines these cases provoke out of
// the test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// resultEvent builds the event a Runner publishes for one device. The
// field names are internal/runner's ResultEntry json tags, and the outcome
// vocabulary is the Runner's ("completed"), not this package's
// ("succeeded"), which is the translation under test.
func resultEvent(t *testing.T, jobID, deviceID, outcome, reason string) event.Event {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"id":        jobID + ":" + deviceID,
		"job_id":    jobID,
		"device_id": deviceID,
		"outcome":   outcome,
		"reason":    reason,
	})
	if err != nil {
		t.Fatalf("failed to encode a result: %v", err)
	}
	return event.Event{ID: jobID + ":" + deviceID, Data: body}
}

// TestResultConsumer_EndsAJobOnceEveryDeviceReports is the whole point of
// the consumer, driven one device at a time as the mesh delivers them.
func TestResultConsumer_EndsAJobOnceEveryDeviceReports(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1", "dev-2")
	consumer := dispatch.NewResultConsumer(store, quietLogger())

	if err := consumer.Handle(resultEvent(t, jobID, "dev-1", "completed", "")); err != nil {
		t.Fatalf("Handle(dev-1): %v", err)
	}
	got, _, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "running" {
		t.Fatalf("State with dev-2 still outstanding = %q, want %q", got.State, "running")
	}

	if err := consumer.Handle(resultEvent(t, jobID, "dev-2", "failed", "the play did not converge")); err != nil {
		t.Fatalf("Handle(dev-2): %v", err)
	}
	got, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "completed" {
		t.Errorf("State after every device reported = %q, want %q", got.State, "completed")
	}

	// The Runner said "completed" and this side stores "succeeded". The
	// two vocabularies genuinely differ, because "completed" is already a
	// job state here and means something else, so this asserts the
	// translation rather than assuming it.
	byDevice := map[string]dispatch.JobTask{}
	for _, task := range tasks {
		byDevice[task.DeviceID] = task
	}
	if r := byDevice["dev-1"].Result; r != dispatch.ResultSucceeded {
		t.Errorf("dev-1 Result = %q, want %q: the Runner's \"completed\" must land as succeeded", r, dispatch.ResultSucceeded)
	}
	if r := byDevice["dev-2"].Result; r != dispatch.ResultFailed {
		t.Errorf("dev-2 Result = %q, want %q", r, dispatch.ResultFailed)
	}
}

// TestResultConsumer_AcknowledgesWhatItCanNeverActorOn covers every
// message that must be acknowledged rather than retried.
//
// The shared property is that redelivery cannot help: none of these will
// ever become valid. Retrying one parks a poison message at the head of a
// consumer group carrying every job in the system, so the cost of getting
// this wrong is not one lost result, it is every result after it.
func TestResultConsumer_AcknowledgesWhatItCanNeverActOn(t *testing.T) {
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1")
	consumer := dispatch.NewResultConsumer(store, quietLogger())

	for _, tc := range []struct {
		name string
		evt  event.Event
	}{
		{"a body that is not json", event.Event{ID: "e1", Data: json.RawMessage(`{`)}},
		{"an outcome this build does not know", resultEvent(t, jobID, "dev-1", "partially", "")},
		{"an empty outcome", resultEvent(t, jobID, "dev-1", "", "")},
		{"a device this job never dispatched to", resultEvent(t, jobID, "dev-unknown", "completed", "")},
		{"a job that does not exist", resultEvent(t, "00000000-0000-0000-0000-000000000000", "dev-1", "completed", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := consumer.Handle(tc.evt); err != nil {
				t.Fatalf("Handle returned %v, want nil: this message must be acknowledged, since redelivering it would block every job's results behind it", err)
			}
		})
	}

	// And none of that moved the job, which is the other half: a message
	// being acknowledged must not mean it was treated as a real result.
	got, _, err := store.Get(t.Context(), jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "running" {
		t.Errorf("State = %q, want %q: no unusable message should have advanced the job", got.State, "running")
	}
}

// TestResultConsumer_IsIdempotentUnderRedelivery proves at-least-once
// delivery cannot end a job early.
func TestResultConsumer_IsIdempotentUnderRedelivery(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1", "dev-2")
	consumer := dispatch.NewResultConsumer(store, quietLogger())

	evt := resultEvent(t, jobID, "dev-1", "completed", "")
	for i := range 3 {
		if err := consumer.Handle(evt); err != nil {
			t.Fatalf("Handle delivery %d: %v", i+1, err)
		}
	}

	got, _, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "running" {
		t.Errorf("State after three deliveries of one device's result = %q, want %q: dev-2 has not reported", got.State, "running")
	}

	// The last device reports, and a redelivery of THAT is harmless too.
	last := resultEvent(t, jobID, "dev-2", "completed", "")
	for i := range 2 {
		if err := consumer.Handle(last); err != nil {
			t.Fatalf("Handle of the last device, delivery %d: %v", i+1, err)
		}
	}
	got, _, err = store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "completed" {
		t.Errorf("State = %q, want %q", got.State, "completed")
	}
}

// completedSignal wraps a JobStore to announce when the consumer has made
// its final write, so a test can read the store once the handler has
// finished instead of polling it while the handler is still writing.
//
// That distinction is the whole reason this type exists. An earlier version
// of the test below polled store.Get every five milliseconds from the main
// goroutine while the bus delivered to the handler on its own, and the two
// contended for the same shared-cache in-memory SQLite database: about one
// run in fourteen under -race, SQLite answered the handler's write with
// "database table is locked: job_tasks". event.NewInProcessBus logs a
// handler error and drops the delivery, so the result was lost and the job
// never left "running".
//
// Nothing about that is a product defect, which is exactly why it had to be
// removed rather than tolerated: the real bus is JetStream, where a handler
// error is a negative acknowledgement and the message comes back. The
// in-process adapter has no redelivery, so a test using it must not
// manufacture contention the production path would simply retry through.
type completedSignal struct {
	dispatch.JobStore
	done chan struct{}
	once sync.Once
}

func (s *completedSignal) CompleteRunning(ctx context.Context, jobID string) error {
	err := s.JobStore.CompleteRunning(ctx, jobID)
	s.once.Do(func() { close(s.done) })
	return err
}

// TestResultConsumer_SubscribesToTheSubjectRunnersPublishOn is the
// end-to-end half, and it is the one that would have caught the gap this
// consumer was built to close.
//
// Runners had been publishing results since Phase 15 and nothing ever
// subscribed, so the subject carried real traffic that reached nothing. A
// test that called Handle directly would have passed throughout that
// entire period. This one publishes on the subject a Runner derives and
// asserts the job moved, so it fails if the two sides ever name different
// subjects.
func TestResultConsumer_SubscribesToTheSubjectRunnersPublishOn(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1")

	bus := event.NewInProcessBus()
	signal := &completedSignal{JobStore: store, done: make(chan struct{})}
	consumer := dispatch.NewResultConsumer(signal, quietLogger())
	if err := consumer.Subscribe(ctx, bus); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// topology.ResultSubject is what internal/runner's own publish path
	// calls, so this is the real subject rather than a literal copied into
	// the test.
	if err := bus.Publish(context.Background(), topology.ResultSubject(jobID),
		resultEvent(t, jobID, "dev-1", "completed", "")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Waited for rather than polled. The handler runs on the bus's own
	// goroutine, so reading the store from here while it works is real
	// concurrent access to one in-memory SQLite database, and this test
	// used to lose deliveries to it (see completedSignal above). Waiting
	// for the consumer's own final write means the read below happens
	// after the handler is done, with nothing else touching the database.
	select {
	case <-signal.done:
	case <-time.After(30 * time.Second):
		t.Fatal("a result published on the subject a Runner uses never reached the consumer")
	}

	got, _, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "completed" {
		t.Errorf("State = %q, want %q", got.State, "completed")
	}
}
