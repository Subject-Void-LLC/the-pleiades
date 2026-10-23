// Fuzz test of the granted property's decode: a record on disk, never
// trusted to be what RecordGrant wrote.
package inventory_test

import (
	"encoding/json"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// FuzzGrantsFrom: any value under granted decodes or is refused without a
// panic, and what decodes survives its own encoding unchanged in count.
func FuzzGrantsFrom(f *testing.F) {
	// GrantsProperty's own shape: a mapping keyed by capability.
	f.Add([]byte(`{"acme.appliance":{"source":"assertion","by":"ops","reason":"r","at":"2026-10-22T00:00:00Z"}}`))
	f.Add([]byte(`{"acme.appliance":{"source":"probe","program":"acme","binding":"x","properties":{"model":"x9"}},"acme.other":{"source":"assertion"}}`))
	f.Add([]byte(`{"acme.appliance":{"source":"rumour"}}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var v any
		if json.Unmarshal(data, &v) != nil {
			return
		}
		grants, ok, err := inventory.GrantsFrom(inventory.NewProperties(map[string]inventory.PropertyValue{inventory.GrantedProperty: v}))
		if err != nil || !ok {
			return
		}
		again, _, err := inventory.GrantsFrom(inventory.NewProperties(map[string]inventory.PropertyValue{inventory.GrantedProperty: inventory.GrantsProperty(grants)}))
		if err != nil || len(again) != len(grants) {
			t.Fatalf("%d grants decoded, re-encoded to %d (%v)", len(grants), len(again), err)
		}
	})
}
