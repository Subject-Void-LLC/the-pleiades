// Tests that a run stores what its tasks emitted as facts (facts.go).
package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// factSink keeps what a run handed it, or fails when told to.
type factSink struct {
	mu   sync.Mutex
	got  map[string][]facts.Entry
	fail error
}

func (s *factSink) Record(_ context.Context, device string, entries []facts.Entry, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.got[device] = append(s.got[device], entries...)
	return nil
}

// registerFactMethods registers enginefacts.gather, which emits a fact, a
// stat and, when told, a fact holding the run's secret, and fails when its
// param fail is set.
func registerFactMethods(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	gather := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		if err := rc.EmitFact("ansible_kernel", "6.1"); err != nil {
			return collection.Result{}, err
		}
		if err := rc.SetStat("elapsed_ms", 12); err != nil {
			return collection.Result{}, err
		}
		if params["leak"] == true {
			if err := rc.EmitFact("token", "Bearer s3cr3t-pass"); err != nil {
				return collection.Result{}, err
			}
		}
		if params["shaped"] == true {
			if err := rc.EmitFact("banner", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmU=\n-----END OPENSSH PRIVATE KEY-----"); err != nil {
				return collection.Result{}, err
			}
			if err := rc.EmitFact("db_password", "hunter2"); err != nil {
				return collection.Result{}, err
			}
		}
		if params["fail"] == true {
			return collection.Result{}, errors.New("refused")
		}
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "enginefacts.gather", Invoke: gather, Check: gather,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, SupportsCheck: true, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture"}}}); err != nil {
		t.Fatal(err)
	}
}

func factFleet() mapResolver {
	return mapResolver{"fleet": {&inventorytest.Stub{StubID: "dev-1", StubName: "dev-1", StubState: inventory.StateActive}}}
}

func TestFacts_ARealRunStoresWhatItsTasksEmitted(t *testing.T) {
	registerFactMethods(t)
	sink := &factSink{got: map[string][]facts.Entry{}}
	result, err := newCheckExecutor(factFleet(), engine.WithFactRecorder(sink)).Run(context.Background(),
		buildDAG(t, `{"id": "facts", "tasks": [{"name": "g", "fqcn": "enginefacts.gather", "register": "r", "params": {"target": "fleet"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := sink.got["dev-1"]
	if len(got) != 1 || got[0].Key != "ansible_kernel" || got[0].Value != "6.1" || len(got[0].Hash) != 64 {
		t.Fatalf("stored %+v, want ansible_kernel alone", got)
	}
	for _, n := range result.Nodes {
		if _, leaked := n.Stats[sdk.StatFactKeys]; leaked {
			t.Errorf("the reserved %s reached a result: %v", sdk.StatFactKeys, n.Stats)
		}
		if n.Stats["ansible_kernel"] != "6.1" || n.Stats["elapsed_ms"] != 12 {
			t.Errorf("a fact or stat is missing from the result: %v", n.Stats)
		}
	}
}

func TestFacts_NothingIsStoredFromACheckAFailureOrASecret(t *testing.T) {
	registerFactMethods(t)
	for name, tc := range map[string]struct {
		mode   collection.Mode
		params string
	}{
		"a check":       {collection.ModeCheck, `{"target": "fleet"}`},
		"a failed task": {collection.ModeExecute, `{"target": "fleet", "fail": true}`},
	} {
		t.Run(name, func(t *testing.T) {
			sink := &factSink{got: map[string][]facts.Entry{}}
			_, _ = newCheckExecutor(factFleet(), engine.WithFactRecorder(sink), engine.WithMode(tc.mode)).Run(context.Background(),
				buildDAG(t, `{"id": "facts", "tasks": [{"name": "g", "fqcn": "enginefacts.gather", "params": `+tc.params+`}]}`))
			if len(sink.got) != 0 {
				t.Errorf("stored %v", sink.got)
			}
		})
	}
	t.Run("a check_mode task in a real run", func(t *testing.T) {
		sink := &factSink{got: map[string][]facts.Entry{}}
		_, err := newCheckExecutor(factFleet(), engine.WithFactRecorder(sink)).Run(context.Background(),
			buildDAG(t, `{"id": "facts", "tasks": [{"name": "g", "fqcn": "enginefacts.gather", "check_mode": true, "params": {"target": "fleet"}}]}`))
		if err != nil || len(sink.got) != 0 {
			t.Errorf("stored %v (%v)", sink.got, err)
		}
	})
	t.Run("a secret by its shape or its name, with no secret known", func(t *testing.T) {
		sink := &factSink{got: map[string][]facts.Entry{}}
		_, err := newCheckExecutor(factFleet(), engine.WithFactRecorder(sink)).Run(context.Background(),
			buildDAG(t, `{"id": "facts", "tasks": [{"name": "g", "fqcn": "enginefacts.gather", "params": {"target": "fleet", "shaped": true}}]}`))
		if err != nil {
			t.Fatal(err)
		}
		stored := map[string]bool{}
		for _, e := range sink.got["dev-1"] {
			stored[e.Key] = true
		}
		if stored["banner"] || stored["db_password"] || !stored["ansible_kernel"] {
			t.Errorf("stored %v: want the kernel alone", stored)
		}
	})
	t.Run("a secret", func(t *testing.T) {
		sink := &factSink{got: map[string][]facts.Entry{}}
		_, err := newCheckExecutor(factFleet(), engine.WithFactRecorder(sink), engine.WithKnownSecrets([]string{"s3cr3t-pass"})).Run(
			context.Background(),
			buildDAG(t, `{"id": "facts", "tasks": [{"name": "g", "fqcn": "enginefacts.gather", "params": {"target": "fleet", "leak": true}}]}`))
		if err != nil {
			t.Fatal(err)
		}
		stored := map[string]bool{}
		for _, e := range sink.got["dev-1"] {
			stored[e.Key] = true
		}
		if stored["token"] || !stored["ansible_kernel"] {
			t.Errorf("stored %v: want the kernel and not the fact holding the run's secret", stored)
		}
	})
}

func TestFacts_AStoreThatFailsLeavesTheRunAlone(t *testing.T) {
	registerFactMethods(t)
	sink := &factSink{got: map[string][]facts.Entry{}, fail: errors.New("disk full")}
	result, err := newCheckExecutor(factFleet(), engine.WithFactRecorder(sink)).Run(context.Background(),
		buildDAG(t, `{"id": "facts", "tasks": [{"name": "g", "fqcn": "enginefacts.gather", "params": {"target": "fleet"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range result.Nodes {
		if n.Err != nil {
			t.Errorf("a failed fact store failed the task: %v", n.Err)
		}
	}
}
