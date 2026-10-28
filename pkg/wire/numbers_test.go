// Tests that numbers crossing the per-task child's pipe keep their type:
// an int stays an int with every digit, a whole float stays a float.
package wire_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestChildResponse_FactsKeepTheirNumberTypes: facts written by a child
// read back in the parent as the ints and floats the method recorded,
// through maps and lists, rather than as float64 throughout.
func TestChildResponse_FactsKeepTheirNumberTypes(t *testing.T) {
	sent := wire.ChildResponse{Changed: true, Facts: map[string]any{
		"count":  3,
		"big":    int64(9007199254740993),
		"neg":    -12,
		"ratio":  2.5,
		"whole":  4.0,
		"wide":   1e21,
		"floats": []float64{1, 1.5},
		"items":  []string{"a", "b"},
		"nested": map[string]any{"n": 7, "list": []any{1, 2.0, "x"}},
	}}
	data, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got wire.ChildResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := map[string]any{
		"count":  3,
		"big":    9007199254740993,
		"neg":    -12,
		"ratio":  2.5,
		"whole":  4.0,
		"wide":   1e21,
		"floats": []any{1.0, 1.5},
		"items":  []any{"a", "b"},
		"nested": map[string]any{"n": 7, "list": []any{1, 2.0, "x"}},
	}
	if !got.Changed || !reflect.DeepEqual(got.Facts, want) {
		t.Errorf("facts read back as\n%#v\nwant\n%#v", got.Facts, want)
	}
	// The whole float crossed as a float, written with its point.
	if !strings.Contains(string(data), `"whole":4.0`) {
		t.Errorf("a whole float was not written with its point: %s", data)
	}
	// Marshaling copies: the method's own facts are left as they were.
	if sent.Facts["whole"] != 4.0 {
		t.Errorf("marshaling changed the facts it was handed: %#v", sent.Facts["whole"])
	}
}

// TestChildRequest_ParamsKeepTheirNumberTypes: a task's params reach the
// child as the ints and floats the parent rendered, so a big number reaches
// a device command with every digit, and every other field is unchanged.
func TestChildRequest_ParamsKeepTheirNumberTypes(t *testing.T) {
	sent := wire.ChildRequest{FQCN: "net.ios.ping", SSHPort: 22, Params: map[string]any{"count": 5, "big": 9007199254740993, "timeout": 2.0}}
	data, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got wire.ChildRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, sent) {
		t.Errorf("request read back as\n%#v\nwant\n%#v", got, sent)
	}

	// The device's properties decode as encoding/json always decoded them,
	// which the device types' own readers expect.
	withProps := wire.ChildRequest{FQCN: "m", DeviceProperties: map[string]any{"port": 2222}}
	data, err = json.Marshal(withProps)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var props wire.ChildRequest
	if err := json.Unmarshal(data, &props); err != nil || props.DeviceProperties["port"] != float64(2222) {
		t.Errorf("device properties = %#v, %v, want them decoded as before", props.DeviceProperties, err)
	}

	var empty wire.ChildRequest
	if err := json.Unmarshal([]byte(`{"fqcn":"m","params":null}`), &empty); err != nil || empty.Params != nil || empty.FQCN != "m" {
		t.Errorf("a request with no params = %#v, %v", empty, err)
	}
}

// TestChildResponse_RefusesANumberNoFloatHolds: a number too large for a
// float64 is refused, as encoding/json's own decoding refuses it, rather
// than read back as something else.
func TestChildResponse_RefusesANumberNoFloatHolds(t *testing.T) {
	var got wire.ChildResponse
	if err := json.Unmarshal([]byte(`{"changed":false,"facts":{"n":1e400}}`), &got); err == nil {
		t.Fatalf("a number past float64's range was read back as %#v", got.Facts)
	}
	// An integer past int64's range is still a number a float64 holds.
	if err := json.Unmarshal([]byte(`{"changed":false,"facts":{"n":99999999999999999999}}`), &got); err != nil || got.Facts["n"] != 1e20 {
		t.Errorf("an integer past int64 = %#v, %v, want the float", got.Facts["n"], err)
	}
}

// TestDispatchPayload_ExtraVarsKeepTheirNumberTypes: a dispatch's extra
// variables, and a bound credential's, reach the Runner with a whole number
// as an int and every other field decoded as before (Fields' numbers stay
// float64, which launch.Fields' accessors read).
func TestDispatchPayload_ExtraVarsKeepTheirNumberTypes(t *testing.T) {
	data := []byte(`{"job_id":"j1","fields":{"forks":5},"extra_vars":{"n":3,"r":2.5,"l":[1,{"k":2}]},"injected":{"extra_vars":{"port":8443},"mask":["x"]}}`)
	var got wire.DispatchPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if want := map[string]any{"n": 3, "r": 2.5, "l": []any{1, map[string]any{"k": 2}}}; !reflect.DeepEqual(got.ExtraVars, want) {
		t.Errorf("extra_vars = %#v, want %#v", got.ExtraVars, want)
	}
	if got.Injected == nil || got.Injected.ExtraVars["port"] != 8443 || len(got.Injected.Mask) != 1 {
		t.Errorf("injected = %#v, want port as an int and the mask kept", got.Injected)
	}
	if got.JobID != "j1" || got.Fields["forks"] != float64(5) {
		t.Errorf("other fields = %q, %#v, want them decoded as before", got.JobID, got.Fields)
	}
	var none wire.DispatchPayload
	if err := json.Unmarshal([]byte(`{"job_id":"j2"}`), &none); err != nil || none.ExtraVars != nil || none.Injected != nil {
		t.Errorf("a payload with no extra_vars = %#v, %v", none, err)
	}
	if err := json.Unmarshal([]byte(`{"extra_vars":{"n":1e400}}`), &none); err == nil {
		t.Error("a number past float64's range was accepted")
	}
}
