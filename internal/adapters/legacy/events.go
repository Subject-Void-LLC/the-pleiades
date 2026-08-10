package legacy

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
)

// publishJobEvent wraps evt in an event.Event envelope and publishes it to
// topology.LogSubject(jobID) via bus. Deliberately not shared with
// internal/adapters/native's own identical-looking publishJobEvent: the
// two packages must never import each other (neither is a dependency of
// the other's own domain), so a shared helper would need a third,
// common-ancestor package for six lines neither package's own Adapter
// logic otherwise needs.
func publishJobEvent(ctx context.Context, bus event.Bus, jobID string, evt wire.JobEvent) error {
	wrapped, err := event.WrapPayload(uuid.New().String(), "job.log", evt)
	if err != nil {
		return fmt.Errorf("failed to wrap job event: %w", err)
	}
	return bus.Publish(ctx, topology.LogSubject(jobID), *wrapped)
}
