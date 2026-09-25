// Tests that a dispatch carries exactly a type's declared accessor
// properties and its discovery, and nothing else of its record.
package record_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// dispatchItem is the least device type there is.
type dispatchItem struct{ *record.Base }

func (d dispatchItem) HasCapability(capability.Name) bool { return false }

// TestDispatched_OnlyTheDeclaredKeys: a property an operator stored that
// no accessor reads (here an enable secret) never leaves; a declared key
// that is absent is simply absent; the discovery always travels.
func TestDispatched_OnlyTheDeclaredKeys(t *testing.T) {
	t.Cleanup(record.SnapshotForTest())
	record.RegisterDispatchProperties("dispatchtest_router", "host", "cli_prompt", "netconf_port")
	discovery := inventory.Discovery{Protocol: "ssh"}.Property()
	item := dispatchItem{record.NewBase(record.Record{Name: "r1", Type: "dispatchtest_router", Properties: map[string]inventory.PropertyValue{
		"host":                       "10.0.0.1",
		"cli_prompt":                 "r1#",
		"enable_password":            "hunter2",
		"notes":                      "rack 4",
		inventory.DiscoveredProperty: discovery,
	}}, nil)}
	typ, props := record.Dispatched(item)
	want := map[string]any{"host": "10.0.0.1", "cli_prompt": "r1#", inventory.DiscoveredProperty: discovery}
	if typ != "dispatchtest_router" || !reflect.DeepEqual(props, want) {
		t.Errorf("dispatched %s %v, want %v", typ, props, want)
	}

	undeclared := dispatchItem{record.NewBase(record.Record{Name: "x", Type: "dispatchtest_undeclared", Properties: map[string]inventory.PropertyValue{"host": "h"}}, nil)}
	if typ, props := record.Dispatched(undeclared); typ != "dispatchtest_undeclared" || props != nil {
		t.Errorf("an undeclared type dispatched %v", props)
	}
	if typ, props := record.Dispatched(&inventorytest.Stub{StubName: "untyped"}); typ != "" || props != nil {
		t.Errorf("an item reporting no type dispatched %q %v", typ, props)
	}
	if keys, ok := record.DispatchProperties("dispatchtest_router"); !ok || len(keys) != 3 {
		t.Errorf("declared keys %v", keys)
	}
}
