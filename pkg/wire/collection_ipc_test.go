package wire

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestChildRequest_JSONRoundTrip proves ChildRequest survives a marshal and
// unmarshal unchanged, including the omitempty Secrets field, since a
// request built for a device with no stored credential must decode back
// to a nil (not empty-but-non-nil) map for the child's own "no secrets for
// this task" check to behave the same way regardless of which shape
// produced it.
func TestChildRequest_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   ChildRequest
	}{
		{
			name: "with secrets",
			in: ChildRequest{
				FQCN:         "net.ssh.ping",
				Params:       map[string]any{"command": "echo pong"},
				JobID:        "job-1",
				DeviceID:     "device-1",
				DeviceName:   "core-switch-1",
				DeviceHost:   "10.0.0.1",
				SSHPort:      22,
				Capabilities: []capability.Name{capability.NameSSHTransport},
				Secrets:      map[string]string{"username": "admin", "password": "hunter2"},
			},
		},
		{
			name: "no secrets",
			in: ChildRequest{
				FQCN:       "net.ssh.ping",
				Params:     map[string]any{},
				JobID:      "job-2",
				DeviceID:   "device-2",
				DeviceName: "core-switch-2",
				DeviceHost: "10.0.0.2",
				SSHPort:    22,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotJSON, err := json.Marshal(tt.in)
			if err != nil {
				t.Fatalf("Marshal() returned an error: %v", err)
			}

			var got ChildRequest
			if err := json.Unmarshal(gotJSON, &got); err != nil {
				t.Fatalf("Unmarshal() returned an error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.in) {
				t.Errorf("round trip = %+v, want %+v", got, tt.in)
			}
		})
	}
}

// TestChildResponse_ErrorDistinguishesFailureFromCrash proves a
// ChildResponse reporting a Collection-method failure decodes with a
// non-empty Error, while the absence of any response bytes at all (the
// crash case, simulated here by decoding an empty byte slice) is a decode
// error the parent must handle separately rather than a zero-value
// ChildResponse silently standing in for "the child crashed."
func TestChildResponse_ErrorDistinguishesFailureFromCrash(t *testing.T) {
	failure := ChildResponse{Error: "dial tcp: connection refused"}
	gotJSON, err := json.Marshal(failure)
	if err != nil {
		t.Fatalf("Marshal() returned an error: %v", err)
	}

	var got ChildResponse
	if err := json.Unmarshal(gotJSON, &got); err != nil {
		t.Fatalf("Unmarshal() returned an error: %v", err)
	}
	if got.Error == "" {
		t.Errorf("Error = %q, want non-empty", got.Error)
	}

	var crashed ChildResponse
	if err := json.Unmarshal(nil, &crashed); err == nil {
		t.Error("Unmarshal(nil) succeeded, want an error: an empty response must not decode as a valid zero-value success")
	}
}
