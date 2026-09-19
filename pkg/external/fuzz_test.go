// Fuzz tests for the child side of the boundary.
//
// FuzzReadChildRequest moved here from internal/adapters/native. Its
// original comment anticipated a third-party Collection binary using the
// same decoder; pkg/external is that binary's SDK, so the writer of the
// frame this decodes is no longer necessarily this codebase, and the fuzz
// now carries a decoded frame all the way through InvokeRequest into a
// real method body rather than stopping at the decode.
package external_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// fuzzDeviceReader is a method body that touches everything a method can
// read from what the request carried: every secret, every param, and
// every accessor of the rebuilt device. A decoded frame that could panic
// any of those downstream of the decoder panics here.
func fuzzDeviceReader(_ context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	for k, v := range rc.InjectSecrets() {
		if err := rc.SetStat("secret_len_"+k, len(v)); err != nil {
			return collection.Result{}, err
		}
	}
	for k, v := range params {
		if err := rc.EmitFact("param_"+k, v); err != nil {
			return collection.Result{}, err
		}
	}
	_ = device.ID()
	_ = device.Name()
	_ = device.Properties().Raw()
	_ = device.ShowInfo().Len()
	for _, c := range device.Capabilities() {
		_ = device.HasCapability(c)
	}
	_ = device.HasCapability(capability.NameServiceManager)
	if ssh, ok := device.(capability.SSHTransportCapable); ok {
		_ = ssh.SSHHost()
		_ = ssh.SSHPort()
	}
	return collection.Result{Changed: len(params) > 0}, nil
}

// FuzzReadChildRequest fuzzes the child's own inbound frame decoder, and
// then the rest of the child with whatever decoded. The property is that a
// hostile or truncated frame is refused as an error and never panics the
// process, and that a frame which does decode can be served, whatever it
// holds, without a panic anywhere downstream: in the mode parse, the
// device rebuild, the secret copy, or the method reading all of it.
func FuzzReadChildRequest(f *testing.F) {
	valid, err := json.Marshal(&wire.ChildRequest{
		FQCN:         "externaltest.fuzz",
		JobID:        "job-1",
		Params:       map[string]any{"message": "hello"},
		Capabilities: []capability.Name{capability.NameSystemd},
		Secrets:      map[string]string{"username": "u"},
	})
	if err != nil {
		f.Fatalf("marshaling seed: %v", err)
	}
	f.Add(string(valid))
	f.Add("")
	f.Add("{}")
	f.Add("null")
	f.Add("[]")
	f.Add("{\"fqcn\":")
	f.Add("{\"fqcn\":\"externaltest.fuzz\",\"mode\":\"check\"}")
	f.Add("{\"fqcn\":\"externaltest.fuzz\",\"mode\":\"\\u0000\"}")
	f.Add("{\"fqcn\":\"externaltest.fuzz\",\"capabilities\":[\"\",\"NoSuchCapable\"],\"ssh_port\":-1}")
	f.Add("{\"params\":{\"a\":{\"b\":{\"c\":1}}}}")
	f.Add("{\"secrets\":{\"k\":123}}")
	f.Add(strings.Repeat("{", 128))

	// Every decoded frame is served against this one method, whatever FQCN
	// it names, so the method body is reached as often as the mode allows.
	desc := collection.Descriptor{
		Name: "externaltest.fuzz",
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: fixtureReversibility,
			SupportsCheck: true,
		},
		Invoke: fuzzDeviceReader,
		Check:  fuzzDeviceReader,
	}
	anyName := func(string) (collection.Descriptor, bool) { return desc, true }

	f.Fuzz(func(t *testing.T, payload string) {
		req, err := external.ReadChildRequest(strings.NewReader(payload))
		if err != nil {
			return
		}
		// If it decoded, it must be servable without a panic anywhere
		// downstream, and must produce a response a parent can encode.
		resp := external.InvokeRequest(context.Background(), anyName, req)
		if _, err := json.Marshal(&resp); err != nil {
			t.Fatalf("the response to a decoded frame does not encode: %v", err)
		}
	})
}

// FuzzInvokeRequestMode fuzzes the one field that decides whether a device
// is changed. The property is exact: the three spellings ParseMode accepts
// each run their one function exactly once, and every other string runs
// nothing and comes back as an error. A mode string that ran Invoke
// without being one of the two execute spellings would be a dry run that
// changed a device.
func FuzzInvokeRequestMode(f *testing.F) {
	for _, seed := range []string{"", "execute", "check", "Check", "EXECUTE", "chekc", "simulate", "check ", "execute\x00", "\u0441heck"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, mode string) {
		var calls callCounter
		desc := countingDescriptor("externaltest.fuzzmode", true, &calls)

		resp := external.InvokeRequest(context.Background(), tableLookup(desc), wire.ChildRequest{FQCN: desc.Name, Mode: mode})

		switch mode {
		case "", string(collection.ModeExecute):
			if calls.invoke != 1 || calls.check != 0 || resp.Error != "" || !resp.Changed {
				t.Fatalf("mode %q: Invoke %d, Check %d, response %+v; want Invoke once and a changed response", mode, calls.invoke, calls.check, resp)
			}
		case string(collection.ModeCheck):
			if calls.invoke != 0 || calls.check != 1 || resp.Error != "" || resp.Changed {
				t.Fatalf("mode %q: Invoke %d, Check %d, response %+v; want Check once and an unchanged response", mode, calls.invoke, calls.check, resp)
			}
		default:
			if calls.invoke != 0 || calls.check != 0 {
				t.Fatalf("mode %q ran a method (Invoke %d, Check %d times), want a refusal that runs nothing", mode, calls.invoke, calls.check)
			}
			if resp.Error == "" || resp.Changed || len(resp.Facts) != 0 {
				t.Fatalf("mode %q: response %+v, want an error and nothing else", mode, resp)
			}
		}
	})
}
