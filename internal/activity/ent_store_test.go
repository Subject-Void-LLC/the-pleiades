package activity_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers the ent-backed Store against a real in-memory SQLite
// database, per RULE 0. Ordering, the descending keyset cursor and the
// narrowing predicates all live in the query itself, so a mock would prove
// nothing about any of them.

// newTestStore opens a fresh in-memory database and returns a store over it.
func newTestStore(t *testing.T) activity.Store {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:activity%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	return activity.NewEntStore(client)
}

// mustRecord appends an entry or fails the test.
func mustRecord(t *testing.T, store activity.Store, e activity.Entry) {
	t.Helper()
	if err := store.Record(context.Background(), e); err != nil {
		t.Fatalf("Record(%+v): %v", e, err)
	}
}

// entry builds a valid entry, so a test naming one field is naming the
// field it is about.
func entry(actor string, action activity.Action, kind string, id int, name string) activity.Entry {
	return activity.Entry{Actor: actor, Action: action, ObjectKind: kind, ObjectID: id, ObjectName: name}
}

func TestRecordThenList_ReadsNewestFirst(t *testing.T) {
	store := newTestStore(t)

	mustRecord(t, store, entry("ada@example.com", activity.ActionCreated, activity.KindOrganization, 1, "Network"))
	mustRecord(t, store, entry("grace@example.com", activity.ActionUpdated, activity.KindTeam, 2, "Edge"))
	mustRecord(t, store, entry("ada@example.com", activity.ActionDeleted, activity.KindUser, 3, "kay@example.com"))

	entries, err := store.List(context.Background(), activity.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("List returned %d entries, want 3", len(entries))
	}

	// Newest first is what a stream means. Reading it oldest first would
	// put the change somebody is investigating on the last page.
	if entries[0].ObjectKind != activity.KindUser {
		t.Errorf("List put %q first, want the most recent entry (%q)", entries[0].ObjectKind, activity.KindUser)
	}
	if entries[2].ObjectKind != activity.KindOrganization {
		t.Errorf("List put %q last, want the oldest entry (%q)", entries[2].ObjectKind, activity.KindOrganization)
	}
	if entries[0].At.IsZero() {
		t.Error("List returned an entry with no time, so the stream cannot say when anything happened")
	}
}

func TestList_PagesDownwardWithoutRepeatingOrSkipping(t *testing.T) {
	store := newTestStore(t)

	const total = 5
	for i := 1; i <= total; i++ {
		mustRecord(t, store, entry("ada@example.com", activity.ActionCreated, activity.KindTeam, i, fmt.Sprintf("team %d", i)))
	}

	seen := map[int]bool{}
	cursor := 0
	for page := 0; page < total; page++ {
		entries, err := store.List(context.Background(), activity.Query{After: cursor, Limit: 2})
		if err != nil {
			t.Fatalf("List(after=%d): %v", cursor, err)
		}
		if len(entries) == 0 {
			break
		}
		for _, e := range entries {
			if seen[e.ID] {
				t.Fatalf("entry %d appeared on two pages", e.ID)
			}
			seen[e.ID] = true
		}
		cursor = entries[len(entries)-1].ID
	}

	if len(seen) != total {
		t.Errorf("paging saw %d of %d entries, so the cursor skips or stalls", len(seen), total)
	}
}

func TestList_NarrowsByActorAndByObject(t *testing.T) {
	store := newTestStore(t)

	mustRecord(t, store, entry("ada@example.com", activity.ActionCreated, activity.KindTeam, 7, "Edge"))
	mustRecord(t, store, entry("grace@example.com", activity.ActionUpdated, activity.KindTeam, 7, "Edge"))
	mustRecord(t, store, entry("grace@example.com", activity.ActionCreated, activity.KindOrganization, 7, "Network"))

	byActor, err := store.List(context.Background(), activity.Query{Actor: "ada@example.com"})
	if err != nil {
		t.Fatalf("List(actor): %v", err)
	}
	if len(byActor) != 1 {
		t.Errorf("List narrowed to one subject returned %d entries, want 1", len(byActor))
	}

	// The organization and the team share primary key 7, which is the whole
	// point of this assertion: an id means nothing without its kind, and a
	// narrowing that applied the id alone would mix two objects' histories.
	byObject, err := store.List(context.Background(), activity.Query{ObjectKind: activity.KindTeam, ObjectID: 7})
	if err != nil {
		t.Fatalf("List(object): %v", err)
	}
	if len(byObject) != 2 {
		t.Fatalf("List narrowed to team 7 returned %d entries, want 2", len(byObject))
	}
	for _, e := range byObject {
		if e.ObjectKind != activity.KindTeam {
			t.Errorf("narrowing to team 7 returned a %s, so an id is being matched across tables", e.ObjectKind)
		}
	}
}

func TestRecord_RefusesAnEntryThatIsNotEvidence(t *testing.T) {
	store := newTestStore(t)

	cases := map[string]activity.Entry{
		"no actor":  entry("", activity.ActionCreated, activity.KindTeam, 1, "Edge"),
		"no action": entry("ada@example.com", activity.Action(""), activity.KindTeam, 1, "Edge"),
		"unknown action": entry("ada@example.com", activity.Action("exfiltrated"),
			activity.KindTeam, 1, "Edge"),
		"no kind":   entry("ada@example.com", activity.ActionCreated, "", 1, "Edge"),
		"no object": entry("ada@example.com", activity.ActionCreated, activity.KindTeam, 0, "Edge"),
	}

	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			if err := store.Record(context.Background(), e); !errors.Is(err, activity.ErrInvalidEntry) {
				t.Errorf("Record(%s) returned %v, want ErrInvalidEntry", name, err)
			}
		})
	}

	entries, err := store.List(context.Background(), activity.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("refused entries left %d rows behind", len(entries))
	}
}

func TestGet_ReadsOneEntryAndReportsAMissingOne(t *testing.T) {
	store := newTestStore(t)
	mustRecord(t, store, entry("ada@example.com", activity.ActionAttested, activity.KindOrganization, 4, "Network"))

	entries, err := store.List(context.Background(), activity.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	got, err := store.Get(context.Background(), entries[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Action != activity.ActionAttested || got.ObjectName != "Network" {
		t.Errorf("Get returned %+v, want the attestation of Network", got)
	}

	if _, err := store.Get(context.Background(), entries[0].ID+1000); !errors.Is(err, activity.ErrNotFound) {
		t.Errorf("Get(missing) returned %v, want ErrNotFound", err)
	}
}

func TestList_CannotBeAskedForTheWholeTable(t *testing.T) {
	store := newTestStore(t)

	// More rows than the ceiling, or the assertion below could not fail:
	// with three rows in the table, every limit returns three.
	const rows = activity.MaxPageSizeForTest + 5
	for i := 1; i <= rows; i++ {
		mustRecord(t, store, entry("ada@example.com", activity.ActionCreated, activity.KindTeam, i, "Edge"))
	}

	// The ceiling is what stops one request allocating a database's worth
	// of rows. Asserted through the public port, which is the only way a
	// caller could reach it.
	entries, err := store.List(context.Background(), activity.Query{Limit: 100000})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != activity.MaxPageSizeForTest {
		t.Errorf("List(limit=100000) returned %d entries over a table of %d, want the %d-row ceiling",
			len(entries), rows, activity.MaxPageSizeForTest)
	}
}

func TestEntry_DescribesItselfInOneSentence(t *testing.T) {
	got := entry("ada@example.com", activity.ActionCreated, activity.KindTeam, 3, "Edge").Describe()
	want := "ada@example.com created team Edge"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}

	// A deleted object whose name was never captured still describes the
	// kind, rather than trailing off after the verb.
	got = entry("ada@example.com", activity.ActionDeleted, activity.KindBinding, 3, "").Describe()
	want = "ada@example.com deleted role binding"
	if got != want {
		t.Errorf("Describe() with no name = %q, want %q", got, want)
	}
}
