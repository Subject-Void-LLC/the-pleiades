// Tests that a stored fact's value goes nowhere but the condition reading it:
// not a skip reason, a failure, the journal, the run's events or its log,
// whether the value came from the store or from this run's own gather, and
// whether the condition held, did not, or failed to evaluate on the value.
package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
)

// The values the device holds: no hex, so no digest or id could match one.
const (
	storedNeedle  = "Quokka-Lantern-Seven"
	overlayNeedle = "Marmot-Teapot-Nine"
)

// keptJournal keeps every entry a run journals, as JSON.
type keptJournal struct {
	mu  sync.Mutex
	out bytes.Buffer
}

func (j *keptJournal) Record(_ context.Context, entries []engine.JournalEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return json.NewEncoder(&j.out).Encode(entries)
}

// TestFactsRoot_NoFactValueReachesAnyOutput: a stored value and an overlaid
// one, read by a condition that skips, one that runs, and one that fails on
// the value (CEL's own message would quote it), appear in no skip reason, no
// error, no journal entry, no published event and no log line.
func TestFactsRoot_NoFactValueReachesAnyOutput(t *testing.T) {
	registerFactsRead(t)
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	bus := event.NewInProcessBus()
	t.Cleanup(func() { _ = bus.Close() })
	var events bytes.Buffer
	var eventsMu sync.Mutex
	if err := bus.Subscribe(context.Background(), "pleiades.events.>", func(evt event.Event) error {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		events.Write(evt.Data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	journal := &keptJournal{}
	reader := &fakeReader{answers: map[string]map[string]engine.FactAnswer{
		"dev-1": {"tfx.codename": {State: engine.FactPresent, Value: storedNeedle}},
		"dev-2": {"tfx.codename": {State: engine.FactPresent, Value: storedNeedle}},
	}}
	actions := engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), engine.NewDeviceRunbookContext)
	x := engine.NewExecutor(factsFleet(), actions, lock.NewInProcessManager(), bus, engine.NewInProcessWorkflowContext(), 0,
		engine.WithFactReader(reader), engine.WithJournal(journal))
	// Two runs, since a failed condition ends a run at its level: one
	// reading the store, one reading what its own gather overlaid.
	var nodes []engine.NodeResult
	for _, runbook := range []string{`
id: leak-stored
hosts: fleet
tasks:
  - name: skip on the stored value
    when: facts["tfx.codename"] == "nothing"
    tfx.probe: {}
  - name: run on the stored value
    when: facts["tfx.codename"] != "nothing"
    tfx.probe: {}
  - name: fail on the stored value
    when_cel: '{"a": 1}[facts["tfx.codename"]] == 1'
    tfx.probe: {}
`, `
id: leak-overlaid
hosts: fleet
tasks:
  - name: gather
    tfx.gather: {note: ` + overlayNeedle + `}
  - name: skip on the overlaid value
    when: facts["tfx.note"] == "nothing"
    tfx.probe: {}
  - name: fail on the overlaid value
    when_cel: 'vars[facts["tfx.note"]] == 1'
    tfx.probe: {}
`} {
		result, err := x.Run(context.Background(), factsDAG(t, runbook))
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, result.Nodes...)
	}
	var said strings.Builder
	failedOnValue := 0
	for _, n := range nodes {
		said.WriteString(n.SkipReason + "\n")
		if n.Err != nil {
			said.WriteString(n.Err.Error() + "\n")
			if strings.Contains(n.Err.Error(), "CEL's own message is not shown") {
				failedOnValue++
			}
		}
	}
	if failedOnValue != 4 {
		t.Errorf("%d results failed on a fact's value with CEL's message withheld, want the two failing tasks on both devices:\n%s", failedOnValue, said.String())
	}
	eventsMu.Lock()
	published := events.String()
	eventsMu.Unlock()
	for where, text := range map[string]string{"a result": said.String(), "the journal": journal.out.String(), "an event": published, "the log": logs.String()} {
		for _, needle := range []string{storedNeedle, overlayNeedle} {
			if strings.Contains(text, needle) {
				t.Errorf("%s holds the fact value %q:\n%s", where, needle, text)
			}
		}
	}
	if journal.out.Len() == 0 || published == "" {
		t.Fatalf("nothing was journaled (%d bytes) or published (%d bytes): the search found nothing because there was nothing", journal.out.Len(), len(published))
	}
}
