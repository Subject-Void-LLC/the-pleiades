// Tests for a program's capability definitions on the program's own side.
package external_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// widgetProgram is a program defining acme.widget_api, with one method
// requiring it.
func widgetProgram() external.Program {
	return external.Program{
		Capabilities: []capability.DataDefinition{{
			Name:       "acme.widget_api",
			Properties: []capability.DataProperty{{Name: "endpoint", Type: "string", Required: true}},
		}},
		Methods: []collection.Descriptor{{
			Name: "acme.widget.read",
			Manifest: collection.Manifest{
				Status:               collection.StatusImplemented,
				RequiredCapabilities: []capability.Name{"acme.widget_api"},
				Reversibility:        collection.Reversibility{ReadOnly: true, Notes: "Reads the widget controller and changes nothing."},
			},
			Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
				return collection.Result{}, nil
			},
		}},
	}
}

// TestServeProgram_DescribesTheCapabilitiesItDefines: describe lists the
// program's definitions beside its methods.
func TestServeProgram_DescribesTheCapabilitiesItDefines(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	var out, errs bytes.Buffer
	if code := external.ServeProgram(context.Background(), []string{external.CommandDescribe}, widgetProgram(), external.Streams{Out: &out, Err: &errs}); code != 0 {
		t.Fatalf("describe exited %d: %s", code, errs.String())
	}
	var desc external.Description
	if err := json.Unmarshal(out.Bytes(), &desc); err != nil {
		t.Fatal(err)
	}
	if len(desc.Capabilities) != 1 || desc.Capabilities[0].Name != "acme.widget_api" || len(desc.Methods) != 1 {
		t.Errorf("description = %+v, want the capability and the method", desc)
	}
}

// TestServeProgram_RefusesWhatTheLoaderWouldRefuse: a definition outside the
// program's namespaces fails on the author's machine, exit 2, naming why.
func TestServeProgram_RefusesWhatTheLoaderWouldRefuse(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	p := widgetProgram()
	p.Capabilities[0].Name = "other.widget_api"
	p.Methods[0].Manifest.RequiredCapabilities = nil
	var out, errs bytes.Buffer
	if code := external.ServeProgram(context.Background(), []string{external.CommandDescribe}, p, external.Streams{Out: &out, Err: &errs}); code != 2 {
		t.Fatalf("describe exited %d, want 2", code)
	}
	if !strings.Contains(errs.String(), "none of this program's methods uses") {
		t.Errorf("stderr = %q, want it to say why", errs.String())
	}
}

// TestCheckCapabilities_HoldsEachDefinitionToItsProgram: a probe that is
// one of the program's own read-only methods and does not require what it
// probes is accepted; every other shape is refused, naming why.
func TestCheckCapabilities_HoldsEachDefinitionToItsProgram(t *testing.T) {
	probed := func() external.Program {
		p := widgetProgram()
		p.Capabilities[0].Probe = "acme.widget.probe"
		probe := p.Methods[0]
		probe.Name = "acme.widget.probe"
		probe.Manifest.RequiredCapabilities = nil
		p.Methods = append(p.Methods, probe)
		return p
	}
	if p := probed(); external.CheckCapabilities(p.Capabilities, p.Methods) != nil {
		t.Fatalf("a well-formed probe was refused: %v", external.CheckCapabilities(p.Capabilities, p.Methods))
	}
	for name, tc := range map[string]struct {
		mutate func(*external.Program)
		want   string
	}{
		"a malformed name":    {func(p *external.Program) { p.Capabilities[0].Name = "Widget" }, "<namespace>.<name>"},
		"defined twice":       {func(p *external.Program) { p.Capabilities = append(p.Capabilities, p.Capabilities[0]) }, "defined twice"},
		"a probe not its own": {func(p *external.Program) { p.Capabilities[0].Probe = "acme.widget.gone" }, "not one of this program's methods"},
		"a probe that writes": {func(p *external.Program) { p.Methods[1].Manifest.Reversibility.ReadOnly = false }, "only reads"},
		"a probe requiring what it probes": {func(p *external.Program) {
			p.Methods[1].Manifest.RequiredCapabilities = []capability.Name{"acme.widget_api"}
		}, "which requires it"},
		"a requirement it does not define": {func(p *external.Program) {
			p.Methods[0].Manifest.RequiredCapabilities = []capability.Name{"acme.gadget_api"}
		}, "does not define"},
	} {
		p := probed()
		tc.mutate(&p)
		if err := external.CheckCapabilities(p.Capabilities, p.Methods); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error saying %q", name, err, tc.want)
		}
	}
}

// TestServeProgram_RefusesANameAlreadyTaken: a definition the program's own
// rules accept but this process already holds fails before any method is
// registered.
func TestServeProgram_RefusesANameAlreadyTaken(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	if err := capability.RegisterData(capability.DataDefinition{Name: "acme.widget_api"}); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := external.ServeProgram(context.Background(), []string{external.CommandDescribe}, widgetProgram(), external.Streams{Out: &out, Err: &errs}); code != 2 {
		t.Fatalf("describe exited %d, want 2", code)
	}
	if !strings.Contains(errs.String(), "already defined") {
		t.Errorf("stderr = %q, want it to say the name is taken", errs.String())
	}
}

// TestGranted_ReadsTheDevicesGrant: a method finds its capability's grant
// and its properties, and nothing for a capability not granted, a device
// with a malformed grant, or no device.
func TestGranted_ReadsTheDevicesGrant(t *testing.T) {
	grants := inventory.GrantsProperty([]inventory.Grant{
		{Capability: "acme.other", Source: inventory.GrantAsserted},
		{Capability: "acme.widget_api", Source: inventory.GrantProbed, Program: "acme", Properties: map[string]any{"endpoint": "https://w"}},
	})
	device := &inventorytest.Stub{Props: map[string]inventory.PropertyValue{inventory.GrantedProperty: grants}}
	if props, ok := external.Granted(device, "acme.widget_api"); !ok || props["endpoint"] != "https://w" {
		t.Errorf("Granted = %v, %v; want the grant's properties", props, ok)
	}
	if _, ok := external.Granted(device, "acme.gadget_api"); ok {
		t.Error("a capability not granted was found")
	}
	malformed := &inventorytest.Stub{Props: map[string]inventory.PropertyValue{inventory.GrantedProperty: "acme.widget_api"}}
	if _, ok := external.Granted(malformed, "acme.widget_api"); ok {
		t.Error("a malformed grant was found")
	}
	if _, ok := external.Granted(nil, "acme.widget_api"); ok {
		t.Error("no device held a grant")
	}
}
