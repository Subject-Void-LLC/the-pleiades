package wire

import (
	"encoding/json"
	"testing"
)

// TestJobEvent_JSONShape proves JobEvent's wire encoding matches the shape
// its three predecessors (internal/adapters/native.LogEvent,
// internal/engine's nodeEvent, internal/ansible.AnsibleEvent's Message-only
// half) already agreed on, so switching a publisher onto this type changes
// nothing a consumer observes on the wire.
func TestJobEvent_JSONShape(t *testing.T) {
	evt := JobEvent{
		Timestamp: "2026-08-09T00:00:00Z",
		Status:    "changed",
		Host:      "10.0.0.1",
		Task:      "ping",
	}
	evt.EventData.Message = "pong"

	gotJSON, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal() returned an error: %v", err)
	}

	want := `{"timestamp":"2026-08-09T00:00:00Z","status":"changed","host":"10.0.0.1","task":"ping","event_data":{"message":"pong"}}`
	if string(gotJSON) != want {
		t.Errorf("Marshal() = %s, want %s", gotJSON, want)
	}

	var got JobEvent
	if err := json.Unmarshal(gotJSON, &got); err != nil {
		t.Fatalf("Unmarshal() returned an error: %v", err)
	}
	if got != evt {
		t.Errorf("round trip = %+v, want %+v", got, evt)
	}
}
