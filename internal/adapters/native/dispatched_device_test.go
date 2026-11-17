// Tests that the Runner rebuilds a dispatched device as its real type, so
// its accessors answer here as on the Controller, and falls back to the
// address-only device only where it must.
package native

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// apiPayload dispatches an onboarded generic_http device.
func apiPayload() wire.DispatchPayload {
	props := map[string]any{
		generic.BaseURLProperty:  "https://api.example.com/v2",
		generic.HTTPAuthProperty: "bearer",
	}
	// Bound to the dispatched properties, as the Controller's stored,
	// onboarded discovery is.
	found := inventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}}
	found.Binding = generic.Binding(generic.TypeHTTP, inventory.NewProperties(props))
	props[inventory.DiscoveredProperty] = found.Property()
	return wire.DispatchPayload{
		JobID: "j1", DeviceID: "d1", DeviceName: "api1", DeviceHost: "api.example.com",
		Capabilities:     []capability.Name{capability.NameNetworkAddressable, capability.NameHTTPAPI},
		DeviceType:       generic.TypeHTTP,
		DeviceProperties: props,
	}
}

// TestDispatchedDevice_RebuildsTheRealType: a generic_http dispatch is a
// generic_http device on the Runner, its base URL and credential mode
// readable and its discovered capability held; a linux_server's
// working_directory reaches its accessor.
func TestDispatchedDevice_RebuildsTheRealType(t *testing.T) {
	item, err := dispatchedDevice(apiPayload())
	if err != nil {
		t.Fatal(err)
	}
	api, ok := item.(capability.HTTPAPICapable)
	if !ok || api.HTTPBaseURL() != "https://api.example.com/v2" || api.HTTPAuth() != "bearer" || !item.HasCapability(capability.NameHTTPAPI) {
		t.Fatalf("rebuilt %T: ok=%v", item, ok)
	}

	linuxItem, err := dispatchedDevice(wire.DispatchPayload{
		DeviceName: "web1", DeviceHost: "10.0.0.1", DeviceType: "linux_server",
		Capabilities:     []capability.Name{capability.NameShellExec},
		DeviceProperties: map[string]any{"host": "10.0.0.1", "working_directory": "/srv/app"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if wd := linuxItem.(capability.CommandExecCapable).WorkingDirectory(); wd != "/srv/app" {
		t.Errorf("working directory %q", wd)
	}
}

// TestDispatchedDevice_CarriesTheFirstUsePinAndItsWarning: a device
// trusting a pin onboarding recorded on first use is dispatched with it
// (record.Dispatched, the Controller's own selection), and the Runner's
// rebuild, after the payload's JSON round trip, trusts the same pin, says on
// every run that nobody confirmed it, and keeps the discovery bound to it. A
// dispatch that lost the pin is refused trust rather than treated as
// verified: no pin, and no discovered capability.
func TestDispatchedDevice_CarriesTheFirstUsePinAndItsWarning(t *testing.T) {
	pin := "sha256:" + strings.Repeat("ab", 32)
	props := map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://10.0.0.9", generic.HTTPAuthProperty: "bearer", devicetls.FirstUseProperty: pin}
	found := inventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}}
	found.Binding = generic.Binding(generic.TypeHTTP, inventory.NewProperties(props))
	props[inventory.DiscoveredProperty] = found.Property()
	stored, err := generic.NewHTTP(record.Record{ID: "d1", Name: "api1", Type: generic.TypeHTTP, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	typ, dispatched := record.Dispatched(stored)
	if dispatched[devicetls.FirstUseProperty] != pin {
		t.Fatalf("the dispatch carries %v, want the first-use pin", dispatched[devicetls.FirstUseProperty])
	}
	raw, err := json.Marshal(wire.DispatchPayload{JobID: "j1", DeviceID: "d1", DeviceName: "api1", DeviceHost: "10.0.0.9", DeviceType: typ, DeviceProperties: dispatched})
	if err != nil {
		t.Fatal(err)
	}
	var payload wire.DispatchPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	item, err := dispatchedDevice(payload)
	if err != nil {
		t.Fatal(err)
	}
	s := devicetls.For(item)
	if s.FirstUsePin() != pin || !item.HasCapability(capability.NameHTTPAPI) {
		t.Errorf("rebuilt: first use %q, HTTPAPI %v", s.FirstUsePin(), item.HasCapability(capability.NameHTTPAPI))
	}
	if w := s.Warnings("api1"); len(w) != 1 || !strings.Contains(w[0], "no person has confirmed") {
		t.Errorf("the Runner's warnings %q do not say the pin is unconfirmed", w)
	}

	delete(payload.DeviceProperties, devicetls.FirstUseProperty)
	lost, err := dispatchedDevice(payload)
	if err != nil {
		t.Fatal(err)
	}
	if devicetls.For(lost).Pins() != nil || lost.HasCapability(capability.NameHTTPAPI) {
		t.Error("a dispatch without the first-use pin was trusted as if it held one, or kept its discovery")
	}
}

// TestDispatchedDevice_FallsBackOnlyWhereItMust: no type (an older
// Controller) and an unknown type (a newer one) get the address-only
// device; a known type whose record will not build is refused, never run
// as a different device.
func TestDispatchedDevice_FallsBackOnlyWhereItMust(t *testing.T) {
	for _, typ := range []string{"", "a_type_from_a_newer_controller"} {
		p := apiPayload()
		p.DeviceType = typ
		item, err := dispatchedDevice(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := item.(*external.Device); !ok {
			t.Errorf("type %q rebuilt as %T", typ, item)
		}
	}
	broken := apiPayload()
	delete(broken.DeviceProperties, generic.BaseURLProperty)
	if _, err := dispatchedDevice(broken); err == nil || !strings.Contains(err.Error(), "rebuilding device") {
		t.Fatalf("a record that cannot build: %v", err)
	}
}

// TestChild_RebuildsTheRealType: the per-task child rebuilds the same
// device from the request, so a method reading an accessor works there;
// the address-only device an external program gets has no base URL.
func TestChild_RebuildsTheRealType(t *testing.T) {
	var seen inventory.InventoryItem
	lookup := func(string) (collection.Descriptor, bool) {
		return collection.Descriptor{
			Name:     "childtest.reads",
			Manifest: collection.Manifest{Status: collection.StatusImplemented},
			Invoke: func(_ context.Context, _ sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
				seen = device
				return collection.Result{}, nil
			},
		}, true
	}
	req := childRequest(collection.Descriptor{Name: "childtest.reads"}, collection.ModeExecute, nil, apiPayload())
	if resp := external.InvokeRequestFor(context.Background(), lookup, req, nil, dispatchedDevice); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	if _, ok := seen.(capability.HTTPAPICapable); !ok {
		t.Errorf("the child ran against %T", seen)
	}
	if resp := external.InvokeRequest(context.Background(), lookup, req); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	if _, ok := seen.(capability.HTTPAPICapable); ok {
		t.Error("the address-only device claims a base URL")
	}
	if seen.Properties().Raw()[generic.BaseURLProperty] != "https://api.example.com/v2" {
		t.Errorf("the address-only device lost the dispatched properties: %v", seen.Properties().Raw())
	}
}
