//go:build unix

// Package loader: tests that a program is sent its own capabilities'
// grants and no one else's.
package loader

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestBuildRequest_SendsAProgramItsOwnGrantsOnly: of a device's two grants,
// the program is sent the one of a capability it defines, with its source
// and properties but not who asserted it or why, and a method reads it with
// external.Granted.
func TestBuildRequest_SendsAProgramItsOwnGrantsOnly(t *testing.T) {
	t.Cleanup(capability.SnapshotForTest())
	for _, def := range []capability.DataDefinition{
		{Name: "acme.widget_api", Provider: "widgets", Properties: []capability.DataProperty{{Name: "endpoint", Type: "string"}}},
		{Name: "other.thing", Provider: "other"},
	} {
		if err := capability.RegisterData(def); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Unix(1700000000, 0).UTC()
	device := external.NewDevice(wire.DispatchPayload{
		DeviceID: "d1", DeviceName: "w1",
		Capabilities: []capability.Name{"acme.widget_api", "other.thing"},
		DeviceProperties: map[string]any{inventory.GrantedProperty: inventory.GrantsProperty([]inventory.Grant{
			{Capability: "acme.widget_api", Source: inventory.GrantAsserted, By: "ops", Reason: "lab", Properties: map[string]any{"endpoint": "https://w"}, At: at},
			{Capability: "other.thing", Source: inventory.GrantAsserted, By: "ops", At: at},
		})},
	})

	req := buildRequest("acme.widget.read", collection.ModeExecute, nil, device, nil, "widgets")
	granted, ok := req.DeviceProperties[inventory.GrantedProperty].(map[string]any)
	if !ok || len(granted) != 1 {
		t.Fatalf("the request carries grants %v, want exactly the program's own", req.DeviceProperties)
	}
	entry, _ := granted["acme.widget_api"].(map[string]any)
	if _, by := entry["by"]; by || entry["reason"] != nil {
		t.Errorf("the grant sent says who asserted it and why: %v", entry)
	}

	child := external.NewDevice(wire.DispatchPayload{DeviceProperties: req.DeviceProperties})
	props, ok := external.Granted(child, "acme.widget_api")
	if !ok || props["endpoint"] != "https://w" {
		t.Errorf("Granted = %v, %v; want the grant's properties", props, ok)
	}
	if _, ok := external.Granted(child, "other.thing"); ok {
		t.Error("the program read another program's grant")
	}

	if req := buildRequest("acme.widget.read", collection.ModeExecute, nil, device, nil, "nobody"); req.DeviceProperties != nil {
		t.Errorf("a program defining none of them was sent %v", req.DeviceProperties)
	}
}
