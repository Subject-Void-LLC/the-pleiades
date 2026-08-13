package dispatch_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
)

// One reap pass, against each answer its store can give.
//
// The contract is that a pass never stops early. A failure listing stale
// jobs, or publishing for one particular job, costs at most one reap
// interval of extra delay, because the next tick's own scan finds the same
// still-stale job again. A pass that returned on the first republish error
// would leave every job behind it unreclaimed until whichever job was
// failing stopped failing, which on a permanently poisoned job id is
// forever.

// staleLister answers a scan with a fixed list, or refuses.
//
// It embeds the interface rather than implementing twenty methods, and
// leaves the rest nil deliberately: a pass that called anything else would
// panic rather than quietly pass, which is the failure this stands in for.
type staleLister struct {
	dispatch.JobStore
	ids  []string
	err  error
	sawn int
}

func (s *staleLister) ListStaleFanOuts(context.Context, time.Duration) ([]string, error) {
	s.sawn++
	if s.err != nil {
		return nil, s.err
	}
	return s.ids, nil
}

// refusingBus fails the publish of every job id in refuse, and records the
// rest, standing in for a broker that rejects one message without being
// down.
type refusingBus struct {
	event.Bus
	refuse    map[string]bool
	published []string
}

func (b *refusingBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	// The reaper keys the publish by job id through the idempotency key,
	// which is the only place the id is legible from here without
	// decoding the payload this package keeps unexported.
	key, _ := event.IdempotencyKeyFromContext(ctx)
	if b.refuse[key] {
		return errors.New("broker refused this message")
	}
	b.published = append(b.published, key)
	return b.Bus.Publish(ctx, topic, evt)
}

func newRefusingBus(refuse ...string) *refusingBus {
	set := make(map[string]bool, len(refuse))
	for _, id := range refuse {
		set[id] = true
	}
	return &refusingBus{Bus: event.NewInProcessBus(), refuse: set}
}

func TestReaper_Sweep_PublishesOneEventPerStaleJob(t *testing.T) {
	bus := newRefusingBus()
	store := &staleLister{ids: []string{"job-a", "job-b", "job-c"}}

	dispatch.NewReaper(store, bus, time.Hour).Sweep(t.Context())

	if got, want := len(bus.published), 3; got != want {
		t.Fatalf("published %d events, want %d", got, want)
	}
	for i, want := range []string{"job-a", "job-b", "job-c"} {
		if bus.published[i] != want {
			t.Errorf("event %d carried job id %q, want %q", i, bus.published[i], want)
		}
	}
}

func TestReaper_Sweep_AFailedScanTouchesNothing(t *testing.T) {
	bus := newRefusingBus()
	store := &staleLister{err: errors.New("the database is unreachable")}

	// Logged and returned, not retried in place and not panicked on. The
	// next tick calls the same scan again, so a transient outage costs one
	// interval rather than the reaper.
	dispatch.NewReaper(store, bus, time.Hour).Sweep(t.Context())

	if len(bus.published) != 0 {
		t.Errorf("published %v after a failed scan, want nothing", bus.published)
	}
	if store.sawn != 1 {
		t.Errorf("scanned %d times in one pass, want 1", store.sawn)
	}
}

func TestReaper_Sweep_OneUnpublishableJobDoesNotStopTheRest(t *testing.T) {
	// The middle one fails, so the assertion is about continuing rather
	// than about the loop happening to have finished already.
	bus := newRefusingBus("job-b")
	store := &staleLister{ids: []string{"job-a", "job-b", "job-c"}}

	dispatch.NewReaper(store, bus, time.Hour).Sweep(t.Context())

	if got, want := len(bus.published), 2; got != want {
		t.Fatalf("published %d events, want %d: a refused job must not strand the ones behind it", got, want)
	}
	for _, id := range []string{"job-a", "job-c"} {
		if !contains(bus.published, id) {
			t.Errorf("%q was never republished, though nothing refused it", id)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
