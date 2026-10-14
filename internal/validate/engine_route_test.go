// Tests of the engine route planner: the plan line naming how each
// container task reaches its device's engine.
package validate_test

import (
	"context"
	"strings"
	"testing"

	internalinventory "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/engines"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerEngineMethods registers fixture methods that reach an engine in
// each way a manifest can say, for the rest of the test.
func registerEngineMethods(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	method := func(name string, transports []string, required ...capability.Name) collection.Descriptor {
		return collection.Descriptor{Name: name, Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			SupportedTransports: transports, RequiredCapabilities: required,
			Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture that changes nothing"}}}
	}
	for _, d := range []collection.Descriptor{
		method("t119.docker", capability.EngineTransports(), capability.NameDocker),
		method("t119.podman", capability.EngineTransports(), capability.NamePodman),
		method("t119.anyengine", capability.EngineTransports(), capability.NameContainerEngine),
		method("t119.mixed", []string{capability.TransportSSH, capability.TransportEngineSocket}),
		method("t119.nowhere", nil),
	} {
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
}

// dockerHost is a container_host whose discovery found Docker reached
// through its socket over SSH.
func dockerHost(t *testing.T) inventory.InventoryItem {
	t.Helper()
	props := map[string]inventory.PropertyValue{"host": "10.0.0.5"}
	d := inventory.Discovery{Protocol: "docker",
		Capabilities: []capability.Name{capability.NameContainerEngine, capability.NameDocker, capability.NameEngineSocket},
		Facts: map[string]any{
			engines.FactKey(capability.EngineDocker, engines.FieldSocket):   "/var/run/docker.sock",
			engines.FactKey(capability.EngineDocker, engines.FieldID):       "engine-1",
			engines.FactKey(capability.EngineDocker, engines.FieldCarriers): []any{"socket"},
		},
		Binding: record.DiscoveryBinding(container.TypeHost, inventory.NewProperties(props))}
	props[inventory.DiscoveredProperty] = d.Property()
	item, err := internalinventory.NewItemFactory().Build(record.Record{ID: "h1", Name: "h1", Type: container.TypeHost, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// TestEngineRoutes: a task of an engine-only method is planned onto the
// engine and carrier its session will use; a method that needs an engine
// the device does not run, a method that also reaches some other way, and
// one that declares no transports get no line.
func TestEngineRoutes(t *testing.T) {
	registerEngineMethods(t)
	dag := yamlDAG(t, `id: r
hosts: h1
tasks:
  - name: docker
    t119.docker: {}
  - name: any engine
    t119.anyengine: {}
  - name: podman
    t119.podman: {}
  - name: mixed
    t119.mixed: {}
  - name: nowhere
    t119.nowhere: {}
`)
	report := validate.Validate(validate.WorldView{Items: []inventory.InventoryItem{dockerHost(t)}, DAG: dag})
	if len(report.Routes) != 2 {
		t.Fatalf("routes = %+v, want the docker and any-engine tasks only", report.Routes)
	}
	for i, rt := range report.Routes {
		if rt.Transport != capability.TransportEngineSocket || rt.Detail != "the docker engine" || rt.Device != "h1" {
			t.Errorf("route %d = %+v, want the docker engine over its socket", i, rt)
		}
		if i > 0 && report.Routes[i-1].Node > rt.Node {
			t.Error("routes are not sorted by node")
		}
	}
	text := report.String()
	if strings.Count(text, "[route]") != 2 || !strings.Contains(text, "engine_socket, the docker engine") {
		t.Errorf("the report does not show both routes:\n%s", text)
	}
}
