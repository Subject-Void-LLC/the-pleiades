package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
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
//
// This test is worth reading as a cautionary example of its own subject,
// which is why the original assertion is quoted rather than deleted. It
// used to read `dispatchDedupKey(testPayload()) == "job-1:dev-1"`, and it
// passed for as long as the feature was completely dead. It pinned that
// the key AGREED with the other two mechanisms and never once asked
// whether the agreed string was legal where it had to be a KV key, which
// it was not: see FAILURE_PATTERNS.md #205. Agreement is not validity.
func TestDispatchDedupKeyMatchesTheProducerAndTheWAL(t *testing.T) {
	// The identity, which is what must agree.
	identity := testPayload().JobID + ":" + testPayload().DeviceID
	if got, want := dispatchDedupKey(testPayload()), topology.SubjectToken(identity); got != want {
		t.Fatalf("dispatchDedupKey = %q, want %q, the encoding of %q, which is the identity internal/dispatch stamps on the publish and internal/runner's write-ahead log derives",
			got, want, identity)
	}
}

// TestDispatchDedupKeyIsLegalAsAKVKey is the assertion whose absence let
// FAILURE_PATTERNS.md #205 ship dead. It checks the property the key has to
// have to do anything at all, rather than the property it was chosen for.
//
// The character class is nats.go's own (jetstream/kv.go:502) restated here
// rather than imported, because keyValid is unexported. The container test
// beside this one is what proves the restatement is faithful; this one is
// what makes a violation legible without Docker.
func TestDispatchDedupKeyIsLegalAsAKVKey(t *testing.T) {
	legalKVKey := regexp.MustCompile(`^[-/_=.a-zA-Z0-9]+$`)
	for _, p := range []wire.DispatchPayload{
		testPayload(),
		{JobID: "4d6e9c14-0785-49d2-b821-51a81b54b1cc", DeviceID: "router1.example.com"},
		{JobID: "", DeviceID: ""},
		{JobID: "job 1", DeviceID: "dev:2"},
		{JobID: "*", DeviceID: ">"},
	} {
		key := dispatchDedupKey(p)
		if !legalKVKey.MatchString(key) {
			t.Errorf("dispatchDedupKey(%+v) = %q, which nats.go rejects with ErrInvalidKey before any wire traffic", p, key)
		}
		if strings.Contains(key, ".") {
			t.Errorf("dispatchDedupKey(%+v) = %q, which spans several subject tokens under $KV.<bucket>.", p, key)
		}
	}
}

func TestAlreadyExecuted(t *testing.T) {
	tests := []struct {
		name  string
		store *fakeDedupStore
		want  bool
	}{
		{"unseen work runs", &fakeDedupStore{}, false},
		// Keyed by the real encoder rather than a literal: a fixture that
		// restates the key cannot notice the encoder changing under it,
		// which is how FAILURE_PATTERNS.md #205 stayed invisible.
		{"seen work is suppressed", &fakeDedupStore{seen: map[string]bool{dispatchDedupKey(testPayload()): true}}, true},
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
	if len(store.marked) != 1 || store.marked[0] != dispatchDedupKey(testPayload()) {
		t.Fatalf("marked = %v, want exactly [%s]", store.marked, dispatchDedupKey(testPayload()))
	}

	failing := &fakeDedupStore{markErr: errors.New("kv unavailable")}
	a.dedup = failing
	a.markExecuted(context.Background(), testPayload())
}
