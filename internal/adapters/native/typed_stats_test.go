// Tests that a Collection method's stats mean the same to a later task's
// condition and template on the Walk tier, where they cross the per-task
// child's JSON pipe, as on the Crawl tier, where they never leave the
// process: an int stays an int, a big one keeps every digit, and a typed
// slice reads as a list either way.
package native

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The typed-stats fixture methods, registered at init so the re-executed
// child has them too.
const (
	typedStatsMethod  = "nativetypedtest.stats"
	typedRecordMethod = "nativetypedtest.record"
)

// typedStatsBig is above 2^53, the largest integer a float64 holds
// exactly, so a stat decoded through float64 loses its last digit.
const typedStatsBig int64 = 9007199254740993

func init() {
	fixture := collection.Reversibility{Notes: "a test fixture that changes nothing"}
	collection.MustRegister(collection.Descriptor{
		Name:     typedStatsMethod,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: fixture},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			for k, v := range map[string]any{
				"count":  3,
				"big":    typedStatsBig,
				"ratio":  2.5,
				"whole":  4.0,
				"items":  []string{"a", "b"},
				"ports":  []int{22, 443},
				"nested": map[string]any{"n": 7},
			} {
				if err := rc.SetStat(k, v); err != nil {
					return collection.Result{}, err
				}
			}
			return collection.Result{}, nil
		},
	})
	// record writes each parameter it was handed back as a stat naming its
	// Go type and its text, which is what a method building a device
	// command from it would see. A list's text alone is recorded: a list
	// parameter is []any on both tiers once it is not a rendered value
	// (YAML decodes one so), and a method reads it element by element.
	var params []collection.Param
	for _, name := range []string{"text", "count", "big", "ports", "whole"} {
		params = append(params, collection.Param{Name: name, Type: collection.ParamTypeAny, Description: "a value to record"})
	}
	collection.MustRegister(collection.Descriptor{
		Name:     typedRecordMethod,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: fixture, Doc: collection.Doc{Params: params}},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			for k, v := range params {
				if err := rc.SetStat("got_"+k, fmt.Sprintf("%s %v", typedKind(v), v)); err != nil {
					return collection.Result{}, err
				}
			}
			return collection.Result{}, nil
		},
	})
}

// typedKind names v's kind as a method reading a parameter tells it apart:
// an integer of any width (an int64 stat reads back as an int once it has
// crossed the pipe, which sdk.IntParam, CEL and the journal treat alike), a
// float, a list, or v's own type.
func typedKind(v any) string {
	switch reflect.TypeOf(v).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Slice:
		return "list"
	}
	return fmt.Sprintf("%T", v)
}

// typedStatsRunbook registers the fixture's stats as r, then reads them in
// a changed_when, in conditions and in templates. Every condition holds on
// the Crawl tier, so each gated task runs there.
const typedStatsRunbook = `id: typed-stats
tasks:
  - name: stats
    nativetypedtest.stats: {}
    register: r
    changed_when: "result.r.count > 2 && size(result.r.ports) == 2"
  - name: equal
    noop: {}
    when_cel: "result.r.count == 3"
  - name: greater
    noop: {}
    when_cel: "result.r.count > 2"
  - name: arithmetic
    noop: {}
    when_cel: "result.r.count + 1 == 4"
  - name: int-typed
    noop: {}
    when_cel: "type(result.r.count) == int"
  - name: size
    noop: {}
    when_cel: "size(result.r.items) > 0 && result.r.items == ['a', 'b']"
  - name: list-element
    noop: {}
    when_cel: "result.r.ports[0] + 1 == 23"
  - name: big
    noop: {}
    when_cel: "result.r.big == 9007199254740993"
  - name: nested
    noop: {}
    when_cel: "result.r.nested.n == 7"
  - name: ratio
    noop: {}
    when_cel: "result.r.ratio == 2.5"
  - name: whole-float
    noop: {}
    when_cel: "type(result.r.whole) == double && result.r.whole * 1.5 == 6.0"
  - name: render
    nativetypedtest.record:
      text: "ping count {{ result.r.count }} big {{ result.r.big }} ratio {{ result.r.ratio }}"
      count: "{{ result.r.count }}"
      big: "{{ result.r.big }}"
      ports: "{{ result.r.ports }}"
      whole: "{{ result.r.whole }}"
`

// typedNode is what a test compares of one node's result across tiers.
type typedNode struct {
	ID      string
	Skipped bool
	Changed bool
	Err     string
	Stats   string
}

// runTypedStats runs typedStatsRunbook through the engine stack the
// Adapter builds, with invoke as the Collection invoker: nil runs each
// method in this process as the Crawl tier does, and the Adapter's own
// invoker runs it in a real child as a Runner does.
func runTypedStats(t *testing.T, invoke engine.CollectionInvoker) []typedNode {
	t.Helper()
	src := writeRunbook(t, "typed-stats", typedStatsRunbook)
	dag, err := src.GetDAG(context.Background(), "typed-stats")
	if err != nil {
		t.Fatalf("GetDAG: %v", err)
	}
	payload := wire.DispatchPayload{JobID: "job-1", DeviceID: "dev-1", DeviceName: "router1", DeviceHost: "10.0.0.1"}
	device, err := dispatchedDevice(payload)
	if err != nil {
		t.Fatalf("dispatchedDevice: %v", err)
	}
	var opts []engine.CollectionActionExecutorOption
	if invoke != nil {
		opts = append(opts, engine.WithCollectionInvoker(invoke))
	}
	actions := engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), runbookContextFor(payload), opts...)
	locks := lock.NewInProcessManager()
	defer locks.Close()
	bus := event.NewInProcessBus()
	defer bus.Close()
	x := engine.NewExecutor(singleDeviceResolver{device: device}, actions, locks, bus, engine.NewInProcessWorkflowContext(), 0, engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Logf("Run: %v", err)
	}
	var nodes []typedNode
	for _, n := range result.Nodes {
		node := typedNode{ID: n.NodeID, Skipped: n.Skipped, Changed: n.Changed}
		if n.Err != nil {
			node.Err = n.Err.Error()
		}
		var keys []string
		for k := range n.Stats {
			if strings.HasPrefix(k, "got_") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			node.Stats += fmt.Sprintf("%s=%v; ", k, n.Stats[k])
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// TestTypedStats_MeanTheSameOnBothTiers runs one runbook reading a
// method's int, big int, float, typed slice and nested map stats through
// the Crawl tier's in-process call and through the Runner's real per-task
// child, and requires every node to come out the same: the same tasks run,
// the same changed_when answer, and the same values, with the same Go
// types, reaching the next method's parameters.
func TestTypedStats_MeanTheSameOnBothTiers(t *testing.T) {
	ipc, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatalf("newIPCCollectionExecutor: %v", err)
	}
	crawl := runTypedStats(t, nil)
	walk := runTypedStats(t, ipc.invoke)

	for _, n := range crawl {
		if n.Skipped || n.Err != "" {
			t.Errorf("crawl tier: node %s skipped=%v err=%q, want it run", n.ID, n.Skipped, n.Err)
		}
	}
	if !reflect.DeepEqual(crawl, walk) {
		for i := range max(len(crawl), len(walk)) {
			var c, w typedNode
			if i < len(crawl) {
				c = crawl[i]
			}
			if i < len(walk) {
				w = walk[i]
			}
			if c != w {
				t.Errorf("node %d differs:\ncrawl %+v\nwalk  %+v", i, c, w)
			}
		}
	}
	want := "got_big=integer 9007199254740993; got_count=integer 3; got_ports=list [22 443]; got_text=string ping count 3 big 9007199254740993 ratio 2.5; got_whole=float 4; "
	if len(crawl) == 0 || crawl[len(crawl)-1].Stats != want {
		t.Errorf("the render task's parameters = %+v, want %s", crawl, want)
	}
	if len(crawl) == 0 || !crawl[0].Changed {
		t.Error("changed_when over the stats did not hold on the crawl tier")
	}
}
