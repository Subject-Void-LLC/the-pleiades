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
	// Times: a fraction, an offset, the edges of the years a grant may hold,
	// and times that cross them once read in UTC.
	f.Add([]byte(`{"acme.appliance":{"source":"probe","at":"2024-05-06T07:08:09.123456789+02:00"}}`))
	f.Add([]byte(`{"acme.appliance":{"source":"probe","at":"9999-12-31T23:00:00-02:00"},"acme.b":{"source":"probe","at":"0001-01-01T00:30:00+01:00"}}`))
	f.Add([]byte(`{"acme.appliance":{"source":"probe","at":"0000-01-01T00:00:00Z"},"acme.b":{"source":"probe","at":"0001-01-01T00:00:00Z"}}`))
	f.Add([]byte(`{"acme.appliance":{"source":"probe","at":"yesterday"},"acme.b":{"source":"probe","at":17}}`))
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
			// Every time it accepts is the same instant once written and read
			// again; a time it could not read stays unreadable, and is never
			// the zero time passed off as one.
			switch {
			case (a.Unreadable == "") != (b.Unreadable == ""):
				t.Fatalf("grant %d's time was readable %v, then %v: %q, %q", i, a.Unreadable == "", b.Unreadable == "", a.Unreadable, b.Unreadable)
			case a.Unreadable == "" && (a.At.IsZero() || !a.At.Equal(b.At)):
				t.Fatalf("grant %d's time %v read back as %v", i, a.At, b.At)
			case a.Unreadable != "" && (!a.At.IsZero() || !b.At.IsZero()):
				t.Fatalf("an unreadable grant carries a time: %v, %v", a.At, b.At)
			}
		}
	})
}
