package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	pkginventory "github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

// Dispatcher coordinates the execution of Runbooks against an inventory group.
type Dispatcher struct {
	repo inventory.Repository
	bus  event.Bus
}

// NewDispatcher creates a new task dispatcher.
//
// bus is the event.Bus port, not a raw jetstream.JetStream: this is the
// fix for a real, previously silent production bug. DispatchRunbook used
// to publish straight to a bare "runbooks.dispatch" literal via
// jetstream.JetStream.PublishMsg, a subject no stream's configured filter
// ever covered (FAILURE_PATTERNS.md #17's bug family), so every dispatch
// failed with its error swallowed into errCount. Publishing through Bus
// and topology.DispatchSubject fixes the subject mismatch and gives
// Bus.Publish its first real, meaningful production caller.
//
// This used to also take an auth.Evaluator, so the per-device loop below
// could re-check "runbook:execute" on every iteration. Phase 12 removed
// it: the check was an invariant, identical for every device on every
// call, so it was one boundary-level check away from being redundant, not
// defense in depth. Now that api.RequireScope enforces the same scope at
// the router boundary before this handler is ever reached, the loop's own
// copy could never fail differently from the one at the door, and kept
// alive would have been dead code pretending to be a security control. See
// FAILURE_PATTERNS.md #66.
func NewDispatcher(repo inventory.Repository, bus event.Bus) *Dispatcher {
	return &Dispatcher{
		repo: repo,
		bus:  bus,
	}
}

// DispatchPayload is the structure of the message sent to NATS.
type DispatchPayload struct {
	JobID      string `json:"job_id"`
	RunbookID  string `json:"runbook_id"`
	DeviceName string `json:"device_name"`
	DeviceIP   string `json:"device_ip"`
}

// DispatchRunbook executes a runbook against an inventory group. It
// iterates through the group and emits one NATS message per device.
// Authorization (the caller holds auth.ScopeRunbookExecute) is enforced
// once, at the router boundary, by api.RequireScope before this handler
// ever runs; the identity is read from context here only to stamp the
// actor onto each published event, not to authorize anything.
func (d *Dispatcher) DispatchRunbook(w http.ResponseWriter, r *http.Request) {
	// 1. Extract Identity from Context. Its absence here means the
	// request reached this handler with no middleware in front of it at
	// all (a direct unit-test call, or a second router mounting this
	// handler unguarded, PATTERNS.md's Front Controller entry's whole
	// concern) rather than an authorization failure, which RequireScope
	// already turned into a 403 upstream.
	id, ok := IdentityFromContext(r.Context())
	if !ok {
		RespondError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	groupName := r.URL.Query().Get("group")
	runbookID := r.URL.Query().Get("runbook")

	if groupName == "" || runbookID == "" {
		RespondError(w, r, http.StatusBadRequest, "missing 'group' or 'runbook' query parameter")
		return
	}

	// 2. Obtain an Iterator for the target group
	iter, err := d.repo.GetGroup(r.Context(), pkginventory.Selector{GroupName: groupName})
	if err != nil {
		// The repository's own error text is logged, not returned: it can
		// name tables, columns, and hosts, which an authenticated caller
		// holding only runbook:execute has no business reading.
		loggerFrom(r).ErrorContext(r.Context(), "failed to query inventory for dispatch",
			slog.String("group", groupName),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}
	defer iter.Close()

	var dispatchedCount int
	var errCount int

	jobID := uuid.New().String()

	// 3. The NATS Push Loop
	for iter.Next(r.Context()) {
		device := iter.Item()

		// Capability check: HasCapability("runbook:execute" and friends) is
		// Phase 14's own open checklist item, not this one's. The scope
		// check that used to live here is gone; api.RequireScope already
		// enforced auth.ScopeRunbookExecute before this handler was ever
		// reached, so re-checking the identical, per-call-invariant fact on
		// every device would only have been dead code pretending to guard
		// something.

		// 4. Build and Publish NATS Payload
		// The typed Properties accessor replaces an unchecked map assertion
		// that used to panic the handler for any device lacking "ip".
		deviceIP, ok := device.Properties().String("ip")
		if !ok {
			errCount++
			continue
		}

		deviceName := string(device.ID())
		payload := DispatchPayload{
			JobID:      jobID,
			RunbookID:  runbookID,
			DeviceName: deviceName,
			DeviceIP:   deviceIP,
		}

		evt, err := event.WrapPayload(uuid.New().String(), "runbook.dispatched", payload)
		if err != nil {
			errCount++
			continue
		}

		// Background context for publishing, same reasoning the original
		// code already had for its own background context: an HTTP
		// client disconnecting mid-loop should not cancel dispatches
		// already queued for other devices. Actor and TraceID are
		// stamped explicitly from the request onto that background
		// context, rather than inherited via cancellation, so the
		// published envelope still carries who and which trace triggered
		// it. IdempotencyKey is set to JobID+DeviceName, a natural key
		// stable across a retry of this exact device's dispatch within
		// this job and more meaningful than a fresh random default.
		pubCtx := event.WithActor(context.Background(), id.Subject)
		if traceID, ok := TraceIDFromContext(r.Context()); ok {
			pubCtx = event.WithTraceID(pubCtx, traceID)
		}
		// The envelope's TraceID field is for a human reading an audit
		// row. The machine-readable W3C trace context that actually lets
		// the Runner continue this trace rides in the NATS message
		// headers, injected by the Bus adapter, and needs the live span
		// from the request context rather than the detached background
		// one, so it is grafted back on here.
		pubCtx = trace.ContextWithSpan(pubCtx, trace.SpanFromContext(r.Context()))
		pubCtx = event.WithIdempotencyKey(pubCtx, jobID+":"+deviceName)

		if err := d.bus.Publish(pubCtx, topology.DispatchSubject(), *evt); err != nil {
			errCount++
			continue
		}

		dispatchedCount++
	}

	if err := iter.Error(); err != nil {
		// The iterator's own message is logged, never returned: it can
		// name tables, columns, and hosts, and this endpoint is reachable
		// by any caller holding runbook:execute.
		loggerFrom(r).ErrorContext(r.Context(), "inventory iterator failed mid-dispatch",
			slog.String("job_id", jobID),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	// A typed struct rather than the map[string]interface{} this used to
	// build. FAILURE_PATTERNS.md #71 records what a map costs on the way
	// out: every integer in it round-tripped through float64, so a
	// dispatch count past 2^53 would have been served wrong. A typed
	// struct is marshaled once, from the Go values themselves.
	resp := dispatchResponse{
		Status:     "dispatched",
		JobID:      jobID,
		Dispatched: dispatchedCount,
		Failed:     errCount,
	}
	Respond(w, r, http.StatusOK, &resp)
}

// dispatchResponse is the body a successful dispatch returns.
type dispatchResponse struct {
	LinkSet

	Status     string `json:"status"`
	JobID      string `json:"job_id"`
	Dispatched int    `json:"dispatched"`
	Failed     int    `json:"failed"`
}
