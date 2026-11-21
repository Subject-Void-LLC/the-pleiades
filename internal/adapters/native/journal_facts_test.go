// Package native: a test that the Walk tier's run journal never carries a
// fact's own name or value, through the real Adapter.Execute and the
// per-task child, searched in the bytes the Runner publishes.
package native

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// journalFactsMethod emits item facts beside declared stats and one
// undeclared stat named like a fact. It is registered at init so the per-task
// child process, which re-runs this test binary, has it too.
const journalFactsMethod = "nativejournalfactstest.gather"

// nativeJournalFactNames are the item fact names the fixture emits, the names
// an attacker reading the journal stream would guess at.
var nativeJournalFactNames = []string{"package.openssh-server", "user.alice", "file.%2Fsrv%2Fpayroll%2Fledger.db"}

// nativeJournalFactValues are the distinctive texts the fixture's facts and
// its undeclared stat hold.
var nativeJournalFactValues = []string{"Quartz9Meridian", "Velvet3Orchard", "Tundra5Lantern", "Marigold8Spire"}

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: journalFactsMethod,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"},
			Doc: collection.Doc{
				Summary: "A fixture that emits facts beside its stats.",
				Returns: []collection.ReturnField{
					{Name: "package_count", Type: "integer", Description: "A declared result."},
					{Name: "package.<name>", Type: "object", Description: "One fact per package."},
				},
			},
			Facts: []collection.FactSpec{
				{Key: "package.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "a package"},
				{Key: "user.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "a user"},
				{Key: "file.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "a file"},
			}},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			emitted := map[string]any{
				"package.openssh-server":            map[string]any{"version": "Quartz9Meridian"},
				"user.alice":                        map[string]any{"home": "/home/Velvet3Orchard"},
				"file.%2Fsrv%2Fpayroll%2Fledger.db": map[string]any{"owner": "Tundra5Lantern"},
			}
			for key, value := range emitted {
				if err := rc.EmitFact(key, value); err != nil {
					return collection.Result{}, err
				}
			}
			if err := rc.SetStat("package_count", 1); err != nil {
				return collection.Result{}, err
			}
			// Undeclared, and named like an item fact.
			if err := rc.SetStat("package.nginx", "Marigold8Spire"); err != nil {
				return collection.Result{}, err
			}
			return collection.Result{}, nil
		},
	})
}

// TestExecute_TheRunJournalNeverCarriesAFactsNameOrValue drives the real
// Adapter.Execute with a fact seal key, so the facts are really published
// (the control), and searches every message on the job's journal subject as
// bytes: the declared result's name is there, and no item fact's name, no
// fact's value and no undeclared stat's name is.
func TestExecute_TheRunJournalNeverCarriesAFactsNameOrValue(t *testing.T) {
	bus := &mockBus{}
	runbooks := writeRunbook(t, "journal-facts", "# Reports facts. Read only.\nid: journal-facts\ntasks:\n  - name: gather\n    "+journalFactsMethod+": {}\n")
	adapter, err := NewAdapter(bus, runbooks, nil, WithRunnerID("runner-test"))
	if err != nil {
		t.Fatal(err)
	}
	priv, pub := factSealPair(t)
	payload := wire.DispatchPayload{
		JobID: "11111111-2222-3333-4444-777777777777", RunbookID: "journal-facts",
		DeviceID: "device-1", DeviceName: "router1", DeviceHost: "10.0.0.1",
		FactSealKey: pub.String(),
	}
	if _, err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The control: every item fact was published, so the run really
	// observed them.
	published := map[string]bool{}
	var journaled strings.Builder
	for _, p := range bus.published {
		switch p.topic {
		case topology.FactsSubject("runner-test", payload.JobID):
			for _, e := range openPublished(t, priv, p.evt.Data).Entries {
				published[e.Key] = true
			}
		case topology.JournalSubject("runner-test", payload.JobID):
			journaled.Write(p.evt.Data)
			journaled.WriteByte(0)
		}
	}
	for _, name := range nativeJournalFactNames {
		if !published[name] {
			t.Fatalf("the fact %s was not published (published %v), so this run proves nothing about the journal", name, published)
		}
	}
	written := journaled.String()
	if !strings.Contains(written, `"package_count"`) {
		t.Fatalf("the control failed: the declared result's name is not in the published journal:\n%s", written)
	}
	forbidden := append(append(append([]string(nil), nativeJournalFactNames...), nativeJournalFactValues...),
		"package.nginx", "openssh-server", "alice", "payroll", sdk.StatFactKeys)
	for _, text := range forbidden {
		if strings.Contains(written, text) {
			t.Errorf("the published journal holds %q; no fact's name or value, and no undeclared stat's name, may reach it", text)
		}
	}
}
