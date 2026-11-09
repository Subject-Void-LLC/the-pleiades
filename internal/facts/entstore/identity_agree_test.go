// Tests that the Controller's two stores of a Runner's records, the run
// journal's and the facts', keep the same Collection identity for one task
// (Phase 136b): both hold a delivered identity to Valid, which this
// controller writes, and neither to how this controller's build routes the
// method, which is the Runner's build's to say.
package entstore_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func TestStores_KeepTheSameIdentityForOneTask(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	admitted := func(context.Context, string, string) (journal.Admission, error) { return journal.AdmitDispatched, nil }
	routedElsewhere := gatherIdentity
	routedElsewhere.Group = "file"
	formatChar := gatherIdentity
	formatChar.Group = "fa" + string(rune(0x202e)) + "cts"
	for name, c := range map[string]struct {
		sent, want wire.CollectionIdentity
	}{
		"one this build routes the same way": {gatherIdentity, gatherIdentity},
		"one another build routed otherwise": {routedElsewhere, routedElsewhere},
		"one that fails Valid":               {formatChar, wire.CollectionIdentity{}},
	} {
		t.Run(name, func(t *testing.T) {
			client, device := open(t)
			// The task's journal row, as a Runner publishes it.
			journalStore := journal.NewEntStore(client)
			at := time.Now().UTC()
			entry := engine.JournalEntry{JobID: "job-1", DeviceID: device, Attempt: 1, RunID: "run-1", Sequence: 1, NodeID: "tasks[0]",
				FQCN: "facts.gather", Outcome: engine.OutcomeRan, StartedAt: at, FinishedAt: at, Collection: c.sent}
			wrapped, err := event.WrapPayload("k-1", journal.EventType, journal.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, Entries: []engine.JournalEntry{entry}})
			if err != nil {
				t.Fatal(err)
			}
			if err := journal.NewSubscriber(journalStore, quiet).Handle(*wrapped); err != nil {
				t.Fatalf("the journal subscriber: %v", err)
			}
			// The same task's facts.
			factStore := entstore.New(client)
			batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: at,
				Entries: prepared(t, map[string]any{"os.kernel.release": "6.18.33"}),
				By:      facts.Provenance{Run: "run-1", Node: "tasks[0]", Method: "facts.gather", Runner: "r1", Collection: c.sent}}
			if err := handle(t, entstore.NewSubscriber(factStore, quiet, admitted), batch); err != nil {
				t.Fatalf("the facts subscriber: %v", err)
			}

			rows, _, err := journalStore.ForJob(ctx, "job-1", 0)
			if err != nil || len(rows) != 1 {
				t.Fatalf("the journal holds %+v, %v", rows, err)
			}
			current, err := factStore.Current(ctx, device)
			if err != nil || len(current) != 1 {
				t.Fatalf("the fact store holds %+v, %v", current, err)
			}
			if rows[0].Collection != c.want || current[0].By.Collection != c.want {
				t.Errorf("the journal kept %+v and the fact store %+v, want both %+v", rows[0].Collection, current[0].By.Collection, c.want)
			}
		})
	}
}
