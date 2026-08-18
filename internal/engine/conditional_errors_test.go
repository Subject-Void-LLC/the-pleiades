package engine_test

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// This file covers the refusal paths of the conditional model: the shapes
// a `when` can be written in that are not conditions, and the two ways a
// condition can fail after it has been accepted.
//
// They are worth their own file because they are the paths that decide
// what a mistake in a runbook looks like. A condition this layer accepts
// loosely does not fail: it evaluates to something, and a task runs or is
// skipped on the strength of it. Every case below is one where being
// permissive would mean silently doing the wrong thing rather than
// refusing.

// TestStringList_RefusesAShapeThatIsNotACondition covers UnmarshalYAML's
// default branch and UnmarshalJSON's fallthrough.
func TestStringList_RefusesAShapeThatIsNotACondition(t *testing.T) {
	t.Run("a YAML mapping", func(t *testing.T) {
		// `when: {a: b}` parses as YAML and means nothing as a condition.
		// Accepting it would leave an empty condition list, which reads as
		// "no condition" and runs the task unconditionally, which is the
		// opposite of what a runbook that wrote a condition wanted.
		var got struct {
			When engine.StringList `yaml:"when"`
		}
		err := yaml.Unmarshal([]byte("when:\n  a: b\n"), &got)
		if err == nil {
			t.Fatal("a YAML mapping was accepted as a condition list")
		}
		if !strings.Contains(err.Error(), "string or a list of strings") {
			t.Errorf("error = %q, want it to say what a condition may be written as", err)
		}
	})

	t.Run("a YAML list holding a mapping", func(t *testing.T) {
		var got struct {
			When engine.StringList `yaml:"when"`
		}
		if err := yaml.Unmarshal([]byte("when:\n  - a: b\n"), &got); err == nil {
			t.Fatal("a list of mappings was accepted as a list of conditions")
		}
	})

	t.Run("a JSON number", func(t *testing.T) {
		var list engine.StringList
		err := json.Unmarshal([]byte("42"), &list)
		if err == nil {
			t.Fatal("a JSON number was accepted as a condition")
		}
		// The offending value is echoed back, because the JSON shape
		// reaching this decoder is often generated rather than typed, and
		// a message without it names nothing an author can search for.
		if !strings.Contains(err.Error(), "42") {
			t.Errorf("error = %q, want it to carry the value it rejected", err)
		}
	})

	t.Run("a JSON object", func(t *testing.T) {
		var list engine.StringList
		if err := json.Unmarshal([]byte(`{"a":"b"}`), &list); err == nil {
			t.Fatal("a JSON object was accepted as a condition")
		}
	})

	t.Run("a JSON array of numbers", func(t *testing.T) {
		var list engine.StringList
		if err := json.Unmarshal([]byte("[1,2]"), &list); err == nil {
			t.Fatal("a JSON array of numbers was accepted as a list of conditions")
		}
	})
}

// TestConditional_RefusesAnExpressionThatDoesNotCompile covers
// compileItems' error branch for each of the three keywords.
//
// Compilation happens before execution on purpose, so a typo in a
// condition fails the whole run up front rather than the first time that
// task is reached, possibly after earlier tasks have already changed a
// device. The keyword and the position have to survive into the message:
// a runbook with four conditions and one typo needs to say which.
func TestConditional_RefusesAnExpressionThatDoesNotCompile(t *testing.T) {
	cel, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("NewCELEvaluator: %v", err)
	}

	tests := []struct {
		name    string
		cond    engine.Conditional
		keyword string
	}{
		{
			name:    "when",
			cond:    engine.Conditional{When: engine.StringList{"stat.ok == true", "this is not ( valid"}},
			keyword: "when",
		},
		{
			name:    "when_or",
			cond:    engine.Conditional{WhenOr: engine.StringList{"this is not ( valid"}},
			keyword: "when_or",
		},
		{
			name:    "when_cel",
			cond:    engine.Conditional{WhenCEL: "this is not ( valid"},
			keyword: "when_cel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := tt.cond.Compile(cel)
			if err == nil {
				t.Fatal("an expression that does not compile was accepted")
			}
			if prg != nil {
				t.Error("a failed compile returned a program, which a caller could then evaluate")
			}
			if !strings.Contains(err.Error(), tt.keyword) {
				t.Errorf("error = %q, want it to name the %s keyword", err, tt.keyword)
			}
			if !strings.Contains(err.Error(), "this is not ( valid") {
				t.Errorf("error = %q, want it to quote the expression that failed", err)
			}
		})
	}

	t.Run("the failing item is named by position", func(t *testing.T) {
		cond := engine.Conditional{When: engine.StringList{"true", "true", "this is not ( valid"}}
		_, err := cond.Compile(cel)
		if err == nil {
			t.Fatal("an expression that does not compile was accepted")
		}
		if !strings.Contains(err.Error(), "expression 3") {
			t.Errorf("error = %q, want it to name the third expression rather than just the list", err)
		}
	})
}

// TestConditionProgram_ReportsAnEvaluationFailure covers the eval error
// branch in both evalAnd and evalOr.
//
// A condition that compiles can still fail against the facts it is given,
// and the two outcomes must stay distinct: an expression that evaluated
// false means skip this task, and an expression that could not be
// evaluated at all means something is wrong. Collapsing the second into
// the first would silently skip a task whose condition was broken, which
// looks exactly like a task that was correctly not needed.
func TestConditionProgram_ReportsAnEvaluationFailure(t *testing.T) {
	cel, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("NewCELEvaluator: %v", err)
	}

	// Indexing a map with a key that is not there compiles and fails at
	// evaluation, which is the shape a real broken condition takes: a
	// reference to a register a previous task did not produce.
	const broken = `stat["missing"] == "x"`

	for _, tt := range []struct {
		name    string
		cond    engine.Conditional
		keyword string
	}{
		{name: "when (and)", cond: engine.Conditional{When: engine.StringList{broken}}, keyword: "when"},
		{name: "when_or", cond: engine.Conditional{WhenOr: engine.StringList{broken}}, keyword: "when_or"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := tt.cond.Compile(cel)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}

			res, err := prg.Eval(map[string]interface{}{"stat": map[string]interface{}{}, "nodes": map[string]interface{}{}})
			if err == nil {
				t.Fatalf("a condition that could not be evaluated returned %+v instead of an error", res)
			}
			if res.OK {
				t.Error("a failed evaluation reported the condition as holding")
			}
			if !strings.Contains(err.Error(), tt.keyword) {
				t.Errorf("error = %q, want it to name the %s keyword", err, tt.keyword)
			}
			if !strings.Contains(err.Error(), broken) {
				t.Errorf("error = %q, want it to quote the expression that failed", err)
			}
		})
	}
}
