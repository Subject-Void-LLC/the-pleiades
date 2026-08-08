package engine_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"go.yaml.in/yaml/v3"
)

// TestStringList_UnmarshalYAML confirms StringList accepts either a bare
// YAML string (one element) or a YAML list (one element per item).
func TestStringList_UnmarshalYAML(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected engine.StringList
	}{
		{
			name:     "bare string",
			input:    `field: hello`,
			expected: engine.StringList{"hello"},
		},
		{
			name:     "list",
			input:    "field:\n  - hello\n  - world",
			expected: engine.StringList{"hello", "world"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var holder struct {
				Field engine.StringList `yaml:"field"`
			}
			if err := yaml.Unmarshal([]byte(c.input), &holder); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(holder.Field) != len(c.expected) {
				t.Fatalf("expected %v, got %v", c.expected, holder.Field)
			}
			for i := range c.expected {
				if holder.Field[i] != c.expected[i] {
					t.Errorf("expected %v, got %v", c.expected, holder.Field)
				}
			}
		})
	}
}

// TestStringList_UnmarshalJSON confirms StringList accepts either a bare
// JSON string (one element) or a JSON array (one element per item).
func TestStringList_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected engine.StringList
	}{
		{
			name:     "bare string",
			input:    `{"field": "hello"}`,
			expected: engine.StringList{"hello"},
		},
		{
			name:     "list",
			input:    `{"field": ["hello", "world"]}`,
			expected: engine.StringList{"hello", "world"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var holder struct {
				Field engine.StringList `json:"field"`
			}
			if err := json.Unmarshal([]byte(c.input), &holder); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(holder.Field) != len(c.expected) {
				t.Fatalf("expected %v, got %v", c.expected, holder.Field)
			}
			for i := range c.expected {
				if holder.Field[i] != c.expected[i] {
					t.Errorf("expected %v, got %v", c.expected, holder.Field)
				}
			}
		})
	}
}

// TestConditional_Compile covers the three mutually exclusive ways to
// express a condition (When, WhenOr, WhenCEL), the conflict and
// no-condition cases, and the empty-list-is-absent rule.
func TestConditional_Compile(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to build CEL evaluator: %v", err)
	}

	// trueInput makes "stat.a" true and "stat.b" false; falseAndTrue is the
	// mirror image. bothTrue makes both true. Program.Eval's activation is
	// the top-level CEL variable map directly (cel.go), so every fixture
	// here wraps its facts under "stat" itself rather than relying on an
	// implicit wrap.
	falseAndTrue := map[string]interface{}{"stat": map[string]interface{}{"a": false, "b": true}}
	trueAndFalse := map[string]interface{}{"stat": map[string]interface{}{"a": true, "b": false}}
	bothTrue := map[string]interface{}{"stat": map[string]interface{}{"a": true, "b": true}}

	t.Run("When single string ANDs trivially", func(t *testing.T) {
		c := engine.Conditional{When: engine.StringList{"stat.a == true"}}
		prg, err := c.Compile(eval)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prg == nil {
			t.Fatalf("expected a compiled program, got nil")
		}
		result, err := prg.Eval(map[string]interface{}{"stat": map[string]interface{}{"a": true}})
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}
		if !result.OK {
			t.Errorf("expected true, got false")
		}
	})

	t.Run("When single false item names its own expression in Reason", func(t *testing.T) {
		c := engine.Conditional{When: engine.StringList{"stat.a == true"}}
		prg, err := c.Compile(eval)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, err := prg.Eval(map[string]interface{}{"stat": map[string]interface{}{"a": false}})
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}
		if result.OK {
			t.Fatalf("expected false, got true")
		}
		if !strings.Contains(result.Reason, "stat.a == true") {
			t.Errorf("expected Reason to name the false expression, got %q", result.Reason)
		}
	})

	t.Run("When multi-item list behaves as AND", func(t *testing.T) {
		c := engine.Conditional{When: engine.StringList{"stat.a == true", "stat.b == true"}}
		prg, err := c.Compile(eval)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		result, err := prg.Eval(trueAndFalse)
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}
		if result.OK {
			t.Errorf("expected AND of true/false to be false, got true")
		}

		result, err = prg.Eval(bothTrue)
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}
		if !result.OK {
			t.Errorf("expected AND of true/true to be true, got false")
		}
	})

	t.Run("When multi-item AND names the specific false item, not the whole list", func(t *testing.T) {
		c := engine.Conditional{When: engine.StringList{"stat.a == true", "stat.b == true", "stat.c == true"}}
		prg, err := c.Compile(eval)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// The second item ("stat.b == true") is the false one; the first
		// (true) and third (never evaluated, AND short-circuits) must not
		// appear as the reported cause.
		result, err := prg.Eval(map[string]interface{}{"stat": map[string]interface{}{"a": true, "b": false, "c": true}})
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}
		if result.OK {
			t.Fatalf("expected false, got true")
		}
		if !strings.Contains(result.Reason, "stat.b == true") {
			t.Errorf("expected Reason to name the second (false) item, got %q", result.Reason)
		}
		if strings.Contains(result.Reason, "stat.c == true") {
			t.Errorf("expected Reason not to mention the third item (never evaluated due to short-circuit), got %q", result.Reason)
		}
	})

	t.Run("WhenOr behaves as OR", func(t *testing.T) {
		c := engine.Conditional{WhenOr: engine.StringList{"stat.a == true", "stat.b == true"}}
		prg, err := c.Compile(eval)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		result, err := prg.Eval(falseAndTrue)
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}
		if !result.OK {
			t.Errorf("expected OR of false/true to be true, got false")
		}
		if result.Reason != "" {
			t.Errorf("expected no Reason when OK is true, got %q", result.Reason)
		}

		result, err = prg.Eval(map[string]interface{}{"stat": map[string]interface{}{"a": false, "b": false}})
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}
		if result.OK {
			t.Errorf("expected OR of false/false to be false, got true")
		}
	})

	t.Run("WhenOr all-false names every item in Reason", func(t *testing.T) {
		c := engine.Conditional{WhenOr: engine.StringList{"stat.a == true", "stat.b == true"}}
		prg, err := c.Compile(eval)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		result, err := prg.Eval(map[string]interface{}{"stat": map[string]interface{}{"a": false, "b": false}})
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}
		if result.OK {
			t.Fatalf("expected false, got true")
		}
		if !strings.Contains(result.Reason, "stat.a == true") || !strings.Contains(result.Reason, "stat.b == true") {
			t.Errorf("expected Reason to mention both false items, got %q", result.Reason)
		}
	})

	t.Run("WhenCEL passes through unchanged", func(t *testing.T) {
		c := engine.Conditional{WhenCEL: "stat.a == true && stat.b == false"}
		prg, err := c.Compile(eval)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		result, err := prg.Eval(trueAndFalse)
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}
		if !result.OK {
			t.Errorf("expected true, got false")
		}
	})

	t.Run("WhenCEL false names the raw expression in Reason", func(t *testing.T) {
		c := engine.Conditional{WhenCEL: "stat.a == true && stat.b == true"}
		prg, err := c.Compile(eval)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		result, err := prg.Eval(trueAndFalse)
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}
		if result.OK {
			t.Fatalf("expected false, got true")
		}
		if !strings.Contains(result.Reason, "stat.a == true && stat.b == true") {
			t.Errorf("expected Reason to contain the raw when_cel expression, got %q", result.Reason)
		}
	})

	t.Run("more than one set is an error, not a panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Compile panicked: %v", r)
			}
		}()

		c := engine.Conditional{
			When:   engine.StringList{"stat.a == true"},
			WhenOr: engine.StringList{"stat.b == true"},
		}
		prg, err := c.Compile(eval)
		if err == nil {
			t.Fatalf("expected an error when both when and when_or are set")
		}
		if prg != nil {
			t.Errorf("expected a nil program on error, got %v", prg)
		}
	})

	t.Run("all three set is an error, not a panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Compile panicked: %v", r)
			}
		}()

		c := engine.Conditional{
			When:    engine.StringList{"stat.a == true"},
			WhenOr:  engine.StringList{"stat.b == true"},
			WhenCEL: "stat.a == true",
		}
		_, err := c.Compile(eval)
		if err == nil {
			t.Fatalf("expected an error when all three are set")
		}
	})

	t.Run("none set is unconditional", func(t *testing.T) {
		c := engine.Conditional{}
		prg, err := c.Compile(eval)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prg != nil {
			t.Errorf("expected a nil program for an unconditional Conditional, got %v", prg)
		}
	})

	t.Run("empty When list is treated as absent", func(t *testing.T) {
		c := engine.Conditional{When: engine.StringList{}}
		prg, err := c.Compile(eval)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prg != nil {
			t.Errorf("expected a nil program for an empty When list, got %v", prg)
		}
	})
}

// TestBuild_TaskWhenCondition confirms a runbook task carrying a "when"
// field builds successfully and lands in the DAG's Conditions map, keyed by
// its synthesized ID, while a task with no "when" field has no entry there.
func TestBuild_TaskWhenCondition(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "task-when",
		"tasks": [
			{"name": "A", "fqcn": "noop", "when": "stat.ok == true"},
			{"name": "B", "fqcn": "ssh_exec"}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}

	if dag.Conditions["tasks[0]"] == nil {
		t.Errorf("expected a compiled condition for tasks[0], got nil")
	}
	if dag.Conditions["tasks[1]"] != nil {
		t.Errorf("expected no compiled condition for tasks[1], got %v", dag.Conditions["tasks[1]"])
	}
}

// TestBuild_ConflictingWhenFields confirms a task that sets both "when"
// and "when_or" fails to build with an actionable error, not a generic
// parse failure.
func TestBuild_ConflictingWhenFields(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	t.Run("conflicting task fields", func(t *testing.T) {
		payload := []byte(`{
			"id": "task-conflict",
			"tasks": [
				{"name": "A", "fqcn": "noop", "when": "stat.ok == true", "when_or": "stat.retry == true"}
			]
		}`)

		_, err := builder.Build(payload)
		if err == nil {
			t.Fatalf("expected an error for conflicting when/when_or on a task")
		}
		if !strings.Contains(err.Error(), "when") || !strings.Contains(err.Error(), "when_or") {
			t.Errorf("expected error to mention when and when_or, got: %v", err)
		}
		if !strings.Contains(err.Error(), "tasks[0]") {
			t.Errorf("expected error to mention the offending task's synthesized ID, got: %v", err)
		}
	})
}
