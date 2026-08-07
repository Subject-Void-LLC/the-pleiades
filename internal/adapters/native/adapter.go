// Package native implements runner.ExecutionAdapter for native Go collections.
package native

import (
	"context"
	"log/slog"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
)

// Adapter implements the runner.ExecutionAdapter for native Go functions.
type Adapter struct {
	bus event.Bus
}

// NewAdapter creates a new native execution adapter.
//
// bus replaces a raw jetstream.JetStream handle: streamLog now publishes
// through the event.Bus port to topology.LogSubject, instead of a raw
// jetstream.JetStream.PublishMsg call to a bare "jobs.logs.<id>" literal
// that was never covered by any stream this package itself declared
// (FAILURE_PATTERNS.md #17).
func NewAdapter(bus event.Bus) *Adapter {
	return &Adapter{bus: bus}
}

// LogEvent structure expected by the UI.
type LogEvent struct {
	Timestamp string `json:"timestamp"`
	Status    string `json:"status"`
	Host      string `json:"host"`
	Task      string `json:"task"`
	EventData struct {
		Message string `json:"message"`
	} `json:"event_data"`
}

// Execute simulates running a native Go runbook (e.g. Ping).
func (a *Adapter) Execute(ctx context.Context, payload wire.DispatchPayload) error {
	// Simulate Ping Execution - Step 1: Start
	a.streamLog(ctx, payload.JobID, LogEvent{
		Timestamp: time.Now().Format(time.RFC3339),
		Status:    "started",
		Host:      payload.DeviceName,
		Task:      "Executing Native Collection: " + payload.RunbookID,
	})

	time.Sleep(500 * time.Millisecond)

	// Simulate Ping Execution - Step 2: Ping
	a.streamLog(ctx, payload.JobID, LogEvent{
		Timestamp: time.Now().Format(time.RFC3339),
		Status:    "changed",
		Host:      payload.DeviceName,
		Task:      "ping",
		EventData: struct {
			Message string `json:"message"`
		}{
			// DeviceHost, never the old DeviceIP: this is the property
			// every concrete device type in this codebase actually
			// populates (pkg/wire.DispatchPayload's own doc comment), so
			// this now reads a real address instead of the old field,
			// which named a property ("ip") no device type ever set.
			Message: "pong from " + payload.DeviceName + " (" + payload.DeviceHost + ")",
		},
	})

	time.Sleep(500 * time.Millisecond)

	// Simulate Ping Execution - Step 3: Complete
	a.streamLog(ctx, payload.JobID, LogEvent{
		Timestamp: time.Now().Format(time.RFC3339),
		Status:    "ok",
		Host:      payload.DeviceName,
		Task:      "task.completed",
		EventData: struct {
			Message string `json:"message"`
		}{
			Message: "Native execution finished successfully",
		},
	})

	return nil
}

// streamLog wraps evt in the DRY envelope and publishes it to
// topology.LogSubject(jobID) via the Bus port.
//
// A publish failure here is deliberately not returned to Execute's own
// caller: log streaming is an observability side effect of an execution
// that has already happened, not a precondition for it, so losing a log
// line must never fail the runbook it is describing. That decision used to
// be silent (the previous version discarded the error with no trace at
// all); it is now logged, matching this codebase's "must say so, not
// pretend" convention rather than pretending nothing could go wrong.
func (a *Adapter) streamLog(ctx context.Context, jobID string, logEvt LogEvent) {
	evt, err := event.WrapPayload(uuid.New().String(), "job.log", logEvt)
	if err != nil {
		slog.Error("failed to wrap log event", slog.String("job_id", jobID), slog.String("error", err.Error()))
		return
	}
	if err := a.bus.Publish(ctx, topology.LogSubject(jobID), *evt); err != nil {
		slog.Error("failed to publish log event", slog.String("job_id", jobID), slog.String("error", err.Error()))
	}
}
