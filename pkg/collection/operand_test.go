// Tests for OperandProblem, the rule a value is held to when its parameter
// is a name placed on a device's command line, and for Register's refusal of
// an operand declared on a parameter that cannot hold a name.
package collection_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestOperandProblem holds a value to the operand rule in every shape a
// task writes or a template renders: text, a list item by item, and a value
// of another kind, which is the type rule's. A parameter that is not an
// operand takes anything.
func TestOperandProblem(t *testing.T) {
	name := collection.Param{Name: "name", Type: "string", Format: collection.ParamFormatOperand}
	deps := collection.Param{Name: "dependencies", Type: "list", Format: collection.ParamFormatOperand}
	open := collection.Param{Name: "command", Type: "string"}
	for label, tc := range map[string]struct {
		p    collection.Param
		v    any
		want string // a substring; "" means none
	}{
		"a name":                   {name, "curl", ""},
		"an option":                {name, "--autoremove", `begins with "-"`},
		"a pattern":                {name, "s*", "pattern"},
		"null, an absent value":    {name, nil, ""},
		"a number is the type's":   {name, 7, ""},
		"a list of names":          {deps, []any{"libjq1", "libonig5"}, ""},
		"a Go list of names":       {deps, []string{"libjq1"}, ""},
		"a list with an option":    {deps, []any{"libjq1", "-oX"}, `item 1 is refused: it begins with "-"`},
		"a list with a non-string": {deps, []any{3, "jq"}, ""},
		"not an operand":           {open, "--anything *", ""},
	} {
		t.Run(label, func(t *testing.T) {
			got := collection.OperandProblem(tc.p, tc.v)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("OperandProblem(%s, %#v) = %q, want none", tc.p.Name, tc.v, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("OperandProblem(%s, %#v) = %q, want it to say %q", tc.p.Name, tc.v, got, tc.want)
			}
		})
	}
}

// TestRegister_HoldsOperandsToText: an operand is text or a list of text,
// and Register refuses one declared on any other type, naming the
// parameter.
func TestRegister_HoldsOperandsToText(t *testing.T) {
	restore := collection.SnapshotForTest()
	t.Cleanup(restore)
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	register := func(name string, p collection.Param) error {
		return collection.Register(collection.Descriptor{Name: name, Invoke: run, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture"},
			Doc: collection.Doc{Params: []collection.Param{p}}}})
	}
	for _, typ := range []string{"bool", "int", "dict", "list of dict"} {
		err := register("operandtest.bad_"+strings.ReplaceAll(typ, " ", "_"), collection.Param{Name: "name", Type: typ, Format: collection.ParamFormatOperand})
		if err == nil || !strings.Contains(err.Error(), `parameter "name" of type `+typ+" an operand") {
			t.Errorf("%s: err = %v, want a refusal naming the parameter", typ, err)
		}
	}
	for _, typ := range []string{"string", "list", "list of string"} {
		if err := register("operandtest.ok_"+strings.ReplaceAll(typ, " ", "_"), collection.Param{Name: "name", Type: typ, Format: collection.ParamFormatOperand}); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
}
