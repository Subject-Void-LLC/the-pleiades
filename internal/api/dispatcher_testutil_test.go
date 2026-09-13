// This file holds the fixture builders and helpers shared by every test in
// this package's Dispatcher/Job suite (dispatcher_test.go,
// dispatcher_selector_test.go, dispatcher_bench_test.go,
// dispatcher_fuzz_test.go, dispatcher_release_test.go, and jobs_test.go):
// real collaborators for internal/dispatch's own JobStore and Worker ports
// and internal/runbook's own Source port, per AGENTS.md RULE 0, rather than
// each file hand-rolling its own copy of the same fixtures. Phase 14
// replaced Dispatcher's synchronous, in-handler fan-out loop with an
// asynchronous launch-and-poll shape (dispatcher.go's own doc comment), and
// every helper here exists to stand up the real pieces that shape now
// depends on: a real runbook.Source, a real dispatch.JobStore, and a real
// event.Bus.
package api_test

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	_ "github.com/mattn/go-sqlite3"
)

// newSerializedSQLiteClient opens an in-memory SQLite-backed *ent.Client
// whose connection pool is capped at exactly one connection.
//
// This is not merely an optimization: several tests in this suite have a
// real dispatch.Worker writing JobTask rows on its own goroutine while the
// test's main goroutine concurrently polls the same database for progress
// (pollJobUntilTerminal), and Go's database/sql package opens additional
// connections on demand under concurrent load by default. mattn/go-sqlite3
// reports a second connection's access to a table a first connection
// already has open as "database table is locked" (SQLITE_LOCKED, a
// shared-cache-mode condition distinct from ordinary SQLITE_BUSY), and,
// per that driver's own documented limitation, does not honor
// "_busy_timeout" for that particular error unless it was built with
// SQLITE_ENABLE_UNLOCK_NOTIFY, which this project's go.mod pin is not.
// Capping the pool at one connection means every query, from every
// goroutine, is serialized onto that single connection by database/sql
// itself, which removes the cross-connection locking condition entirely
// rather than merely waiting it out.
func newSerializedSQLiteClient(t testing.TB, name string) *ent.Client {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1&_busy_timeout=5000", name)
	db, err := stdsql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("opening sqlite3 database %q: %v", name, err)
	}
	db.SetMaxOpenConns(1)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(ent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// noopRunbookFQCN names the one action fqcn engine.ActionCapability
// (internal/engine/action_capability.go) does not bind to a capability: a
// runbook built from it requires nothing of a target device. Most tests in
// this suite are about the dispatch/job-status plumbing itself, not
// capability admission (internal/dispatch's own worker_test.go already
// covers that against the real "ios_backup" binding), so their fixture
// devices do not need to be shaped around a specific capability at all.
const noopRunbookFQCN = "noop"

// writeTestRunbook writes a minimal, valid runbook YAML file named
// id+".yaml" under dir. This is the identical minimal shape
// internal/runbook/dir_source_test.go's own writeRunbook helper writes,
// reproduced here rather than imported: that helper lives in a _test.go
// file in a different package, and a _test.go file cannot be imported
// across a package boundary.
func writeTestRunbook(t testing.TB, dir, id, fqcn string) {
	t.Helper()
	content := "id: " + id + "\ntasks:\n  - name: step\n    fqcn: " + fqcn + "\n"
	path := filepath.Join(dir, id+".yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write runbook fixture %s: %v", path, err)
	}
}

// newTestRunbookSource builds a real runbook.Source (RULE 0: never a
// hand-rolled fake Source) over a fresh temp directory containing exactly
// the runbook ids named in ids, each compiled from noopRunbookFQCN so it
// requires no device capability. A test that needs a runbook requiring a
// real capability builds its own directory directly, the way
// internal/dispatch/worker_test.go's newTestRunbookSource does.
func newTestRunbookSource(t testing.TB, ids ...string) runbook.Source {
	t.Helper()
	dir := t.TempDir()
	for _, id := range ids {
		writeTestRunbook(t, dir, id, noopRunbookFQCN)
	}
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}
	return src
}

// jobStoreDBSeq makes each newTestJobStore's in-memory database name
// unique, mirroring hateoas_release_test.go's own gateFixtureSeq: SQLite's
// "cache=shared" mode means two connections opened under the same name
// share one database, so a name derived from t.Name() alone would collide
// whenever a single test builds more than one store, as the
// trace-propagation and release-gate tests below do.
var jobStoreDBSeq atomic.Int64

// newTestJobStore spins up a real, isolated in-memory SQLite-backed
// dispatch.JobStore, over a newSerializedSQLiteClient (see its own doc
// comment for why a capped connection pool is load bearing here, not
// cosmetic). Per RULE 0, every test in this suite exercises the exact ent
// write path production code runs, never a hand-rolled JobStore double.
func newTestJobStore(t testing.TB) dispatch.JobStore {
	t.Helper()
	client := newSerializedSQLiteClient(t, fmt.Sprintf("jobstore-%d", jobStoreDBSeq.Add(1)))
	return dispatch.NewEntJobStore(client)
}

// dispatchTestIdentity is the fixed identity most Dispatcher-handler tests
// in this suite stamp onto their request. DispatchRunbook only ever reads
// id.Subject (to stamp the Job's Actor and the published event), so one
// shared value is enough; a test that cares about a specific Subject value
// builds its own *auth.Identity.
var dispatchTestIdentity = &auth.Identity{Subject: "dispatch-test-user"}

// dispatchTestRequest builds an authenticated POST request against the
// dispatch route for group and runbookID, with dispatchTestIdentity already
// placed in context the way AuthMiddleware would, for a test that calls
// Dispatcher.DispatchRunbook directly rather than through a full router.
func dispatchTestRequest(t testing.TB, group, runbookID string) *http.Request {
	t.Helper()
	target := "/dispatch?group=" + group + "&runbook=" + runbookID
	req := httptest.NewRequest(http.MethodPost, target, nil)
	return req.WithContext(contextWithIdentity(req, dispatchTestIdentity))
}

// capturingBus wraps a real event.Bus (event.NewInProcessBus; never a mock,
// per RULE 0) and additionally records every event published through it, so
// a test can assert on publish counts, topics, and the context each publish
// carried, none of which the wrapped bus's own Publish contract exposes on
// its own. This mirrors internal/dispatch/worker_test.go's own capturingBus
// exactly; it is redefined here, rather than imported, because that type is
// unexported in a different test package.
type capturingBus struct {
	event.Bus
	mu        sync.Mutex
	published []event.Event
	topics    []string
	contexts  []context.Context
}

// newCapturingBus builds a capturingBus over a fresh, real in-process Bus.
func newCapturingBus() *capturingBus {
	return &capturingBus{Bus: event.NewInProcessBus()}
}

// Publish records evt, topic, and ctx before delegating to the wrapped real
// Bus, so the recording can never diverge from what was actually published.
func (b *capturingBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	b.mu.Lock()
	b.published = append(b.published, evt)
	b.topics = append(b.topics, topic)
	b.contexts = append(b.contexts, ctx)
	b.mu.Unlock()
	return b.Bus.Publish(ctx, topic, evt)
}

// count returns the total number of Publish calls observed so far, across
// every topic.
func (b *capturingBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

// countTopic returns how many Publish calls so far were made against
// exactly topic, e.g. distinguishing the one job.requested launch publish
// from the per-device dispatch publishes a Worker performs afterward.
func (b *capturingBus) countTopic(topic string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, tp := range b.topics {
		if tp == topic {
			n++
		}
	}
	return n
}

// dispatchPublishes returns how many Publish calls landed on a subject the
// fleet dispatch consumer would receive, and how many DISTINCT such
// subjects there were.
//
// Two numbers rather than one, because since Phase 101a they answer
// different questions: the total is "was every device dispatched", and the
// distinct count is "did each device get its OWN subject". Before that
// change the second number was always 1 and there was nothing to ask.
//
// The prefix is derived from topology.DispatchSubjectAll rather than
// spelled here, so this helper cannot drift from the filter the real
// consumer uses. Dropping the trailing ">" is sound because this is a Go
// prefix test rather than a NATS filter match, and every subject under it
// carries exactly one further token.
func (b *capturingBus) dispatchPublishes() (total, distinct int) {
	prefix := strings.TrimSuffix(topology.DispatchSubjectAll(), ">")
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := make(map[string]struct{})
	for _, tp := range b.topics {
		if !strings.HasPrefix(tp, prefix) {
			continue
		}
		total++
		seen[tp] = struct{}{}
	}
	return total, len(seen)
}

// firstDispatch returns the first event published against any device's
// dispatch subject, for a caller spot-checking one real payload.
func (b *capturingBus) firstDispatch() (event.Event, bool) {
	prefix := strings.TrimSuffix(topology.DispatchSubjectAll(), ">")
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, tp := range b.topics {
		if strings.HasPrefix(tp, prefix) {
			return b.published[i], true
		}
	}
	return event.Event{}, false
}

// firstOnTopic returns the first event published against exactly topic, or
// (zero value, false) if none was.
func (b *capturingBus) firstOnTopic(topic string) (event.Event, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, tp := range b.topics {
		if tp == topic {
			return b.published[i], true
		}
	}
	return event.Event{}, false
}

// lastContext returns the context argument of the most recent Publish call,
// for a test asserting on what Dispatcher grafted onto it (trace context,
// actor, idempotency key).
func (b *capturingBus) lastContext() context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.contexts[len(b.contexts)-1]
}

// pollJobUntilTerminal polls store.Get for jobID until its State reaches a
// terminal value ("completed" or "failed") or timeout elapses, sleeping a
// short, fixed interval between polls.
//
// This is a bounded retry loop, not a single blind time.Sleep used as the
// synchronization primitive itself: IMPLEMENTATION.md's own Phase 18
// checklist calls out replacing sleep-based synchronization with
// event-driven or poll-based waits, since a fixed sleep either wastes time
// once the work has already finished or, worse, is too short under CI load
// and fails a test despite correct production behavior. Polling
// store.Get repeatedly, bounded by an overall deadline, avoids both
// failure modes.
func pollJobUntilTerminal(t testing.TB, ctx context.Context, store dispatch.JobStore, jobID string, timeout time.Duration) *dispatch.Job {
	t.Helper()
	// timeout is what the work should take on a normal machine; the race
	// detector makes it about an order of magnitude slower, and `make ci`
	// runs `go test -race`. Scaling here, rather than asking every call
	// site to remember, is what stops a budget written against a plain
	// `go test` run from failing the build CI actually judges. See
	// racebudget_race_test.go for the measurements behind the factor.
	timeout *= raceTimeScale
	deadline := time.Now().Add(timeout)
	const pollInterval = 5 * time.Millisecond
	for {
		job, _, err := store.Get(ctx, jobID)
		if err != nil {
			t.Fatalf("polling job %s: %v", jobID, err)
		}
		if job.State == "completed" || job.State == "failed" {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not reach a terminal state within %s (last observed state %q)", jobID, timeout, job.State)
		}
		time.Sleep(pollInterval)
	}
}
