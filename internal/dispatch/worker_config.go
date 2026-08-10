// Package dispatch: Worker's own construction and configuration, split
// into its own sibling file, following job.go/ent_store.go's existing
// file-per-concern shape, so worker.go itself can stay focused on
// HandleJobRequested, the actual consumer logic, once that single
// function's surrounding file grew past AGENTS.md's ~300-line soft cap on
// logic files.
package dispatch

import (
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
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
	// credentials resolves a device's stored credential at fan-out time,
	// so it can be attached directly to wire.DispatchPayload.Secrets
	// (PLAN.md Section 17's Just-in-Time delivery principle: the Runner
	// never holds its own copy of the decryption key, the Controller
	// resolves and hands it over already-decrypted, per job, per device).
	// A device with no stored credential is not a fan-out failure: only a
	// task that actually needs a secret fails downstream, the same place
	// a missing credential already fails at the Walk tier
	// (worker_devices.go's admitAndDispatchDevice).
	credentials credential.Store
	// fanOutLeaseTTL is this Worker's own fan-out lease window: both the
	// staleAfter duration passed to JobStore.BeginFanOut and the bound on
	// HandleJobRequested's own per-invocation context (see that method's
	// own doc comment, worker.go). Defaults to DefaultFanOutLeaseTTL;
	// overridable via WithFanOutLeaseTTL.
	fanOutLeaseTTL time.Duration
}

// NewWorker builds a Worker over its five collaborator ports: store
// persists job and per-device task state, repo streams the target
// inventory group, runbooks resolves a job's RunbookID to its compiled
// capability requirements, bus is where a per-device dispatch event is
// published to and where job.requested itself is consumed from, and
// credentials resolves each admitted device's stored credential so it can
// be attached to the dispatch payload. opts applies optional, non-default
// configuration (see WorkerOption); every existing caller (e.g.
// cmd/controller/main.go) can omit it entirely and gets
// DefaultFanOutLeaseTTL.
func NewWorker(store JobStore, repo inventory.Repository, runbooks runbook.Source, bus event.Bus, credentials credential.Store, opts ...WorkerOption) *Worker {
	w := &Worker{
		store:          store,
		repo:           repo,
		runbooks:       runbooks,
		bus:            bus,
		credentials:    credentials,
		fanOutLeaseTTL: DefaultFanOutLeaseTTL,
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
// production default (DefaultFanOutLeaseTTL) to ttl. It exists chiefly for
// tests that need to prove BeginFanOut's stale-reclaim behavior or
// HandleJobRequested's own context-timeout behavior (see that method's
// own doc comment, worker.go) without waiting out the real, production
// ten-minute window; production callers should leave this at its default.
func WithFanOutLeaseTTL(ttl time.Duration) WorkerOption {
	return func(w *Worker) {
		w.fanOutLeaseTTL = ttl
	}
}

// DefaultFanOutLeaseTTL is how long a job may sit in "fanning_out" with no
// RecordTask heartbeat before JobStore.BeginFanOut treats it as abandoned,
// most likely by a Worker process that crashed, was OOM-killed, or was
// restarted after claiming the fan-out but before calling Complete or
// Fail. It must comfortably exceed the time a healthy, actively-dispatching
// Worker can plausibly go between two consecutive per-device RecordTask
// calls (each one refreshes the heartbeat), so a slow but alive fan-out is
// never mistaken for a stuck one. This is the value every Worker uses
// unless overridden via WithFanOutLeaseTTL.
//
// **Reaching that stale state does not, on its own, get reclaimed by a
// redelivered job.requested**, despite what an earlier version of this
// comment claimed: JetStream's own redelivery budget (internal/topology's
// MaxDeliverDefault redeliveries at consumerAckWait apart) is far shorter
// than this value, production ten minutes, so the message dead-letters
// long before a job becomes eligible for reclaim, and no further delivery
// of it ever arrives. Reaper (reaper.go) is what actually triggers the
// reclaim, by re-publishing job.requested itself once
// JobStore.ListStaleFanOuts reports a job past this same window.
//
// Exported so a composition root can pass the identical value to both
// NewWorker (via WithFanOutLeaseTTL, or by leaving it at this default) and
// NewReaper: the two must agree, since ListStaleFanOuts and BeginFanOut
// both need to consider the same job stale at the same point for the
// reclaim to actually happen when the Reaper expects it to.
const DefaultFanOutLeaseTTL = 10 * time.Minute

// jobRequestedPayload is the small local payload this package's own
// job.requested publisher (a later stage in this session, not this file)
// sends: just enough to look the job back up. Everything else about the
// job (RunbookID, GroupName, Actor) already lives in the Job row itself,
// so the event does not need to duplicate it.
type jobRequestedPayload struct {
	// JobID identifies the job to fan out.
	JobID string `json:"job_id"`
}
