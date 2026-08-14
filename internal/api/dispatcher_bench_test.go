package api_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// BenchmarkLaunchTemplate measures the cost of a launch itself: resolving a
// template, recording what the launch supplied, persisting one Job row
// through a real dispatch.JobStore, and publishing one job.requested event
// through a real event.Bus.
//
// Constant with respect to how many devices the dispatch will eventually
// reach, and that is the property worth having a benchmark for.
// internal/dispatch.Worker owns fan-out entirely, off the request path, so
// a launch against ten devices and one against ten thousand cost the same
// here; a change that made this scale with fleet size would be putting the
// fan-out back where Phase 14 took it out of.
func BenchmarkLaunchTemplate(b *testing.B) {
	dispatcher := api.NewDispatcher(newTestRunbookSource(b, "pb-1"), newTestJobStore(b), event.NewInProcessBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}),
		api.WithLaunchConfigs(&recordingConfigs{}))

	ctx := context.Background()
	cfg := launch.Config{Overrides: launch.Fields{"limit": "edge-01"}}

	b.ResetTimer()
	for range b.N {
		if _, _, err := dispatcher.LaunchTemplate(ctx, "bench@example.com", 12, cfg, nil); err != nil {
			b.Fatalf("LaunchTemplate: %v", err)
		}
	}
}
