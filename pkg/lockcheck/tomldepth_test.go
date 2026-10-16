// Tests for the nesting bound applied to a Cargo file before go-toml.
package lockcheck

import (
	"strings"
	"testing"
)

// TestCargo_RefusesNestingThatWouldExhaustTheStack: a megabyte of "[" in
// either file is refused by name. Before the bound, go-toml recursed once
// per bracket and the test binary died of a stack overflow, which no
// recover can catch.
func TestCargo_RefusesNestingThatWouldExhaustTheStack(t *testing.T) {
	f, _ := Lookup("cargo")
	deep := "a = " + strings.Repeat("[", 1<<20) + "\n"
	if _, err := f.Verify([]byte(cargoLockOK), []byte(deep), nil); err == nil || !strings.Contains(err.Error(), "Cargo.toml nests arrays or tables more than 100 deep") {
		t.Errorf("a deep Cargo.toml: %v", err)
	}
	if _, err := f.Verify([]byte(deep), []byte(cargoManifestOK), nil); err == nil || !strings.Contains(err.Error(), "Cargo.lock nests") {
		t.Errorf("a deep Cargo.lock: %v", err)
	}
}

// TestCheckTOMLNesting counts brackets outside strings and comments only,
// in every kind of TOML string, so neither a bracket in a string refuses a
// file nor a quote hides one outside a string.
func TestCheckTOMLNesting(t *testing.T) {
	nest := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	for name, tc := range map[string]struct {
		doc  string
		deep bool
	}{
		"at the bound":                         {"a = " + nest(maxTOMLNesting), false},
		"past the bound":                       {"a = " + nest(maxTOMLNesting+1), true},
		"inline tables count":                  {"a = " + strings.Repeat("{b=", maxTOMLNesting+1) + "1" + strings.Repeat("}", maxTOMLNesting+1), true},
		"brackets in a basic string":           {`a = "` + strings.Repeat("[", 500) + `"`, false},
		"brackets in a literal string":         {`a = '` + strings.Repeat("[", 500) + `'`, false},
		"brackets in a multi-line basic":       {"a = \"\"\"\n" + strings.Repeat("[", 500) + "\n\"\"\"", false},
		"brackets in a multi-line literal":     {"a = '''\n" + strings.Repeat("[", 500) + "\n'''", false},
		"brackets in a comment":                {"# " + strings.Repeat("[", 500) + "\na = 1", false},
		"an escaped quote keeps the string":    {`a = "\"` + strings.Repeat("[", 500) + `"`, false},
		"closing brackets in a string":         {"a = " + strings.Repeat(`["]]]",`, maxTOMLNesting+1), true},
		"a literal string has no escapes":      {`a = '\'` + "\nb = " + nest(maxTOMLNesting+1), true},
		"quotes before a multi-line delimiter": {"a = \"\"\"x\"\"\"\"\"\nb = " + nest(maxTOMLNesting+1), true},
		"a newline ends a single-line string":  {"a = \"x\nb = " + nest(maxTOMLNesting+1), true},
		"table headers":                        {"[a]\n[[b]]\n[c.'d']\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkTOMLNesting("Cargo.toml", []byte(tc.doc))
			if (err != nil) != tc.deep {
				t.Errorf("checkTOMLNesting = %v, want refused: %v", err, tc.deep)
			}
		})
	}
}
