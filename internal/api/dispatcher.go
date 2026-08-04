package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	"github.com/google/uuid"
)

// Dispatcher coordinates the execution of Runbooks against an inventory group.
type Dispatcher struct {
	repo inventory.Repository
	auth auth.Evaluator
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
func NewDispatcher(repo inventory.Repository, evaluator auth.Evaluator, bus event.Bus) *Dispatcher {
	return &Dispatcher{
		repo: repo,
		auth: evaluator,
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

// DispatchRunbook executes a runbook against an inventory group.
// It iterates through the group, verifies authorization per device, and emits a NATS message.
func (d *Dispatcher) DispatchRunbook(w http.ResponseWriter, r *http.Request) {
	// 1. Extract Identity from Context
	id, ok := r.Context().Value(identityKey).(*auth.Identity)
	if !ok || id == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	groupName := r.URL.Query().Get("group")
	runbookID := r.URL.Query().Get("runbook")

	if groupName == "" || runbookID == "" {
		http.Error(w, "Missing 'group' or 'runbook' query parameter", http.StatusBadRequest)
		return
	}

	// 2. Obtain an Iterator for the target group
	iter, err := d.repo.GetGroup(r.Context(), groupName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query inventory: %v", err), http.StatusInternalServerError)
		return
	}
	defer iter.Close()

	var dispatchedCount int
	var errCount int

	jobID := uuid.New().String()

	// 3. The NATS Push Loop
	for iter.Next(r.Context()) {
		device := iter.Item()

		// 4. Capability Check: Ensure the user is allowed to run runbooks
		// In a full implementation, we might check `runbook:execute:<device_type>`
		if err := d.auth.CheckAccess(r.Context(), id, "runbook:execute"); err != nil {
			errCount++
			continue // Skip unauthorized devices instead of failing the whole batch
		}

		// 5. Build and Publish NATS Payload
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
		if traceID, ok := r.Context().Value(traceIDKey).(string); ok {
			pubCtx = event.WithTraceID(pubCtx, traceID)
		}
		pubCtx = event.WithIdempotencyKey(pubCtx, jobID+":"+deviceName)

		if err := d.bus.Publish(pubCtx, topology.DispatchSubject(), *evt); err != nil {
			errCount++
			continue
		}

		dispatchedCount++
	}

	if err := iter.Error(); err != nil {
		http.Error(w, fmt.Sprintf("Iterator failed during stream: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "dispatched",
		"job_id":     jobID,
		"dispatched": dispatchedCount,
		"failed":     errCount,
	})
}
