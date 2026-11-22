// Package validate tests the parts of the vars scope reader that only an
// in-package test reaches: condition parsing and its failure modes.
package validate

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestConditionVarReads_ReadsEveryFormAndSurvivesBadInput: the index form, the
// in form and the optional forms are found, and a condition that does not
// parse reads nothing (ConditionRule refuses it).
func TestConditionVarReads_ReadsEveryFormAndSurvivesBadInput(t *testing.T) {
	read, tested := conditionVarReads(engine.Conditional{WhenCEL: `vars["a"] == 1 && "b" in vars && has(vars.c) && vars.d == 2`})
	if !reflect.DeepEqual(read, []string{"a", "d"}) || !reflect.DeepEqual(tested, []string{"b", "c"}) {
		t.Errorf("read %v tested %v", read, tested)
	}
	// Ansible's is defined in a when: list tests the name as has() does, and
	// in when_cel, which is CEL as written, it is no condition at all.
	read, tested = conditionVarReads(engine.Conditional{When: engine.StringList{"vars.e is defined", "vars.f is not defined", "vars.g == 1"}})
	if !reflect.DeepEqual(read, []string{"g"}) || !reflect.DeepEqual(tested, []string{"e", "f"}) {
		t.Errorf("is defined: read %v tested %v", read, tested)
	}
	if read, tested := conditionVarReads(engine.Conditional{WhenCEL: "vars.e is defined"}); len(read)+len(tested) != 0 {
		t.Errorf("is defined in when_cel read %v %v", read, tested)
	}
	if read, tested := conditionVarReads(engine.Conditional{WhenCEL: "vars.("}); len(read)+len(tested) != 0 {
		t.Errorf("a condition that does not parse read %v %v", read, tested)
	}
	// The optional field and index are written for the name's absence, and
	// parse with the syntax the condition compiles with (FAILURE_PATTERNS 1351).
	read, tested = conditionVarReads(engine.Conditional{WhenCEL: `vars.?h.orValue(1) == 1 && vars[?"i"].hasValue() && vars.j == 1`})
	if !reflect.DeepEqual(read, []string{"j"}) || !reflect.DeepEqual(tested, []string{"h", "i"}) {
		t.Errorf("optional: read %v tested %v", read, tested)
	}
}
