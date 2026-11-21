// Fixtures for the facts root's tests (facts_when_test.go and its
// siblings): methods that gather, install and probe, a fleet, and a reader
// over a real sealed file store.
package engine_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// factsRead counts how often each fixture method's functions ran, by device.
type factsRead struct {
	probeRuns, probeChecks atomic.Int32
	installChecks          atomic.Int32
}

// registerFactsRead registers tfx.gather (read-only, checks by running,
// emitting tfx.kernel, tfx.mem_mb, tfx.load and, when asked, tfx.codename
// and tfx.note), tfx.install (changes the device, emits a tfx.pkg. family
// fact when it runs and nothing when checked) and tfx.probe (read-only, no
// facts; each run and check counted).
func registerFactsRead(t *testing.T) *factsRead {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	m := &factsRead{}
	gather := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		kernel, _ := params["kernel"].(string)
		if kernel == "" {
			kernel = "6.1"
		}
		emits := map[string]any{"tfx.kernel": kernel, "tfx.mem_mb": 4096, "tfx.load": 0.75}
		if mem, ok := params["mem"].(int); ok {
			emits["tfx.mem_mb"] = mem
		}
		if s, ok := params["codename"].(string); ok {
			emits["tfx.codename"] = s
		}
		// Whatever type the runbook gave, so a value of the wrong type can
		// be emitted.
		if s, ok := params["note"]; ok {
			emits["tfx.note"] = s
		}
		// services adds a family's item; only narrows what is emitted to
		// the keys it lists, as a gather's filter does.
		if params["services"] == true {
			emits["tfx.svc.sshd"] = map[string]any{"state": "running"}
		}
		if only, ok := params["only"].([]any); ok {
			kept := map[string]any{}
			for _, k := range only {
				if key, ok := k.(string); ok {
					if v, ok := emits[key]; ok {
						kept[key] = v
					}
				}
			}
			emits = kept
		}
		for k, v := range emits {
			if err := rc.EmitFact(k, v); err != nil {
				return collection.Result{}, err
			}
		}
		return collection.Result{}, nil
	}
	install := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		name, _ := params["name"].(string)
		return collection.Result{Changed: true}, rc.EmitFact("tfx.pkg."+name, map[string]any{"version": "1.2"})
	}
	probe := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		m.probeRuns.Add(1)
		return collection.Result{}, nil
	}
	readOnly := collection.Reversibility{ReadOnly: true, Notes: "a test fixture that only reads"}
	for _, d := range []collection.Descriptor{
		{Name: "tfx.gather", Invoke: gather, Check: gather, Manifest: collection.Manifest{Status: collection.StatusImplemented, SupportsCheck: true, Reversibility: readOnly,
			Facts: []collection.FactSpec{
				{Key: "tfx.kernel", Kind: collection.FactStable, Type: collection.FactString, Description: "the kernel"},
				{Key: "tfx.mem_mb", Kind: collection.FactStable, Type: collection.FactInteger, Description: "the memory"},
				{Key: "tfx.load", Kind: collection.FactVolatile, Type: collection.FactNumber, Description: "the load"},
				{Key: "tfx.codename", Kind: collection.FactStable, Type: collection.FactString, Description: "the codename"},
				{Key: "tfx.note", Kind: collection.FactStable, Type: collection.FactString, Description: "a note"},
				{Key: "tfx.svc.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "a service"},
			}}},
		{Name: "tfx.install", Invoke: install,
			Check: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
				// Already installed: nothing would change, and the check says so.
				m.installChecks.Add(1)
				return collection.Result{}, nil
			},
			Manifest: collection.Manifest{Status: collection.StatusImplemented, SupportsCheck: true, Reversibility: collection.Reversibility{Notes: "a test fixture that installs"},
				Facts: []collection.FactSpec{{Key: "tfx.pkg.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "a package"}}}},
		{Name: "tfx.probe", Invoke: probe,
			Check: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
				m.probeChecks.Add(1)
				return collection.Result{}, nil
			},
			Manifest: collection.Manifest{Status: collection.StatusImplemented, SupportsCheck: true, Reversibility: readOnly}},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

// factsFleet is two active devices under "fleet".
func factsFleet() mapResolver {
	return mapResolver{"fleet": {
		&inventorytest.Stub{StubID: "dev-1", StubName: "web1", StubState: inventory.StateActive},
		&inventorytest.Stub{StubID: "dev-2", StubName: "web2", StubState: inventory.StateActive},
	}, "one": {&inventorytest.Stub{StubID: "dev-1", StubName: "web1", StubState: inventory.StateActive}}}
}

// factsDAG compiles a YAML runbook through the real builder.
func factsDAG(t *testing.T, src string) *engine.DAG {
	t.Helper()
	dag, err := factsDAGErr(src)
	if err != nil {
		t.Fatalf("building the runbook: %v", err)
	}
	return dag
}

// factsDAGErr compiles a YAML runbook, returning the builder's error.
func factsDAGErr(src string) (*engine.DAG, error) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		return nil, err
	}
	return engine.NewBuilder(eval).BuildFromYAML([]byte(src))
}

// storeReader is a FactReader over a real sealed file store, as the CLI's
// is (cmd/pleiades/facts_reader.go): one Lookup, then engine.AnswerFacts.
type storeReader struct{ store *facts.FileStore }

func (s storeReader) ReadFacts(ctx context.Context, device string, keys []string) (map[string]engine.FactAnswer, error) {
	l, err := s.store.Lookup(ctx, device, keys)
	if err != nil {
		return nil, err
	}
	return engine.AnswerFacts(keys, l.Current, time.Now()), nil
}

// sealedFactStore is a real file store in a test directory, sealed under a key
// of its own.
func sealedFactStore(t *testing.T) *facts.FileStore {
	t.Helper()
	keys, err := facts.NewKeys([]byte("a test secret of thirty-two bytes!"), "test")
	if err != nil {
		t.Fatal(err)
	}
	store, err := facts.NewFileStore(t.TempDir(), keys)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// resultsOf is every result of nodeID, by device.
func resultsOf(result engine.RunResult, nodeID string) map[string]engine.NodeResult {
	out := map[string]engine.NodeResult{}
	for _, n := range result.Nodes {
		if n.NodeID == nodeID {
			out[n.Device] = n
		}
	}
	return out
}

// describe is a result in a line, for a failure message.
func describe(n engine.NodeResult) string {
	return fmt.Sprintf("skipped=%v unchecked=%v err=%v reason=%q", n.Skipped, n.Unchecked, n.Err, n.SkipReason)
}
