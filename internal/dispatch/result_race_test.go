// A Runner that reports before the fan-out has written the device's row:
// the result must still land, never be dropped as a message about nothing.
package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// instantRunnerBus is a real in-process bus whose every dispatch is
// answered before Publish returns, by the Controller's real result
// consumer, the way a Runner whose task fails at once can beat the
// fan-out's own row write under load. A result the consumer asks to have
// redelivered (a non-nil error) is kept, as JetStream keeps it, and handed
// back by redeliver.
type instantRunnerBus struct {
	event.Bus
	t        testing.TB
	consumer *dispatch.ResultConsumer

	mu      sync.Mutex
	pending []event.Event
	handled int
}

func (b *instantRunnerBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	if err := b.Bus.Publish(ctx, topic, evt); err != nil {
		return err
	}
	var payload wire.DispatchPayload
	if err := json.Unmarshal(evt.Data, &payload); err != nil || payload.JobID == "" {
		return nil
	}
	b.deliver(resultEvent(b.t, payload.JobID, payload.DeviceID, "failed", "the check refused at once"))
	return nil
}

// deliver hands one result to the consumer, keeping it for redelivery
// when the consumer says so.
func (b *instantRunnerBus) deliver(evt event.Event) {
	err := b.consumer.Handle(evt)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handled++
	if err != nil {
		b.pending = append(b.pending, evt)
	}
}

// redeliver hands every kept result back once, as the broker does after
// its backoff.
func (b *instantRunnerBus) redeliver() {
	b.mu.Lock()
	kept := b.pending
	b.pending = nil
	b.mu.Unlock()
	for _, evt := range kept {
		b.deliver(evt)
	}
}

// TestResultConsumer_AResultBeforeTheRowIsRecordedOnceTheRowExists: the
// fan-out publishes a device's dispatch and then writes its row, so a
// result can arrive first. It must be kept for redelivery, not
// acknowledged as a result for a device this job never dispatched to, and
// land once the row is written; the job then completes. Acknowledging it
// left the device "dispatched" with no result, and the job running, for
// good: every later delivery of the same result is the same message.
func TestResultConsumer_AResultBeforeTheRowIsRecordedOnceTheRowExists(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := &instantRunnerBus{Bus: event.NewInProcessBus(), t: t, consumer: dispatch.NewResultConsumer(store, quietLogger())}
	device := capableDevice("dev-fast", "fast-router", "10.0.0.7")
	worker := dispatch.NewWorker(store, &fakeRepository{Devices: []pkginventory.InventoryItem{device}}, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	bus.redeliver()

	job, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Result != dispatch.ResultFailed {
		t.Fatalf("tasks = %+v, want the device's failure recorded (results handled %d)", tasks, bus.handled)
	}
	if job.State != "completed" {
		t.Errorf("job State = %q, want completed once its one device reported", job.State)
	}
}

// TestRecordResult_OnlyAMissingRowMidFanOutIsWorthRedelivering: while a
// job is still fanning out, a result for a device with no row yet is
// ErrResultBeforeDispatch, and one for a device whose row says it was
// skipped is still ErrJobNotFound, since that device never ran and
// redelivery will not change it. Once the fan-out settles, every row it
// will write exists, so a missing one is permanent too (the consumer's own
// "a device this job never dispatched to" case).
func TestRecordResult_OnlyAMissingRowMidFanOutIsWorthRedelivering(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{DeviceID: "dev-skipped", DeviceName: "dev-skipped", Outcome: dispatch.OutcomeSkipped, Reason: "not active"}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.RecordResult(ctx, job.JobID, "dev-not-yet", dispatch.ResultFailed, "at once", 0); !errors.Is(err, dispatch.ErrResultBeforeDispatch) {
		t.Errorf("a result for a device with no row mid fan-out = %v, want ErrResultBeforeDispatch", err)
	}
	if _, err := store.RecordResult(ctx, job.JobID, "dev-skipped", dispatch.ResultFailed, "", 0); !errors.Is(err, dispatch.ErrJobNotFound) || errors.Is(err, dispatch.ErrResultBeforeDispatch) {
		t.Errorf("a result for a skipped device mid fan-out = %v, want ErrJobNotFound alone", err)
	}
}
