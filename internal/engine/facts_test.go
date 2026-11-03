// Tests that a run stores what its tasks emitted as facts (facts.go).
package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"

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
	by   []facts.Provenance
	fail error
}

func (s *factSink) Record(_ context.Context, device string, obs facts.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.got[device] = append(s.got[device], obs.Entries...)
	s.by = append(s.by, obs.By)
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
		if params["note"] == true {
			// An ordinary name and no secret's shape: only a secret_mask
			// naming it can say it is secret.
			if err := rc.EmitFact("login_note", "Lattice7Harbor"); err != nil {
				return collection.Result{}, err
			}
		}
		if params["more"] == true {
			for key, value := range map[string]any{"uptime": 42, "undeclared": "x", "cores": "four"} {
				if err := rc.EmitFact(key, value); err != nil {
					return collection.Result{}, err
				}
			}
		}
		if params["fail"] == true {
			return collection.Result{}, errors.New("refused")
		}
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "enginefacts.gather", Invoke: gather, Check: gather,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, SupportsCheck: true, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture"},
			Facts: []collection.FactSpec{
				{Key: "ansible_kernel", Kind: collection.FactStable, Type: collection.FactString, Description: "the kernel"},
				{Key: "uptime", Kind: collection.FactVolatile, Type: collection.FactInteger, Description: "the uptime"},
				{Key: "cores", Kind: collection.FactStable, Type: collection.FactInteger, Description: "the cores"},
				// Declared, so the secret tests below refuse them for what
				// they hold rather than for being undeclared.
				{Key: "token", Kind: collection.FactStable, Type: collection.FactString, Description: "a token"},
				{Key: "banner", Kind: collection.FactStable, Type: collection.FactString, Description: "a banner"},
				{Key: "db_password", Kind: collection.FactStable, Type: collection.FactString, Description: "a password"},
				{Key: "login_note", Kind: collection.FactStable, Type: collection.FactString, Description: "a note a later task marks secret"},
			}}}); err != nil {
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
	if got[0].Kind != facts.KindStable {
		t.Errorf("kind = %q, want stable as declared", got[0].Kind)
	}
	// The engine names the run, the node and the method; the order is the
	// recorder's to add.
	if len(sink.by) != 1 || sink.by[0].Run == "" || sink.by[0].Node == "" || sink.by[0].Method != "enginefacts.gather" {
		t.Errorf("provenance = %+v, want the run, node and method", sink.by)
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

// TestFacts_OnlyWhatTheMethodDeclaresIsStoredAsDeclared: a key the method
// does not declare, and a value of another type than declared, are not
// stored; a volatile key is stored as volatile.
func TestFacts_OnlyWhatTheMethodDeclaresIsStoredAsDeclared(t *testing.T) {
	registerFactMethods(t)
	sink := &factSink{got: map[string][]facts.Entry{}}
	_, err := newCheckExecutor(factFleet(), engine.WithFactRecorder(sink)).Run(context.Background(),
		buildDAG(t, `{"id": "facts", "tasks": [{"name": "g", "fqcn": "enginefacts.gather", "params": {"target": "fleet", "more": true}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]facts.Kind{}
	for _, e := range sink.got["dev-1"] {
		kinds[e.Key] = e.Kind
	}
	want := map[string]facts.Kind{"ansible_kernel": facts.KindStable, "uptime": facts.KindVolatile}
	if len(kinds) != len(want) || kinds["ansible_kernel"] != want["ansible_kernel"] || kinds["uptime"] != want["uptime"] {
		t.Errorf("stored %v, want %v: undeclared and cores (a string declared integer) left out", kinds, want)
	}
}

// TestFacts_AFieldALaterSecretMaskMarksIsNeverStored: a secret_mask runs on
// a later task, after the facts of the task whose register it names were
// stored, so the run's secrets cannot catch the value then; the runbook says
// ahead that the field is secret, and it is not stored (owner question,
// 2026-11-02).
func TestFacts_AFieldALaterSecretMaskMarksIsNeverStored(t *testing.T) {
	registerFactMethods(t)
	sink := &factSink{got: map[string][]facts.Entry{}}
	result, err := newCheckExecutor(factFleet(), engine.WithFactRecorder(sink)).Run(context.Background(), buildDAG(t, `{"id": "facts", "tasks": [
		{"name": "g", "fqcn": "enginefacts.gather", "register": "g", "params": {"target": "fleet", "note": true}},
		{"name": "mask", "fqcn": "enginefacts.gather", "secret_mask": {"register": "g", "fields": ["login_note"]}, "params": {"target": "fleet"}}
	]}`))
	if err != nil || result.HasErrors() {
		t.Fatalf("run: %v, %+v", err, result)
	}
	stored := map[string]bool{}
	for _, e := range sink.got["dev-1"] {
		stored[e.Key] = true
		if e.Value == "Lattice7Harbor" {
			t.Errorf("the masked value was stored under %s", e.Key)
		}
	}
	if stored["login_note"] || !stored["ansible_kernel"] {
		t.Errorf("stored %v: want the kernel and not the field the later secret_mask marks", stored)
	}
}
