// This file holds Clone to sharing nothing with what it copied.
package credtype_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// TestCloneSharesNothing edits every slice and map of a copy and expects the
// original unchanged, and a nil document to stay nil.
func TestCloneSharesNothing(t *testing.T) {
	original := nulBaseType()
	original.Injectors.OmitEmpty = []string{"API_TOKEN"}
	original.Injectors.ExtraVars["list"] = []any{map[string]any{"k": "v"}}
	before := nulBaseType()
	before.Injectors.OmitEmpty = []string{"API_TOKEN"}
	before.Injectors.ExtraVars["list"] = []any{map[string]any{"k": "v"}}

	clone := original.Clone()
	clone.Inputs.Fields[0].Secret = false
	clone.Inputs.Fields[1].Choices[0] = "north"
	clone.Inputs.Required[0] = "region"
	clone.Injectors.Env["API_TOKEN"] = "changed"
	clone.Injectors.File["template"] = "changed"
	clone.Injectors.OmitEmpty[0] = "changed"
	clone.Injectors.ExtraVars["api"].(map[string]any)["region"] = "changed"
	clone.Injectors.ExtraVars["list"].([]any)[0].(map[string]any)["k"] = "changed"

	if !reflect.DeepEqual(original, before) {
		t.Errorf("editing a clone changed the original:\n got  %+v\n want %+v", original, before)
	}
	if empty := (credtype.CredentialType{}).Clone(); !reflect.DeepEqual(empty, credtype.CredentialType{}) {
		t.Errorf("a clone of an empty type is %+v, want it empty", empty)
	}
}
