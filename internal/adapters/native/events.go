package native

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
)

// publishJobEvent wraps evt in an event.Event envelope and publishes it to
// topology.LogSubject(jobID) via bus, the shared wire.JobEvent DTO
// replacing this package's own former private LogEvent struct (Phase 16's
// own "adopt the shared job-event DTO instead of a private log-event
// struct" item). It returns bus.Publish's own error unchanged, rather than
// logging and swallowing it as the adapter this replaces did.
func publishJobEvent(ctx context.Context, bus event.Bus, jobID string, evt wire.JobEvent) error {
	wrapped, err := event.WrapPayload(uuid.New().String(), "job.log", evt)
	if err != nil {
		return fmt.Errorf("failed to wrap job event: %w", err)
	}
	return bus.Publish(ctx, topology.LogSubject(jobID), *wrapped)
}
