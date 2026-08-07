package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// LogStreamer handles SSE log streaming to clients.
//
// The {id} URL parameter it reads is validated as a UUID before it is used
// for anything. That is not cosmetic input tidying: topology.LogSubject
// builds a NATS subject by concatenating the job ID onto a prefix, and
// NATS subject wildcards are ordinary characters, so a job ID of ">"
// produced the FilterSubject "pleiades.jobs.logs.>" and streamed every
// job's logs in the system to whoever asked. Every job ID this platform
// mints is a UUID (api.Dispatcher's own uuid.New()), so requiring one
// closes that hole with no loss of function. See FAILURE_PATTERNS.md.
type LogStreamer struct {
	js jetstream.JetStream
}

// NewLogStreamer constructs a LogStreamer against js, the shared JetStream
// handle topology.EnsureStream has already provisioned the Pleiades stream
// on.
//
// This used to take one already-built jetstream.Consumer instead: a real
// bug found while wiring this phase's composition roots. A single
// Consumer's FilterSubject is fixed at construction time, but StreamLogs
// reads the job ID from each request's own {id} URL param and never used
// it to filter anything, so every concurrent viewer, regardless of which
// job they requested, saw whatever job the one shared Consumer happened to
// be constructed against. NewLogStreamer now builds a fresh,
// topology.LogViewerConsumerConfig(jobID)-scoped ephemeral consumer per
// request instead, so the {id} param actually does something.
func NewLogStreamer(js jetstream.JetStream) *LogStreamer {
	return &LogStreamer{js: js}
}

// StreamLogs handles GET /api/v1/jobs/{id}/logs via SSE.
func (ls *LogStreamer) StreamLogs(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(jobID); err != nil {
		RespondError(w, r, http.StatusBadRequest, "job id must be a UUID")
		return
	}

	consumer, err := ls.js.CreateOrUpdateConsumer(r.Context(), topology.StreamName, topology.LogViewerConsumerConfig(jobID))
	if err != nil {
		// The job ID is deliberately not echoed back. It is
		// caller-supplied, and reflecting it into a response body is the
		// shape of a reflected-injection bug even after the UUID check
		// above makes this particular one unreachable; the real error goes
		// to the log, where an operator can still see which job failed.
		slog.Error("failed to create log consumer",
			slog.String("job_id", jobID),
			slog.String("error", err.Error()),
		)
		RespondError(w, r, http.StatusInternalServerError, "failed to create log consumer")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		RespondError(w, r, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ctx := r.Context()

	// http.ResponseWriter is not safe for concurrent use (the net/http
	// Handler contract requires a handler to serialize its own writes),
	// but two goroutines write to w here: this one, for the initial
	// "event: init" ping below, and the Consume callback's own goroutine,
	// for every message delivery. writeMu serializes every write to w
	// across both, closing a real race a -race run of this file's own
	// test suite caught directly.
	var writeMu sync.Mutex

	// Headers are set before Consume starts, not after. Setting a header
	// is not a write: it mutates a map and commits nothing, so this keeps
	// the "no write before Consume is checked" property below intact while
	// closing a real data race a -race run caught directly. The Consume
	// callback runs on its own goroutine and writes to w, and the first
	// such write reads this header map to build the response; mutating it
	// afterwards, as this code used to, is a concurrent map read and write
	// that writeMu cannot help with, because one side of it is not a write
	// to w at all. See FAILURE_PATTERNS.md.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Allow CORS for the web UI dev server
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Consume is started, and checked for error, before anything is
	// written to w: a Write (even just the "event: init" line below)
	// implicitly commits the response to a 200 status, after which a
	// later error can never actually be reported as an HTTP error status
	// anymore, only as body text appended to an already-successful
	// response. Starting Consume first means every real failure path
	// here can still report its own honest status code.
	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		writeMu.Lock()
		defer writeMu.Unlock()

		// Forward JSON data directly to the SSE stream
		fmt.Fprintf(w, "data: %s\n\n", msg.Data())
		// AckNonePolicy (topology.LogViewerConsumerConfig) means the
		// server does not track this consumer's acks at all; Ack() is
		// still called to follow the jetstream.Msg contract, and its
		// error is logged rather than silently dropped, but it carries
		// no delivery-guarantee consequence either way.
		if err := msg.Ack(); err != nil {
			slog.Error("failed to ack log message", slog.String("job_id", jobID), slog.String("error", err.Error()))
		}
		flusher.Flush()
	})
	if err != nil {
		RespondError(w, r, http.StatusInternalServerError, "failed to start consumer")
		return
	}

	// Send an initial ping to establish connection. Consume above may
	// already be delivering messages concurrently by this point (the
	// real, network-latency-bound JetStream adapter has a natural gap
	// here, but nothing structurally guarantees one), so this write goes
	// through the same writeMu the message handler uses.
	writeMu.Lock()
	fmt.Fprintf(w, "event: init\ndata: connected to job %s\n\n", jobID)
	flusher.Flush()
	writeMu.Unlock()

	// Block until client disconnects.
	<-ctx.Done()

	// Stop the consumer and wait for Closed() to close before returning.
	// Closed() only closes once consuming is fully stopped and no more
	// messages will be handled (see jetstream.ConsumeContext), so this
	// guarantees the callback above will not write to w again after this
	// point. Stop() alone does not give that guarantee: per jetstream's
	// docs it does not wait for an in-flight handler invocation to
	// finish, so without this wait the callback's goroutine could still
	// be calling fmt.Fprintf(w, ...) and flusher.Flush() after this
	// handler has returned and relinquished its right to use w (see
	// net/http's Handler contract), racing with anything that reads the
	// response afterward.
	cc.Stop()
	<-cc.Closed()
}
