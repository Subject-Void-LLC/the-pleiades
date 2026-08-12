package access_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers the access store against a real in-memory SQLite
// database, per RULE 0. Two of the behaviours cannot be observed any other
// way: the uniqueness constraints belong to the schema rather than to the Go
// code above them, and the last-system-grant guard is a count against stored
// rows.

func newTestStore(t *testing.T) (access.Store, *ent.Client) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:access%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	return access.NewEntStore(client), client
}

// seedOrg creates an organization and returns its id.
func seedOrg(t *testing.T, store access.Store, name string) int {
	t.Helper()
	org, err := store.CreateOrganization(context.Background(), access.Organization{Name: name})
	if err != nil {
		t.Fatalf("creating organization %q: %v", name, err)
	}
	return org.ID
}

// seedTeam creates a team in an organization and returns its id.
func seedTeam(t *testing.T, store access.Store, orgID int, name string, userIDs ...int) int {
	t.Helper()
	team, err := store.CreateTeam(context.Background(), access.Team{
		Name: name, OrganizationID: orgID, UserIDs: userIDs,
	})
	if err != nil {
		t.Fatalf("creating team %q: %v", name, err)
	}
	return team.ID
}

// systemGrant creates a system-scope Allow, which most tests need in order
// to have something other than the binding under test keeping the guard
// satisfied.
func systemGrant(t *testing.T, store access.Store, teamID int) access.Binding {
	t.Helper()
	b, err := store.CreateBinding(context.Background(), access.Binding{
		TeamID: teamID, Role: auth.RoleAdmin, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow,
	})
	if err != nil {
		t.Fatalf("creating a system grant: %v", err)
	}
	return b
}

func TestOrganizations_RoundTripAndUniqueness(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	created, err := store.CreateOrganization(ctx, access.Organization{Name: "  acme  "})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	// Trimmed, so " acme" and "acme " cannot become two tenants that look
	// identical in every list they appear in.
	if created.Name != "acme" {
		t.Errorf("name = %q, want it trimmed to \"acme\"", created.Name)
	}
	if created.CreatedAt.IsZero() {
		t.Error("timestamps were not populated")
	}

	if _, err := store.CreateOrganization(ctx, access.Organization{Name: "acme"}); !errors.Is(err, access.ErrExists) {
		t.Errorf("duplicate name: err = %v, want ErrExists", err)
	}
	if _, err := store.CreateOrganization(ctx, access.Organization{Name: "   "}); err == nil {
		t.Error("a blank organization name was accepted")
	}

	got, err := store.GetOrganization(ctx, created.ID)
	if err != nil || got.Name != "acme" {
		t.Fatalf("GetOrganization = %+v, %v", got, err)
	}
	if _, err := store.GetOrganization(ctx, 4242); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("missing organization: err = %v, want ErrNotFound", err)
	}
}

func TestOrganizations_ListPagesAndSearches(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	seedOrg(t, store, "alpha")
	second := seedOrg(t, store, "beta")
	seedOrg(t, store, "gamma")

	all, err := store.ListOrganizations(ctx, access.Query{})
	if err != nil || len(all) != 3 {
		t.Fatalf("ListOrganizations returned %d, %v; want 3", len(all), err)
	}

	paged, err := store.ListOrganizations(ctx, access.Query{After: second})
	if err != nil || len(paged) != 1 || paged[0].Name != "gamma" {
		t.Fatalf("cursor paging returned %+v, %v", paged, err)
	}

	found, err := store.ListOrganizations(ctx, access.Query{Search: "ALPH"})
	if err != nil || len(found) != 1 || found[0].Name != "alpha" {
		t.Fatalf("case-insensitive search returned %+v, %v", found, err)
	}

	// The ceiling is the store's, never the caller's.
	capped, err := store.ListOrganizations(ctx, access.Query{Limit: 100000})
	if err != nil || len(capped) != 3 {
		t.Fatalf("an absurd limit returned %d, %v", len(capped), err)
	}
}

func TestOrganizations_UpdateAndDelete(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	id := seedOrg(t, store, "acme")

	if err := store.UpdateOrganization(ctx, access.Organization{ID: id, Name: "acme-renamed"}); err != nil {
		t.Fatalf("UpdateOrganization: %v", err)
	}
	got, _ := store.GetOrganization(ctx, id)
	if got.Name != "acme-renamed" {
		t.Errorf("name = %q, want the update to have landed", got.Name)
	}

	if err := store.UpdateOrganization(ctx, access.Organization{ID: 4242, Name: "ghost"}); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("updating a missing organization: err = %v, want ErrNotFound", err)
	}
	if err := store.DeleteOrganization(ctx, id); err != nil {
		t.Fatalf("DeleteOrganization: %v", err)
	}
	if err := store.DeleteOrganization(ctx, 4242); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("deleting a missing organization: err = %v, want ErrNotFound", err)
	}
}

// TestDeleteOrganization_LeavesItsDevicesAlone is the same guarantee
// inventory.Set makes about its members: deleting an administrative grouping
// must never delete somebody's hardware.
func TestDeleteOrganization_LeavesItsDevicesAlone(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")

	client.Device.Create().SetName("rtr-01").SetType("linux_server").
		SetOrganizationID(orgID).SaveX(ctx)

	if err := store.DeleteOrganization(ctx, orgID); err != nil {
		t.Fatalf("DeleteOrganization: %v", err)
	}
	if n := client.Device.Query().CountX(ctx); n != 1 {
		t.Errorf("%d devices survive, want 1: deleting an organization deleted hardware", n)
	}
}

func TestTeams_RoundTripAndMembership(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")

	user, err := store.CreateUser(ctx, access.User{Email: "operator@example.com"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	other, err := store.CreateUser(ctx, access.User{Email: "second@example.com"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	team, err := store.CreateTeam(ctx, access.Team{
		Name: "netops", OrganizationID: orgID, UserIDs: []int{user.ID},
	})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team.OrganizationID != orgID {
		t.Errorf("organization = %d, want %d", team.OrganizationID, orgID)
	}
	if len(team.UserIDs) != 1 || team.UserIDs[0] != user.ID {
		t.Errorf("membership = %v, want [%d]", team.UserIDs, user.ID)
	}

	// Replaced wholesale rather than merged, so removing the last member is
	// expressible at all.
	team.UserIDs = []int{other.ID}
	if err := store.UpdateTeam(ctx, team); err != nil {
		t.Fatalf("UpdateTeam: %v", err)
	}
	got, _ := store.GetTeam(ctx, team.ID)
	if len(got.UserIDs) != 1 || got.UserIDs[0] != other.ID {
		t.Errorf("membership = %v, want it replaced with [%d]", got.UserIDs, other.ID)
	}

	empty := got
	empty.UserIDs = nil
	if err := store.UpdateTeam(ctx, empty); err != nil {
		t.Fatalf("clearing membership: %v", err)
	}
	if cleared, _ := store.GetTeam(ctx, team.ID); len(cleared.UserIDs) != 0 {
		t.Errorf("membership = %v, want it emptied", cleared.UserIDs)
	}
}

// TestTeams_MustBelongToAnOrganization refuses the row whose grants would
// resolve against no organization scope at all.
func TestTeams_MustBelongToAnOrganization(t *testing.T) {
	store, _ := newTestStore(t)

	if _, err := store.CreateTeam(context.Background(), access.Team{Name: "orphan"}); err == nil {
		t.Fatal("a team with no organization was accepted")
	}
}

func TestTeams_ListFiltersByOrganization(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	acme := seedOrg(t, store, "acme")
	globex := seedOrg(t, store, "globex")

	seedTeam(t, store, acme, "netops")
	seedTeam(t, store, globex, "secops")

	mine, err := store.ListTeams(ctx, access.TeamQuery{OrganizationIDs: []int{acme}})
	if err != nil || len(mine) != 1 || mine[0].Name != "netops" {
		t.Fatalf("filtered listing returned %+v, %v", mine, err)
	}

	// Empty means no restriction, which is the caller's decision rather than
	// this store's.
	all, err := store.ListTeams(ctx, access.TeamQuery{})
	if err != nil || len(all) != 2 {
		t.Fatalf("unfiltered listing returned %d, %v; want 2", len(all), err)
	}
}

// TestDeleteTeam_RevokesTheGrantsItHeld proves a deletion leaves no rows
// granting access to a principal that no longer exists, which is precisely
// what an orphaned permission is.
func TestDeleteTeam_RevokesTheGrantsItHeld(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	keeper := seedTeam(t, store, orgID, "keeper")
	doomed := seedTeam(t, store, orgID, "doomed")

	// A system grant elsewhere, so the guard is not what refuses this.
	systemGrant(t, store, keeper)
	if _, err := store.CreateBinding(ctx, access.Binding{
		TeamID: doomed, Role: auth.RoleOperator, ScopeType: auth.ScopeOrganization,
		ScopeID: orgID, Effect: auth.EffectAllow,
	}); err != nil {
		t.Fatalf("CreateBinding: %v", err)
	}

	if err := store.DeleteTeam(ctx, doomed); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	if n := client.RoleBinding.Query().CountX(ctx); n != 1 {
		t.Errorf("%d bindings survive, want only the keeper's: a deleted team left grants behind", n)
	}
}

func TestUsers_RoundTripAndNormalisation(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	created, err := store.CreateUser(ctx, access.User{Email: "  Operator@Example.COM  "})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Lowercased because it is the join key against a token's subject. Two
	// rows differing only in case would be two identities for one person,
	// with whichever the token matched deciding what they reach.
	if created.Email != "operator@example.com" {
		t.Errorf("email = %q, want it normalised", created.Email)
	}

	if _, err := store.CreateUser(ctx, access.User{Email: "OPERATOR@EXAMPLE.COM"}); !errors.Is(err, access.ErrExists) {
		t.Errorf("a case variant was accepted as a second identity: err = %v", err)
	}
	for _, bad := range []string{"", "   ", "not-an-address"} {
		if _, err := store.CreateUser(ctx, access.User{Email: bad}); err == nil {
			t.Errorf("CreateUser(%q) was accepted", bad)
		}
	}
}

func TestUsers_ListAndUpdateAndDelete(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	first, _ := store.CreateUser(ctx, access.User{Email: "alpha@example.com"})
	store.CreateUser(ctx, access.User{Email: "beta@example.com"}) //nolint:errcheck // asserted by the listing below

	all, err := store.ListUsers(ctx, access.Query{})
	if err != nil || len(all) != 2 {
		t.Fatalf("ListUsers returned %d, %v; want 2", len(all), err)
	}
	found, err := store.ListUsers(ctx, access.Query{Search: "ALPHA"})
	if err != nil || len(found) != 1 {
		t.Fatalf("search returned %d, %v; want 1", len(found), err)
	}

	first.Email = "renamed@example.com"
	if err := store.UpdateUser(ctx, first); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if got, _ := store.GetUser(ctx, first.ID); got.Email != "renamed@example.com" {
		t.Errorf("email = %q, want the update to have landed", got.Email)
	}

	if err := store.DeleteUser(ctx, first.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := store.GetUser(ctx, first.ID); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("the user survived its own delete: %v", err)
	}
}

// TestBindings_RefuseAnythingUnresolvable is the security assertion of this
// package. Every case here would otherwise store successfully and then
// evaluate to something other than what its author meant, which is the worst
// combination available: the operator reads a row saying what they intended,
// and the resolver reads something else.
func TestBindings_RefuseAnythingUnresolvable(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")

	valid := access.Binding{
		TeamID: teamID, Role: auth.RoleOperator,
		ScopeType: auth.ScopeOrganization, ScopeID: orgID, Effect: auth.EffectAllow,
	}

	for _, tc := range []struct {
		name   string
		mutate func(*access.Binding)
	}{
		{"no team", func(b *access.Binding) { b.TeamID = 0 }},
		{"unknown role", func(b *access.Binding) { b.Role = "superuser" }},
		{"empty role", func(b *access.Binding) { b.Role = "" }},
		{"unknown scope type", func(b *access.Binding) { b.ScopeType = "galaxy" }},
		{"unknown effect", func(b *access.Binding) { b.Effect = "maybe" }},
		// The two halves of the FAILURE_PATTERNS #99 invariant. A zero at a
		// non-system scope is the exact row that made a device-scoped
		// binding answer every organization-level question.
		{"zero id at a real scope", func(b *access.Binding) { b.ScopeID = 0 }},
		{"negative id at a real scope", func(b *access.Binding) { b.ScopeID = -1 }},
		{"system scope carrying an id", func(b *access.Binding) {
			b.ScopeType, b.ScopeID = auth.ScopeSystem, 7
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := valid
			tc.mutate(&b)
			_, err := store.CreateBinding(ctx, b)
			if !errors.Is(err, access.ErrInvalidBinding) {
				t.Fatalf("err = %v, want ErrInvalidBinding", err)
			}
		})
	}
}

// TestBindings_SystemScopeStoresANullTarget proves the row a system grant
// writes is one the resolver reads as "names no target", rather than one
// carrying an accidental zero.
func TestBindings_SystemScopeStoresANullTarget(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")

	systemGrant(t, store, teamID)

	row := client.RoleBinding.Query().FirstX(ctx)
	if row.ScopeID != nil {
		t.Errorf("scope_id = %v, want NULL for a system-scope binding", *row.ScopeID)
	}
}

func TestBindings_RoundTripAndList(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	netops := seedTeam(t, store, orgID, "netops")
	secops := seedTeam(t, store, orgID, "secops")

	system := systemGrant(t, store, netops)
	if !system.SystemWide() {
		t.Error("a system-scope binding does not report itself system-wide")
	}

	scoped, err := store.CreateBinding(ctx, access.Binding{
		TeamID: secops, Role: auth.RoleViewer,
		ScopeType: auth.ScopeOrganization, ScopeID: orgID, Effect: auth.EffectDeny,
	})
	if err != nil {
		t.Fatalf("CreateBinding: %v", err)
	}
	if scoped.ScopeID != orgID || scoped.Effect != auth.EffectDeny {
		t.Errorf("binding did not round trip: %+v", scoped)
	}

	byTeam, err := store.ListBindings(ctx, access.BindingQuery{TeamIDs: []int{secops}})
	if err != nil || len(byTeam) != 1 || byTeam[0].ID != scoped.ID {
		t.Fatalf("team filter returned %+v, %v", byTeam, err)
	}

	// The first question an auditor asks is who holds system scope.
	byScope, err := store.ListBindings(ctx, access.BindingQuery{ScopeTypes: []auth.ScopeType{auth.ScopeSystem}})
	if err != nil || len(byScope) != 1 || byScope[0].ID != system.ID {
		t.Fatalf("scope filter returned %+v, %v", byScope, err)
	}

	if _, err := store.GetBinding(ctx, 4242); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("missing binding: err = %v, want ErrNotFound", err)
	}
}

// TestBindings_LastSystemGrantCannotBeRemoved covers all three ways to reach
// a deployment nobody can administer. Deleting the grant, denying it, and
// re-pointing it are the same outcome, and only one of them looks like a
// deletion.
func TestBindings_LastSystemGrantCannotBeRemoved(t *testing.T) {
	ctx := context.Background()

	t.Run("delete", func(t *testing.T) {
		store, _ := newTestStore(t)
		orgID := seedOrg(t, store, "acme")
		only := systemGrant(t, store, seedTeam(t, store, orgID, "netops"))

		if err := store.DeleteBinding(ctx, only.ID); !errors.Is(err, access.ErrLastSystemBinding) {
			t.Fatalf("err = %v, want ErrLastSystemBinding", err)
		}
		if _, err := store.GetBinding(ctx, only.ID); err != nil {
			t.Errorf("the refused delete removed the binding anyway: %v", err)
		}
	})

	t.Run("edited into a deny", func(t *testing.T) {
		store, _ := newTestStore(t)
		orgID := seedOrg(t, store, "acme")
		only := systemGrant(t, store, seedTeam(t, store, orgID, "netops"))

		only.Effect = auth.EffectDeny
		if err := store.UpdateBinding(ctx, only); !errors.Is(err, access.ErrLastSystemBinding) {
			t.Fatalf("err = %v, want ErrLastSystemBinding", err)
		}
	})

	t.Run("removable once a second exists", func(t *testing.T) {
		store, _ := newTestStore(t)
		orgID := seedOrg(t, store, "acme")
		first := systemGrant(t, store, seedTeam(t, store, orgID, "netops"))
		systemGrant(t, store, seedTeam(t, store, orgID, "secops"))

		if err := store.DeleteBinding(ctx, first.ID); err != nil {
			t.Fatalf("deleting one of two system grants: %v", err)
		}
	})

	// A non-system binding is not load-bearing for administration and must
	// not be caught by the guard.
	t.Run("a scoped binding is freely removable", func(t *testing.T) {
		store, _ := newTestStore(t)
		orgID := seedOrg(t, store, "acme")
		teamID := seedTeam(t, store, orgID, "netops")
		scoped, err := store.CreateBinding(ctx, access.Binding{
			TeamID: teamID, Role: auth.RoleViewer,
			ScopeType: auth.ScopeOrganization, ScopeID: orgID, Effect: auth.EffectAllow,
		})
		if err != nil {
			t.Fatalf("CreateBinding: %v", err)
		}
		if err := store.DeleteBinding(ctx, scoped.ID); err != nil {
			t.Errorf("deleting a scoped binding: %v", err)
		}
	})
}

// TestBindings_UpdateChangesRoleAndEffectOnly pins the deliberate narrowing.
// Re-pointing a grant at a different target is indistinguishable from
// revoking one and issuing another, and an audit trail showing a grant
// quietly changing what it covers is one nobody can reconstruct.
func TestBindings_UpdateChangesRoleAndEffectOnly(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")
	systemGrant(t, store, teamID)

	scoped, err := store.CreateBinding(ctx, access.Binding{
		TeamID: teamID, Role: auth.RoleViewer,
		ScopeType: auth.ScopeOrganization, ScopeID: orgID, Effect: auth.EffectAllow,
	})
	if err != nil {
		t.Fatalf("CreateBinding: %v", err)
	}

	submitted := scoped
	submitted.Role = auth.RoleAdmin
	submitted.Effect = auth.EffectDeny
	submitted.ScopeType = auth.ScopeDevice
	submitted.ScopeID = 999
	submitted.TeamID = 12345

	if err := store.UpdateBinding(ctx, submitted); err != nil {
		t.Fatalf("UpdateBinding: %v", err)
	}

	got, _ := store.GetBinding(ctx, scoped.ID)
	if got.Role != auth.RoleAdmin || got.Effect != auth.EffectDeny {
		t.Errorf("role/effect did not change: %+v", got)
	}
	if got.ScopeType != auth.ScopeOrganization || got.ScopeID != orgID {
		t.Errorf("the scope moved to %s/%d; a grant must not silently change what it covers",
			got.ScopeType, got.ScopeID)
	}
	if got.TeamID != teamID {
		t.Errorf("the grant moved to team %d; it must not silently change who holds it", got.TeamID)
	}

	if err := store.UpdateBinding(ctx, access.Binding{ID: 4242, Role: auth.RoleViewer}); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("updating a missing binding: err = %v, want ErrNotFound", err)
	}
}

// TestUsers_CarryTheirTeamMembership completes the round trip in the
// direction a user's own page reads it.
func TestUsers_CarryTheirTeamMembership(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")

	user, err := store.CreateUser(ctx, access.User{Email: "operator@example.com"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	teamID := seedTeam(t, store, orgID, "netops", user.ID)

	got, err := store.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if len(got.TeamIDs) != 1 || got.TeamIDs[0] != teamID {
		t.Errorf("TeamIDs = %v, want [%d]", got.TeamIDs, teamID)
	}
}

// TestStorageFailuresAreWrappedNotMisreported walks every method against a
// closed database.
//
// The distinction being asserted is the one that matters to an operator: a
// storage outage must not be reported as ErrNotFound. Those two map onto a
// 500 and a 404, and answering "no such organization" while the database is
// unreachable tells somebody the opposite of what is happening.
func TestStorageFailuresAreWrappedNotMisreported(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	if err := client.Close(); err != nil {
		t.Fatalf("closing the client: %v", err)
	}

	calls := map[string]func() error{
		"CreateOrganization": func() error { _, e := store.CreateOrganization(ctx, access.Organization{Name: "x"}); return e },
		"GetOrganization":    func() error { _, e := store.GetOrganization(ctx, 1); return e },
		"ListOrganizations":  func() error { _, e := store.ListOrganizations(ctx, access.Query{}); return e },
		"UpdateOrganization": func() error { return store.UpdateOrganization(ctx, access.Organization{ID: 1, Name: "x"}) },
		"DeleteOrganization": func() error { return store.DeleteOrganization(ctx, 1) },
		"CreateTeam":         func() error { _, e := store.CreateTeam(ctx, access.Team{Name: "x", OrganizationID: 1}); return e },
		"GetTeam":            func() error { _, e := store.GetTeam(ctx, 1); return e },
		"ListTeams":          func() error { _, e := store.ListTeams(ctx, access.TeamQuery{}); return e },
		"UpdateTeam":         func() error { return store.UpdateTeam(ctx, access.Team{ID: 1, Name: "x"}) },
		"DeleteTeam":         func() error { return store.DeleteTeam(ctx, 1) },
		"CreateUser":         func() error { _, e := store.CreateUser(ctx, access.User{Email: "a@b.c"}); return e },
		"GetUser":            func() error { _, e := store.GetUser(ctx, 1); return e },
		"ListUsers":          func() error { _, e := store.ListUsers(ctx, access.Query{}); return e },
		"UpdateUser":         func() error { return store.UpdateUser(ctx, access.User{ID: 1, Email: "a@b.c"}) },
		"DeleteUser":         func() error { return store.DeleteUser(ctx, 1) },
		"CreateBinding": func() error {
			_, e := store.CreateBinding(ctx, access.Binding{
				TeamID: 1, Role: auth.RoleAdmin, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow,
			})
			return e
		},
		"AttestOrganization": func() error { return store.AttestOrganization(ctx, 1, "dana@example.com") },
		"AttestTeam":         func() error { return store.AttestTeam(ctx, 1, "dana@example.com") },
		"CreateContact": func() error {
			_, e := store.CreateContact(ctx, access.Contact{
				Name: "Dana", Role: access.ContactOwner, Email: "dana@example.com", OrganizationID: 1,
			})
			return e
		},
		"GetContact":   func() error { _, e := store.GetContact(ctx, 1); return e },
		"ListContacts": func() error { _, e := store.ListContacts(ctx, access.ContactQuery{}); return e },
		"UpdateContact": func() error {
			return store.UpdateContact(ctx, access.Contact{
				ID: 1, Name: "Dana", Role: access.ContactOwner, Email: "dana@example.com", OrganizationID: 1,
			})
		},
		"DeleteContact": func() error { return store.DeleteContact(ctx, 1) },
		"GetBinding":    func() error { _, e := store.GetBinding(ctx, 1); return e },
		"ListBindings":  func() error { _, e := store.ListBindings(ctx, access.BindingQuery{}); return e },
		"UpdateBinding": func() error { return store.UpdateBinding(ctx, access.Binding{ID: 1, Role: auth.RoleAdmin}) },
		"DeleteBinding": func() error { return store.DeleteBinding(ctx, 1) },
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("a closed database was reported as success")
			}
			if errors.Is(err, access.ErrNotFound) {
				t.Errorf("a storage failure was reported as ErrNotFound, which an API maps to 404: %v", err)
			}
			if errors.Is(err, access.ErrExists) {
				t.Errorf("a storage failure was reported as ErrExists: %v", err)
			}
		})
	}
}

// TestBindingVocabulariesCoverEveryDeclaredValue is the guard the validation
// tables promise in their own doc comments.
//
// They are declared here rather than derived from internal/auth, because auth
// exposes its constants without a set. That is a real risk: a role added
// there would be silently unwritable, and a scope level added there would be
// silently unbindable, with nothing failing to say so. This asserts the sets
// agree, and it is the only mechanism that can.
func TestBindingVocabulariesCoverEveryDeclaredValue(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")

	// Every role internal/auth declares must be writable.
	for _, role := range []auth.Role{auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin} {
		if _, err := store.CreateBinding(ctx, access.Binding{
			TeamID: teamID, Role: role,
			ScopeType: auth.ScopeOrganization, ScopeID: orgID, Effect: auth.EffectAllow,
		}); err != nil {
			t.Errorf("role %q is declared by internal/auth but not writable here: %v", role, err)
		}
	}

	// Every level of the containment hierarchy must be bindable, or a level
	// exists that no operator can grant at.
	for _, scope := range []auth.ScopeType{
		auth.ScopeOrganization, auth.ScopeInventory, auth.ScopeGroup, auth.ScopeDevice,
	} {
		if _, err := store.CreateBinding(ctx, access.Binding{
			TeamID: teamID, Role: auth.RoleViewer,
			ScopeType: scope, ScopeID: 1, Effect: auth.EffectAllow,
		}); err != nil {
			t.Errorf("scope %q is declared by internal/auth but not bindable here: %v", scope, err)
		}
	}
	if _, err := store.CreateBinding(ctx, access.Binding{
		TeamID: teamID, Role: auth.RoleViewer, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow,
	}); err != nil {
		t.Errorf("system scope is not bindable: %v", err)
	}

	// Both effects must be expressible, or Deny becomes unreachable and the
	// resolver's whole precedence rule is decorative.
	for _, effect := range []auth.Effect{auth.EffectAllow, auth.EffectDeny} {
		if _, err := store.CreateBinding(ctx, access.Binding{
			TeamID: teamID, Role: auth.RoleViewer,
			ScopeType: auth.ScopeDevice, ScopeID: 2, Effect: effect,
		}); err != nil {
			t.Errorf("effect %q is not writable: %v", effect, err)
		}
	}
}

// TestDeleteTeam_RefusalLeavesEverythingIntact is the regression test for a
// defect this shipped with, found by an adversarial reviewer.
//
// The first version deleted a team's grants in a bare loop and then the team.
// A team holding the deployment's last system-scope Allow had its other
// grants deleted one at a time, then tripped the guard on that last one, and
// the call returned "refusing to delete the last system-scope grant". An
// operator reads that as "nothing happened". The grants already deleted were
// gone for good, with no record of which ones they had been.
func TestDeleteTeam_RefusalLeavesEverythingIntact(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")

	// Ordered so the scoped grant is deleted first by any loop that walks
	// them in id order, which is what made the original defect reachable.
	scoped, err := store.CreateBinding(ctx, access.Binding{
		TeamID: teamID, Role: auth.RoleOperator,
		ScopeType: auth.ScopeOrganization, ScopeID: orgID, Effect: auth.EffectAllow,
	})
	if err != nil {
		t.Fatalf("CreateBinding: %v", err)
	}
	system := systemGrant(t, store, teamID) // the deployment's only one

	if err := store.DeleteTeam(ctx, teamID); !errors.Is(err, access.ErrLastSystemBinding) {
		t.Fatalf("err = %v, want ErrLastSystemBinding", err)
	}

	// "Refusing" has to mean nothing happened, or the word is a lie.
	if _, err := store.GetTeam(ctx, teamID); err != nil {
		t.Errorf("the refused delete removed the team: %v", err)
	}
	if _, err := store.GetBinding(ctx, scoped.ID); err != nil {
		t.Errorf("the refused delete permanently destroyed a grant it had already reached: %v", err)
	}
	if _, err := store.GetBinding(ctx, system.ID); err != nil {
		t.Errorf("the system grant is gone: %v", err)
	}
}

// TestDeleteTeam_RemovesEveryGrantBeyondOnePage covers the second half of the
// same defect. The original read the team's grants through the paged listing,
// capped at 200, so a team holding more than that had exactly 200 destroyed
// and then failed a foreign key on the team row itself.
func TestDeleteTeam_RemovesEveryGrantBeyondOnePage(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	keeper := seedTeam(t, store, orgID, "keeper")
	doomed := seedTeam(t, store, orgID, "doomed")
	systemGrant(t, store, keeper)

	// One past the page cap, which is the boundary that mattered.
	const grants = 201
	for i := range grants {
		if _, err := store.CreateBinding(ctx, access.Binding{
			TeamID: doomed, Role: auth.RoleViewer,
			ScopeType: auth.ScopeDevice, ScopeID: i + 1, Effect: auth.EffectAllow,
		}); err != nil {
			t.Fatalf("seeding grant %d: %v", i, err)
		}
	}

	if err := store.DeleteTeam(ctx, doomed); err != nil {
		t.Fatalf("DeleteTeam with %d grants: %v", grants, err)
	}
	if n := client.RoleBinding.Query().CountX(ctx); n != 1 {
		t.Errorf("%d grants survive, want only the keeper's: the deletion stopped at a page boundary", n)
	}
}

// TestMapWriteError_SeparatesAReferenceFromADuplicate pins the distinction
// that was missing.
//
// Both arrive as ent constraint errors, and reporting a foreign-key
// violation as "a record with that name already exists" does not merely
// confuse: it describes the opposite operation. A caller needs to know
// something still points at this record.
func TestMapWriteError_SeparatesAReferenceFromADuplicate(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")
	systemGrant(t, store, teamID)

	// A duplicate name is a conflict about identity.
	if _, err := store.CreateOrganization(ctx, access.Organization{Name: "acme"}); !errors.Is(err, access.ErrExists) {
		t.Errorf("duplicate name: err = %v, want ErrExists", err)
	}

	// A row something still points at is a conflict about references.
	// Deleting the team row directly, behind the store's own cascade, is
	// what leaves the grant dangling.
	err := client.Team.DeleteOneID(teamID).Exec(ctx)
	if err == nil {
		t.Skip("this database does not enforce the reference, so there is nothing to classify")
	}
	if mapped := access.MapWriteErrorForTest(err, "team", teamID, "netops"); !errors.Is(mapped, access.ErrInUse) {
		t.Errorf("foreign key violation mapped to %v, want ErrInUse", mapped)
	}
}

// TestMapWriteError_ReadsAnUnrecognisedConstraintAsADuplicate pins the
// fallback the classification's own doc comment promises.
//
// The two drivers word a reference violation differently and a third would
// word it a third way, so an unmatched constraint error has to mean
// something. Reporting a conflict is the safer of the two to be wrong
// about: it says two records collide, where the other reading would invent
// a dependency that may not exist.
func TestMapWriteError_ReadsAnUnrecognisedConstraintAsADuplicate(t *testing.T) {
	mapped := access.MapWriteErrorForTest(&ent.ConstraintError{}, "organization", 7, "acme")
	if !errors.Is(mapped, access.ErrExists) {
		t.Errorf("an unrecognised constraint mapped to %v, want ErrExists", mapped)
	}
	if errors.Is(mapped, access.ErrInUse) {
		t.Error("an unrecognised constraint was read as a reference violation, inventing a dependency")
	}
}

// TestDeleteTeam_MissingTeamIsNotFoundAndNotASilentSuccess covers the
// transaction's own failure path.
//
// Nothing about "delete a team that is not there" is exotic: it is what a
// retried request and a double-clicked button both produce. What matters is
// that the transaction unwinds and the caller is told, because a store that
// answered nil here would let an API report a deletion that never happened.
func TestDeleteTeam_MissingTeamIsNotFoundAndNotASilentSuccess(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	keeper := seedTeam(t, store, orgID, "keeper")
	systemGrant(t, store, keeper)

	if err := store.DeleteTeam(ctx, keeper+1000); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	// The unwind has to leave the rest of the deployment alone, which is the
	// whole reason the deletion runs in a transaction at all.
	if n := client.Team.Query().CountX(ctx); n != 1 {
		t.Errorf("%d teams remain, want 1: the failed deletion reached something", n)
	}
	if n := client.RoleBinding.Query().CountX(ctx); n != 1 {
		t.Errorf("%d grants remain, want 1: the failed deletion reached something", n)
	}
}

// TestDeleteTeam_SurrendersASystemGrantWhenAnotherRemains is the guard's
// allowing half.
//
// The refusal is already covered. This is the case the refusal must not
// swallow: a team holding a system grant is perfectly deletable as long as
// the deployment keeps one, and a guard that refused both would make the
// first admin team permanent.
func TestDeleteTeam_SurrendersASystemGrantWhenAnotherRemains(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	keeper := seedTeam(t, store, orgID, "keeper")
	doomed := seedTeam(t, store, orgID, "doomed")
	systemGrant(t, store, keeper)
	surrendered := systemGrant(t, store, doomed)

	if err := store.DeleteTeam(ctx, doomed); err != nil {
		t.Fatalf("DeleteTeam: %v, want the deletion to be allowed while another system grant stands", err)
	}
	if _, err := store.GetBinding(ctx, surrendered.ID); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("the deleted team's system grant survived: %v", err)
	}
	if n := client.RoleBinding.Query().CountX(ctx); n != 1 {
		t.Errorf("%d grants remain, want only the keeper's", n)
	}
}

// TestRollback_ReportsTheCauseNotTheUnwind pins which of two failures a
// caller is told about.
//
// An already-finished transaction is the stand-in for a rollback failing
// during an outage, because it fails for the same reason a real one would:
// the connection can no longer carry the statement. The cause has to
// survive it. A caller handed "transaction has already been committed"
// would be reading the second symptom of the outage and would have no way
// to reach the first.
func TestRollback_ReportsTheCauseNotTheUnwind(t *testing.T) {
	_, client := newTestStore(t)
	ctx := context.Background()
	cause := errors.New("the write that actually failed")

	live, err := client.Tx(ctx)
	if err != nil {
		t.Fatalf("opening a transaction: %v", err)
	}
	if got := access.RollbackForTest(live, cause); !errors.Is(got, cause) {
		t.Errorf("a clean unwind returned %v, want the cause", got)
	}

	finished, err := client.Tx(ctx)
	if err != nil {
		t.Fatalf("opening a second transaction: %v", err)
	}
	if err := finished.Commit(); err != nil {
		t.Fatalf("committing: %v", err)
	}
	got := access.RollbackForTest(finished, cause)
	if !errors.Is(got, cause) {
		t.Fatalf("a failed unwind returned %v, which loses the cause entirely", got)
	}
	if !strings.Contains(got.Error(), "rollback also failed") {
		t.Errorf("the failed unwind is not mentioned at all: %v", got)
	}
}

// TestListBindings_NarrowsToOneTargetOnlyWhenTheLevelIsUnambiguous is the
// per-object Access section's own query, and the assertion that it cannot
// leak another tenant's grants onto a record's page.
//
// scope_id carries no foreign key and its values are per level, so an
// organization and a device can share the integer 1. A narrowing that
// applied the id without a level would put a device's grants on an
// organization's page.
func TestListBindings_NarrowsToOneTargetOnlyWhenTheLevelIsUnambiguous(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	orgID := seedOrg(t, store, "network")
	teamID := seedTeam(t, store, orgID, "netops")

	// Two grants held by one team: one at this organization, one at a
	// device that happens to share the organization's id.
	for _, binding := range []access.Binding{
		{TeamID: teamID, Role: auth.RoleOperator, ScopeType: auth.ScopeOrganization, ScopeID: orgID, Effect: auth.EffectAllow},
		{TeamID: teamID, Role: auth.RoleAdmin, ScopeType: auth.ScopeDevice, ScopeID: orgID, Effect: auth.EffectAllow},
	} {
		if _, err := store.CreateBinding(ctx, binding); err != nil {
			t.Fatalf("seeding a grant: %v", err)
		}
	}

	narrowed, err := store.ListBindings(ctx, access.BindingQuery{
		ScopeTypes: []auth.ScopeType{auth.ScopeOrganization},
		ScopeID:    orgID,
	})
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(narrowed) != 1 {
		t.Fatalf("narrowing to organization %d returned %d grants, want 1", orgID, len(narrowed))
	}
	if narrowed[0].ScopeType != auth.ScopeOrganization {
		t.Errorf("narrowing to an organization returned a %s grant, so an id is matched across levels",
			narrowed[0].ScopeType)
	}

	// With no level named, the id is ignored rather than applied, so the
	// caller gets everything instead of a silently wrong subset.
	unnarrowed, err := store.ListBindings(ctx, access.BindingQuery{ScopeID: orgID})
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(unnarrowed) != 2 {
		t.Errorf("a scope id with no level returned %d grants, want all 2: an id alone names no record", len(unnarrowed))
	}

	// With more than one level named, likewise: the caller has not said
	// which level's record they mean.
	ambiguous, err := store.ListBindings(ctx, access.BindingQuery{
		ScopeTypes: []auth.ScopeType{auth.ScopeOrganization, auth.ScopeDevice},
		ScopeID:    orgID,
	})
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(ambiguous) != 2 {
		t.Errorf("a scope id with two levels returned %d grants, want all 2", len(ambiguous))
	}
}
