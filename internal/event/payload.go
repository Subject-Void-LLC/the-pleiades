package event

import (
	"encoding/json"
	"time"
)

// Event is the generic, DRY envelope for all system events.
// Modeled after the CloudEvents spec, it ensures we don't rewrite JSON
// serializers for every new domain event (device.created, workflow.failed).
type Event struct {
	// ID is a unique UUID for idempotency.
	ID string `json:"id"`
	// Type defines the event schema (e.g., "device.created").
	Type string `json:"type"`
	// Timestamp is when the event occurred in UTC.
	Timestamp time.Time `json:"timestamp"`
	// Data holds the raw JSON payload specific to the Type.
	Data json.RawMessage `json:"data"`
}

// WrapPayload is a helper to marshal any Go struct into an Event envelope.
func WrapPayload(id string, eventType string, payload interface{}) (*Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Event{
		ID:        id,
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Data:      b,
	}, nil
}
