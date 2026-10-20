// Tests for ParamFits, the rule a task's value is held to against its
// method's declared parameter type, and for Register's refusal of a type
// that rule does not know.
package collection_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestParamFits holds every declared type to the values each tier produces
// for it, and to the values YAML makes easy to write by mistake: a number
// where text was meant (an unquoted 0644, stdin: 4), and text where a
// number or a boolean was (a quoted "22", "true").
func TestParamFits(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		v    any
		want bool
	}{
		{"string", "text", true},
		{"string", "", true},
		{"string", 4, false},
		{"string", 420, false},
		{"string", true, false},
		{"string", []any{"a"}, false},
		{"bool", true, true},
		{"bool", "true", false},
		{"int", 22, true},
		{"int", int64(22), true},
		{"int", float64(22), true},
		{"int", json.Number("22"), true},
		{"int", 22.5, false},
		{"int", "22", false},
		{"int", math.NaN(), false},
		{"int", math.Inf(1), false},
		{"float", 0.5, true},
		{"float", 2, true},
		{"float", "0.5", false},
		{"list", []any{"a", 1}, true},
		{"list", []string{"a"}, true},
		{"list", "a,b", false},
		{"list of string", []any{"a", "b"}, true},
		{"list of string", []string{"a"}, true},
		{"list of string", []string{}, true},
		{"list of string", []any{"a", 1}, false},
		{"list of string", "a", false},
		{"list of dict", []any{map[string]any{"k": 1}}, true},
		{"list of dict", []any{"k"}, false},
		{"dict", map[string]any{"k": 1}, true},
		{"dict", []any{}, false},
		{"int or list of int", 200, true},
		{"int or list of int", []any{200, float64(204)}, true},
		{"int or list of int", []any{200, "204"}, false},
		{"scalar or list", "domain,private", true},
		{"scalar or list", 22, true},
		{"scalar or list", []any{"server", "a.b", 0}, true},
		{"scalar or list", true, false},
		{"scalar or list", map[string]any{}, false},
		{"scalar or list", []any{map[string]any{}}, false},
		{"any", map[string]any{}, true},
		{"string", nil, true},
		{"int", nil, true},
	} {
		got, err := collection.ParamFits(tc.typ, tc.v)
		if err != nil || got != tc.want {
			t.Errorf("ParamFits(%q, %#v) = %v, %v; want %v", tc.typ, tc.v, got, err, tc.want)
		}
	}
	if _, err := collection.ParamFits("str", "x"); err == nil || !strings.Contains(err.Error(), "not a parameter type") {
		t.Errorf("an unknown type was accepted: %v", err)
	}
}

// TestRegister_RefusesAnUnknownParamType: a method declaring a type no rule
// knows, or none at all, is refused at registration, since validation and
// the engine would refuse every value of it.
func TestRegister_RefusesAnUnknownParamType(t *testing.T) {
	restore := collection.SnapshotForTest()
	t.Cleanup(restore)
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	for _, typ := range []string{"str", ""} {
		err := collection.Register(collection.Descriptor{Name: "typetest.m" + typ, Invoke: run, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture"},
			Doc: collection.Doc{Params: []collection.Param{{Name: "p", Type: typ}}}}})
		if err == nil || !strings.Contains(err.Error(), "not a parameter type") {
			t.Errorf("type %q: err = %v, want a refusal", typ, err)
		}
	}
}

// TestValueKind names a value's kind without its value.
func TestValueKind(t *testing.T) {
	for v, want := range map[any]string{"": "an empty string", "secret": "a string", 4: "a number", true: "a boolean"} {
		if got := collection.ValueKind(v); got != want {
			t.Errorf("ValueKind(%#v) = %q, want %q", v, got, want)
		}
	}
	if got := collection.ValueKind([]string{"a"}); got != "a list" {
		t.Errorf("ValueKind([]string) = %q", got)
	}
}

// TestParamFits_EveryNumberShapeAndKind covers the decoders' other number
// shapes (uint, float32, json.Number) and the values that are not numbers at
// all, which a number parameter refuses whatever shape they arrive in.
func TestParamFits_EveryNumberShapeAndKind(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		v    any
		want bool
	}{
		{"int", uint(3), true},
		{"int", uint64(3), true},
		{"int", float32(3), true},
		{"int", float32(3.5), false},
		{"int", float32(math.Inf(1)), false},
		{"int", json.Number("3.5"), false},
		{"int", json.Number("3"), true},
		{"int", json.Number("x"), false},
		{"int", true, false},
		{"float", float32(0.5), true},
		{"float", uint8(2), true},
		{"float", json.Number("0.5"), true},
		{"float", json.Number("x"), false},
		{"float", true, false},
		{"float", math.Inf(-1), false},
		{"int", []any{1}, false},
		{"int or list of int", []any{1, 2.5}, false},
		{"int or list of int", "1", false},
		{"scalar or list", []any{1, "a"}, true},
		{"scalar or list", []any{1.5}, true},
		{"scalar or list", []any{true}, false},
		{"list", 3, false},
	} {
		got, err := collection.ParamFits(tc.typ, tc.v)
		if err != nil || got != tc.want {
			t.Errorf("ParamFits(%q, %#v) = %v, %v; want %v", tc.typ, tc.v, got, err, tc.want)
		}
	}
}

// TestValueKind_NamesEachKind: a value's kind for a message, without the
// value, covering the shapes a decoder produces and the one it does not.
func TestValueKind_NamesEachKind(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want string
	}{
		{nil, "null"},
		{true, "a boolean"},
		{map[string]any{}, "a map"},
		{[]any{}, "a list"},
		{uint8(1), "a number"},
		{json.Number("1.5"), "a number"},
		{struct{}{}, "a value of type struct {}"},
	} {
		if got := collection.ValueKind(tc.v); got != tc.want {
			t.Errorf("ValueKind(%#v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}
