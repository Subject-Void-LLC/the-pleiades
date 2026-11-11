// Tests of the Collection identity on the Controller's facts (Phase 136b):
// what a Runner's batch names is kept when a build could have written it and
// dropped when none could, and a row edited to name another is found.
package entstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// gatherIdentity is a well-formed identity of the facts group, which
// facts.gather belongs to.
var gatherIdentity = wire.CollectionIdentity{Group: "facts", Version: "0.4.0", Manifests: "sha256:" + strings.Repeat("ab", 32)}

// TestSubscriber_KeepsARunnersIdentityAndDropsOneNoBuildWrites: a batch
// naming an identity that passes the frozen rule for a delivered identity
// (wire's Deliverable) is stored with it as sent, whichever group this
// controller's build routes its method to, since a group list grown or a
// method moved between the Runner's build and this one reads differently
// here (LESSONS 415), and that difference is logged at info; one that fails
// it (a digest of the wrong shape, a direction override or a tag character
// in its group) is stored with its facts and no identity, with a warning.
func TestSubscriber_KeepsARunnersIdentityAndDropsOneNoBuildWrites(t *testing.T) {
	ctx := context.Background()
	admitted := func(context.Context, string, string) (journal.Admission, error) { return journal.AdmitDispatched, nil }
	formatChar := gatherIdentity
	formatChar.Group = "fa" + string(rune(0x202e)) + "cts"
	tagged := gatherIdentity
	tagged.Group = "facts" + string(rune(0xE0041))
	for _, c := range []struct {
		name  string
		id    wire.CollectionIdentity
		want  wire.CollectionIdentity
		level string
	}{
		{name: "the method's group", id: gatherIdentity, want: gatherIdentity},
		{name: "another group", id: wire.CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: gatherIdentity.Manifests},
			want: wire.CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: gatherIdentity.Manifests}, level: "INFO"},
		{name: "a program's, for a first-party method", id: wire.CollectionIdentity{Group: "facts", Program: true, Manifests: gatherIdentity.Manifests},
			want: wire.CollectionIdentity{Group: "facts", Program: true, Manifests: gatherIdentity.Manifests}, level: "INFO"},
		{name: "a digest of the wrong shape", id: wire.CollectionIdentity{Group: "facts", Version: "0.4.0", Manifests: "sha256:ABC"}, level: "WARN"},
		{name: "a format character in the group", id: formatChar, level: "WARN"},
		{name: "a tag character in the group", id: tagged, level: "WARN"},
	} {
		t.Run(c.name, func(t *testing.T) {
			client, device := open(t)
			store := entstore.New(client)
			var logged bytes.Buffer
			sub := entstore.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})), admitted)
			batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(),
				Entries: prepared(t, map[string]any{"os.kernel.release": "6.18.33"}),
				By:      facts.Provenance{Run: "run-1", Node: "tasks[0]", Method: "facts.gather", Runner: "r1", Collection: c.id}}
			if err := handle(t, sub, batch); err != nil {
				t.Fatal(err)
			}
			current, err := store.Current(ctx, device)
			if err != nil || len(current) != 1 {
				t.Fatalf("Current = %+v, %v; want the one fact stored", current, err)
			}
			if got := current[0].By.Collection; got != c.want {
				t.Fatalf("stored identity %+v, want %+v", got, c.want)
			}
			if v, err := store.Verify(ctx, device); err != nil || v.Break != nil || v.Values != 1 {
				t.Fatalf("Verify = %+v, %v; want one chained value", v, err)
			}
			said := identityLines(logged.String())
			switch {
			case c.level == "" && len(said) != 0:
				t.Errorf("logged %q about an identity this build routes the same way", said)
			case c.level != "" && (len(said) != 1 || !strings.Contains(said[0], "level="+c.level)):
				t.Errorf("logged %q, want one %s line about the identity", said, c.level)
			case strings.ContainsRune(logged.String(), 0x202e) || strings.ContainsRune(logged.String(), 0xE0041):
				t.Errorf("a log line holds the group's format character raw:\n%s", logged.String())
			}
		})
	}
}

// TestSubscriber_KeepsAnIdentityOnlyThisToolchainsTablesRefuse: a program's
// group holding a code point this toolchain has not assigned, which Valid
// refuses here and which is what a letter a newer toolchain assigned looks
// like to this build, passes the frozen rule the Controller holds a
// delivered identity to. The batch is stored with the identity as sent, its chain
// verifies, and one info line says this controller's own Valid would differ,
// repeating none of the group.
func TestSubscriber_KeepsAnIdentityOnlyThisToolchainsTablesRefuse(t *testing.T) {
	ctx := context.Background()
	const marker = "zq9probe"
	unassigned := unassignedHere(t)
	id := wire.CollectionIdentity{Group: marker + string(unassigned), Program: true, Manifests: gatherIdentity.Manifests}
	if id.Valid() == nil || id.Deliverable() != nil {
		t.Fatal("the fixture needs an identity this build's Valid refuses and the frozen rule keeps")
	}
	client, device := open(t)
	store := entstore.New(client)
	var logged bytes.Buffer
	admitted := func(context.Context, string, string) (journal.Admission, error) { return journal.AdmitDispatched, nil }
	sub := entstore.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})), admitted)
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(),
		Entries: prepared(t, map[string]any{"probe.widgets": 3}),
		By:      facts.Provenance{Run: "run-1", Node: "tasks[0]", Method: "probe.gather", Runner: "r1", Collection: id}}
	if err := handle(t, sub, batch); err != nil {
		t.Fatal(err)
	}
	current, err := store.Current(ctx, device)
	if err != nil || len(current) != 1 || current[0].By.Collection != id {
		t.Fatalf("Current = %+v, %v; want the fact stored under the identity as sent", current, err)
	}
	if v, err := store.Verify(ctx, device); err != nil || v.Break != nil || v.Values != 1 {
		t.Fatalf("Verify = %+v, %v; want one chained value", v, err)
	}
	said := identityLines(logged.String())
	if len(said) != 1 || !strings.Contains(said[0], "level=INFO") {
		t.Errorf("logged %q, want one info line about the identity", said)
	}
	if strings.Contains(logged.String(), marker) || strings.ContainsRune(logged.String(), unassigned) {
		t.Errorf("a log line repeats the group:\n%s", logged.String())
	}
}

// identityLines returns the log lines that speak of a Collection identity.
func identityLines(log string) []string {
	var out []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "Collection identity") {
			out = append(out, line)
		}
	}
	return out
}

// TestStore_ARowEditedToAnotherIdentityBreaksTheChain: the chain covers the
// identity as it was stored, so a provenance rewritten in SQL to name
// another digest of the same group, or another group, is found, and the row
// is read back as it stands rather than refused or rewritten.
func TestStore_ARowEditedToAnotherIdentityBreaksTheChain(t *testing.T) {
	for name, edit := range map[string]func(p *facts.Provenance){
		"another digest": func(p *facts.Provenance) { p.Collection.Manifests = "sha256:" + strings.Repeat("cd", 32) },
		"another group":  func(p *facts.Provenance) { p.Collection.Group = "file" },
		"no identity":    func(p *facts.Provenance) { p.Collection = wire.CollectionIdentity{} },
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "facts.db"))
			client := enttest.Open(t, "sqlite3", dsn)
			t.Cleanup(func() { _ = client.Close() })
			dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(ctx)
			store := entstore.New(client)
			at := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
			by := facts.Provenance{Run: "r", Method: "facts.gather", Collection: gatherIdentity}
			for i, kernel := range []string{"6.1", "6.2"} {
				by.Order = facts.Order{Source: facts.ClockSource, Token: uint64(i + 1)}
				if err := store.Record(ctx, dev.DeviceID, facts.Observation{Entries: prepared(t, map[string]any{"os.kernel.release": kernel}), At: at.Add(time.Duration(i) * time.Minute), By: by}); err != nil {
					t.Fatal(err)
				}
			}
			if v, err := store.Verify(ctx, dev.DeviceID); err != nil || v.Break != nil || v.Values != 2 {
				t.Fatalf("before the edit: %+v, %v", v, err)
			}
			first := client.Fact.Query().Order(ent.Asc("id")).FirstX(ctx)
			edited := by
			edited.Order = facts.Order{Source: facts.ClockSource, Token: 1}
			edit(&edited)
			raw, err := json.Marshal(edited)
			if err != nil {
				t.Fatal(err)
			}
			col, err := json.Marshal(map[string]string{"json": string(raw)})
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite3", dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.Exec(`UPDATE facts SET provenance = ? WHERE id = ?`, string(col), first.ID); err != nil {
				t.Fatal(err)
			}
			v, err := store.Verify(ctx, dev.DeviceID)
			if err != nil || v.Break == nil || v.Break.Index != 0 {
				t.Fatalf("after the edit: %+v, %v; want a break at the edited value", v, err)
			}
			history, err := store.History(ctx, dev.DeviceID, "os.kernel.release")
			if err != nil || len(history) != 2 {
				t.Fatalf("History = %+v, %v", history, err)
			}
			if got := history[0].By.Collection; got != edited.Collection {
				t.Fatalf("the edited row reads with identity %+v, want it as it stands, %+v", got, edited.Collection)
			}
		})
	}
}
