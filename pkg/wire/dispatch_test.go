// Package wire's tests prove the JSON wire shape of DispatchPayload
// exactly, field name and tag together, rather than only compiling.
//
// A struct tag typo (job_id misspelled job-id, say) compiles cleanly:
// encoding/json silently falls back to matching on the Go field name, or
// simply drops the value, and nothing short of an actual encode/decode
// catches it. Since DispatchPayload crosses a process boundary (the
// Controller marshals it, the Runner unmarshals it, in two separate
// binaries with no shared compiler pass between them), a tag mismatch
// here would not fail to build; it would silently decode into a
// zero-valued field on the far side.
package wire

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestDispatchPayload_JSONRoundTrip proves every field of DispatchPayload
// survives a marshal followed by an unmarshal unchanged, and that the
// marshaled JSON uses the exact wire key each field's json tag promises.
//
// The test is table-driven per .AGENTS/AGENTS.md's convention even though
// there is presently one meaningful case (all fields populated) plus the
// zero-value case, because a future field addition to DispatchPayload
// should extend this table rather than prompt a new, differently shaped
// test.
func TestDispatchPayload_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   DispatchPayload
		// wantJSON is the exact expected wire encoding, keyed on the
		// literal JSON field names the struct tags promise. Asserting
		// against the literal string, not just a round trip through the
		// same struct, is what would actually catch a tag typo: a round
		// trip alone would still pass if both the encoder and the decoder
		// used the same wrong tag.
		wantJSON string
	}{
		{
			name: "all fields populated",
			in: DispatchPayload{
				JobID:         "job-123",
				RunbookID:     "runbook-456",
				DeviceID:      "device-789",
				DeviceName:    "core-switch-1",
				DeviceHost:    "10.0.0.1",
				Interruptible: true,
				SSHPort:       22,
				Capabilities:  []capability.Name{capability.NameSSHTransport},
				Secrets:       map[string]string{"password": "hunter2"},
			},
			wantJSON: `{"job_id":"job-123","runbook_id":"runbook-456","device_id":"device-789","device_name":"core-switch-1","device_host":"10.0.0.1","interruptible":true,"ssh_port":22,"capabilities":["SSHTransportCapable"],"secrets":{"password":"hunter2"}}`,
		},
		{
			name:     "zero value",
			in:       DispatchPayload{},
			wantJSON: `{"job_id":"","runbook_id":"","device_id":"","device_name":"","device_host":"","interruptible":false,"ssh_port":0,"capabilities":null}`,
		},
		{
			name: "device name and device id deliberately differ",
			// This case exists to prove the two fields are genuinely
			// independent on the wire, the exact property the old,
			// deleted duplicate lacked: it had only one field where this
			// type has two, so a caller populating DeviceID with a name
			// derived from ID() and DeviceName from Name() could never be
			// caught by round-tripping alone if the fields were secretly
			// aliased. They are not: this asserts both keys appear with
			// their own distinct values.
			in: DispatchPayload{
				JobID:      "job-1",
				RunbookID:  "rb-1",
				DeviceID:   "id-only-value",
				DeviceName: "name-only-value",
				DeviceHost: "192.168.1.1",
			},
			wantJSON: `{"job_id":"job-1","runbook_id":"rb-1","device_id":"id-only-value","device_name":"name-only-value","device_host":"192.168.1.1","interruptible":false,"ssh_port":0,"capabilities":null}`,
		},
		{
			name: "interruptible false is explicit on the wire, not merely absent",
			// Interruptible has no omitempty (unlike a hypothetical
			// convenience shortcut): PLAN.md Section 16's safe default is
			// "interruptible" (true), decided upstream in
			// engine.Metadata.IsInterruptible(); an omitted key here would
			// decode to Go's own bool zero value, false, silently inverting
			// that default for whatever Runner reads it. This case proves
			// false always appears explicitly, never by omission.
			in: DispatchPayload{
				JobID:         "job-2",
				RunbookID:     "rb-2",
				DeviceID:      "device-2",
				DeviceName:    "device-2-name",
				DeviceHost:    "10.0.0.2",
				Interruptible: false,
			},
			wantJSON: `{"job_id":"job-2","runbook_id":"rb-2","device_id":"device-2","device_name":"device-2-name","device_host":"10.0.0.2","interruptible":false,"ssh_port":0,"capabilities":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotJSON, err := json.Marshal(tt.in)
			if err != nil {
				t.Fatalf("Marshal() returned an error: %v", err)
			}
			if string(gotJSON) != tt.wantJSON {
				t.Errorf("Marshal() = %s, want %s", gotJSON, tt.wantJSON)
			}

			// Decode back and compare against the original struct, which
			// proves the tags are symmetric: a tag that only the encoder
			// or only the decoder got right would fail one direction of
			// this test but not the other. reflect.DeepEqual, not !=,
			// because Capabilities and Secrets are a slice and a map:
			// DispatchPayload stopped being comparable with == the moment
			// either field was added.
			var got DispatchPayload
			if err := json.Unmarshal(gotJSON, &got); err != nil {
				t.Fatalf("Unmarshal() returned an error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.in) {
				t.Errorf("round trip = %+v, want %+v", got, tt.in)
			}
		})
	}
}

// TestDispatchPayload_UnmarshalFromWireKeys proves decoding works from a
// hand-written JSON literal using the exact key names the json tags
// declare, independent of whatever this package's own Marshal call
// produces. This is the direction that matters most in practice: the
// Runner decodes bytes a separate Controller binary produced, so the
// wire keys have to match by contract, not merely by both ends sharing
// the same struct definition.
func TestDispatchPayload_UnmarshalFromWireKeys(t *testing.T) {
	const raw = `{"job_id":"j1","runbook_id":"r1","device_id":"d1","device_name":"n1","device_host":"h1","interruptible":true,"ssh_port":22,"capabilities":["SSHTransportCapable"],"secrets":{"password":"hunter2"}}`

	want := DispatchPayload{
		JobID:         "j1",
		RunbookID:     "r1",
		DeviceID:      "d1",
		DeviceName:    "n1",
		DeviceHost:    "h1",
		Interruptible: true,
		SSHPort:       22,
		Capabilities:  []capability.Name{capability.NameSSHTransport},
		Secrets:       map[string]string{"password": "hunter2"},
	}

	var got DispatchPayload
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal() returned an error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unmarshal(%s) = %+v, want %+v", raw, got, want)
	}
}

// TestDispatchPayload_UnmarshalOmitsNewFields proves a payload published by
// an older Controller binary (one that predates SSHPort/Capabilities/
// Secrets) still decodes cleanly: the three new fields fall back to their
// Go zero values rather than the Unmarshal call failing. This is the
// backward-compatibility direction TestDispatchPayload_UnmarshalFromWireKeys
// does not exercise, since that test's own raw literal already includes
// every field.
func TestDispatchPayload_UnmarshalOmitsNewFields(t *testing.T) {
	const raw = `{"job_id":"j1","runbook_id":"r1","device_id":"d1","device_name":"n1","device_host":"h1","interruptible":true}`

	want := DispatchPayload{
		JobID:         "j1",
		RunbookID:     "r1",
		DeviceID:      "d1",
		DeviceName:    "n1",
		DeviceHost:    "h1",
		Interruptible: true,
	}

	var got DispatchPayload
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal() returned an error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unmarshal(%s) = %+v, want %+v", raw, got, want)
	}
}
