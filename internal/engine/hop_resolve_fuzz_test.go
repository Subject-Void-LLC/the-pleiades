package engine

import (
	"encoding/json"
	"testing"
)

// FuzzRouteExtract proves routeExtract never panics against arbitrary
// JSON, the real shape a Group's, Inventory's, or Device's Properties
// bag actually arrives in (ent's JSON column decodes into exactly
// map[string]interface{}, the same shape json.Unmarshal produces here).
// A route is new attacker-influenceable structure (Phase 72's own
// Schema/Injection Hardening item), so this is what proves a
// deeply-nested, self-referential-looking, or otherwise adversarial
// payload fails closed (returns ok=false) rather than crashing the
// dispatch that reads it.
func FuzzRouteExtract(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"route": []}`,
		`{"route": ["bastion-1", "bastion-2"]}`,
		`{"route": "not-a-list"}`,
		`{"route": [1, 2, 3]}`,
		`{"route": ["ok", 42, "also-ok"]}`,
		`{"route": null}`,
		`{"route": [null]}`,
		`{"route": [{"nested": "object"}]}`,
		`{"other-key": "value"}`,
		`{"route": [[[["deeply-nested"]]]]}`,
		`null`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, jsonInput string) {
		var props map[string]interface{}
		if err := json.Unmarshal([]byte(jsonInput), &props); err != nil {
			// Not a JSON object: not the shape this function's caller
			// ever hands it (a Properties bag decodes to a map, never a
			// bare scalar or array), so there is nothing for routeExtract
			// to prove against.
			return
		}

		// The only property under test: it must never panic, for any
		// map shape json.Unmarshal can produce.
		names, ok := routeExtract(props)
		if ok && names == nil {
			t.Errorf("routeExtract(%v) = nil, true: ok=true must always come with a non-nil (possibly empty) slice", props)
		}
	})
}
