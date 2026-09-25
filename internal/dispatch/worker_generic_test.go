// Tests that a generic device is dispatched, and that its dispatch carries
// its type and accessor properties and nothing else of its record.
package dispatch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestWorker_DispatchesAGenericDevice: a generic_http device has no host
// property (its address is its base URL), and is dispatched to its
// declared address rather than skipped; the payload names its type and
// carries its base URL, credential mode and discovery, and not the enable
// secret an operator kept beside them.
func TestWorker_DispatchesAGenericDevice(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	discovery := pkginventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}}.Property()
	device, err := generic.NewHTTP(record.Record{
		ID: "api-id", Name: "api1", Type: generic.TypeHTTP, State: pkginventory.StateActive,
		Properties: map[string]pkginventory.PropertyValue{
			generic.BaseURLProperty:         "https://api.example.com/v2",
			generic.HTTPAuthProperty:        "bearer",
			"enable_password":               "hunter2",
			pkginventory.DiscoveredProperty: discovery,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "api.yaml"), []byte("id: api\ntasks:\n  - name: read\n    fqcn: http.request\n    params:\n      url: /interfaces\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	worker := dispatch.NewWorker(store, repo, runbooks, bus, nil)
	if err := worker.HandleJobRequested(requestJob(t, ctx, store, "api", "routers")); err != nil {
		t.Fatal(err)
	}
	if bus.count() == 0 || bus.lastTopic() != topology.DispatchSubject("api-id") {
		t.Fatal("the generic device was not dispatched")
	}
	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DeviceHost != "api.example.com" || payload.DeviceType != generic.TypeHTTP {
		t.Errorf("dispatched to %q as %q", payload.DeviceHost, payload.DeviceType)
	}
	if payload.DeviceProperties[generic.BaseURLProperty] != "https://api.example.com/v2" || payload.DeviceProperties[generic.HTTPAuthProperty] != "bearer" {
		t.Errorf("properties %v", payload.DeviceProperties)
	}
	if _, ok := payload.DeviceProperties[pkginventory.DiscoveredProperty]; !ok {
		t.Error("the discovery did not travel")
	}
	if _, leaked := payload.DeviceProperties["enable_password"]; leaked {
		t.Error("a property no accessor reads rode the dispatch")
	}
}
