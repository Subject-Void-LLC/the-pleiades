// Tests that a request's mode reaches exactly one of a method's two
// functions across the process boundary, and that a mode the child does
// not recognize reaches neither.
//
// Each case runs twice: once handed straight to InvokeRequest, and once
// marshaled into a frame and served by ServeChild, which is the path a
// real child takes. The second is what proves the mode survives the JSON
// boundary rather than only the function call. A codec that dropped the
// field would turn every check into a real run, and the direct call alone
// would never notice.
package external_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// modePath is one way a request reaches a method: the direct call, or the
// full frame a real child reads and writes.
type modePath struct {
	name string
	run  func(t *testing.T, lookup external.LookupFunc, req wire.ChildRequest) wire.ChildResponse
}

// modePaths are both ways a request reaches a method.
var modePaths = []modePath{
	{
		name: "InvokeRequest",
		run: func(_ *testing.T, lookup external.LookupFunc, req wire.ChildRequest) wire.ChildResponse {
			return external.InvokeRequest(context.Background(), lookup, req)
		},
	},
	{
		name: "ServeChild frame",
		run: func(t *testing.T, lookup external.LookupFunc, req wire.ChildRequest) wire.ChildResponse {
			t.Helper()
			frame, err := json.Marshal(&req)
			if err != nil {
				t.Fatalf("marshaling request: %v", err)
			}
			var response, errOut bytes.Buffer
			if code := external.ServeChild(context.Background(), lookup, bytes.NewReader(frame), &response, &errOut); code != 0 {
				t.Fatalf("ServeChild() = %d, want 0: a refused mode is a response, not a broken exchange (stderr: %s)", code, errOut.String())
			}
			var resp wire.ChildResponse
			if err := json.NewDecoder(&response).Decode(&resp); err != nil {
				t.Fatalf("decoding response frame: %v", err)
			}
			return resp
		},
	},
}

// TestModeSelectsExactlyOneFunction is the table the stream's contract
// comes down to: an empty mode and "execute" run Invoke, "check" runs
// Check and never Invoke, "check" against a method without check support
// runs nothing, and a mode the child does not know runs nothing.
func TestModeSelectsExactlyOneFunction(t *testing.T) {
	cases := []struct {
		name          string
		mode          string
		supportsCheck bool
		wantInvoke    int
		wantCheck     int
		wantChanged   bool
		wantRan       string
		wantErr       string
	}{
		{name: "empty mode runs Invoke", mode: "", supportsCheck: true, wantInvoke: 1, wantChanged: true, wantRan: "invoke"},
		{name: "execute runs Invoke", mode: "execute", supportsCheck: true, wantInvoke: 1, wantChanged: true, wantRan: "invoke"},
		{name: "execute without check support still runs Invoke", mode: "execute", supportsCheck: false, wantInvoke: 1, wantChanged: true, wantRan: "invoke"},
		{name: "check runs Check and never Invoke", mode: "check", supportsCheck: true, wantCheck: 1, wantChanged: false, wantRan: "check"},
		{name: "check without check support runs nothing", mode: "check", supportsCheck: false, wantErr: "does not support check mode"},
		{name: "simulate is refused", mode: "simulate", supportsCheck: true, wantErr: "unknown mode"},
		{name: "a misspelled check is refused", mode: "chekc", supportsCheck: true, wantErr: "unknown mode"},
		{name: "mode is case sensitive", mode: "Check", supportsCheck: true, wantErr: "unknown mode"},
		{name: "a padded mode is refused", mode: " check", supportsCheck: true, wantErr: "unknown mode"},
	}
	for _, path := range modePaths {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				var calls callCounter
				desc := countingDescriptor("externaltest.modes", tc.supportsCheck, &calls)

				resp := path.run(t, tableLookup(desc), wire.ChildRequest{FQCN: desc.Name, Mode: tc.mode})

				if calls.invoke != tc.wantInvoke || calls.check != tc.wantCheck {
					t.Errorf("Invoke ran %d times and Check %d, want %d and %d", calls.invoke, calls.check, tc.wantInvoke, tc.wantCheck)
				}
				if tc.wantErr != "" {
					if !strings.Contains(resp.Error, tc.wantErr) {
						t.Errorf("Error = %q, want it to contain %q", resp.Error, tc.wantErr)
					}
					if resp.Changed || len(resp.Facts) != 0 {
						t.Errorf("a refused request's response = %+v, want no Changed and no Facts", resp)
					}
					return
				}
				if resp.Error != "" {
					t.Fatalf("Error = %q, want empty", resp.Error)
				}
				if resp.Changed != tc.wantChanged {
					t.Errorf("Changed = %v, want %v", resp.Changed, tc.wantChanged)
				}
				if got := resp.Facts["ran"]; got != tc.wantRan {
					t.Errorf("Facts[ran] = %v, want %q", got, tc.wantRan)
				}
			})
		}
	}
}

// TestCheckReceivesWhatInvokeWould proves Check is handed exactly the
// arguments Invoke is, which collection.Descriptor's doc comment requires:
// the params, the rebuilt device, and the device's credential. A check
// that could not read the device (no secret, no address) could only guess
// at what a real run would change. Both functions record what they saw,
// and the two responses must match field for field apart from Changed.
func TestCheckReceivesWhatInvokeWould(t *testing.T) {
	record := func(changed bool) collection.Method {
		return func(_ context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			host, _ := device.Properties().String("host")
			for key, value := range map[string]any{
				"param":    params["message"],
				"device":   device.Name(),
				"host":     host,
				"username": rc.InjectSecrets()["username"],
			} {
				if err := rc.SetStat(key, value); err != nil {
					return collection.Result{}, err
				}
			}
			return collection.Result{Changed: changed}, nil
		}
	}
	lookup := tableLookup(collection.Descriptor{
		Name: "externaltest.samearguments",
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: fixtureReversibility,
			SupportsCheck: true,
		},
		Invoke: record(true),
		Check:  record(false),
	})

	responses := map[string]wire.ChildResponse{}
	for _, mode := range []string{string(collection.ModeExecute), string(collection.ModeCheck)} {
		responses[mode] = modePaths[1].run(t, lookup, wire.ChildRequest{
			FQCN:       "externaltest.samearguments",
			Mode:       mode,
			Params:     map[string]any{"message": "hello"},
			DeviceName: "router1",
			DeviceHost: "10.0.0.1",
			Secrets:    map[string]string{"username": "admin"},
		})
	}

	execute, check := responses["execute"], responses["check"]
	if execute.Error != "" || check.Error != "" {
		t.Fatalf("errors: execute %q, check %q", execute.Error, check.Error)
	}
	if !execute.Changed || check.Changed {
		t.Errorf("Changed: execute %v, check %v, want true and false", execute.Changed, check.Changed)
	}
	want := map[string]any{"param": "hello", "device": "router1", "host": "10.0.0.1", "username": "admin"}
	for key, value := range want {
		if execute.Facts[key] != value {
			t.Errorf("execute Facts[%s] = %v, want %v", key, execute.Facts[key], value)
		}
		if check.Facts[key] != value {
			t.Errorf("check Facts[%s] = %v, want %v: Check must receive what Invoke does", key, check.Facts[key], value)
		}
	}
}

// TestInvokeRequest_CannotCheckCrossesAsItsOwnFlag covers the child half of
// the "cannot check this call" answer: returned from a check, however it
// was wrapped, it becomes CannotCheck with the method's reason as Error;
// returned from a real run it is an ordinary failure with no flag, so a
// parent can never read it as a skip there.
func TestInvokeRequest_CannotCheckCrossesAsItsOwnFlag(t *testing.T) {
	refuse := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{Changed: true}, fmt.Errorf("wrapped: %w", collection.CannotCheck("no creates was given"))
	}
	lookup := tableLookup(collection.Descriptor{
		Name: "externaltest.partly",
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: fixtureReversibility,
			SupportsCheck: true,
		},
		Invoke: refuse,
		Check:  refuse,
	})
	for _, tc := range []struct {
		mode      collection.Mode
		wantFlag  bool
		wantError string
	}{
		{collection.ModeCheck, true, "no creates was given"},
		{collection.ModeExecute, false, "wrapped: this call cannot be checked: no creates was given"},
	} {
		resp := external.InvokeRequest(context.Background(), lookup, wire.ChildRequest{FQCN: "externaltest.partly", Mode: string(tc.mode)})
		if resp.CannotCheck != tc.wantFlag || resp.Error != tc.wantError || resp.Changed || len(resp.Facts) != 0 {
			t.Errorf("%s: response = %+v, want CannotCheck %v and Error %q, and nothing else", tc.mode, resp, tc.wantFlag, tc.wantError)
		}
	}
}
