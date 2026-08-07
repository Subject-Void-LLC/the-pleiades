package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/dispatch"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runbook"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// This file covers Dispatcher.DispatchRunbook's own contract under Phase
// 14's asynchronous shape: a valid launch returns 202 Accepted with a
// Location header and a job_id, an unresolvable runbook is a 404 (new
// coverage this phase adds: before internal/runbook existed there was no
// runbook storage at all to be unresolvable against), and the two query
// parameters are still required exactly as before. The per-device fan-out
// this handler used to perform inline is now internal/dispatch.Worker's own
// concern, covered by internal/dispatch/worker_test.go and, end to end
// against a real repository, by dispatcher_selector_test.go and
// dispatcher_release_test.go in this package.

// jobAcceptedBody is the wire shape a successful launch returns, decoded
// exactly as a real client would decode it rather than through the
// server's own unexported jobAcceptedResponse type.
type jobAcceptedBody struct {
	Status string `json:"status"`
	JobID  string `json:"job_id"`
}

func decodeJobAccepted(t *testing.T, rr *httptest.ResponseRecorder) jobAcceptedBody {
	t.Helper()
	var body jobAcceptedBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response %q: %v", rr.Body.String(), err)
	}
	return body
}

// TestDispatchRunbook_ValidRequestReturns202Accepted proves the launch
// itself: a request naming a real, resolvable runbook returns 202
// Accepted, a Location header naming the new job resource, and a body
// carrying that same job id. It also confirms the Job was actually
// persisted (State "pending", since no Worker is subscribed in this test)
// and that exactly one job.requested event was published, since a launch
// that answered 202 without either of those would be lying to the caller.
func TestDispatchRunbook_ValidRequestReturns202Accepted(t *testing.T) {
	runbooks := newTestRunbookSource(t, "pb-1")
	jobs := newTestJobStore(t)
	bus := newCapturingBus()
	dispatcher := api.NewDispatcher(runbooks, jobs, bus)

	req := dispatchTestRequest(t, "routers", "pb-1")
	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}

	body := decodeJobAccepted(t, rr)
	if body.Status != "accepted" {
		t.Errorf("status field = %q, want %q", body.Status, "accepted")
	}
	if body.JobID == "" {
		t.Fatal("job_id is empty")
	}

	wantLocation := api.APIVersionPrefix + "/jobs/" + body.JobID
	if got := rr.Header().Get("Location"); got != wantLocation {
		t.Errorf("Location header = %q, want %q", got, wantLocation)
	}

	job, _, err := jobs.Get(req.Context(), body.JobID)
	if err != nil {
		t.Fatalf("the launched job was not persisted: %v", err)
	}
	if job.State != "pending" {
		t.Errorf("persisted job State = %q, want %q (no Worker is subscribed in this test)", job.State, "pending")
	}
	if job.RunbookID != "pb-1" || job.GroupName != "routers" {
		t.Errorf("persisted job = %+v, want RunbookID=pb-1 GroupName=routers", job)
	}

	if got := bus.countTopic(topology.JobRequestedSubject()); got != 1 {
		t.Errorf("job.requested publishes = %d, want 1", got)
	}
}

// TestDispatchRunbook_UnknownRunbookReturns404 is new coverage this phase
// adds: before internal/runbook existed, the "runbook" query parameter
// only ever labeled a fixed NATS subject, so there was no way for it to
// name something that does not exist. Now that a real runbook.Source
// backs it, an id with no matching file must be refused before any Job is
// created or anything is published, not merely accepted and left to fail
// silently downstream.
func TestDispatchRunbook_UnknownRunbookReturns404(t *testing.T) {
	runbooks := newTestRunbookSource(t) // no ids seeded: every id is unresolvable.
	jobs := newTestJobStore(t)
	bus := newCapturingBus()
	dispatcher := api.NewDispatcher(runbooks, jobs, bus)

	req := dispatchTestRequest(t, "routers", "does-not-exist")
	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response %q: %v", rr.Body.String(), err)
	}
	if body.Error != "runbook not found" {
		t.Errorf("error message = %q, want %q", body.Error, "runbook not found")
	}

	// An unresolvable runbook must fail before anything durable happens:
	// no event published, and (implicitly, since no job id was ever
	// returned to check) no Job row left behind for a caller who will
	// never learn its id.
	if got := bus.count(); got != 0 {
		t.Errorf("publishes = %d, want 0: an unresolvable runbook must never reach the publish step", got)
	}
}

// TestDispatchRunbook_MissingQueryParamsReturns400 proves the group and
// runbook query parameters are still required exactly as they were before
// this phase's asynchronous rewrite: this validation runs before the
// runbook is even resolved, so it is unaffected by anything internal/
// runbook or internal/dispatch added.
func TestDispatchRunbook_MissingQueryParamsReturns400(t *testing.T) {
	for _, tc := range []struct {
		name      string
		group     string
		runbookID string
	}{
		{"missing group", "", "pb-1"},
		{"missing runbook", "routers", ""},
		{"missing both", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runbooks := newTestRunbookSource(t, "pb-1")
			jobs := newTestJobStore(t)
			bus := newCapturingBus()
			dispatcher := api.NewDispatcher(runbooks, jobs, bus)

			req := dispatchTestRequest(t, tc.group, tc.runbookID)
			rr := httptest.NewRecorder()
			dispatcher.DispatchRunbook(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusBadRequest, rr.Body.String())
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding response %q: %v", rr.Body.String(), err)
			}
			if body.Error != "missing 'group' or 'runbook' query parameter" {
				t.Errorf("error message = %q, want the fixed rejection message", body.Error)
			}
			if got := bus.count(); got != 0 {
				t.Errorf("publishes = %d, want 0", got)
			}
		})
	}
}

// TestDispatchRunbook_PropagatesTraceIDFromContext proves the
// trace-propagation branch survives the rewrite to an async launch: when a
// real OpenTelemetry span is recording on the request context (as
// TracingMiddleware makes it in the real router), DispatchRunbook bridges
// that span's trace ID onto the context it publishes the one job.requested
// event with, and grafts the live span itself onto that context so the Bus
// adapter can inject W3C trace context into the outgoing message headers.
//
// The span is started through a real SDK tracer rather than a fake context
// value: a no-op tracer mints an all-zero, invalid span context, so a test
// built on one would pass while proving nothing about the real path.
func TestDispatchRunbook_PropagatesTraceIDFromContext(t *testing.T) {
	runbooks := newTestRunbookSource(t, "pb-1")
	jobs := newTestJobStore(t)
	bus := newCapturingBus()
	dispatcher := api.NewDispatcher(runbooks, jobs, bus)

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		if err := tp.Shutdown(t.Context()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	req := dispatchTestRequest(t, "routers", "pb-1")
	ctx, span := tp.Tracer("test").Start(req.Context(), "test-request")
	defer span.End()
	req = req.WithContext(ctx)
	wantTraceID := span.SpanContext().TraceID().String()

	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}
	if got := bus.count(); got != 1 {
		t.Fatalf("publishes = %d, want 1", got)
	}

	gotTraceID, ok := event.TraceIDFromContext(bus.lastContext())
	if !ok || gotTraceID != wantTraceID {
		t.Errorf("expected the publish context to carry TraceID %q, got (%q, %v)", wantTraceID, gotTraceID, ok)
	}
	// The live span must survive onto the publish context too, not just
	// its ID: without it the Bus adapter has nothing to inject and the
	// trace stops at the bus boundary.
	if got := trace.SpanContextFromContext(bus.lastContext()).TraceID().String(); got != wantTraceID {
		t.Errorf("expected the publish context to carry the live span (trace %q), got %q", wantTraceID, got)
	}
}

// TestDispatchRunbook_OmitsTraceIDWhenAbsentFromContext proves the other
// half of the same branch: a request with no recording span still
// dispatches successfully, without fabricating a trace ID.
func TestDispatchRunbook_OmitsTraceIDWhenAbsentFromContext(t *testing.T) {
	runbooks := newTestRunbookSource(t, "pb-1")
	jobs := newTestJobStore(t)
	bus := newCapturingBus()
	dispatcher := api.NewDispatcher(runbooks, jobs, bus)

	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, dispatchTestRequest(t, "routers", "pb-1"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}
	if got := bus.count(); got != 1 {
		t.Fatalf("publishes = %d, want 1", got)
	}
	if _, ok := event.TraceIDFromContext(bus.lastContext()); ok {
		t.Error("expected no TraceID in the publish context when absent from the request context")
	}
}

// TestDispatchRunbook_NoIdentityInContextReturns401 proves the guard at the
// very top of DispatchRunbook: reaching this handler with no Identity in
// context (a direct unit-test call bypassing AuthMiddleware, or a second
// router mounting this handler unguarded) is answered 401, the same
// "middleware absence, not an authorization failure" case
// TestRequireScope_NoIdentityIs401 (authz_test.go) already proves for the
// router boundary itself. Nothing is created or published: this check runs
// before any of that.
func TestDispatchRunbook_NoIdentityInContextReturns401(t *testing.T) {
	runbooks := newTestRunbookSource(t, "pb-1")
	jobs := newTestJobStore(t)
	bus := newCapturingBus()
	dispatcher := api.NewDispatcher(runbooks, jobs, bus)

	// Built directly with httptest.NewRequest, not dispatchTestRequest:
	// dispatchTestRequest always stamps dispatchTestIdentity onto the
	// context, which is exactly the setup step this test needs to omit.
	req := httptest.NewRequest(http.MethodPost, "/dispatch?group=routers&runbook=pb-1", nil)
	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
	if got := bus.count(); got != 0 {
		t.Errorf("publishes = %d, want 0: a request with no Identity must never reach the publish step", got)
	}
}

// TestDispatchRunbook_RunbookResolutionInternalErrorReturns500 proves the
// "err, not ErrNotFound" branch of the runbook-resolution step: a real
// runbook.DirSource asked to compile a file that exists but is not valid
// runbook YAML returns a real compile error distinct from ErrNotFound
// (dir_source.go's own Get), and DispatchRunbook must map that to a
// generic 500 (never leaking the compiler's own error text, which can name
// a file path) rather than the 404 the previous test proves for a genuinely
// missing runbook. Per RULE 0, this exercises the real
// runbook.NewDirSource/Get path rather than a hand-rolled Source double,
// the same way TestDispatchRunbook_UnknownRunbookReturns404 exercises the
// real ErrNotFound branch.
func TestDispatchRunbook_RunbookResolutionInternalErrorReturns500(t *testing.T) {
	dir := t.TempDir()
	// Syntactically invalid YAML (an unterminated flow sequence), so
	// parseWorkflowYAML fails inside Get with a real compile error, never
	// ErrNotFound: the file exists and is readable, it just does not
	// parse.
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("id: [unterminated\n"), 0o644); err != nil {
		t.Fatalf("writing broken runbook fixture: %v", err)
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}
	jobs := newTestJobStore(t)
	bus := newCapturingBus()
	dispatcher := api.NewDispatcher(runbooks, jobs, bus)

	req := dispatchTestRequest(t, "routers", "broken")
	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response %q: %v", rr.Body.String(), err)
	}
	if body.Error != "internal error" {
		t.Errorf("error message = %q, want the generic %q (the compiler's own error text must never reach the client)", body.Error, "internal error")
	}
	if got := bus.count(); got != 0 {
		t.Errorf("publishes = %d, want 0", got)
	}
}

// TestDispatchRunbook_JobPersistFailureReturns500 proves the "job create
// failed" branch: a real dispatch.JobStore whose underlying *ent.Client has
// already been closed (a real, representative failure mode, a lost database
// connection, not a hand-rolled JobStore double per RULE 0) makes
// jobs.Create return a real error, and DispatchRunbook must map that to a
// generic 500 without ever publishing the job.requested event a caller
// could otherwise poll for forever against a Job that was never actually
// persisted.
func TestDispatchRunbook_JobPersistFailureReturns500(t *testing.T) {
	runbooks := newTestRunbookSource(t, "pb-1")
	client := newSerializedSQLiteClient(t, "dispatcher-job-persist-failure")
	if err := client.Close(); err != nil {
		t.Fatalf("closing client ahead of the real test: %v", err)
	}
	jobs := dispatch.NewEntJobStore(client)
	bus := newCapturingBus()
	dispatcher := api.NewDispatcher(runbooks, jobs, bus)

	req := dispatchTestRequest(t, "routers", "pb-1")
	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	if got := bus.count(); got != 0 {
		t.Errorf("publishes = %d, want 0: a job that failed to persist must never be published as requested", got)
	}
}

// publishFailingBus is a minimal event.Bus whose Publish always fails.
// event.NewInProcessBus's own real Publish implementation can only ever
// fail from a canceled context, and DispatchRunbook's publish step always
// builds pubCtx fresh from context.Background() (never inheriting the
// request context's cancellation), so no real Bus implementation in this
// codebase can be driven into this branch; a minimal stub honoring the
// event.Bus interface is this file's own adapter_test.go precedent
// (failingBus) for the identical situation, matching AGENTS.md's "mock
// interfaces, not concrete types" allowance.
type publishFailingBus struct{}

func (publishFailingBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	return errors.New("deliberate publish failure")
}

func (publishFailingBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}

func (publishFailingBus) Close() error { return nil }

// TestDispatchRunbook_PublishFailureReturns500 proves the last failure
// branch: the Job row is already durably persisted by the time Publish is
// attempted (step 3 runs before step 4, dispatcher.go's own numbered
// comments), so a publish failure must still answer 500 rather than the
// 202 a caller would wrongly read as "the worker will pick this up",
// leaving a Job stuck in "pending" with no job.requested event ever sent
// for it, exactly the crash-between-steps scenario DispatchRunbook's own
// step-3 comment describes.
func TestDispatchRunbook_PublishFailureReturns500(t *testing.T) {
	runbooks := newTestRunbookSource(t, "pb-1")
	jobs := newTestJobStore(t)
	dispatcher := api.NewDispatcher(runbooks, jobs, publishFailingBus{})

	req := dispatchTestRequest(t, "routers", "pb-1")
	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response %q: %v", rr.Body.String(), err)
	}
	if body.Error != "internal error" {
		t.Errorf("error message = %q, want %q", body.Error, "internal error")
	}
}
