// Tests that the condition reference is read from the real environment: each
// library adds functions, a library's overloads are told from the standard
// library's own, and optional values bring macros.
package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

func TestConditionLibraries(t *testing.T) {
	libs, err := engine.ConditionLibraries()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ext.Network", "ext.Encoders", "cel.OptionalTypes", "filters"}
	if len(libs) != len(want) {
		t.Fatalf("%d libraries, want %d", len(libs), len(want))
	}
	macros := 0
	for i, lib := range libs {
		if lib.Name != want[i] || len(lib.Functions) == 0 {
			t.Errorf("library %d = %q with %d functions, want %q with some", i, lib.Name, len(lib.Functions), want[i])
		}
		for _, f := range lib.Functions {
			if f.Name == "" || len(f.Overloads) == 0 {
				t.Errorf("%s: function %+v", lib.Name, f)
			}
			if f.Macro {
				macros++
			} else if f.Overloads[0].Signature == "" {
				t.Errorf("%s.%s has an overload with no signature", lib.Name, f.Name)
			}
		}
	}
	if macros == 0 {
		t.Error("cel.OptionalTypes brought no macro")
	}
	// ext.Network adds overloads to the standard library's own string
	// function: only those are its.
	found := false
	for _, f := range libs[0].Functions {
		if f.Name == "string" {
			found = true
			for _, o := range f.Overloads {
				if o.Signature != "string(net.CIDR) -> string" && o.Signature != "string(net.IP) -> string" {
					t.Errorf("string overload %q is not ext.Network's", o.Signature)
				}
			}
		}
	}
	if !found {
		t.Error("ext.Network's overloads of string are missing")
	}
}
