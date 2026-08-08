package event_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
)

// FuzzPayloadEnvelope tests the CloudEvent wrapper against arbitrary
// types, massive strings, and malformed data to ensure the serialization
// boundary never panics.
func FuzzPayloadEnvelope(f *testing.F) {
	f.Add("uuid-1", "device.created", `{"status":"ok"}`)
	f.Add("", "", `{}`)
	f.Add("xSS", strings.Repeat("long-type-", 50), `{"inj":"<script>alert(1)</script>"}`)

	f.Fuzz(func(t *testing.T, id string, evtType string, rawJSON string) {
		// Attempt to wrap a raw JSON string (simulating arbitrary payload)
		evt, err := event.WrapPayload(id, evtType, json.RawMessage(rawJSON))
		if err != nil {
			t.Logf("Handled wrap error gracefully: %v", err)
			return
		}

		// Marshal the resulting envelope
		b, err := json.Marshal(evt)
		if err != nil {
			t.Logf("Handled marshal error gracefully: %v", err)
			return
		}

		// Unmarshal it back to ensure symmetry
		var decoded event.Event
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Fatalf("Symmetry broken! Failed to unmarshal what we just marshaled: %v", err)
		}

		// JSON marshalling coerces invalid UTF-8 into unicode replacement chars (\ufffd)
		// So we only assert symmetry on valid UTF-8 strings.
		if utf8.ValidString(id) && decoded.ID != id {
			t.Errorf("ID mismatch: got %s, want %s", decoded.ID, id)
		}
		if utf8.ValidString(evtType) && decoded.Type != evtType {
			t.Errorf("Type mismatch: got %s, want %s", decoded.Type, evtType)
		}
	})
}
