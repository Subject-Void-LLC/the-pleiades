package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/go-chi/chi/v5"
	"github.com/nats-io/nats.go/jetstream"
)

// testJobID is a well-formed job ID. It is a UUID because StreamLogs
// requires one: the job ID is concatenated into a NATS subject, where a
// wildcard character would widen the subscription past the requested job.
const testJobID = "1f8c8a48-0f21-4c65-9a45-2f6bd9b0a111"

// mockJetStreamForLogs fakes only CreateOrUpdateConsumer, the one method
// LogStreamer's per-request consumer creation calls; every other
// jetstream.JetStream method falls through to the embedded nil interface
// and is never exercised by this test.
type mockJetStreamForLogs struct {
	jetstream.JetStream
	consumer *MockLogConsumer
}

func (m *mockJetStreamForLogs) CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	return m.consumer, nil
}

type MockLogConsumer struct {
	jetstream.Consumer
	PayloadMsgs []jetstream.Msg
}

func (m *MockLogConsumer) Consume(handler jetstream.MessageHandler, opts ...jetstream.PullConsumeOpt) (jetstream.ConsumeContext, error) {
	if len(m.PayloadMsgs) == 0 {
		return nil, errors.New("no messages")
	}

	msgs := m.PayloadMsgs
	m.PayloadMsgs = nil

	// done mirrors real jetstream's ConsumeContext.Closed(): it closes
	// once this goroutine has finished invoking handler for every
	// message, i.e. once consuming is "fully stopped" and no more calls
	// into handler (and therefore no more writes to the caller's
	// http.ResponseWriter) will happen. A caller that waits on Closed()
	// before returning, as StreamLogs now does, depends on this actually
	// closing only after the last handler call: returning a nil channel
	// here (as this mock used to) would make that wait block forever.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, msg := range msgs {
			handler(msg)
		}
	}()

	return &MockConsumeContext{done: done}, nil
}

type MockConsumeContext struct {
	done chan struct{}
}

func (m *MockConsumeContext) Stop()                   {}
func (m *MockConsumeContext) Drain()                  {}
func (m *MockConsumeContext) Closed() <-chan struct{} { return m.done }

type MockMessageBatch struct {
	msgs []jetstream.Msg
}

func (m *MockMessageBatch) Messages() <-chan jetstream.Msg {
	ch := make(chan jetstream.Msg, len(m.msgs))
	for _, msg := range m.msgs {
		ch <- msg
	}
	close(ch)
	return ch
}

func (m *MockMessageBatch) Error() error {
	return nil
}

type MockLogMsg struct {
	jetstream.Msg
	data []byte
	ack  bool
}

func (m *MockLogMsg) Data() []byte {
	return m.data
}

func (m *MockLogMsg) Ack() error {
	m.ack = true
	return nil
}

// failingAckMsg is a jetstream.Msg whose Ack always errors, so
// TestStreamLogs_ToleratesAckFailure can prove StreamLogs logs an Ack
// failure rather than crashing (AckNonePolicy means the server does not
// track it anyway, but the client-side call and its error handling still
// run on every delivery).
type failingAckMsg struct {
	jetstream.Msg
	data []byte
}

func (m *failingAckMsg) Data() []byte { return m.data }
func (m *failingAckMsg) Ack() error   { return errors.New("deliberate ack failure") }

// consumerCreationErrJetStream fakes CreateOrUpdateConsumer failing, so
// TestStreamLogs_ReturnsErrorWhenConsumerCreationFails can prove
// StreamLogs surfaces that failure as a 500 instead of panicking on a nil
// consumer.
type consumerCreationErrJetStream struct {
	jetstream.JetStream
}

func (consumerCreationErrJetStream) CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	return nil, errors.New("deliberate consumer creation failure")
}

// nonFlushingResponseWriter implements http.ResponseWriter but
// deliberately not http.Flusher, so
// TestStreamLogs_ReturnsErrorWhenStreamingUnsupported can exercise
// StreamLogs's own "Streaming unsupported" guard, which httptest's own
// ResponseRecorder (used by every other test in this file) cannot reach
// because it always implements Flusher.
type nonFlushingResponseWriter struct {
	header     http.Header
	statusCode int
}

func (w *nonFlushingResponseWriter) Header() http.Header         { return w.header }
func (w *nonFlushingResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nonFlushingResponseWriter) WriteHeader(statusCode int)  { w.statusCode = statusCode }

func TestStreamLogs_ReleaseGate(t *testing.T) {
	consumer := &MockLogConsumer{
		PayloadMsgs: []jetstream.Msg{
			&MockLogMsg{data: []byte(`{"event":"runner_on_ok"}`)},
		},
	}
	js := &mockJetStreamForLogs{consumer: consumer}

	streamer := api.NewLogStreamer(js)

	r := chi.NewRouter()
	r.Get("/api/v1/jobs/{id}/logs", streamer.StreamLogs)

	req := httptest.NewRequest("GET", "/api/v1/jobs/"+testJobID+"/logs", nil)
	// Add a short timeout so the SSE stream closes automatically
	ctx, cancel := context.WithTimeout(req.Context(), 50*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Server blocks until context is cancelled
	r.ServeHTTP(rr, req)

	res := rr.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %v", res.StatusCode)
	}

	contentType := res.Header.Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Errorf("expected text/event-stream content type, got %v", contentType)
	}

	body := rr.Body.String()

	if !strings.Contains(body, "event: init") {
		t.Errorf("expected init event in stream, got: %s", body)
	}

	if !strings.Contains(body, `data: {"event":"runner_on_ok"}`) {
		t.Errorf("expected JSON log data in stream, got: %s", body)
	}
}

func TestStreamLogs_RequiresJobID(t *testing.T) {
	streamer := api.NewLogStreamer(&mockJetStreamForLogs{})

	r := chi.NewRouter()
	r.Get("/api/v1/jobs/{id}/logs", streamer.StreamLogs)

	// A trailing-slash-free path with an empty {id} segment does not
	// route at all in chi, so this calls StreamLogs directly with a bare
	// request chi never populated a URL param on, the same shape as any
	// caller that reached this handler without going through the router.
	req := httptest.NewRequest("GET", "/api/v1/jobs//logs", nil)
	rr := httptest.NewRecorder()
	streamer.StreamLogs(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for a missing job id, got %d", rr.Code)
	}
}

// TestStreamLogs_RejectsSubjectInjectingJobIDs is the regression test for
// a real vulnerability found while auditing this boundary: the job ID is
// concatenated into a NATS subject (topology.LogSubject), and NATS subject
// wildcards are ordinary characters, so a job ID of ">" built the
// FilterSubject "pleiades.jobs.logs.>" and streamed every job's logs in the
// system to any authenticated caller. A "*" did the same for one segment.
//
// The assertion is that no such request ever reaches consumer creation:
// consumerCreationErrJetStream would answer 500 if it did, so a 400 here
// means the handler refused before touching the bus at all.
func TestStreamLogs_RejectsSubjectInjectingJobIDs(t *testing.T) {
	tests := []struct {
		name  string
		jobID string
	}{
		{name: "full wildcard", jobID: ">"},
		{name: "token wildcard", jobID: "*"},
		{name: "wildcard suffix", jobID: "1f8c8a48.>"},
		{name: "subject separator", jobID: "1f8c8a48.0f21"},
		{name: "empty", jobID: ""},
		{name: "space", jobID: " "},
		{name: "newline injection", jobID: "1f8c8a48-0f21-4c65-9a45-2f6bd9b0a111\ndata: forged"},
		{name: "not a uuid", jobID: "123"},
	}

	streamer := api.NewLogStreamer(consumerCreationErrJetStream{})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/jobs/x/logs", nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.jobID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			rr := httptest.NewRecorder()
			streamer.StreamLogs(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("job id %q returned %d, want %d: it reached the bus instead of being refused",
					tt.jobID, rr.Code, http.StatusBadRequest)
			}
			// The body must be the fixed message and nothing else: a
			// rejection that quotes the rejected input back is how a
			// refused injection becomes a reflected one. Phase 13 moved
			// every error body onto the JSON encoder seam (respond.go),
			// so this now decodes rather than string-compares, but the
			// property under test is unchanged and is asserted more
			// strictly below: the rejected input appears nowhere in the
			// response at all.
			// Exact equality, not a substring check: the body must be
			// this fixed document and nothing else, which is a stricter
			// statement of "no caller input in it" than searching for
			// the input would be. (A substring search cannot express it
			// at all for a degenerate input like a single space, which
			// occurs in the fixed message too.)
			const wantBody = `{"error":"job id must be a UUID"}`
			if got := strings.TrimSpace(rr.Body.String()); got != wantBody {
				t.Errorf("error body is %q, want exactly %q", got, wantBody)
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("error body is not JSON: %q: %v", rr.Body.String(), err)
			}
		})
	}
}

func TestStreamLogs_ReturnsErrorWhenConsumerCreationFails(t *testing.T) {
	streamer := api.NewLogStreamer(consumerCreationErrJetStream{})

	req := httptest.NewRequest("GET", "/api/v1/jobs/"+testJobID+"/logs", nil)
	rr := httptest.NewRecorder()

	// chi.URLParam needs a routing context to read "id" from; set one up
	// directly rather than going through a full router for this
	// single-handler test.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", testJobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	streamer.StreamLogs(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when consumer creation fails, got %d", rr.Code)
	}
}

func TestStreamLogs_ReturnsErrorWhenConsumeFails(t *testing.T) {
	// An empty PayloadMsgs slice makes MockLogConsumer.Consume itself
	// return an error, exercising StreamLogs's own "Failed to start
	// consumer" branch.
	consumer := &MockLogConsumer{PayloadMsgs: nil}
	js := &mockJetStreamForLogs{consumer: consumer}
	streamer := api.NewLogStreamer(js)

	req := httptest.NewRequest("GET", "/api/v1/jobs/"+testJobID+"/logs", nil)
	rr := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", testJobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	streamer.StreamLogs(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when Consume fails, got %d", rr.Code)
	}
}

func TestStreamLogs_ReturnsErrorWhenStreamingUnsupported(t *testing.T) {
	consumer := &MockLogConsumer{PayloadMsgs: []jetstream.Msg{&MockLogMsg{data: []byte(`{}`)}}}
	js := &mockJetStreamForLogs{consumer: consumer}
	streamer := api.NewLogStreamer(js)

	req := httptest.NewRequest("GET", "/api/v1/jobs/"+testJobID+"/logs", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", testJobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := &nonFlushingResponseWriter{header: make(http.Header)}
	streamer.StreamLogs(w, req)

	if w.statusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for a ResponseWriter with no Flusher support, got %d", w.statusCode)
	}
}

func TestStreamLogs_ToleratesAckFailure(t *testing.T) {
	consumer := &MockLogConsumer{
		PayloadMsgs: []jetstream.Msg{&failingAckMsg{data: []byte(`{"event":"runner_on_ok"}`)}},
	}
	js := &mockJetStreamForLogs{consumer: consumer}
	streamer := api.NewLogStreamer(js)

	r := chi.NewRouter()
	r.Get("/api/v1/jobs/{id}/logs", streamer.StreamLogs)

	req := httptest.NewRequest("GET", "/api/v1/jobs/"+testJobID+"/logs", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 50*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	// Must not panic even though every Ack call fails.
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK despite the ack failure, got %d", rr.Code)
	}
}
