package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// fakeDedupStore is an in-memory DedupStore whose failures are
// controllable, so the degradation behaviour can be asserted.
type fakeDedupStore struct {
	seen    map[string]bool
	readErr error
	markErr error
	marked  []string
}

func (f *fakeDedupStore) SeenRecently(_ context.Context, key string) (bool, error) {
	if f.readErr != nil {
		return false, f.readErr
	}
	return f.seen[key], nil
}

func (f *fakeDedupStore) MarkSeen(_ context.Context, key string, _ time.Duration) error {
	if f.markErr != nil {
		return f.markErr
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	f.seen[key] = true
	f.marked = append(f.marked, key)
	return nil
}

// dedupTestAgent is the smallest Agent these assertions need: the dedup
// helpers touch only the store, the TTL and the logger.
func dedupTestAgent() *Agent {
	return &Agent{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func testPayload() wire.DispatchPayload {
	return wire.DispatchPayload{JobID: "job-1", DeviceID: "dev-1", DeviceName: "dev"}
}

// TestDispatchDedupKeyMatchesTheProducerAndTheWAL pins the identity three
// separate mechanisms already agree on. If this key ever diverges, the
// suppression silently stops matching the dispatches it is meant to
// suppress, and nothing else would notice.
func TestDispatchDedupKeyMatchesTheProducerAndTheWAL(t *testing.T) {
	if got, want := dispatchDedupKey(testPayload()), "job-1:dev-1"; got != want {
		t.Fatalf("dispatchDedupKey = %q, want %q, which is the key internal/dispatch stamps on the publish and internal/runner's write-ahead log derives", got, want)
	}
}

func TestAlreadyExecuted(t *testing.T) {
	tests := []struct {
		name  string
		store *fakeDedupStore
		want  bool
	}{
		{"unseen work runs", &fakeDedupStore{}, false},
		{"seen work is suppressed", &fakeDedupStore{seen: map[string]bool{"job-1:dev-1": true}}, true},
		{"a store read failure runs the work rather than skipping it",
			&fakeDedupStore{readErr: errors.New("kv unavailable")}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := dedupTestAgent()
			a.dedup = tt.store

			if got := a.alreadyExecuted(context.Background(), testPayload()); got != tt.want {
				t.Fatalf("alreadyExecuted = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAlreadyExecutedWithoutAStoreRunsTheWork covers the degradation
// path: a Runner that could not bind the bucket behaves exactly as it did
// before Phase 96c rather than refusing work.
func TestAlreadyExecutedWithoutAStoreRunsTheWork(t *testing.T) {
	a := dedupTestAgent()
	a.dedup = nil

	if a.alreadyExecuted(context.Background(), testPayload()) {
		t.Fatal("an Agent with no dedup store suppressed work")
	}
	// And marking must be a no-op rather than a nil dereference.
	a.markExecuted(context.Background(), testPayload())
}

// TestMarkExecutedRecordsTheKey proves the mark half, and that a store
// failure there is tolerated: the consequence of a missed mark is a
// possible re-run, which is the pre-Phase-96c behaviour, and it must not
// fail a job that genuinely completed.
func TestMarkExecutedRecordsTheKey(t *testing.T) {
	a := dedupTestAgent()
	store := &fakeDedupStore{}
	a.dedup = store
	a.dedupTTL = time.Hour

	a.markExecuted(context.Background(), testPayload())
	if len(store.marked) != 1 || store.marked[0] != "job-1:dev-1" {
		t.Fatalf("marked = %v, want exactly [job-1:dev-1]", store.marked)
	}

	failing := &fakeDedupStore{markErr: errors.New("kv unavailable")}
	a.dedup = failing
	a.markExecuted(context.Background(), testPayload())
}
