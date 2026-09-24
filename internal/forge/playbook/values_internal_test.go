// Tests for the value rules the translator borrows from Python and
// Ansible: truth, comparison and membership as Jinja folds them, a
// resolved value's conversion to a native type, CEL literals, merge keys,
// and the report model's JSON forms.
package playbook

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestPyValues covers Python's truth, Ansible's bool and int filters, and
// len, against what Python itself answers.
func TestPyValues(t *testing.T) {
	for _, tc := range []struct {
		v      any
		truthy bool
	}{
		{nil, false}, {true, true}, {int64(0), false}, {int64(-1), true}, {0.0, false}, {0.5, true},
		{"", false}, {"0", true}, {[]any{}, false}, {[]any{nil}, true}, {map[string]any{}, false}, {map[string]any{"k": 1}, true},
	} {
		if got := pyTruthy(tc.v); got != tc.truthy {
			t.Errorf("bool(%#v) = %v, want %v", tc.v, got, tc.truthy)
		}
	}
	for v, want := range map[any]bool{"Yes": true, " on ": true, "t": true, "1": true, "no": false, "2": false, int64(1): true, int64(2): false, 1.0: true, true: true, nil: false} {
		if got := ansibleBool(v); got != want {
			t.Errorf("%#v | bool = %v, want %v", v, got, want)
		}
	}
	for _, tc := range []struct {
		v    any
		want int64
		ok   bool
	}{{int64(3), 3, true}, {3.9, 3, true}, {true, 1, true}, {false, 0, true}, {" 42 ", 42, true}, {"4x", 0, false}, {nil, 0, false}} {
		if got, ok := toInt(tc.v); got != tc.want || ok != tc.ok {
			t.Errorf("%#v | int = %d %v, want %d %v", tc.v, got, ok, tc.want, tc.ok)
		}
	}
	if n, ok := length("héllo"); n != 5 || !ok {
		t.Errorf("length counts %d characters, want 5", n)
	}
	if _, ok := length(int64(3)); ok {
		t.Error("a number has a length")
	}
}

// TestPyCompare covers ==, ordering and in across Python's kinds, and the
// comparisons that cannot be answered, which are refused.
func TestPyCompare(t *testing.T) {
	for _, tc := range []struct {
		a      any
		op     string
		b      any
		want   bool
		wantOK bool
	}{
		{int64(1), "==", 1.0, true, true},
		{true, "==", int64(1), true, true},
		{"1", "==", int64(1), false, true},
		{nil, "==", nil, true, true},
		{int64(2), "!=", int64(3), true, true},
		{int64(2), "<", 2.5, true, true},
		{int64(2), "<=", int64(2), true, true},
		{int64(3), ">", int64(2), true, true},
		{int64(2), ">=", int64(3), false, true},
		{"abc", "<", "abd", true, true},
		{"b", ">=", "a", true, true},
		{int64(1), "<", "a", false, false},
		{"el", "in", "hello", true, true},
		{int64(1), "in", "hello", false, false},
		{1.0, "in", []any{int64(1), "x"}, true, true},
		{"z", "not in", []any{"a"}, true, true},
		{"k", "in", map[string]any{"k": nil}, true, true},
		// Python answers False, but YAML's map keys are read as text here,
		// so a number's membership cannot be answered and is refused.
		{int64(1), "in", map[string]any{"1": nil}, false, false},
		{"a", "in", int64(5), false, false},
	} {
		got, ok := pyCompare(tc.op, tc.a, tc.b)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("%#v %s %#v = %v %v, want %v %v", tc.a, tc.op, tc.b, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestCoerceGo covers a resolved value's conversion to each native type,
// with Ansible's own spellings accepted and every conversion that would
// guess refused.
func TestCoerceGo(t *testing.T) {
	for _, tc := range []struct {
		v          any
		typ, param string
		want       any
	}{
		{"x", "string", "p", "x"},
		{int64(8080), "string", "port", "8080"},
		{int64(420), "string", "mode", nil},
		{true, "string", "p", nil},
		{1.5, "string", "p", nil},
		{"a", "list of string", "p", []any{"a"}},
		{"a,b", "list of string", "p", nil},
		{[]any{"a", int64(2)}, "list", "p", []any{"a", "2"}},
		{int64(5), "int or list of int", "p", []any{int64(5)}},
		{[]any{"7"}, "int or list of int", "p", []any{int64(7)}},
		{"T", "bool", "p", true},
		{"off", "bool", "p", false},
		{int64(1), "bool", "p", true},
		{int64(2), "bool", "p", nil},
		{"maybe", "bool", "p", nil},
		{"42", "int", "p", int64(42)},
		{"0x2a", "int", "p", nil},
		{2.0, "int", "p", nil},
		{int64(2), "float", "p", 2.0},
		{2.5, "float", "p", 2.5},
		{"2.5", "float", "p", nil},
		{map[string]any{"k": "v"}, "dict", "p", map[string]any{"k": "v"}},
		{[]any{}, "dict", "p", nil},
	} {
		got, err := coerceGo(tc.v, tc.typ, tc.param)
		if tc.want == nil {
			if err == nil {
				t.Errorf("%#v as %s = %#v, want refused", tc.v, tc.typ, got)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%#v as %s = %#v, %v; want %#v", tc.v, tc.typ, got, err, tc.want)
		}
	}
	for v, want := range map[any]string{nil: "null", true: "a boolean", int64(1): "a number", "x": "a text", struct{}{}: "a value"} {
		if got := kindOf(v); got != want {
			t.Errorf("kindOf(%#v) = %q, want %q", v, got, want)
		}
	}
}

// TestCELLiteral covers each constant's CEL form: every string quoted, a
// float always written as one, map keys in order, and the values CEL has
// no literal for refused.
func TestCELLiteral(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want string
	}{
		{nil, "null"}, {true, "true"}, {int64(-3), "-3"}, {7, "7"}, {2.0, "2.0"}, {1e21, "1e+21"}, {0.25, "0.25"},
		{`a"b`, `"a\"b"`}, {[]any{"x", int64(1)}, `["x", 1]`}, {map[string]any{"b": int64(1), "a": false}, `{"a": false, "b": 1}`},
	} {
		if got, err := celLiteral(tc.v); err != nil || got != tc.want {
			t.Errorf("celLiteral(%#v) = %q, %v; want %q", tc.v, got, err, tc.want)
		}
	}
	for _, v := range []any{math.NaN(), math.Inf(1), []any{math.Inf(-1)}, map[string]any{"k": math.NaN()}, struct{}{}} {
		if got, err := celLiteral(v); err == nil {
			t.Errorf("celLiteral(%#v) = %q, want refused", v, got)
		}
	}
}

// TestMergeKeys covers YAML merge keys as PyYAML (Ansible's parser)
// reads them: an explicit key wins over a merged one, an earlier map in a
// merge list wins over a later one, a later merge key wins over an
// earlier one, and only an explicit key repeated is a repeat.
func TestMergeKeys(t *testing.T) {
	var doc yaml.Node
	src := "a: &a {x: 1, y: 1}\nb: &b {y: 2, z: 2}\nm:\n  <<: [*a, *b]\n  x: 0\nn:\n  <<: *a\n  <<: *b\nr: {k: 1, k: 2}\n"
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	values := func(key string) (map[string]string, int) {
		entries, repeated := mapEntries(lookup(&doc, key))
		out := map[string]string{}
		for _, e := range entries {
			out[e.key] = deref(e.value).Value
		}
		return out, len(repeated)
	}
	if got, rep := values("m"); !reflect.DeepEqual(got, map[string]string{"x": "0", "y": "1", "z": "2"}) || rep != 0 {
		t.Errorf("m = %v (%d repeated)", got, rep)
	}
	if got, _ := values("n"); !reflect.DeepEqual(got, map[string]string{"x": "1", "y": "2", "z": "2"}) {
		t.Errorf("n = %v", got)
	}
	if _, rep := values("r"); rep != 1 {
		t.Errorf("r repeats %d keys, want 1", rep)
	}
	if entries, _ := mapEntries(lookup(&doc, "missing")); entries != nil {
		t.Error("a missing map has entries")
	}
}

// TestModelJSON covers the report model's JSON forms: a class and an
// outcome by name and back, an undecided class as null, and an unknown
// name refused rather than read as the zero value.
func TestModelJSON(t *testing.T) {
	for c := ClassUnclassified; c <= ClassObserve; c++ {
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var back Class
		if err := json.Unmarshal(data, &back); err != nil || back != c {
			t.Errorf("%s round-trips to %s, %v", c, back, err)
		}
	}
	for o := OutcomeConverted; o <= OutcomeBlocked; o++ {
		data, _ := json.Marshal(o)
		var back Outcome
		if err := json.Unmarshal(data, &back); err != nil || back != o {
			t.Errorf("%s round-trips to %s, %v", o, back, err)
		}
	}
	var c Class
	var o Outcome
	for _, bad := range []error{json.Unmarshal([]byte(`"promoted"`), &c), json.Unmarshal([]byte(`7`), &c), json.Unmarshal([]byte(`"skipped"`), &o)} {
		if bad == nil {
			t.Error("an unknown name was accepted")
		}
	}
	if Class(9).String() != "Class(9)" || Outcome(9).String() != "Outcome(9)" {
		t.Error("an out-of-range value does not name itself as one")
	}
	r := Report{Findings: []Finding{{Outcome: OutcomeInfo}}}
	if r.NeedsHuman() {
		t.Error("an info finding asks for a person")
	}
	r.Findings = append(r.Findings, Finding{Outcome: OutcomeReview})
	if !r.NeedsHuman() {
		t.Error("a review finding does not ask for a person")
	}
}
