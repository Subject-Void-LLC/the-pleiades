// Tests of the discovery rules table: what onboarding may grant a type,
// what a discovery binds to, and how a record's discovery is read back.
package record_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The fixture types: one whose discovery binds to its host, one unbound.
const (
	boundType   = "record_test_bound"
	unboundType = "record_test_unbound"
)

func init() {
	record.RegisterDiscoveryRules(boundType, record.DiscoveryRules{Grants: []capability.Name{capability.NameSSHTransport}, Bound: []string{"host"}})
	record.RegisterDiscoveryRules(unboundType, record.DiscoveryRules{Grants: []capability.Name{capability.NameSSHTransport}})
}

// discovered is props with d recorded, as onboarding writes it.
func discovered(props map[string]inventory.PropertyValue, d inventory.Discovery) map[string]inventory.PropertyValue {
	out := map[string]inventory.PropertyValue{}
	for k, v := range props {
		out[k] = v
	}
	out[inventory.DiscoveredProperty] = d.Property()
	return out
}

// TestDiscoverable: a registered type's grants, and nothing for a type
// onboarding grants nothing; the copy returned is the caller's own.
func TestDiscoverable(t *testing.T) {
	grants := record.Discoverable(boundType)
	if !slices.Equal(grants, []capability.Name{capability.NameSSHTransport}) {
		t.Fatalf("Discoverable = %v", grants)
	}
	grants[0] = "Changed"
	if record.Discoverable(boundType)[0] != capability.NameSSHTransport {
		t.Error("changing the returned grants changed the table")
	}
	if got := record.Discoverable("record_test_unregistered"); got != nil {
		t.Errorf("an unregistered type may be granted %v", got)
	}
}

// TestDiscoveryBinding: a bound type's digest follows its bound
// properties and nothing else; an unbound or unregistered type has none.
func TestDiscoveryBinding(t *testing.T) {
	props := inventory.NewProperties(map[string]inventory.PropertyValue{"host": "a.example", "note": "x"})
	digest := record.DiscoveryBinding(boundType, props)
	if digest == "" {
		t.Fatal("a bound type has no binding")
	}
	if got := record.DiscoveryBinding(boundType, inventory.NewProperties(map[string]inventory.PropertyValue{"host": "a.example", "note": "y"})); got != digest {
		t.Error("an unbound property changed the binding")
	}
	if got := record.DiscoveryBinding(boundType, inventory.NewProperties(map[string]inventory.PropertyValue{"host": "b.example"})); got == digest {
		t.Error("the bound property did not change the binding")
	}
	for _, typ := range []string{unboundType, "record_test_unregistered"} {
		if got := record.DiscoveryBinding(typ, props); got != "" {
			t.Errorf("%s has binding %q", typ, got)
		}
	}
}

// TestDeclared: each way a record's discovery is read, refused or held
// stale.
func TestDeclared(t *testing.T) {
	baseline := []capability.Name{capability.NameNetworkAddressable}
	props := map[string]inventory.PropertyValue{"host": "a.example"}
	binding := record.DiscoveryBinding(boundType, inventory.NewProperties(props))
	granted := inventory.Discovery{Capabilities: []capability.Name{capability.NameSSHTransport}, Binding: binding}

	caps, stale, err := record.Declared(boundType, record.Record{Name: "d", Properties: props}, baseline)
	if err != nil || stale != "" || !slices.Equal(caps, baseline) {
		t.Errorf("no discovery: %v, %q, %v; want the baseline", caps, stale, err)
	}

	caps, stale, err = record.Declared(boundType, record.Record{Name: "d", Properties: discovered(props, granted)}, baseline)
	if err != nil || stale != "" || !slices.Contains(caps, capability.NameSSHTransport) || !slices.Contains(caps, capability.NameNetworkAddressable) {
		t.Errorf("a matching discovery: %v, %q, %v; want the baseline and the grant", caps, stale, err)
	}

	moved := discovered(map[string]inventory.PropertyValue{"host": "b.example"}, granted)
	caps, stale, err = record.Declared(boundType, record.Record{Name: "d", Properties: moved}, baseline)
	if err != nil || !strings.Contains(stale, "other values of host") || !slices.Equal(caps, baseline) {
		t.Errorf("a repointed record: %v, %q, %v; want the baseline and a stale reason", caps, stale, err)
	}

	unbound := granted
	unbound.Binding = ""
	_, stale, err = record.Declared(boundType, record.Record{Name: "d", Properties: discovered(props, unbound)}, baseline)
	if err != nil || !strings.Contains(stale, "predates binding") {
		t.Errorf("a discovery with no binding: %q, %v; want the predates reason", stale, err)
	}

	foreign := inventory.Discovery{Capabilities: []capability.Name{capability.NameWinRM}, Binding: binding}
	if _, _, err := record.Declared(boundType, record.Record{Name: "d", Properties: discovered(props, foreign)}, baseline); err == nil || !strings.Contains(err.Error(), "may not grant") {
		t.Errorf("a discovery granting what the type may not: %v", err)
	}

	malformed := map[string]inventory.PropertyValue{"host": "a.example", inventory.DiscoveredProperty: "not a discovery"}
	if _, _, err := record.Declared(boundType, record.Record{Name: "d", Properties: malformed}, baseline); err == nil {
		t.Error("a malformed discovery was read")
	}

	// An unbound type holds its discovery however its record changes.
	caps, stale, err = record.Declared(unboundType, record.Record{Name: "d", Properties: moved}, nil)
	if err != nil || stale != "" || !slices.Contains(caps, capability.NameSSHTransport) {
		t.Errorf("an unbound type: %v, %q, %v", caps, stale, err)
	}
}

// TestDiscoveredFactAndList: the facts a type's accessors read, with
// anything not text dropped rather than guessed at.
func TestDiscoveredFactAndList(t *testing.T) {
	props := inventory.NewProperties(discovered(nil, inventory.Discovery{Facts: map[string]any{
		"kernel": "6.1", "count": 3, "mixed": []any{"a", 2, "b"}, "typed": []string{"x", "y"},
	}}))
	if got := record.DiscoveredFact(props, "kernel"); got != "6.1" {
		t.Errorf("DiscoveredFact(kernel) = %q", got)
	}
	for _, key := range []string{"count", "missing"} {
		if got := record.DiscoveredFact(props, key); got != "" {
			t.Errorf("DiscoveredFact(%s) = %q, want empty", key, got)
		}
	}
	if got := record.DiscoveredList(props, "mixed"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("DiscoveredList(mixed) = %v", got)
	}
	if got := record.DiscoveredList(props, "typed"); !slices.Equal(got, []string{"x", "y"}) {
		t.Errorf("DiscoveredList(typed) = %v", got)
	}
	if got := record.DiscoveredList(props, "kernel"); got != nil {
		t.Errorf("DiscoveredList of a text fact = %v", got)
	}

	for name, p := range map[string]inventory.Properties{
		"no discovery": inventory.NewProperties(nil),
		"malformed":    inventory.NewProperties(map[string]inventory.PropertyValue{inventory.DiscoveredProperty: 7}),
	} {
		if record.DiscoveredFact(p, "kernel") != "" || record.DiscoveredList(p, "mixed") != nil {
			t.Errorf("%s: a fact was read", name)
		}
	}
}
