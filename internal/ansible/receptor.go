// Package ansible provides the legacy Ansible/AWX interoperability adapter.
package ansible

import (
	"context"
	"fmt"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	"github.com/google/uuid"
)

// AnsibleEvent represents the structured JSON output from an Ansible runner/receptor.
type AnsibleEvent struct {
	Event     string                 `json:"event"`
	Task      string                 `json:"task,omitempty"`
	Host      string                 `json:"host,omitempty"`
	Status    string                 `json:"status,omitempty"` // ok, failed, changed, skipped
	EventData map[string]interface{} `json:"event_data,omitempty"`
	Timestamp string                 `json:"timestamp"`
}

// ReceptorAdapter acts as the interface to the Ansible execution plane.
type ReceptorAdapter struct {
	bus event.Bus
}

// NewReceptorAdapter initializes the adapter.
//
// bus replaces a raw jetstream.JetStream handle, the same fix
// internal/adapters/native.NewAdapter got: publishing through the Bus port
// to topology.LogSubject instead of a raw jetstream.JetStream.PublishMsg
// call to a bare "jobs.logs.<id>" literal.
func NewReceptorAdapter(bus event.Bus) *ReceptorAdapter {
	return &ReceptorAdapter{bus: bus}
}

// StreamMockJob generates mock AWX/Ansible events and publishes them to NATS.
// This is used to scaffold the UI and verify streaming performance.
func (r *ReceptorAdapter) StreamMockJob(ctx context.Context, jobID string, numEvents int) error {
	hosts := []string{"router-1", "router-2", "switch-a", "core-fw-1"}
	tasks := []string{"Gathering Facts", "Ensure configuration is present", "Write memory", "Verify OSPF adjacencies"}
	statuses := []string{"ok", "ok", "changed", "failed", "skipped"}

	for i := 0; i < numEvents; i++ {
		// Stop if context is cancelled
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		ansibleEvt := AnsibleEvent{
			Event:     "runner_on_ok",
			Task:      tasks[i%len(tasks)],
			Host:      hosts[i%len(hosts)],
			Status:    statuses[i%len(statuses)],
			Timestamp: time.Now().Format(time.RFC3339Nano),
			EventData: map[string]interface{}{
				"seq":     i,
				"message": fmt.Sprintf("Executed task %d successfully", i),
			},
		}

		// Inject an occasional failure for realistic UI rendering
		if i%10 == 0 {
			ansibleEvt.Event = "runner_on_failed"
			ansibleEvt.Status = "failed"
			ansibleEvt.EventData["message"] = "Connection timed out"
		}

		if err := r.publish(ctx, jobID, ansibleEvt); err != nil {
			return fmt.Errorf("failed to publish event %d for job %s: %w", i, jobID, err)
		}

		// Sleep slightly to simulate realistic streaming but keep it fast enough for testing
		time.Sleep(1 * time.Millisecond)
	}

	// Publish an EOF event so clients know the stream is done
	eofEvent := AnsibleEvent{
		Event:     "playbook_on_stats",
		Timestamp: time.Now().Format(time.RFC3339Nano),
		EventData: map[string]interface{}{"status": "completed"},
	}
	if err := r.publish(ctx, jobID, eofEvent); err != nil {
		return fmt.Errorf("failed to publish EOF event for job %s: %w", jobID, err)
	}

	return nil
}

// publish wraps ansibleEvt in the DRY envelope and publishes it to
// topology.LogSubject(jobID) via the Bus port.
func (r *ReceptorAdapter) publish(ctx context.Context, jobID string, ansibleEvt AnsibleEvent) error {
	evt, err := event.WrapPayload(uuid.New().String(), "job.log.ansible", ansibleEvt)
	if err != nil {
		return err
	}
	return r.bus.Publish(ctx, topology.LogSubject(jobID), *evt)
}
