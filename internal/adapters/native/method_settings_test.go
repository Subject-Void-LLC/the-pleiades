// Tests for the job's method settings through the adapter's real Execute: a
// setting this Runner's methods do not declare, or a value outside a
// setting's choices, refuses the dispatch before any task runs.
package native

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestExecute_RefusesAMethodSettingThisRunnerDoesNotKnow is the Runner's own
// hold on what the Controller held to its catalog: a Controller newer than
// this Runner, or a row written before the field's check, never runs every
// task with the parameter's default in place of what the job asked for.
func TestExecute_RefusesAMethodSettingThisRunnerDoesNotKnow(t *testing.T) {
	registerValidationFixtures(t)
	for name, settings := range map[string]map[string]any{
		"not a method this build knows": {"nothing.here.mode": "warn"},
		"declares no parameter a run":   {"valfixture.write.path": "/etc"},
	} {
		t.Run(name, func(t *testing.T) {
			bus := &mockBus{}
			adapter, err := NewAdapter(bus, writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n  - name: step\n    noop:\n      changed: true\n"), nil)
			if err != nil {
				t.Fatalf("NewAdapter: %v", err)
			}
			payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "pb-1", DeviceName: "router1", DeviceHost: "10.0.0.1",
				Fields: map[string]any{launch.MethodSettingsField: settings}}
			if _, err := adapter.Execute(context.Background(), payload); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("Execute() = %v, want a refusal saying %q", err, name)
			}
			if final := bus.lastJobEvent(t); final.Status != "failed" || !strings.Contains(final.EventData.Message, name) {
				t.Errorf("the job log ends %q (%q), want failed naming why", final.Status, final.EventData.Message)
			}
		})
	}
}
