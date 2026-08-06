package auth_test

import (
	"context"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// Real SQLite throughout this file (RULE 0): every row below is written
// and read through the actual generated ent client, not a hand-built fake.

func TestEntRoleBindingRepository_ListForTeams_RealSQLiteRoundTrip(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:rolebindingrepo?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	teamA := client.Team.Create().SetName("platform").SetOrganization(org).SaveX(ctx)
	teamB := client.Team.Create().SetName("security").SetOrganization(org).SaveX(ctx)
	empty := client.Team.Create().SetName("no-bindings").SetOrganization(org).SaveX(ctx)

	client.RoleBinding.Create().
		SetRole(string(auth.RoleOperator)).
		SetScopeType(string(auth.ScopeOrganization)).
		SetScopeID(org.ID).
		SetEffect(string(auth.EffectAllow)).
		SetTeam(teamA).
		SaveX(ctx)
	client.RoleBinding.Create().
		SetRole(string(auth.RoleAdmin)).
		SetScopeType(string(auth.ScopeSystem)).
		SetEffect(string(auth.EffectAllow)).
		SetTeam(teamB).
		SaveX(ctx)

	repo := auth.NewEntRoleBindingRepository(client)

	t.Run("one team, one binding", func(t *testing.T) {
		bindings, err := repo.ListForTeams(ctx, []int{teamA.ID})
		if err != nil {
			t.Fatalf("ListForTeams: %v", err)
		}
		if len(bindings) != 1 {
			t.Fatalf("got %d bindings, want 1", len(bindings))
		}
		b := bindings[0]
		if b.TeamID != teamA.ID || b.Role != auth.RoleOperator || b.ScopeType != auth.ScopeOrganization ||
			b.Effect != auth.EffectAllow || b.ScopeID == nil || *b.ScopeID != org.ID {
			t.Errorf("unexpected binding: %+v", b)
		}
	})

	t.Run("both teams", func(t *testing.T) {
		bindings, err := repo.ListForTeams(ctx, []int{teamA.ID, teamB.ID})
		if err != nil {
			t.Fatalf("ListForTeams: %v", err)
		}
		if len(bindings) != 2 {
			t.Fatalf("got %d bindings, want 2", len(bindings))
		}
	})

	t.Run("empty teamIDs returns nothing, not an error", func(t *testing.T) {
		bindings, err := repo.ListForTeams(ctx, nil)
		if err != nil {
			t.Fatalf("ListForTeams: %v", err)
		}
		if len(bindings) != 0 {
			t.Errorf("got %d bindings for an empty team list, want 0", len(bindings))
		}
	})

	t.Run("a real team with zero bindings returns an empty slice, not an error", func(t *testing.T) {
		bindings, err := repo.ListForTeams(ctx, []int{empty.ID})
		if err != nil {
			t.Fatalf("ListForTeams: %v", err)
		}
		if len(bindings) != 0 {
			t.Errorf("got %d bindings, want 0", len(bindings))
		}
	})
}

// TestScopeResolver_RealSQLite_DeviceDenyOverridesGroupAllow is the
// PLAN.md Section 18.4 literal worked example, proven end to end against a
// real SQLite database through the real ent-backed repository, not the
// in-memory fake scope_test.go otherwise uses.
func TestScopeResolver_RealSQLite_DeviceDenyOverridesGroupAllow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:scoperesolverdeny?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	prodGroup := client.Group.Create().SetName("prod").SaveX(ctx)
	coreSwitch := client.Device.Create().SetName("core-switch").SetType("cisco_router").AddGroups(prodGroup).SaveX(ctx)
	team := client.Team.Create().SetName("netops").SetOrganization(org).SaveX(ctx)

	client.RoleBinding.Create().
		SetRole(string(auth.RoleOperator)).
		SetScopeType(string(auth.ScopeGroup)).
		SetScopeID(prodGroup.ID).
		SetEffect(string(auth.EffectAllow)).
		SetTeam(team).
		SaveX(ctx)
	client.RoleBinding.Create().
		SetRole(string(auth.RoleOperator)).
		SetScopeType(string(auth.ScopeDevice)).
		SetScopeID(coreSwitch.ID).
		SetEffect(string(auth.EffectDeny)).
		SetTeam(team).
		SaveX(ctx)

	resolver := auth.NewScopeResolver(auth.NewEntRoleBindingRepository(client))
	role, effect, err := resolver.Resolve(ctx, []int{team.ID}, auth.ScopeTarget{
		OrganizationID: org.ID,
		GroupIDs:       []int{prodGroup.ID},
		DeviceID:       coreSwitch.ID,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if effect != auth.EffectDeny {
		t.Fatalf("expected device-level Deny to override group-level Allow, got role=%q effect=%q", role, effect)
	}
}
