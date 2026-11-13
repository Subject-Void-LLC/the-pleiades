// Fuzz test of a program's capability definitions and its grants'
// properties: a program's words, never trusted.
package capability_test

import (
	"encoding/json"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// FuzzValidateData: no definition or grant panics the checks, and a valid
// definition's name always has a data capability's shape.
func FuzzValidateData(f *testing.F) {
	f.Add([]byte(`{"name":"acme.appliance","requires":["SSHTransportCapable"],"properties":[{"name":"model","type":"string","required":true}]}`), []byte(`{"model":"x9"}`))
	f.Add([]byte(`{"name":"Acme","properties":[{"name":"n","type":"int"}]}`), []byte(`{"n":1.5}`))
	f.Fuzz(func(t *testing.T, defJSON, propsJSON []byte) {
		var def capability.DataDefinition
		if json.Unmarshal(defJSON, &def) != nil {
			return
		}
		var props map[string]any
		_ = json.Unmarshal(propsJSON, &props)
		_ = capability.ValidateGrantShape(props)
		if capability.ValidateData(def) != nil {
			return
		}
		if !capability.IsDataName(def.Name) {
			t.Fatalf("a valid definition named %q", def.Name)
		}
		_ = capability.ValidateGrant(def, props)
	})
}

// FuzzDefinitionDigest: for any definition a program could describe, the
// digest has its shape; listing the requirements and properties in another
// order, rewriting the prose and the probe, and reading the definition back
// from its own JSON never move it; and bumping the revision always does.
func FuzzDefinitionDigest(f *testing.F) {
	f.Add([]byte(`{"name":"acme.appliance","provider":"acme","requires":["SSHTransportCapable","POSIXFileSystemCapable"],"properties":[{"name":"model","type":"string","required":true},{"name":"port","type":"int"}]}`), uint64(1), "new doc", "new description")
	f.Add([]byte(`{"name":"acme.x","revision":7,"properties":[{"name":"a","type":"bool"},{"name":"a","type":"int"},{"name":"a","type":"bool","required":true}]}`), uint64(42), "", "\x00")
	f.Add([]byte(`{"name":"","requires":["A","A","B"]}`), uint64(0), "\xff", "<&>")
	f.Fuzz(func(t *testing.T, defJSON []byte, seed uint64, doc, description string) {
		var def capability.DataDefinition
		if json.Unmarshal(defJSON, &def) != nil {
			return
		}
		want := capability.DefinitionDigest(def)
		if !capability.IsDefinitionDigest(want) {
			t.Fatalf("DefinitionDigest returned %q", want)
		}

		// Another order, chosen by seed, and other prose.
		moved := def
		moved.Requires = permuted(def.Requires, seed)
		moved.Properties = permuted(def.Properties, seed>>7)
		for i := range moved.Properties {
			moved.Properties[i].Description = description
		}
		moved.Doc, moved.Probe = doc, description
		if got := capability.DefinitionDigest(moved); got != want {
			t.Fatalf("reordering and rewording %s moved its digest from %s to %s", defJSON, want, got)
		}

		// Read back from its own JSON, as another build's describe gives it.
		encoded, err := json.Marshal(def)
		if err != nil {
			t.Fatalf("a decoded definition did not encode: %v", err)
		}
		var described capability.DataDefinition
		if err := json.Unmarshal(encoded, &described); err != nil {
			t.Fatalf("a definition's own JSON did not decode: %v", err)
		}
		if got := capability.DefinitionDigest(described); got != want {
			t.Fatalf("the definition read back from %s digests %s, want %s", encoded, got, want)
		}

		bumped := def
		bumped.Revision++
		if capability.DefinitionDigest(bumped) == want {
			t.Fatalf("bumping the revision of %s left its digest at %s", defJSON, want)
		}
	})
}

// permuted returns a copy of items in an order chosen by seed, a
// Fisher-Yates shuffle driven by a small linear congruential generator so
// the fuzzer, not the clock, picks it.
func permuted[T any](items []T, seed uint64) []T {
	out := append([]T(nil), items...)
	for i := len(out) - 1; i > 0; i-- {
		seed = seed*6364136223846793005 + 1442695040888963407
		j := int((seed >> 33) % uint64(i+1))
		out[i], out[j] = out[j], out[i]
	}
	return out
}
