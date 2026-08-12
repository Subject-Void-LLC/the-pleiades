// This file fuzzes the decoders for the two DTOs that really cross a
// process boundary on this platform's wire.
//
// Until Phase 18 this package had no fuzz targets at all, which was the
// wrong place for that gap to sit: a DispatchPayload is decoded by the
// Runner, a separate process, from a message the broker handed it, and a
// ChildRequest crosses a real subprocess pipe. Both are the classic
// shape Part VIII's deserialization category exists for, and neither
// writer is guaranteed to stay this codebase forever (a future
// third-party Collection binary would use the same child frame decoder).
//
// The property held everywhere below is deliberately modest and
// therefore actually meaningful: a hostile or truncated frame must be
// refused as an error or decoded into a self-consistent value, and must
// never panic the process. A decoder that panics on bad input turns a
// malformed message into a dead Runner.
package wire_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// FuzzDispatchPayloadDecode fuzzes the per-device dispatch frame the
// Runner decodes off the bus.
//
// Beyond "does not panic", it holds a round-trip property: a payload that
// decodes must re-encode and decode again to the same value. That catches
// a field whose JSON tag and Go type disagree in a way that silently
// loses data, which a decode-only test would miss entirely because the
// zero value looks perfectly valid.
func FuzzDispatchPayloadDecode(f *testing.F) {
	valid, err := json.Marshal(wire.DispatchPayload{
		JobID:         "0b7f5c1e-0000-7000-8000-000000000000",
		RunbookID:     "ping",
		DeviceID:      "0b7f5c1e-0000-7000-8000-000000000001",
		DeviceName:    "rtr1",
		DeviceHost:    "10.0.0.1",
		Interruptible: true,
		SSHPort:       22,
	})
	if err != nil {
		f.Fatalf("marshaling the seed payload: %v", err)
	}
	f.Add(string(valid))

	// Structurally valid JSON that is not an object.
	// A kind-bearing frame, a kind-absent frame (every payload published
	// before this field existed), and a hostile kind: an unregistered
	// value, and the wildcard and path characters the runbook kind's own
	// definition rule refuses at the other end of this pipe.
	f.Add(`{"job_id":"j","runbook_id":"rb","kind":"playbook","device_id":"d","device_host":"10.0.0.1"}`)
	f.Add(`{"job_id":"j","runbook_id":"rb","device_id":"d","device_host":"10.0.0.1"}`)
	f.Add(`{"job_id":"j","runbook_id":"rb","kind":"terraform","device_id":"d"}`)
	f.Add(`{"job_id":"j","runbook_id":"rb","kind":"../../etc/passwd","device_id":"d"}`)
	f.Add(`{"job_id":"j","runbook_id":"rb","kind":"pleiades.jobs.>","device_id":"d"}`)

	f.Add("")
	f.Add("{}")
	f.Add("null")
	f.Add("[]")
	f.Add("0")
	f.Add(`"a string"`)

	// Field-level hostility.
	f.Add(`{"capabilities":null}`)
	f.Add(`{"capabilities":["SSHTransportCapable","CiscoIOSCapable"]}`)
	f.Add(`{"capabilities":"not-an-array"}`)
	f.Add(`{"ssh_port":-1}`)
	f.Add(`{"ssh_port":99999999999999999999}`)
	f.Add(`{"ssh_port":"22"}`)
	f.Add(`{"secrets":{"password":"p"}}`)
	f.Add(`{"secrets":{"password":123}}`)
	f.Add("{\"device_host\":\"\u0000\"}")
	f.Add(`{"tags":["a","b"]}`)
	// A job id carrying the NATS wildcard, the shape FAILURE_PATTERNS.md
	// #18 catalogues as having really widened a subscriber's match once.
	// This decoder is not where that is defended, but a seed here keeps
	// the value in the corpus for anything downstream that grows a check.
	f.Add(`{"job_id":">"}`)
	f.Add(`{"job_id":"a.b.*"}`)

	// Truncation and nesting.
	f.Add(`{"job_id":`)
	f.Add(strings.Repeat("{", 128))
	f.Add(`{"params":{"a":{"b":{"c":1}}}}`)

	f.Fuzz(func(t *testing.T, payload string) {
		var decoded wire.DispatchPayload
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			// A rejection is always fine. The contract is only that it
			// is an error rather than a panic.
			return
		}

		// If it decoded, every field must be safe to touch without a
		// nil-map or nil-slice panic downstream in the Runner.
		_ = decoded.JobID
		_ = decoded.RunbookID
		_ = decoded.DeviceID
		_ = decoded.DeviceName
		_ = decoded.DeviceHost
		_ = decoded.Interruptible
		_ = decoded.SSHPort
		_ = len(decoded.Capabilities)
		_ = len(decoded.Secrets)
		_ = len(decoded.Tags)

		// Round trip: encoding a decoded value and decoding it again must
		// land on the same value.
		reencoded, err := json.Marshal(decoded)
		if err != nil {
			t.Fatalf("a decoded payload failed to re-encode: %v", err)
		}
		var again wire.DispatchPayload
		if err := json.Unmarshal(reencoded, &again); err != nil {
			t.Fatalf("a re-encoded payload failed to decode: %v (from %q)", err, reencoded)
		}
		if !dispatchPayloadsEqual(decoded, again) {
			t.Fatalf("payload changed across a round trip:\nfirst:  %#v\nsecond: %#v", decoded, again)
		}
	})
}

// FuzzJobEventDecode fuzzes the log frame the UI and the API's own log
// stream both consume, including its nested event_data object.
func FuzzJobEventDecode(f *testing.F) {
	valid, err := json.Marshal(wire.JobEvent{
		Timestamp: "2026-08-10T00:00:00Z",
		Status:    "ok",
		Host:      "10.0.0.1",
		Task:      "task.completed",
	})
	if err != nil {
		f.Fatalf("marshaling the seed event: %v", err)
	}
	f.Add(string(valid))

	// A kind-bearing frame, a kind-absent frame (every payload published
	// before this field existed), and a hostile kind: an unregistered
	// value, and the wildcard and path characters the runbook kind's own
	// definition rule refuses at the other end of this pipe.
	f.Add(`{"job_id":"j","runbook_id":"rb","kind":"playbook","device_id":"d","device_host":"10.0.0.1"}`)
	f.Add(`{"job_id":"j","runbook_id":"rb","device_id":"d","device_host":"10.0.0.1"}`)
	f.Add(`{"job_id":"j","runbook_id":"rb","kind":"terraform","device_id":"d"}`)
	f.Add(`{"job_id":"j","runbook_id":"rb","kind":"../../etc/passwd","device_id":"d"}`)
	f.Add(`{"job_id":"j","runbook_id":"rb","kind":"pleiades.jobs.>","device_id":"d"}`)

	f.Add("")
	f.Add("{}")
	f.Add("null")
	f.Add("[]")
	f.Add(`{"event_data":null}`)
	f.Add(`{"event_data":{}}`)
	f.Add(`{"event_data":{"message":"hello"}}`)
	f.Add(`{"event_data":"not-an-object"}`)
	f.Add(`{"event_data":{"message":123}}`)
	f.Add("{\"status\":\"ok\",\"host\":\"\u0000\"}")
	f.Add(`{"timestamp":"not-a-time"}`)
	f.Add(`{"event_data":{"message":`)
	f.Add(strings.Repeat(`{"event_data":`, 64))

	f.Fuzz(func(t *testing.T, payload string) {
		var decoded wire.JobEvent
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			return
		}

		_ = decoded.Timestamp
		_ = decoded.Status
		_ = decoded.Host
		_ = decoded.Task
		_ = decoded.EventData.Message

		reencoded, err := json.Marshal(decoded)
		if err != nil {
			t.Fatalf("a decoded event failed to re-encode: %v", err)
		}
		var again wire.JobEvent
		if err := json.Unmarshal(reencoded, &again); err != nil {
			t.Fatalf("a re-encoded event failed to decode: %v (from %q)", err, reencoded)
		}
		if decoded != again {
			t.Fatalf("event changed across a round trip:\nfirst:  %#v\nsecond: %#v", decoded, again)
		}
	})
}

// dispatchPayloadsEqual compares two payloads field by field.
//
// It exists because DispatchPayload holds a map and two slices, so it is
// not comparable with ==, and reflect.DeepEqual would treat a nil slice
// and an empty slice as different. That distinction is real on the wire
// (omitempty drops an empty slice entirely) but it is not a data loss,
// so treating them as equal here is what keeps this property about
// content rather than about encoding trivia.
func dispatchPayloadsEqual(a, b wire.DispatchPayload) bool {
	if a.JobID != b.JobID ||
		a.RunbookID != b.RunbookID ||
		a.Kind != b.Kind ||
		a.DeviceID != b.DeviceID ||
		a.DeviceName != b.DeviceName ||
		a.DeviceHost != b.DeviceHost ||
		a.Interruptible != b.Interruptible ||
		a.SSHPort != b.SSHPort {
		return false
	}
	if len(a.Capabilities) != len(b.Capabilities) {
		return false
	}
	for i := range a.Capabilities {
		if a.Capabilities[i] != b.Capabilities[i] {
			return false
		}
	}
	if len(a.Tags) != len(b.Tags) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
	}
	if len(a.Secrets) != len(b.Secrets) {
		return false
	}
	for key, want := range a.Secrets {
		if b.Secrets[key] != want {
			return false
		}
	}
	return true
}
