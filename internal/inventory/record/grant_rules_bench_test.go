// Benchmark of building a device that holds the most grants a program can
// define, each held to its definition.
package record_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// mostGrantsRecord registers capability.MaxDataDefinitions definitions of
// capability.MaxDataProperties properties each and returns a record
// granted every one, every grant holding.
func mostGrantsRecord(tb testing.TB) record.Record {
	tb.Helper()
	grants := make([]inventory.Grant, 0, capability.MaxDataDefinitions)
	for i := range capability.MaxDataDefinitions {
		def := capability.DataDefinition{Name: capability.Name(fmt.Sprintf("acme.widget_%d", i)), Provider: "widgets", Requires: []capability.Name{capability.NameSSHTransport}}
		props := map[string]any{}
		for j := range capability.MaxDataProperties {
			name := fmt.Sprintf("p%d", j)
			def.Properties = append(def.Properties, capability.DataProperty{Name: name, Type: "string", Required: true})
			props[name] = "a value a probe found"
		}
		if err := capability.RegisterData(def); err != nil {
			tb.Fatal(err)
		}
		grants = append(grants, inventory.Grant{Capability: def.Name, Source: inventory.GrantProbed, Program: "widgets", Properties: props, At: time.Unix(0, 0), Definition: capability.DefinitionDigest(def)})
	}
	return record.Record{Name: "w1", Type: grantTypeName, Properties: map[string]inventory.PropertyValue{inventory.GrantedProperty: inventory.GrantsProperty(grants)}}
}

// TestNewBase_TheMostGrantsAllHold is the stress case's check: every one
// of the most grants a program can define counts, with no note.
func TestNewBase_TheMostGrantsAllHold(t *testing.T) {
	t.Cleanup(capability.SnapshotForTest())
	b := record.NewBase(mostGrantsRecord(t), []capability.Name{capability.NameSSHTransport})
	if got := len(b.Capabilities()); got != capability.MaxDataDefinitions+1 {
		t.Errorf("the device holds %d capabilities, want %d grants and its baseline", got, capability.MaxDataDefinitions)
	}
	if notes := b.GrantNotes(); len(notes) != 0 {
		t.Errorf("notes %+v, want none", notes)
	}
}

// BenchmarkNewBase_TheMostGrants is the cost every device build pays at
// the bound: decoding, digesting and holding every grant to its
// definition.
func BenchmarkNewBase_TheMostGrants(b *testing.B) {
	b.Cleanup(capability.SnapshotForTest())
	rec := mostGrantsRecord(b)
	baseline := []capability.Name{capability.NameSSHTransport}
	b.ReportAllocs()
	for b.Loop() {
		record.NewBase(rec, baseline)
	}
}
