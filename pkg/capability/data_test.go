// Tests for capabilities a program defines as data.
package capability_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// tenancy is a well-formed definition the tests below vary.
func tenancy() capability.DataDefinition {
	return capability.DataDefinition{
		Name:     "oci.tenancy_api",
		Provider: "pleiades-oci",
		Doc:      "Reaches an OCI tenancy's API.",
		Requires: []capability.Name{capability.NameSSHTransport},
		Properties: []capability.DataProperty{
			{Name: "tenancy_ocid", Type: "string", Required: true},
			{Name: "region", Type: "string", Required: true},
			{Name: "free_tier", Type: "bool"},
			{Name: "max_ocpus", Type: "int"},
		},
	}
}

// TestRegisterData_ADefinitionBecomesACapability: a registered definition
// is found by name, holds its definition, and is held by any item (its
// promise is the grant record, not a Go type).
func TestRegisterData_ADefinitionBecomesACapability(t *testing.T) {
	t.Cleanup(capability.SnapshotForTest())
	if err := capability.RegisterData(tenancy()); err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	def, ok := capability.DataOf("oci.tenancy_api")
	if !ok || def.Provider != "pleiades-oci" || len(def.Properties) != 4 {
		t.Fatalf("DataOf = %+v, %v", def, ok)
	}
	if !capability.Implements(struct{}{}, "oci.tenancy_api") {
		t.Error("a data capability is not held by an item whose record grants it")
	}
	if _, ok := capability.DataOf(capability.NameSSHTransport); ok {
		t.Error("a compiled capability read as a data one")
	}
	if err := capability.RegisterData(tenancy()); err == nil || !strings.Contains(err.Error(), "already defined") {
		t.Errorf("a second definition of the name = %v, want refused", err)
	}
}

// TestRegisterData_RefusesWhatCouldWeakenTheVocabulary: a compiled name, a
// name of the wrong shape, a requirement that is not a compiled capability
// (another data one included), and properties out of bounds are refused.
func TestRegisterData_RefusesWhatCouldWeakenTheVocabulary(t *testing.T) {
	t.Cleanup(capability.SnapshotForTest())
	if err := capability.RegisterData(tenancy()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*capability.DataDefinition){
		"a compiled name":        func(d *capability.DataDefinition) { d.Name = capability.NameSSHTransport },
		"no namespace":           func(d *capability.DataDefinition) { d.Name = "tenancy" },
		"upper case":             func(d *capability.DataDefinition) { d.Name = "OCI.Tenancy" },
		"two dots":               func(d *capability.DataDefinition) { d.Name = "oci.core.vcn" },
		"an unknown requirement": func(d *capability.DataDefinition) { d.Requires = []capability.Name{"TeleportCapable"} },
		"a data requirement": func(d *capability.DataDefinition) {
			d.Name = "oci.other"
			d.Requires = []capability.Name{"oci.tenancy_api"}
		},
		"a property type":            func(d *capability.DataDefinition) { d.Properties[0].Type = "list" },
		"a property name":            func(d *capability.DataDefinition) { d.Properties[0].Name = "Tenancy-OCID" },
		"a property twice":           func(d *capability.DataDefinition) { d.Properties[1].Name = "tenancy_ocid" },
		"a control character in doc": func(d *capability.DataDefinition) { d.Doc = "reaches\x1b[2Jthe API" },
		"too many requirements": func(d *capability.DataDefinition) {
			d.Requires = make([]capability.Name, capability.MaxDataRequires+1)
		},
		"too many properties": func(d *capability.DataDefinition) {
			for i := 0; i <= capability.MaxDataProperties; i++ {
				d.Properties = append(d.Properties, capability.DataProperty{Name: fmt.Sprintf("p%d", i), Type: "string"})
			}
		},
		"a control character in a property's description": func(d *capability.DataDefinition) { d.Properties[0].Description = "the\x07OCID" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			def := tenancy()
			def.Name = "oci.case_under_test"
			mutate(&def)
			if err := capability.RegisterData(def); err == nil {
				t.Errorf("RegisterData accepted %s: %+v", name, def)
			}
		})
	}
}

// TestValidateGrant_HoldsAGrantToItsDefinition: required properties must
// be there, undeclared ones are refused, types are held, and a number read
// back from JSON passes as an int only when whole.
func TestValidateGrant_HoldsAGrantToItsDefinition(t *testing.T) {
	def := tenancy()
	ok := map[string]any{"tenancy_ocid": "ocid1.tenancy.oc1..aaa", "region": "us-ashburn-1", "free_tier": true, "max_ocpus": float64(2)}
	if err := capability.ValidateGrant(def, ok); err != nil {
		t.Fatalf("a well-formed grant was refused: %v", err)
	}
	for name, props := range map[string]map[string]any{
		"a missing required property": {"region": "us-ashburn-1"},
		"an undeclared property":      {"tenancy_ocid": "x", "region": "y", "password": "z"},
		"a wrong type":                {"tenancy_ocid": 7, "region": "y"},
		"a fractional int":            {"tenancy_ocid": "x", "region": "y", "max_ocpus": 1.5},
		"a control character":         {"tenancy_ocid": "x\n", "region": "y"},
		"a bool that is not":          {"tenancy_ocid": "x", "region": "y", "free_tier": "yes"},
		"an int that is text":         {"tenancy_ocid": "x", "region": "y", "max_ocpus": "two"},
		"a string too long":           {"tenancy_ocid": strings.Repeat("x", capability.MaxDataPropertyLength+1), "region": "y"},
		"a string not UTF-8":          {"tenancy_ocid": "x\xff", "region": "y"},
	} {
		if err := capability.ValidateGrant(def, props); err == nil {
			t.Errorf("ValidateGrant accepted %s", name)
		}
	}
}

// TestIsDataName_NeverMatchesACompiledName: no compiled capability has a
// data capability's shape, so the two cannot collide.
func TestIsDataName_NeverMatchesACompiledName(t *testing.T) {
	for name := range capability.All() {
		if capability.IsDataName(name) {
			t.Errorf("compiled capability %q has a data capability's shape", name)
		}
	}
	if !capability.IsDataName("frr.vtysh") || capability.DataNamespace("frr.vtysh") != "frr" {
		t.Error("a data name was not recognised")
	}
}

// TestValidateGrantShape_HoldsAGrantWithNoDefinition: where no definition
// is held, a grant's properties are still bounded snake case names
// holding a plain string, a bool or a whole number.
func TestValidateGrantShape_HoldsAGrantWithNoDefinition(t *testing.T) {
	ok := map[string]any{"region": "us-ashburn-1", "free_tier": true, "max_ocpus": 2, "cores": int64(4), "read_back": float64(8)}
	if err := capability.ValidateGrantShape(ok); err != nil {
		t.Fatalf("a well-formed grant was refused: %v", err)
	}
	many := map[string]any{}
	for i := 0; i <= capability.MaxDataProperties; i++ {
		many[fmt.Sprintf("p%d", i)] = "x"
	}
	for name, props := range map[string]map[string]any{
		"too many properties": many,
		"a name not snake":    {"Region": "x"},
		"a list":              {"zones": []any{"a"}},
		"a fractional number": {"max_ocpus": 1.5},
		"a control character": {"region": "x\x1b"},
	} {
		if err := capability.ValidateGrantShape(props); err == nil {
			t.Errorf("ValidateGrantShape accepted %s", name)
		}
	}
}
