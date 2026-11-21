// Internal tests of the facts root's backstops: what holds for a DAG that did
// not come through the builder, a reader that answers nothing, and each way
// a read can have no answer.
package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"cel.dev/cel-go/common/types"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// nilReader answers every read with nothing, as the CLI's does for a
// project no fact was ever stored in.
type nilReader struct{}

func (nilReader) ReadFacts(context.Context, string, []string) (map[string]FactAnswer, error) {
	return nil, nil
}

// viewOver is a view of device under a run reading through reader, naming
// keys.
func viewOver(reader FactReader, device string, keys ...string) *factsView {
	var opts []ExecutorOption
	if reader != nil {
		opts = append(opts, WithFactReader(reader))
	}
	r := &run{x: NewExecutor(nil, nil, nil, nil, nil, 0, opts...), secrets: newStringSet(),
		knowledge: &factKnowledge{keys: keys, devices: map[string]*deviceFacts{}, missed: map[string]map[string]bool{}}}
	named := map[string]bool{}
	for _, k := range keys {
		named[k] = true
	}
	return &factsView{ctx: context.Background(), r: r, device: device, named: named}
}

// TestFactsView_TheWholeRootFailsAnEvaluationThatBypassedTheBuilder: a
// condition the builder would refuse, evaluated anyway, marks the root used
// whole, and the evaluation fails saying so; every whole-root method marks it.
func TestFactsView_TheWholeRootFailsAnEvaluationThatBypassedTheBuilder(t *testing.T) {
	eval, err := NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{`size(facts) > 0`, `facts == {}`, `facts.exists(k, k == "a")`, `facts[dyn(1)] == 2`, `facts.all(k, true)`} {
		prg, err := eval.Compile(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		v := viewOver(nilReader{}, "dev-1", "a")
		_, _ = prg.Eval(map[string]any{TemplateRootFacts: v})
		if !v.whole {
			t.Errorf("%s did not mark the root used whole", src)
		}
		if err := v.failure("tasks[0]", &Task{}, `"web1"`); err == nil || !strings.Contains(err.Error(), "used the facts root as a value") {
			t.Errorf("%s: failure = %v", src, err)
		}
	}
	for name, use := range map[string]func(v *factsView){
		"ConvertToNative": func(v *factsView) { _, _ = v.ConvertToNative(reflect.TypeOf(map[string]any{})) },
		"ConvertToType":   func(v *factsView) { _ = v.ConvertToType(types.MapType) },
		"Value":           func(v *factsView) { _ = v.Value() },
		"Size":            func(v *factsView) { _ = v.Size() },
		"Iterator":        func(v *factsView) { _ = v.Iterator() },
		"Equal":           func(v *factsView) { _ = v.Equal(types.True) },
	} {
		v := viewOver(nilReader{}, "dev-1")
		use(v)
		if !v.whole {
			t.Errorf("%s did not mark the root used whole", name)
		}
	}
	if v := viewOver(nilReader{}, "dev-1"); v.Type() != types.MapType || v.used() {
		t.Error("asking a view its type marks it used")
	}
}

// TestFactsView_EachReadWithNoAnswerSaysWhy: a key the condition did not
// name, a task with no device, a run with no knowledge or no reader, and a
// reader that answers nothing each leave the key unknown, with a reason.
func TestFactsView_EachReadWithNoAnswerSaysWhy(t *testing.T) {
	task := &Task{Name: "t"}
	for name, tc := range map[string]struct {
		view *factsView
		key  string
		want string
	}{
		"a key not named":     {viewOver(nilReader{}, "dev-1"), "a.b", "does not name it"},
		"no device":           {viewOver(nilReader{}, "", "a.b"), "a.b", "the task has no device"},
		"no reader":           {viewOver(nil, "dev-1", "a.b"), "a.b", "no store of facts"},
		"a reader of nothing": {viewOver(nilReader{}, "dev-1", "a.b"), "a.b", "no method that emits it has observed this device"},
	} {
		t.Run(name, func(t *testing.T) {
			if val := tc.view.Get(types.String(tc.key)); !types.IsError(val) {
				t.Fatalf("Get = %v, want no such key", val)
			}
			err := tc.view.failure("tasks[0]", task, deviceNameOf(nil, tc.view.device))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("failure = %v, want %q", err, tc.want)
			}
		})
	}
	noKnowledge := viewOver(nilReader{}, "dev-1", "a.b")
	noKnowledge.r.knowledge = nil
	if _, found := noKnowledge.Find(types.String("a.b")); found || !errors.Is(noKnowledge.misses[0].err, ErrNoFactReader) {
		t.Errorf("a run with no knowledge: %+v", noKnowledge.misses)
	}
	if got := deviceNameOf(map[string]any{TemplateRootDevice: map[string]any{"name": "web1"}}, "dev-1"); got != `"web1"` {
		t.Errorf("deviceNameOf with a device root = %s", got)
	}
	if got := deviceNameOf(nil, "dev-1"); got != `"dev-1"` {
		t.Errorf("deviceNameOf with a key = %s", got)
	}
}

// TestFactsView_APresentKeyReadsThroughGetAndContains: Get and Contains read
// a present key's value, as Find does.
func TestFactsView_APresentKeyReadsThroughGetAndContains(t *testing.T) {
	v := viewOver(everyKeyPresent{}, "dev-1", "a.b")
	if val := v.Get(types.String("a.b")); types.IsError(val) {
		t.Errorf("Get of a present key = %v", val)
	}
	if v.Contains(types.String("a.b")) != types.True || !v.present {
		t.Error("Contains of a present key is not true")
	}
	if val := v.Contains(types.Int(1)); !types.IsError(val) {
		t.Errorf("Contains of a number = %v, want an error", val)
	}
}

// TestFactReads_WhatABuilderDidNotParseIsReadAgain: a task the builder did
// not build reads its condition again, a condition that does not parse
// reads nothing, and FactReadsOf passes over a nil DAG and a nil task.
func TestFactReads_WhatABuilderDidNotParseIsReadAgain(t *testing.T) {
	if keys := (&Task{Conditional: Conditional{When: StringList{`facts["a.b"] == 1`}}}).conditionFactKeys(); len(keys) != 1 || keys[0] != "a.b" {
		t.Errorf("an unparsed task reads %v", keys)
	}
	if keys := (&Task{Conditional: Conditional{WhenCEL: `facts[`}}).conditionFactKeys(); keys != nil {
		t.Errorf("a condition that does not parse reads %v", keys)
	}
	if keys := (&Task{Conditional: Conditional{WhenCEL: `size(facts) > 0`}}).conditionFactKeys(); keys != nil {
		t.Errorf("a refused read reads %v", keys)
	}
	if FactReadsOf(nil) != nil || FactReadsOf(&DAG{Nodes: map[string]*Task{"x": nil}}) != nil {
		t.Error("FactReadsOf of nothing read something")
	}
	if _, err := sourcesNameFacts([]string{`facts[`}); err == nil {
		t.Error("sourcesNameFacts of a condition that does not parse is no error")
	}
	if _, err := ConditionFactReads(Conditional{WhenCEL: `(`}); err == nil {
		t.Error("ConditionFactReads of a condition that does not parse is no error")
	}
}

// TestAnswerFacts_AValueNeverConfirmedIsSeenNow: a stored value with no last
// confirmation reads as seen when it is read, and a method the registry and
// the catalog do not hold declares nothing.
func TestAnswerFacts_AValueNeverConfirmedIsSeenNow(t *testing.T) {
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	got := AnswerFacts([]string{"a.b", "c.d"}, map[string]facts.Entry{"a.b": {Key: "a.b", Value: "x"}}, []string{"no.such.method"}, now)
	if got["a.b"].SeenAt != now || got["c.d"].State != FactUnknown {
		t.Errorf("AnswerFacts = %+v", got)
	}
}

// TestDeclaredBefore_OnlyAnEarlierTaskCounts: a key a task in an earlier
// level declares is one the run could gather first; one only a later task
// declares, or a node the walk does not reach, is not.
func TestDeclaredBefore_OnlyAnEarlierTaskCounts(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(collection.Descriptor{Name: "tfy.gather",
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, nil
		},
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a fixture"},
			Facts: []collection.FactSpec{{Key: "tfy.k", Kind: collection.FactStable, Type: collection.FactString, Description: "a key"}}}}); err != nil {
		t.Fatal(err)
	}
	dag := buildInternalDAG(t, `{"id": "d", "tasks": [{"name": "a", "fqcn": "noop"}, {"name": "g", "fqcn": "tfy.gather"}, {"name": "b", "fqcn": "noop"}]}`)
	if !DeclaredBefore(dag, "tasks[2]", "tfy.k") {
		t.Error("a key an earlier task declares was not counted")
	}
	if DeclaredBefore(dag, "tasks[0]", "tfy.k") || DeclaredBefore(dag, "tasks[2]", "os.family") || DeclaredBefore(dag, "nowhere", "tfy.k") {
		t.Error("a key only a later task declares, one nothing declares, or a node the walk does not reach, counted as gathered first")
	}
}
