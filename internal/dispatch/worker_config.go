// Package dispatch: Worker's own construction and configuration, split
// into its own sibling file, following job.go/ent_store.go's existing
// file-per-concern shape, so worker.go itself can stay focused on
// HandleJobRequested, the actual consumer logic, once that single
// function's surrounding file grew past AGENTS.md's ~300-line soft cap on
// logic files.
package dispatch

import (
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
)

// Worker consumes job.requested events and performs the durable fan-out
// for each one: resolving the job's runbook and target group, admitting
// or skipping each device, and publishing a wire.DispatchPayload per
// admitted device. It replaces the old synchronous loop
// internal/api/dispatcher.go's DispatchRunbook used to run inline inside
// an HTTP request.
type Worker struct {
	store    JobStore
	repo     inventory.Repository
	runbooks runbook.Source
	bus      event.Bus
	// fanOutLeaseTTL is this Worker's own fan-out lease window: both the
	// staleAfter duration passed to JobStore.BeginFanOut and the bound on
	// HandleJobRequested's own per-invocation context (see that method's
	// own doc comment, worker.go). Defaults to defaultFanOutLeaseTTL;
	// overridable via WithFanOutLeaseTTL.
	fanOutLeaseTTL time.Duration
}

// NewWorker builds a Worker over its four collaborator ports: store
// persists job and per-device task state, repo streams the target
// inventory group, runbooks resolves a job's RunbookID to its compiled
// capability requirements, and bus is where a per-device dispatch event
// is published to and where job.requested itself is consumed from. opts
// applies optional, non-default configuration (see WorkerOption); every
// existing caller (e.g. cmd/controller/main.go) can omit it entirely and
// gets defaultFanOutLeaseTTL.
func NewWorker(store JobStore, repo inventory.Repository, runbooks runbook.Source, bus event.Bus, opts ...WorkerOption) *Worker {
	w := &Worker{
		store:          store,
		repo:           repo,
		runbooks:       runbooks,
		bus:            bus,
		fanOutLeaseTTL: defaultFanOutLeaseTTL,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// WorkerOption configures optional, non-default behavior on a Worker built
// by NewWorker. The only implementation today is WithFanOutLeaseTTL.
type WorkerOption func(*Worker)

// WithFanOutLeaseTTL overrides a Worker's fan-out lease window from its
// production default (defaultFanOutLeaseTTL) to ttl. It exists chiefly for
// tests that need to prove BeginFanOut's stale-reclaim behavior or
// HandleJobRequested's own context-timeout behavior (see that method's
// own doc comment, worker.go) without waiting out the real, production
// ten-minute window; production callers should leave this at its default.
func WithFanOutLeaseTTL(ttl time.Duration) WorkerOption {
	return func(w *Worker) {
		w.fanOutLeaseTTL = ttl
	}
}

// defaultFanOutLeaseTTL is how long a job may sit in "fanning_out" with no
// RecordTask heartbeat before JobStore.BeginFanOut treats it as abandoned,
// most likely by a Worker process that crashed, was OOM-killed, or was
// restarted after claiming the fan-out but before calling Complete or
// Fail, and lets a redelivered job.requested reclaim and re-run it. It
// must comfortably exceed the time a healthy, actively-dispatching Worker
// can plausibly go between two consecutive per-device RecordTask calls
// (each one refreshes the heartbeat), so a slow but alive fan-out is never
// mistaken for a stuck one. This is the value every Worker uses unless
// overridden via WithFanOutLeaseTTL.
const defaultFanOutLeaseTTL = 10 * time.Minute

// jobRequestedPayload is the small local payload this package's own
// job.requested publisher (a later stage in this session, not this file)
// sends: just enough to look the job back up. Everything else about the
// job (RunbookID, GroupName, Actor) already lives in the Job row itself,
// so the event does not need to duplicate it.
type jobRequestedPayload struct {
	// JobID identifies the job to fan out.
	JobID string `json:"job_id"`
}
