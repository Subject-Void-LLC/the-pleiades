// Package validate tests the parts of the vars scope reader that only an
// in-package test reaches: condition parsing and its failure modes.
package validate

import (
	"errors"
	"reflect"
	"testing"

	"cel.dev/cel-go/cel"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestConditionVarReads_ReadsEveryFormAndSurvivesBadInput: the index form and
// the in form are found, a condition that does not parse reads nothing, and an
// environment that cannot be built reads nothing.
func TestConditionVarReads_ReadsEveryFormAndSurvivesBadInput(t *testing.T) {
	read, tested := conditionVarReads(engine.Conditional{WhenCEL: `vars["a"] == 1 && "b" in vars && has(vars.c) && vars.d == 2`})
	if !reflect.DeepEqual(read, []string{"a", "d"}) || !reflect.DeepEqual(tested, []string{"b", "c"}) {
		t.Errorf("read %v tested %v", read, tested)
	}
	if read, tested := conditionVarReads(engine.Conditional{WhenCEL: "vars.("}); len(read)+len(tested) != 0 {
		t.Errorf("a condition that does not parse read %v %v", read, tested)
	}
	old := newCELEnv
	t.Cleanup(func() { newCELEnv = old })
	newCELEnv = func() (*cel.Env, error) { return nil, errors.New("no environment") }
	if read, tested := conditionVarReads(engine.Conditional{WhenCEL: "vars.a == 1"}); len(read)+len(tested) != 0 {
		t.Errorf("with no environment read %v %v", read, tested)
	}
}
