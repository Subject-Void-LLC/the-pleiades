// Tests that a call is held to the shape its parameters select, that
// admission takes a device any shape fits, and that Register refuses a
// shape it could not hold a call to.
package collection_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// shapedManifest is net.trace's shape: Linux over SSH, or a container
// host's engine given container.
func shapedManifest() collection.Manifest {
	return collection.Manifest{
		SupportedTransports:  []string{capability.TransportSSH},
		RequiredCapabilities: []capability.Name{capability.NameLinux, capability.NameCommandExec},
		Shapes: []collection.CallShape{{
			Param:                "container",
			SupportedTransports:  capability.EngineTransports(),
			RequiredCapabilities: []capability.Name{capability.NameContainerEngine},
		}},
		Doc: collection.Doc{Params: []collection.Param{{Name: "container", Type: "string", Description: "x"}}},
	}
}

// stubWith is an active device holding caps.
func stubWith(caps ...capability.Name) inventory.InventoryItem {
	return &inventorytest.Stub{StubName: "dev", StubState: inventory.StateActive, Caps: caps}
}

// TestManifestFor_HoldsACallToTheShapeItSelects: no container is the
// manifest's own shape, a container is the engine's, a null container is
// no container, and the shaped manifest carries no shapes of its own.
func TestManifestFor_HoldsACallToTheShapeItSelects(t *testing.T) {
	m := shapedManifest()
	linux := stubWith(capability.NameLinux, capability.NameCommandExec, capability.NameSSHTransport)
	host := stubWith(capability.NameContainerEngine, capability.NameDocker, capability.NameEngineLocal)

	plain := m.For(map[string]any{"dest": "10.0.0.1"})
	if len(plain.Shapes) != 1 || plain.SupportedTransports[0] != capability.TransportSSH {
		t.Errorf("a plain call's manifest changed: %+v", plain)
	}
	if err := collection.CheckShape(linux, "net.trace", plain); err != nil {
		t.Errorf("a Linux SSH device, plain call: %v", err)
	}
	if err := collection.CheckShape(host, "net.trace", plain); err == nil || !strings.Contains(err.Error(), "LinuxCapable") {
		t.Errorf("a container host, plain call: %v", err)
	}
	if null := m.For(map[string]any{"container": nil}); len(null.Shapes) != 1 {
		t.Errorf("a null container selected the shape")
	}

	shaped := m.For(map[string]any{"container": "h1"})
	if len(shaped.Shapes) != 0 || shaped.RequiredCapabilities[0] != capability.NameContainerEngine {
		t.Errorf("a container call's manifest: %+v", shaped)
	}
	if err := collection.CheckShape(host, "net.trace", shaped); err != nil {
		t.Errorf("a container host, container call: %v", err)
	}
	if err := collection.CheckShape(linux, "net.trace", shaped); err == nil || !strings.Contains(err.Error(), "ContainerEngineCapable") {
		t.Errorf("a Linux device, container call: %v", err)
	}
	// A device with the capability and no way to its engine fails on the
	// transport.
	unreachable := stubWith(capability.NameContainerEngine)
	if err := collection.CheckShape(unreachable, "net.trace", shaped); err == nil || !strings.Contains(err.Error(), "reaches its device over") {
		t.Errorf("an engine with no transport: %v", err)
	}
}

// TestCheckAnyShape_AdmitsADeviceAnyShapeFits: admission, with no call's
// parameters, takes a device either shape fits, and refuses one neither
// does with the manifest's own shape's reason.
func TestCheckAnyShape_AdmitsADeviceAnyShapeFits(t *testing.T) {
	m := shapedManifest()
	for _, dev := range []inventory.InventoryItem{
		stubWith(capability.NameLinux, capability.NameCommandExec, capability.NameSSHTransport),
		stubWith(capability.NameContainerEngine, capability.NameEngineLocal),
	} {
		if err := collection.CheckAnyShape(dev, "net.trace", m); err != nil {
			t.Errorf("%v: %v", dev.Capabilities(), err)
		}
	}
	if err := collection.CheckAnyShape(stubWith(capability.NameWindows), "net.trace", m); err == nil || !strings.Contains(err.Error(), "LinuxCapable") {
		t.Errorf("a device neither shape fits: %v", err)
	}
}

// TestRegister_RefusesAShapeItCannotHoldACallTo: a shape selected by an
// undocumented, reserved or repeated parameter, one declaring nothing, and
// one naming an unknown capability or transport are each refused.
func TestRegister_RefusesAShapeItCannotHoldACallTo(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	doc := collection.Doc{Params: []collection.Param{{Name: "container", Type: "string", Description: "x"}, {Name: "target", Type: "string", Description: "x"}}}
	engine := []capability.Name{capability.NameContainerEngine}
	for want, shapes := range map[string][]collection.CallShape{
		"does not document":              {{Param: "from", RequiredCapabilities: engine}},
		"the engine reads for itself":    {{Param: "target", RequiredCapabilities: engine}},
		"two call shapes":                {{Param: "container", RequiredCapabilities: engine}, {Param: "container", RequiredCapabilities: engine}},
		"declares no transport and no":   {{Param: "container"}},
		"requires unknown capability":    {{Param: "container", RequiredCapabilities: []capability.Name{"NoSuchCapable"}}},
		"which is not one this platform": {{Param: "container", SupportedTransports: []string{"carrier-pigeon"}}},
	} {
		d := collection.Descriptor{Name: "shapetest.method", Manifest: collection.Manifest{Status: collection.StatusDeclared, Shapes: shapes, Doc: doc}}
		if err := collection.Register(d); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("shapes %+v: %v; want a refusal saying %q", shapes, err, want)
		}
	}
	good := collection.Doc{Params: doc.Params[:1]}
	d := collection.Descriptor{Name: "shapetest.method", Manifest: collection.Manifest{Status: collection.StatusDeclared, Shapes: []collection.CallShape{{Param: "container", RequiredCapabilities: engine}}, Doc: good}}
	if err := collection.Register(d); err != nil {
		t.Errorf("a good shape: %v", err)
	}
}
