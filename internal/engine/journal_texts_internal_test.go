// Tests for a list of identifiers as a recorded undo value (InverseParam's
// Texts): kept when every item is text the journal holds, refused
// otherwise, and handed back in the shape a runbook gives a list.
package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestRecordedValue_KeepsAListOfIdentifiers(t *testing.T) {
	for name, value := range map[string]any{
		"as a method records it": []string{"libjq1", "libonig5"},
		"after YAML or JSON":     []any{"libjq1", "libonig5"},
	} {
		p, ok := recordedValue("dependencies", value, true)
		if !ok || !reflect.DeepEqual(p.Texts, []string{"libjq1", "libonig5"}) || p.Text != nil {
			t.Errorf("%s: recorded %+v, %v", name, p, ok)
		}
		if v, ok := p.Value(); !ok || !reflect.DeepEqual(v, []any{"libjq1", "libonig5"}) {
			t.Errorf("%s: Value() = %v, %v, want the list as []any", name, v, ok)
		}
	}
}

func TestRecordedValue_RefusesAListItCannotHoldExactly(t *testing.T) {
	many := make([]string, maxRecordedItems+1)
	for i := range many {
		many[i] = "pkg"
	}
	for name, value := range map[string]any{
		"empty":            []string{},
		"too long":         many,
		"a non-string":     []any{"libjq1", 3},
		"a line break":     []string{"libjq1\nlibfake"},
		"an oversized one": []string{strings.Repeat("x", maxRecordedText+1)},
		"a list of lists":  []any{[]any{"a"}},
	} {
		if p, ok := recordedValue("dependencies", value, true); ok {
			t.Errorf("%s: recorded %+v", name, p)
		}
	}
}

// TestListParam_ReadsEveryStringListType: a parameter declared "list of
// string" is a list for the journal exactly as one declared "list" is, so
// an undo naming one (net.linux.bridge records ports and detach_ports so)
// is kept whole instead of being marked not recorded in full; a list of
// dicts is not, since the journal holds lists of text only.
func TestListParam_ReadsEveryStringListType(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	noop := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{
		Name: "listparamtest.target",
		Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "fixture"},
			Doc: collection.Doc{Params: []collection.Param{
				{Name: "plain", Type: collection.ParamTypeList},
				{Name: "strings", Type: collection.ParamTypeListOfString},
				{Name: "dicts", Type: collection.ParamTypeListOfDict},
				{Name: "text", Type: collection.ParamTypeString},
			}}},
		Invoke: noop,
	}); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]bool{"plain": true, "strings": true, "dicts": false, "text": false, "absent": false} {
		if got := ListParam("listparamtest.target", key); got != want {
			t.Errorf("ListParam(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestRecordedValue_RefusesAListWhereTheUndoTakesText(t *testing.T) {
	if p, ok := recordedValue("name", []string{"libjq1"}, false); ok {
		t.Errorf("a list for a parameter not declared a list was recorded: %+v", p)
	}
	if ListParam("no.such.method", "x") {
		t.Error("an unregistered method's parameter reads as a list")
	}
}
