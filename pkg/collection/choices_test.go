// Tests for ChoiceProblem, the rule a task's value is held to against its
// parameter's declared closed set, and for Register's refusal of a set that
// contradicts the parameter declaring it.
package collection_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestChoiceProblem holds a value to a declared set in every shape a task
// writes or a template renders: text, a list item by item, a number by its
// decimal text, and a case-folded set the way its method folds.
func TestChoiceProblem(t *testing.T) {
	state := collection.Param{Name: "state", Type: "string", Choices: []string{"present", "absent"}}
	folded := collection.Param{Name: "method", Type: "string", Choices: []string{"GET", "HEAD"}, ChoicesIgnoreCase: true}
	kinds := collection.Param{Name: "kinds", Type: "list of string", Choices: []string{"unit", "lint"}}
	port := collection.Param{Name: "port", Type: "int", Choices: []string{"22", "830"}}
	open := collection.Param{Name: "name", Type: "string"}
	for name, tc := range map[string]struct {
		p    collection.Param
		v    any
		want string
	}{
		"a choice":                     {state, "present", ""},
		"not a choice":                 {state, "presnt", "it is not one of present, absent"},
		"a choice in another case":     {state, "Present", "it is not one of present, absent"},
		"an empty string":              {state, "", "it is not one of present, absent"},
		"null, an absent value":        {state, nil, ""},
		"folded, another case":         {folded, "get", ""},
		"folded, surrounding space":    {folded, " head ", ""},
		"folded, not a choice":         {folded, "POST", "it is not one of GET, HEAD (case ignored)"},
		"a list of choices":            {kinds, []any{"unit", "lint"}, ""},
		"a Go list of choices":         {kinds, []string{"lint"}, ""},
		"a list with one outside":      {kinds, []any{"unit", "fuzz"}, "item 1 is not one of unit, lint"},
		"an int choice":                {port, 830, ""},
		"an int from JSON":             {port, float64(22), ""},
		"an int outside":               {port, 23, "it is not one of 22, 830"},
		"a map is the type rule's":     {state, map[string]any{}, ""},
		"a boolean is the type rule's": {state, true, ""},
		"no set takes anything":        {open, "anything", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := collection.ChoiceProblem(tc.p, tc.v); got != tc.want {
				t.Errorf("ChoiceProblem(%s, %#v) = %q, want %q", tc.p.Name, tc.v, got, tc.want)
			}
		})
	}
}

// TestRegister_HoldsChoicesToTheirParameter: a set no value could satisfy,
// or one contradicting the parameter's own default, is refused at
// registration, naming the parameter; a sound set registers.
func TestRegister_HoldsChoicesToTheirParameter(t *testing.T) {
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
	for name, tc := range map[string]struct {
		p    collection.Param
		want string
	}{
		"a default outside the set": {collection.Param{Name: "state", Type: "string", Default: "gone", Choices: []string{"present", "absent"}}, `defaulted to "gone", which is not one of its choices (present, absent)`},
		"an empty choice":           {collection.Param{Name: "state", Type: "string", Choices: []string{"present", " "}}, `an empty choice for parameter "state"`},
		"a choice twice":            {collection.Param{Name: "state", Type: "string", Choices: []string{"present", "present"}}, `the choice "present" twice`},
		"a choice twice, folded":    {collection.Param{Name: "method", Type: "string", Choices: []string{"GET", "get"}, ChoicesIgnoreCase: true}, `the choice "get" twice`},
		"a set on a boolean":        {collection.Param{Name: "force", Type: "bool", Choices: []string{"true"}}, "of type bool, which no choice can name"},
		"a set on a map":            {collection.Param{Name: "env", Type: "dict", Choices: []string{"a"}}, "of type dict, which no choice can name"},
		"folding with no set":       {collection.Param{Name: "state", Type: "string", ChoicesIgnoreCase: true}, "ChoicesIgnoreCase with no choices"},
	} {
		t.Run(name, func(t *testing.T) {
			err := register("choicetest."+strings.ReplaceAll(name, " ", "_"), tc.p)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
	for name, p := range map[string]collection.Param{
		"a default in the set":        {Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent"}},
		"a folded default":            {Name: "method", Type: "string", Default: "get", Choices: []string{"GET", "HEAD"}, ChoicesIgnoreCase: true},
		"a list of string with a set": {Name: "kinds", Type: "list of string", Choices: []string{"unit", "lint"}},
	} {
		if err := register("choicetest.ok_"+strings.ReplaceAll(name, " ", "_"), p); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
