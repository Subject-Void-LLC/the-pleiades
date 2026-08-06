package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	pkginventory "github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory/inventorytest"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// --- Mocks ---

type MockRepository struct {
	Count int
}

func (m *MockRepository) GetGroup(ctx context.Context, sel pkginventory.Selector) (inventory.Iterator, error) {
	return &MockIterator{count: m.Count, current: 0}, nil
}

// GetByName, Create, and Save exist to satisfy the Repository port. The
// dispatcher under test only ever streams a group, so these fail loudly
// rather than returning a zero value: a test that starts depending on them
// should say so out loud instead of silently exercising a stub that does
// nothing.

func (m *MockRepository) GetByName(ctx context.Context, name string) (pkginventory.InventoryItem, error) {
	return nil, errors.New("MockRepository.GetByName is not implemented for these tests")
}

func (m *MockRepository) Create(ctx context.Context, item pkginventory.InventoryItem) error {
	return errors.New("MockRepository.Create is not implemented for these tests")
}

func (m *MockRepository) Save(ctx context.Context, item pkginventory.InventoryItem) error {
	return errors.New("MockRepository.Save is not implemented for these tests")
}

type MockIterator struct {
	count   int
	current int
}

func (i *MockIterator) Next(ctx context.Context) bool {
	if i.current < i.count {
		i.current++
		return true
	}
	return false
}

func (i *MockIterator) Item() pkginventory.InventoryItem {
	return &inventorytest.Stub{
		StubName: "test-device",
		Props:    map[string]pkginventory.PropertyValue{"ip": "10.0.0.1"},
	}
}

func (i *MockIterator) Error() error { return nil }
func (i *MockIterator) Close() error { return nil }

type MockAuthEvaluator struct {
	Allow bool
}

func (m *MockAuthEvaluator) ValidateToken(ctx context.Context, tokenStr string) (*auth.Identity, error) {
	return &auth.Identity{Subject: "user"}, nil
}

func (m *MockAuthEvaluator) CheckAccess(ctx context.Context, id *auth.Identity, requiredScopes ...string) error {
	if m.Allow {
		return nil
	}
	return errors.New("unauthorized")
}

// mockBus is a minimal event.Bus fake: Dispatcher only ever calls Publish,
// never Subscribe. It replaces a previous MockJetStream that embedded the
// entire jetstream.JetStream interface just to override one method -- an
// artifact of Dispatcher depending on a raw driver interface instead of
// the Bus port it depends on now.
type mockBus struct {
	Publishes int
	LastEvent event.Event
	LastCtx   context.Context
}

func (m *mockBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	m.Publishes++
	m.LastEvent = evt
	m.LastCtx = ctx
	return nil
}

func (m *mockBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}

func (m *mockBus) Close() error {
	return nil
}

func TestDispatcher_ReleaseGate(t *testing.T) {
	repo := &MockRepository{Count: 10000} // 10,000 devices!
	eval := &MockAuthEvaluator{Allow: true}
	bus := &mockBus{}

	dispatcher := api.NewDispatcher(repo, eval, bus)

	req := httptest.NewRequest("POST", "/dispatch?group=routers&runbook=pb-1", nil)
	// Inject the mock identity that AuthMiddleware normally would
	ctx := context.WithValue(req.Context(), api.IdentityKeyForTest, &auth.Identity{Subject: "user"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %v", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if int(resp["dispatched"].(float64)) != 10000 {
		t.Errorf("expected 10000 messages to be dispatched, got %v", resp["dispatched"])
	}

	if bus.Publishes != 10000 {
		t.Errorf("expected Bus.Publish to be called 10000 times, got %d", bus.Publishes)
	}
}

// noIPMockIterator yields a single device with no "ip" property, so
// DispatchRunbook's own "typed Properties accessor" guard (the fix for
// the panic-on-missing-ip bug its own comment documents) can be proven
// directly, not just assumed safe because every other test's device
// happens to have one.
type noIPMockIterator struct {
	yielded bool
}

func (i *noIPMockIterator) Next(ctx context.Context) bool {
	if i.yielded {
		return false
	}
	i.yielded = true
	return true
}

func (i *noIPMockIterator) Item() pkginventory.InventoryItem {
	return &inventorytest.Stub{StubName: "no-ip-device", Props: map[string]pkginventory.PropertyValue{}}
}

func (i *noIPMockIterator) Error() error { return nil }
func (i *noIPMockIterator) Close() error { return nil }

type noIPMockRepository struct{}

func (m *noIPMockRepository) GetGroup(ctx context.Context, sel pkginventory.Selector) (inventory.Iterator, error) {
	return &noIPMockIterator{}, nil
}

func (m *noIPMockRepository) GetByName(ctx context.Context, name string) (pkginventory.InventoryItem, error) {
	return nil, errors.New("not implemented for this test")
}

func (m *noIPMockRepository) Create(ctx context.Context, item pkginventory.InventoryItem) error {
	return errors.New("not implemented for this test")
}

func (m *noIPMockRepository) Save(ctx context.Context, item pkginventory.InventoryItem) error {
	return errors.New("not implemented for this test")
}

func dispatchTestRequest(t *testing.T) *http.Request {
	t.Helper()
	req := httptest.NewRequest("POST", "/dispatch?group=routers&runbook=pb-1", nil)
	ctx := context.WithValue(req.Context(), api.IdentityKeyForTest, &auth.Identity{Subject: "user"})
	return req.WithContext(ctx)
}

func decodeDispatchResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func TestDispatcher_UnauthorizedDeviceCountsAsFailed(t *testing.T) {
	repo := &MockRepository{Count: 3}
	eval := &MockAuthEvaluator{Allow: false}
	bus := &mockBus{}
	dispatcher := api.NewDispatcher(repo, eval, bus)

	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, dispatchTestRequest(t))

	resp := decodeDispatchResponse(t, rr)
	if int(resp["dispatched"].(float64)) != 0 {
		t.Errorf("expected 0 dispatched, got %v", resp["dispatched"])
	}
	if int(resp["failed"].(float64)) != 3 {
		t.Errorf("expected 3 failed, got %v", resp["failed"])
	}
	if bus.Publishes != 0 {
		t.Errorf("expected 0 publishes for an unauthorized identity, got %d", bus.Publishes)
	}
}

func TestDispatcher_MissingIPCountsAsFailed(t *testing.T) {
	repo := &noIPMockRepository{}
	eval := &MockAuthEvaluator{Allow: true}
	bus := &mockBus{}
	dispatcher := api.NewDispatcher(repo, eval, bus)

	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, dispatchTestRequest(t))

	resp := decodeDispatchResponse(t, rr)
	if int(resp["dispatched"].(float64)) != 0 {
		t.Errorf("expected 0 dispatched, got %v", resp["dispatched"])
	}
	if int(resp["failed"].(float64)) != 1 {
		t.Errorf("expected 1 failed, got %v", resp["failed"])
	}
	if bus.Publishes != 0 {
		t.Errorf("expected 0 publishes for a device with no ip property, got %d", bus.Publishes)
	}
}

// failingMockBus is an event.Bus whose Publish always errors, proving
// DispatchRunbook's own publish-failure branch counts the device as
// failed rather than panicking or aborting the whole batch -- this is the
// exact fix this phase made for real (the previous code published to a
// subject no stream covered, so this branch was silently exercised on
// every single request in production; see FAILURE_PATTERNS.md #17's
// "Update").
type failingMockBus struct {
	Publishes int
}

func (m *failingMockBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	m.Publishes++
	return errors.New("deliberate publish failure")
}

func (m *failingMockBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}

func (m *failingMockBus) Close() error { return nil }

func TestDispatcher_PublishFailureCountsAsFailed(t *testing.T) {
	repo := &MockRepository{Count: 5}
	eval := &MockAuthEvaluator{Allow: true}
	bus := &failingMockBus{}
	dispatcher := api.NewDispatcher(repo, eval, bus)

	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, dispatchTestRequest(t))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK even when every publish fails (per-device failure, not a batch abort), got %v", rr.Code)
	}

	resp := decodeDispatchResponse(t, rr)
	if int(resp["dispatched"].(float64)) != 0 {
		t.Errorf("expected 0 dispatched, got %v", resp["dispatched"])
	}
	if int(resp["failed"].(float64)) != 5 {
		t.Errorf("expected 5 failed, got %v", resp["failed"])
	}
	if bus.Publishes != 5 {
		t.Errorf("expected Bus.Publish to be attempted for all 5 devices, got %d", bus.Publishes)
	}
}

// TestDispatcher_PropagatesTraceIDFromContext proves the trace-propagation
// branch in DispatchRunbook: when a real OpenTelemetry span is recording
// on the request context (as TracingMiddleware makes it in the real
// router), DispatchRunbook bridges that span's trace ID onto the context
// it publishes with via event.WithTraceID, and grafts the live span itself
// onto that context so the Bus adapter can inject W3C trace context into
// the outgoing message headers.
//
// The published Event's own TraceID field is stamped by Bus.Publish itself
// from that context (see stampEnvelope), not by Dispatcher directly, so
// this checks the context Publish receives, the thing Dispatcher actually
// controls, rather than the Event field a real adapter -- not this test's
// minimal mockBus -- is the one that sets.
//
// The span is started through a real SDK tracer rather than a fake context
// value: a no-op tracer mints an all-zero, invalid span context, so a test
// built on one would pass while proving nothing about the real path.
func TestDispatcher_PropagatesTraceIDFromContext(t *testing.T) {
	repo := &MockRepository{Count: 1}
	eval := &MockAuthEvaluator{Allow: true}
	bus := &mockBus{}
	dispatcher := api.NewDispatcher(repo, eval, bus)

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	req := dispatchTestRequest(t)
	ctx, span := tp.Tracer("test").Start(req.Context(), "test-request")
	defer span.End()
	req = req.WithContext(ctx)
	wantTraceID := span.SpanContext().TraceID().String()

	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, req)

	if bus.Publishes != 1 {
		t.Fatalf("expected 1 publish, got %d", bus.Publishes)
	}
	gotTraceID, ok := event.TraceIDFromContext(bus.LastCtx)
	if !ok || gotTraceID != wantTraceID {
		t.Errorf("expected the publish context to carry TraceID %q, got (%q, %v)", wantTraceID, gotTraceID, ok)
	}
	// The live span must survive onto the publish context too, not just
	// its ID: without it the Bus adapter has nothing to inject and the
	// trace stops at the bus boundary.
	if got := trace.SpanContextFromContext(bus.LastCtx).TraceID().String(); got != wantTraceID {
		t.Errorf("expected the publish context to carry the live span (trace %q), got %q", wantTraceID, got)
	}
}

// TestDispatcher_OmitsTraceIDWhenAbsentFromContext proves the other half
// of the same branch: a request with no recording span still dispatches
// successfully, without fabricating a trace ID.
func TestDispatcher_OmitsTraceIDWhenAbsentFromContext(t *testing.T) {
	repo := &MockRepository{Count: 1}
	eval := &MockAuthEvaluator{Allow: true}
	bus := &mockBus{}
	dispatcher := api.NewDispatcher(repo, eval, bus)

	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, dispatchTestRequest(t))

	if bus.Publishes != 1 {
		t.Fatalf("expected 1 publish, got %d", bus.Publishes)
	}
	if _, ok := event.TraceIDFromContext(bus.LastCtx); ok {
		t.Error("expected no TraceID in the publish context when absent from the request context")
	}
}
