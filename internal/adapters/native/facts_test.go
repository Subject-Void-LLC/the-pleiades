// Package native: tests for the Walk tier's fact sink (facts.go), over this
// package's own mockBus and failingBus.
package native

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
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

// factSealPair is a Controller's key pair for one test: the public half a
// dispatch would carry, and the private half to open what was published.
func factSealPair(t testing.TB) (*sealed.PrivateKey, *sealed.PublicKey) {
	t.Helper()
	priv, err := sealed.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public()
	return priv, &pub
}

// openPublished decodes one published batch and opens its sealed entries as
// the Controller does, failing the test when the batch holds any entry in
// plaintext or its sealed entries are not that batch's.
func openPublished(t *testing.T, priv *sealed.PrivateKey, data []byte) facts.Batch {
	t.Helper()
	var batch facts.Batch
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Entries) != 0 || batch.Sealed == "" {
		t.Fatalf("a published batch holds %d plaintext entries and sealed text of %d bytes; want none and some", len(batch.Entries), len(batch.Sealed))
	}
	file, err := sealed.Parse([]byte(batch.Sealed))
	if err != nil || file.Purpose != sealed.PurposeFactBatch {
		t.Fatalf("the sealed entries parse as %+v, %v; want a fact batch", file, err)
	}
	plaintext, err := file.OpenWithKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	batch.Entries, err = facts.OpenSealedEntries(plaintext, batch)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

// TestFactsPublisher_PublishesTheDispatchsBatchOnItsJobsSubject: the batch
// names the dispatch's job, device and attempt, rides the job's own facts
// subject with its entries sealed to the dispatch's key, and holds no fact's
// key or value in plaintext. Its key names the observation by identity, never
// by content: the same observation recorded again (a caller's retry) repeats
// it, though its sealed bytes are fresh, so the broker drops the repeat; an
// observation at another time, by another node or by another run of the same
// node does not.
func TestFactsPublisher_PublishesTheDispatchsBatchOnItsJobsSubject(t *testing.T) {
	bus := &mockBus{}
	ctx := lock.WithFence(journal.WithAttempt(context.Background(), 3), lock.NewFence(7, "epoch-a"))
	priv, pub := factSealPair(t)
	p := newFactsPublisher(ctx, bus, nil, "job-1", "device-1", "runner-1", pub)
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	observation := facts.Observation{Entries: factEntries(t), At: at, By: facts.Provenance{Node: "tasks[0]"}}
	later := observation
	later.At = at.Add(time.Nanosecond)
	otherNode := observation
	otherNode.By.Node = "tasks[1]"
	otherRun := observation
	otherRun.Iteration = ".item[1]"
	for _, obs := range []facts.Observation{observation, observation, later, otherNode, otherRun} {
		if err := p.Record(context.Background(), "device-1", obs); err != nil {
			t.Fatal(err)
		}
	}
	if len(bus.published) != 5 || bus.published[0].topic != topology.FactsSubject("runner-1", "job-1") {
		t.Fatalf("published %+v", bus.published)
	}
	for _, pub := range bus.published {
		for _, plain := range []string{"os.kernel.release", `"6.1"`} {
			if strings.Contains(string(pub.evt.Data), plain) {
				t.Errorf("a published batch holds %s in plaintext", plain)
			}
		}
	}
	batch := openPublished(t, priv, bus.published[0].evt.Data)
	if batch.JobID != "job-1" || batch.DeviceID != "device-1" || batch.Attempt != 3 || len(batch.Entries) != 1 || !batch.ObservedAt.Equal(at) {
		t.Errorf("batch = %+v", batch)
	}
	// Phase 41b: the batch says which job, attempt and Runner observed it,
	// and the run's order is the device lease's fencing token.
	want := facts.Provenance{Job: "job-1", Node: "tasks[0]", Attempt: 3, Runner: "runner-1", Order: facts.Order{Source: "lease:epoch-a", Token: 7}}
	if batch.By != want {
		t.Errorf("provenance = %+v, want %+v", batch.By, want)
	}
	keys := make([]string, len(bus.published))
	for i, published := range bus.published {
		keys[i] = published.evt.ID
	}
	// The control: the retry's sealed bytes differ, so a digest of them
	// would not have repeated.
	if string(bus.published[0].evt.Data) == string(bus.published[1].evt.Data) {
		t.Fatal("the control failed: one observation sealed twice gave the same bytes")
	}
	if keys[0] != keys[1] {
		t.Errorf("the same observation recorded again has keys %q and %q, want the first repeated", keys[0], keys[1])
	}
	for i, name := range map[int]string{2: "a nanosecond later", 3: "by another node", 4: "by another run of the node"} {
		if keys[i] == keys[0] {
			t.Errorf("an observation %s has the first's key %q", name, keys[0])
		}
	}
	for _, key := range keys {
		for _, plain := range []string{"os.kernel.release", "6.1", "tasks["} {
			if strings.Contains(key, plain) {
				t.Errorf("the key %q holds %q", key, plain)
			}
		}
	}
	// Sealed entries moved into another batch are refused when opened.
	moved := batch
	moved.JobID = "job-2"
	file, err := sealed.Parse([]byte(batch.Sealed))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := file.OpenWithKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facts.OpenSealedEntries(plaintext, moved); err == nil {
		t.Error("sealed entries opened as another job's batch")
	}
}

// TestFactsPublisher_PublishesNothingWithoutAKey: a dispatch that carried no
// key to seal to, or one that does not parse, publishes no fact at all rather
// than one in plaintext, and the run goes on.
func TestFactsPublisher_PublishesNothingWithoutAKey(t *testing.T) {
	for name, key := range map[string]*sealed.PublicKey{"no key": factSealKey(""), "a key that does not parse": factSealKey("not a key")} {
		t.Run(name, func(t *testing.T) {
			bus := &mockBus{}
			p := newFactsPublisher(context.Background(), bus, nil, "job-1", "device-1", "runner-1", key)
			for range 2 {
				if err := p.Record(context.Background(), "device-1", facts.Observation{Entries: factEntries(t), At: time.Now()}); err != nil {
					t.Fatalf("Record: %v", err)
				}
			}
			if len(bus.published) != 0 {
				t.Errorf("published %d batch(es) with no key to seal to", len(bus.published))
			}
		})
	}
}

func TestFactsPublisher_RefusesWhatItCannotPublish(t *testing.T) {
	bus := &mockBus{}
	_, pub := factSealPair(t)
	p := newFactsPublisher(context.Background(), bus, nil, "job-1", "device-1", "runner-1", pub)
	if err := p.Record(context.Background(), "device-1", facts.Observation{Entries: nil, At: time.Now()}); err != nil || len(bus.published) != 0 {
		t.Errorf("no facts: %v, published %d", err, len(bus.published))
	}
	if err := p.Record(context.Background(), "device-2", facts.Observation{Entries: factEntries(t), At: time.Now()}); err == nil || len(bus.published) != 0 {
		t.Errorf("another device's facts: %v, published %d", err, len(bus.published))
	}
	failing := newFactsPublisher(context.Background(), failingBus{}, nil, "job-1", "device-1", "runner-1", pub)
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
	adapter, err := NewAdapter(bus, runbooks, nil, WithRunnerID("runner-test"))
	if err != nil {
		t.Fatal(err)
	}
	priv, pub := factSealPair(t)
	payload := wire.DispatchPayload{
		JobID: "11111111-2222-3333-4444-666666666666", RunbookID: "facts-echo",
		DeviceID: "device-1", DeviceName: "router1", DeviceHost: "10.0.0.1",
		Secrets:     map[string]string{"username": "admin", "password": password},
		FactSealKey: pub.String(),
	}
	if _, err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	stored := map[string]any{}
	for _, p := range bus.published {
		if p.topic != topology.FactsSubject("runner-test", payload.JobID) {
			continue
		}
		batch := openPublished(t, priv, p.evt.Data)
		for _, e := range batch.Entries {
			stored[e.Key] = e.Value
			// The entries are sealed, so the password is looked for in what
			// they open to, not only in the published bytes.
			if raw, err := json.Marshal(e); err != nil || strings.Contains(string(raw), password) {
				t.Errorf("a published fact holds the dispatch's password (%v): %s", err, e.Key)
			}
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
	priv, pub := factSealPair(t)
	p := newFactsPublisher(journal.WithAttempt(context.Background(), 1), bus, nil, "job-1", "device-1", "runner-1", pub)
	observation := facts.Observation{Entries: entries, At: time.Now(), By: facts.Provenance{Node: "tasks[0]"}}
	if err := p.Record(context.Background(), "device-1", observation); err != nil {
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
		batch := openPublished(t, priv, pub.evt.Data)
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
	// Each batch of the one observation has a key of its own, and the
	// observation recorded again repeats every one of them, in order.
	first := len(bus.published)
	if err := p.Record(context.Background(), "device-1", observation); err != nil {
		t.Fatal(err)
	}
	if len(bus.published) != 2*first {
		t.Fatalf("the retry published %d batches, the first record %d", len(bus.published)-first, first)
	}
	distinct := map[string]bool{}
	for i := range first {
		key := bus.published[i].evt.ID
		distinct[key] = true
		if again := bus.published[first+i].evt.ID; again != key {
			t.Errorf("batch %d: the retry's key %q is not the first's %q", i, again, key)
		}
	}
	if len(distinct) != first {
		t.Errorf("%d batches of one observation share %d keys, want one each", first, len(distinct))
	}
}

// TestFactsPublisher_ARetriedRecordIsDeduplicatedOnTheStream: an observation
// large enough to go as several batches, recorded twice, publishes keys that
// repeat pairwise and in order, each batch's its own, so the broker drops the
// whole retry; a publisher built again for the same delivery repeats them
// too. The same observation at another iteration of its node, a nanosecond
// later, by another node, or in another attempt shares no key with it at
// all, so none of its batches is dropped as a repeat of the first's.
func TestFactsPublisher_ARetriedRecordIsDeduplicatedOnTheStream(t *testing.T) {
	observed := map[string]any{}
	for i := range 3000 {
		observed[fmt.Sprintf("package.lib%04d", i)] = map[string]any{"version": "1.2.3-4ubuntu0.1", "arch": "amd64", "source": "apt"}
	}
	entries, _, err := facts.Prepare(observed, facts.Guard{})
	if err != nil {
		t.Fatal(err)
	}
	_, pub := factSealPair(t)
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	base := facts.Observation{Entries: entries, At: at, By: facts.Provenance{Node: "tasks[0]"}, Iteration: ".item[0]"}

	// keysOf records each observation through a publisher of the given
	// attempt and returns the keys of the batches each one published.
	keysOf := func(attempt int, observations ...facts.Observation) [][]string {
		t.Helper()
		bus := &mockBus{}
		p := newFactsPublisher(journal.WithAttempt(context.Background(), attempt), bus, nil, "job-1", "device-1", "runner-1", pub)
		var out [][]string
		for _, obs := range observations {
			before := len(bus.published)
			if err := p.Record(context.Background(), "device-1", obs); err != nil {
				t.Fatal(err)
			}
			var keys []string
			for _, published := range bus.published[before:] {
				keys = append(keys, published.evt.ID)
			}
			out = append(out, keys)
		}
		return out
	}

	recorded := keysOf(1, base, base)
	first, retry := recorded[0], recorded[1]
	if len(first) < 2 {
		t.Fatalf("the observation went as %d batch(es); the test needs it split", len(first))
	}
	// Pairwise and in order: batch i of the retry has batch i's key.
	if !slices.Equal(first, retry) {
		t.Errorf("the retry's keys %q are not the first record's %q, pairwise and in order", retry, first)
	}
	if again := keysOf(1, base)[0]; !slices.Equal(first, again) {
		t.Errorf("a publisher built again for the same delivery gave keys %q, want the first record's %q", again, first)
	}
	// Each batch of the observation has a key of its own.
	held := map[string]bool{}
	for _, key := range first {
		held[key] = true
	}
	if len(held) != len(first) {
		t.Errorf("%d batches of one observation share %d keys, want one each: %q", len(first), len(held), first)
	}

	otherIteration := base
	otherIteration.Iteration = ".item[1]"
	later := base
	later.At = at.Add(time.Nanosecond)
	otherNode := base
	otherNode.By.Node = "tasks[1]"
	variants := keysOf(1, otherIteration, later, otherNode)
	variants = append(variants, keysOf(2, base)...)
	for i, name := range []string{"another iteration", "a nanosecond later", "another node", "another attempt"} {
		if len(variants[i]) != len(first) {
			t.Errorf("%s went as %d batches, the first as %d", name, len(variants[i]), len(first))
		}
		for j, key := range variants[i] {
			if held[key] {
				t.Errorf("%s: batch %d has key %q, which the first observation's batches already used", name, j, key)
			}
		}
	}
}
