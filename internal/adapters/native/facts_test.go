// Package native: tests for the Walk tier's fact sink (facts.go), over this
// package's own mockBus and failingBus.
package native

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// factsEchoMethod reports one ordinary fact and one whose value is the
// dispatch's own password, under a name nothing would call a secret. It is
// registered at init so the per-task child process, which re-runs this test
// binary, has it too (ipc_parent_test.go's nativeIPCEchoMethod says why).
const factsEchoMethod = "nativefactstest.echo_login"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: factsEchoMethod,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"},
			Facts: []collection.FactSpec{
				{Key: "test.plain_note", Kind: collection.FactStable, Type: collection.FactString, Description: "an ordinary fact"},
				{Key: "test.login_note", Kind: collection.FactStable, Type: collection.FactString, Description: "a fact holding the login's password"},
			}},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			if err := rc.EmitFact("test.plain_note", "ok"); err != nil {
				return collection.Result{}, err
			}
			if err := rc.EmitFact("test.login_note", rc.InjectSecrets()["password"]); err != nil {
				return collection.Result{}, err
			}
			return collection.Result{}, nil
		},
	})
}

func factEntries(t *testing.T) []facts.Entry {
	t.Helper()
	entries, _, err := facts.Prepare(map[string]any{"os.kernel.release": "6.1"}, facts.Guard{})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestFactsPublisher_PublishesTheDispatchsBatchOnItsJobsSubject: the batch
// names the dispatch's job, device and attempt, rides the job's own facts
// subject, and a retried publish reproduces its key while another
// observation does not.
func TestFactsPublisher_PublishesTheDispatchsBatchOnItsJobsSubject(t *testing.T) {
	bus := &mockBus{}
	ctx := lock.WithFence(journal.WithAttempt(context.Background(), 3), lock.NewFence(7, "epoch-a"))
	p := newFactsPublisher(ctx, bus, "job-1", "device-1", "runner-1")
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	for range 2 {
		if err := p.Record(context.Background(), "device-1", facts.Observation{Entries: factEntries(t), At: at}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Record(context.Background(), "device-1", facts.Observation{Entries: factEntries(t), At: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if len(bus.published) != 3 || bus.published[0].topic != topology.FactsSubject("job-1") {
		t.Fatalf("published %+v", bus.published)
	}
	var batch facts.Batch
	if err := json.Unmarshal(bus.published[0].evt.Data, &batch); err != nil {
		t.Fatal(err)
	}
	if batch.JobID != "job-1" || batch.DeviceID != "device-1" || batch.Attempt != 3 || len(batch.Entries) != 1 || !batch.ObservedAt.Equal(at) {
		t.Errorf("batch = %+v", batch)
	}
	// Phase 41b: the batch says which job, attempt and Runner observed it,
	// and the run's order is the device lease's fencing token.
	want := facts.Provenance{Job: "job-1", Attempt: 3, Runner: "runner-1", Order: facts.Order{Source: "lease:epoch-a", Token: 7}}
	if batch.By != want {
		t.Errorf("provenance = %+v, want %+v", batch.By, want)
	}
	if bus.published[0].evt.ID != bus.published[1].evt.ID || bus.published[1].evt.ID == bus.published[2].evt.ID {
		t.Errorf("keys %q, %q, %q: want a retry to repeat and a new observation to differ",
			bus.published[0].evt.ID, bus.published[1].evt.ID, bus.published[2].evt.ID)
	}
}

func TestFactsPublisher_RefusesWhatItCannotPublish(t *testing.T) {
	bus := &mockBus{}
	p := newFactsPublisher(context.Background(), bus, "job-1", "device-1", "runner-1")
	if err := p.Record(context.Background(), "device-1", facts.Observation{Entries: nil, At: time.Now()}); err != nil || len(bus.published) != 0 {
		t.Errorf("no facts: %v, published %d", err, len(bus.published))
	}
	if err := p.Record(context.Background(), "device-2", facts.Observation{Entries: factEntries(t), At: time.Now()}); err == nil || len(bus.published) != 0 {
		t.Errorf("another device's facts: %v, published %d", err, len(bus.published))
	}
	failing := newFactsPublisher(context.Background(), failingBus{}, "job-1", "device-1", "runner-1")
	if err := failing.Record(context.Background(), "device-1", facts.Observation{Entries: factEntries(t), At: time.Now()}); err == nil {
		t.Error("a failed publish was not reported")
	}
}

// TestDispatchSecrets_HoldsTheCredentialAndWhatWasInjected: the values the
// run knows from its start, which keep a secret out of its facts, are the
// dispatch's credential and what a bound credential injected.
func TestDispatchSecrets_HoldsTheCredentialAndWhatWasInjected(t *testing.T) {
	got := dispatchSecrets(wire.DispatchPayload{Secrets: map[string]string{"username": "admin", "password": "p@ss"}, Injected: &wire.Injected{Mask: []string{"inj-token"}}})
	held := map[string]bool{}
	for _, s := range got {
		held[s] = true
	}
	if !held["p@ss"] || !held["inj-token"] {
		t.Errorf("dispatchSecrets = %q, want the password and the injected value", got)
	}
}

// TestExecute_PublishesNoFactHoldingTheDispatchsSecret drives the real
// Adapter.Execute, per-task child and all: a task that reports its own
// login password as a fact, under an ordinary name and in no secret's shape,
// has that fact left out of what the Runner publishes, while its other fact
// goes. Only the dispatch's secrets handed to the executor
// (engine.WithKnownSecrets in adapter.go) can catch it, so this fails if that
// wiring is dropped (FAILURE_PATTERNS 829).
func TestExecute_PublishesNoFactHoldingTheDispatchsSecret(t *testing.T) {
	const password = "Lattice7Harbor"
	bus := &mockBus{}
	runbooks := writeRunbook(t, "facts-echo", "# Reports a fact. Read only.\nid: facts-echo\ntasks:\n  - name: echo\n    "+factsEchoMethod+": {}\n")
	adapter, err := NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := wire.DispatchPayload{
		JobID: "11111111-2222-3333-4444-666666666666", RunbookID: "facts-echo",
		DeviceID: "device-1", DeviceName: "router1", DeviceHost: "10.0.0.1",
		Secrets: map[string]string{"username": "admin", "password": password},
	}
	if _, err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	stored := map[string]any{}
	for _, p := range bus.published {
		if p.topic != topology.FactsSubject(payload.JobID) {
			continue
		}
		var batch facts.Batch
		if err := json.Unmarshal(p.evt.Data, &batch); err != nil {
			t.Fatal(err)
		}
		for _, e := range batch.Entries {
			stored[e.Key] = e.Value
		}
		if strings.Contains(string(p.evt.Data), password) {
			t.Errorf("a published facts batch holds the dispatch's password: %s", p.evt.Data)
		}
	}
	if stored["test.plain_note"] != "ok" {
		t.Fatalf("the ordinary fact was not published: %v", stored)
	}
	if _, ok := stored["test.login_note"]; ok {
		t.Errorf("the fact holding the dispatch's password was published: %v", stored)
	}
}

// TestFactsPublisher_SplitsALargeObservationIntoBrokerSizedBatches: three
// thousand package facts, as one large host's packages subset gives, go as
// several batches, each under the broker-message bound, together carrying
// every entry once, each with the run's provenance.
func TestFactsPublisher_SplitsALargeObservationIntoBrokerSizedBatches(t *testing.T) {
	observed := map[string]any{}
	for i := range 3000 {
		observed[fmt.Sprintf("package.lib%04d", i)] = map[string]any{"version": "1.2.3-4ubuntu0.1", "arch": "amd64", "source": "apt"}
	}
	entries, _, err := facts.Prepare(observed, facts.Guard{})
	if err != nil {
		t.Fatal(err)
	}
	bus := &mockBus{}
	p := newFactsPublisher(journal.WithAttempt(context.Background(), 1), bus, "job-1", "device-1", "runner-1")
	if err := p.Record(context.Background(), "device-1", facts.Observation{Entries: entries, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if len(bus.published) < 2 {
		t.Fatalf("published %d batch(es), want the observation split", len(bus.published))
	}
	seen := map[string]bool{}
	for _, pub := range bus.published {
		if len(pub.evt.Data) > 1<<20 {
			t.Errorf("a batch is %d bytes, past the broker's 1 MiB", len(pub.evt.Data))
		}
		var batch facts.Batch
		if err := json.Unmarshal(pub.evt.Data, &batch); err != nil {
			t.Fatal(err)
		}
		if batch.By.Runner != "runner-1" || batch.JobID != "job-1" {
			t.Errorf("a batch lost its provenance: %+v", batch.By)
		}
		for _, e := range batch.Entries {
			if seen[e.Key] {
				t.Errorf("%s published twice", e.Key)
			}
			seen[e.Key] = true
		}
	}
	if len(seen) != 3000 {
		t.Errorf("%d entries published, want 3000", len(seen))
	}
}
