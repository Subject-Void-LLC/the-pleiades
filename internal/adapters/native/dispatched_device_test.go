// Tests that the Runner rebuilds a dispatched device as its real type, so
// its accessors answer here as on the Controller, and falls back to the
// address-only device only where it must.
package native

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// apiPayload dispatches an onboarded generic_http device.
func apiPayload() wire.DispatchPayload {
	return wire.DispatchPayload{
		JobID: "j1", DeviceID: "d1", DeviceName: "api1", DeviceHost: "api.example.com",
		Capabilities: []capability.Name{capability.NameNetworkAddressable, capability.NameHTTPAPI},
		DeviceType:   generic.TypeHTTP,
		DeviceProperties: map[string]any{
			generic.BaseURLProperty:      "https://api.example.com/v2",
			generic.HTTPAuthProperty:     "bearer",
			inventory.DiscoveredProperty: inventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}}.Property(),
		},
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
