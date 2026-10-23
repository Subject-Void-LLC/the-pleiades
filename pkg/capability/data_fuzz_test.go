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
