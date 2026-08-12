package announce_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers the ent-backed Store against a real in-memory SQLite
// database, per RULE 0. Two of the behaviours here live entirely in the
// query and cannot be observed through a mock: the "mine or nobody's" tenant
// filter, and the liveness predicate that is expressed in SQL specifically so
// a page of fifty live messages is fifty rows rather than fifty survivors of
// however many expired ones sorted first.

// newTestStore opens a fresh in-memory database and returns a store over it.
func newTestStore(t *testing.T) (announce.Store, *ent.Client) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:announce%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	return announce.NewEntStore(client), client
}

// newOrg creates an organization and returns its id.
func newOrg(t *testing.T, client *ent.Client, name string) int {
	t.Helper()
	return client.Organization.Create().SetName(name).SaveX(context.Background()).ID
}

// mustCreate persists an announcement or fails the test.
func mustCreate(t *testing.T, store announce.Store, a announce.Announcement) announce.Announcement {
	t.Helper()
	if a.Title == "" {
		a.Title = "notice"
	}
	if a.Body == "" {
		a.Body = "some detail"
	}
	if a.Author == "" {
		a.Author = "admin@example.com"
	}
	created, err := store.Create(context.Background(), a)
	if err != nil {
		t.Fatalf("creating fixture announcement %q: %v", a.Title, err)
	}
	return created
}

func TestEntStore_CreateRoundTrips(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")

	starts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ends := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)

	created, err := store.Create(ctx, announce.Announcement{
		Title:          "change freeze",
		Body:           "no production dispatches",
		Level:          announce.LevelCritical,
		Author:         "admin@example.com",
		OrganizationID: org,
		StartsAt:       &starts,
		EndsAt:         &ends,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "change freeze" || got.Body != "no production dispatches" {
		t.Errorf("title/body did not survive: %+v", got)
	}
	if got.Level != announce.LevelCritical {
		t.Errorf("level = %q, want critical", got.Level)
	}
	if got.Author != "admin@example.com" {
		t.Errorf("author = %q, want it persisted", got.Author)
	}
	if got.OrganizationID != org {
		t.Errorf("organization = %d, want %d", got.OrganizationID, org)
	}
	if got.StartsAt == nil || !got.StartsAt.Equal(starts) {
		t.Errorf("starts_at = %v, want %v", got.StartsAt, starts)
	}
	if got.EndsAt == nil || !got.EndsAt.Equal(ends) {
		t.Errorf("ends_at = %v, want %v", got.EndsAt, ends)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at was not populated")
	}
}

// TestEntStore_CreateWithNoOrganizationIsSystemWide proves the absent edge
// round trips as the meaningful zero rather than becoming an error or a
// dangling reference.
func TestEntStore_CreateWithNoOrganizationIsSystemWide(t *testing.T) {
	store, _ := newTestStore(t)

	created := mustCreate(t, store, announce.Announcement{Title: "platform maintenance"})
	if !created.SystemWide() {
		t.Errorf("organization = %d, want 0 for a system-wide notice", created.OrganizationID)
	}
}

func TestEntStore_CreateRefusesIncompleteAnnouncements(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		a    announce.Announcement
	}{
		{"no title", announce.Announcement{Body: "b", Author: "a"}},
		{"no body", announce.Announcement{Title: "t", Author: "a"}},
		{"whitespace title", announce.Announcement{Title: "   ", Body: "b", Author: "a"}},
		// Refused rather than defaulted to something like "system". An
		// unattributed instruction to change how production is operated is
		// one nobody can question or follow up on.
		{"no author", announce.Announcement{Title: "t", Body: "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.Create(ctx, tc.a); err == nil {
				t.Fatal("an incomplete announcement was accepted")
			}
		})
	}
}

// TestEntStore_CreateNormalisesAnUnknownLevel proves the store writes a
// value from the closed vocabulary rather than whatever it was handed, so a
// row can never carry a level the badge mapping does not recognise.
func TestEntStore_CreateNormalisesAnUnknownLevel(t *testing.T) {
	store, _ := newTestStore(t)

	created := mustCreate(t, store, announce.Announcement{Level: announce.Level("apocalyptic")})
	if created.Level != announce.LevelInfo {
		t.Errorf("level = %q, want info", created.Level)
	}
}

func TestEntStore_GetReportsAMissingAnnouncement(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.Get(context.Background(), 4242)
	if !errors.Is(err, announce.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestEntStore_ListTenantFilterIsMineOrNobodys is the assertion the tenant
// story rests on. A system-wide message reaches everyone, so filtering by
// organization must not hide it, while another tenant's change freeze must
// not leak that tenant's plans.
func TestEntStore_ListTenantFilterIsMineOrNobodys(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	acme := newOrg(t, client, "acme")
	globex := newOrg(t, client, "globex")

	mustCreate(t, store, announce.Announcement{Title: "platform maintenance"})
	mustCreate(t, store, announce.Announcement{Title: "acme freeze", OrganizationID: acme})
	mustCreate(t, store, announce.Announcement{Title: "globex freeze", OrganizationID: globex})

	got, err := store.List(ctx, announce.Query{OrganizationIDs: []int{acme}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	titles := map[string]bool{}
	for _, a := range got {
		titles[a.Title] = true
	}
	if !titles["platform maintenance"] {
		t.Error("the system-wide notice was hidden by a tenant filter")
	}
	if !titles["acme freeze"] {
		t.Error("the caller's own tenant notice was missing")
	}
	if titles["globex freeze"] {
		t.Error("another tenant's notice leaked")
	}
	if len(got) != 2 {
		t.Errorf("returned %d announcements, want 2", len(got))
	}
}

// TestEntStore_ListWithNoTenantReturnsEverything documents that an empty
// OrganizationIDs is "no restriction", which is the caller's decision rather
// than the store's.
func TestEntStore_ListWithNoTenantReturnsEverything(t *testing.T) {
	store, client := newTestStore(t)
	acme := newOrg(t, client, "acme")

	mustCreate(t, store, announce.Announcement{Title: "system"})
	mustCreate(t, store, announce.Announcement{Title: "tenant", OrganizationID: acme})

	got, err := store.List(context.Background(), announce.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("returned %d announcements, want all 2", len(got))
	}
}

// TestEntStore_ListLiveAtFiltersInTheQuery covers every window shape at once,
// which matters because the predicate is expressed in SQL rather than
// filtered in Go. A Go-side filter would still pass a naive test while paging
// wrongly in production.
func TestEntStore_ListLiveAtFiltersInTheQuery(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	at := func(day int) *time.Time {
		d := time.Date(2026, 6, day, 0, 0, 0, 0, time.UTC)
		return &d
	}
	now := *at(10)

	mustCreate(t, store, announce.Announcement{Title: "always"})
	mustCreate(t, store, announce.Announcement{Title: "current", StartsAt: at(1), EndsAt: at(20)})
	mustCreate(t, store, announce.Announcement{Title: "expired", StartsAt: at(1), EndsAt: at(5)})
	mustCreate(t, store, announce.Announcement{Title: "future", StartsAt: at(15), EndsAt: at(20)})
	mustCreate(t, store, announce.Announcement{Title: "open ended", StartsAt: at(1)})
	mustCreate(t, store, announce.Announcement{Title: "no start", EndsAt: at(20)})

	got, err := store.List(ctx, announce.Query{LiveAt: now})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	live := map[string]bool{}
	for _, a := range got {
		live[a.Title] = true
	}
	for _, want := range []string{"always", "current", "open ended", "no start"} {
		if !live[want] {
			t.Errorf("%q should be live at %v but was not returned", want, now)
		}
	}
	for _, unwanted := range []string{"expired", "future"} {
		if live[unwanted] {
			t.Errorf("%q is not live at %v but was returned", unwanted, now)
		}
	}

	// A zero LiveAt returns everything, which is what an administrator
	// managing announcements needs and what an operator reading the
	// dashboard must not get.
	all, err := store.List(ctx, announce.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 6 {
		t.Errorf("an unfiltered list returned %d announcements, want all 6", len(all))
	}
}

// TestEntStore_ListIsNewestFirst pins the ordering, which is deliberately the
// opposite of every other list in this codebase: an announcement's value
// decays, so the newest change freeze is the one that applies.
func TestEntStore_ListIsNewestFirst(t *testing.T) {
	store, _ := newTestStore(t)

	// created_at defaults to time.Now, so distinct rows need distinguishable
	// instants. Sleeping is the honest way to get them from a default the
	// store owns rather than reaching past the port to set the column.
	first := mustCreate(t, store, announce.Announcement{Title: "older"})
	time.Sleep(10 * time.Millisecond)
	second := mustCreate(t, store, announce.Announcement{Title: "newer"})
	if !second.CreatedAt.After(first.CreatedAt) {
		t.Skip("the clock did not advance between writes, so ordering cannot be asserted")
	}

	got, err := store.List(context.Background(), announce.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Title != "newer" {
		t.Errorf("order = %v, want the newest first", titlesOf(got))
	}
}

func TestEntStore_ListPaging(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	for i := range 5 {
		mustCreate(t, store, announce.Announcement{Title: fmt.Sprintf("notice-%d", i)})
	}

	t.Run("honours a limit", func(t *testing.T) {
		got, err := store.List(ctx, announce.Query{Limit: 2})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("returned %d, want 2", len(got))
		}
	})

	// The ceiling is the store's, never the caller's.
	t.Run("caps an absurd limit", func(t *testing.T) {
		got, err := store.List(ctx, announce.Query{Limit: 100000})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 5 {
			t.Fatalf("returned %d, want all 5 within the cap", len(got))
		}
	})
}

// TestEntStore_UpdateCanClearAWindow is the reason the update path is
// clear-then-set. Without the clear, a nil pointer would be indistinguishable
// from "leave it alone" and an announcement could never be made permanent
// again.
func TestEntStore_UpdateCanClearAWindow(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	starts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ends := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	created := mustCreate(t, store, announce.Announcement{
		Title: "temporary", StartsAt: &starts, EndsAt: &ends,
	})

	created.Title = "permanent"
	created.StartsAt, created.EndsAt = nil, nil
	if err := store.Update(ctx, created); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "permanent" {
		t.Errorf("title = %q, want permanent", got.Title)
	}
	if got.StartsAt != nil || got.EndsAt != nil {
		t.Errorf("window survived being cleared: starts=%v ends=%v", got.StartsAt, got.EndsAt)
	}
	// Immutable by schema, because an attribution somebody can rewrite is an
	// attribution nobody can rely on.
	if got.Author != "admin@example.com" {
		t.Errorf("author = %q, want it unchanged by an update", got.Author)
	}
}

func TestEntStore_UpdateReportsAMissingAnnouncement(t *testing.T) {
	store, _ := newTestStore(t)

	err := store.Update(context.Background(), announce.Announcement{ID: 4242, Title: "ghost", Body: "b"})
	if !errors.Is(err, announce.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestEntStore_Delete(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	created := mustCreate(t, store, announce.Announcement{Title: "temporary"})
	if err := store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, created.ID); !errors.Is(err, announce.ErrNotFound) {
		t.Fatalf("announcement survived its own delete: %v", err)
	}

	if err := store.Delete(ctx, 4242); !errors.Is(err, announce.ErrNotFound) {
		t.Fatalf("deleting a missing announcement: err = %v, want ErrNotFound", err)
	}
}

// titlesOf projects announcements onto their titles so a failure message
// reads as a list of notices rather than a wall of structs.
func titlesOf(items []announce.Announcement) []string {
	out := make([]string, 0, len(items))
	for _, a := range items {
		out = append(out, a.Title)
	}
	return out
}
