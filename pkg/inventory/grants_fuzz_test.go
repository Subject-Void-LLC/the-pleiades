// Fuzz test of the granted property's decode: a record on disk, never
// trusted to be what RecordGrant wrote.
package inventory_test

import (
	"encoding/json"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// FuzzGrantsFrom: any value under granted decodes or is refused without a
// panic, and what decodes survives its own encoding unchanged in count and
// in every field that decides whether a grant counts or where it came
// from: its capability, source, program, binding, definition digest and
// build.
func FuzzGrantsFrom(f *testing.F) {
	// GrantsProperty's own shape: a mapping keyed by capability.
	f.Add([]byte(`{"acme.appliance":{"source":"assertion","by":"ops","reason":"r","at":"2026-10-22T00:00:00Z"}}`))
	f.Add([]byte(`{"acme.appliance":{"source":"probe","program":"acme","binding":"x","properties":{"model":"x9"}},"acme.other":{"source":"assertion"}}`))
	f.Add([]byte(`{"acme.appliance":{"source":"probe","definition":"sha256:0000000000000000000000000000000000000000000000000000000000000000","build":"sha256:1111111111111111111111111111111111111111111111111111111111111111"}}`))
	f.Add([]byte(`{"acme.appliance":{"source":"assertion","definition":7,"build":["x"]}}`))
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
		for i := range grants {
			a, b := grants[i], again[i]
			if a.Capability != b.Capability || a.Source != b.Source || a.Program != b.Program ||
				a.Binding != b.Binding || a.Definition != b.Definition || a.Build != b.Build {
				t.Fatalf("grant %d changed in its own encoding:\n%+v\n%+v", i, a, b)
			}
		}
	})
}
