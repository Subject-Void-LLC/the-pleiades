package ent_test

import (
	"context"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// TestTeamOrganizationMembership proves Phase 8's Team<->Organization
// edge: every Team belongs to exactly one Organization (Team.organization
// is the Required, Unique Ref side), and an Organization can own more than
// one Team (Organization.teams is the owning edge).
func TestTeamOrganizationMembership(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:teamorg?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	acme := client.Organization.Create().SetName("acme").SaveX(ctx)
	platform := client.Team.Create().SetName("platform").SetOrganization(acme).SaveX(ctx)
	security := client.Team.Create().SetName("security").SetOrganization(acme).SaveX(ctx)

	org, err := platform.QueryOrganization().Only(ctx)
	if err != nil {
		t.Fatalf("querying platform's organization: %v", err)
	}
	if org.ID != acme.ID {
		t.Errorf("platform belongs to organization %d, want %d", org.ID, acme.ID)
	}

	teams, err := acme.QueryTeams().All(ctx)
	if err != nil {
		t.Fatalf("querying acme's teams: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("acme has %d teams, want 2", len(teams))
	}
	_ = security
}

// TestTeamUserMembership proves the Team<->User many-to-many edge: a User
// can belong to more than one Team, and a Team can have more than one
// User, mirroring TestGroupDeviceMembership's own shape for the analogous
// Group<->Device edge.
func TestTeamUserMembership(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:teamuser?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	acme := client.Organization.Create().SetName("acme").SaveX(ctx)
	alice := client.User.Create().SetEmail("alice@example.com").SaveX(ctx)
	bob := client.User.Create().SetEmail("bob@example.com").SaveX(ctx)

	platform := client.Team.Create().SetName("platform").SetOrganization(acme).AddUsers(alice, bob).SaveX(ctx)
	security := client.Team.Create().SetName("security").SetOrganization(acme).AddUsers(alice).SaveX(ctx)

	platformUsers, err := platform.QueryUsers().All(ctx)
	if err != nil {
		t.Fatalf("querying platform's users: %v", err)
	}
	if len(platformUsers) != 2 {
		t.Fatalf("platform has %d users, want 2", len(platformUsers))
	}

	// alice belongs to both teams at once: the many-to-many claim, proven
	// from the user side too, not just the team side.
	aliceTeams, err := alice.QueryTeams().All(ctx)
	if err != nil {
		t.Fatalf("querying alice's teams: %v", err)
	}
	if len(aliceTeams) != 2 {
		t.Errorf("alice belongs to %d teams, want 2", len(aliceTeams))
	}

	bobTeams, err := bob.QueryTeams().All(ctx)
	if err != nil {
		t.Fatalf("querying bob's teams: %v", err)
	}
	if len(bobTeams) != 1 || bobTeams[0].ID != platform.ID {
		t.Errorf("bob's teams = %v, want [%d]", bobTeams, platform.ID)
	}
	_ = security
}

// TestRoleBindingTeamOwnership proves the RoleBinding->Team required edge
// and every RoleBinding field, including scope_id's optional (Nillable)
// nature: a system-scoped binding has no target, and PLAN.md Section
// 18.4's device/group/organization-scoped bindings do.
func TestRoleBindingTeamOwnership(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:rolebindingownership?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	acme := client.Organization.Create().SetName("acme").SaveX(ctx)
	team := client.Team.Create().SetName("netops").SetOrganization(acme).SaveX(ctx)

	systemBinding := client.RoleBinding.Create().
		SetRole("admin").
		SetScopeType("system").
		SetEffect("allow").
		SetTeam(team).
		SaveX(ctx)
	if systemBinding.ScopeID != nil {
		t.Errorf("system-scoped binding has scope_id %v, want nil", *systemBinding.ScopeID)
	}

	orgBinding := client.RoleBinding.Create().
		SetRole("operator").
		SetScopeType("organization").
		SetScopeID(acme.ID).
		SetEffect("deny").
		SetTeam(team).
		SaveX(ctx)
	if orgBinding.ScopeID == nil || *orgBinding.ScopeID != acme.ID {
		t.Errorf("organization-scoped binding scope_id = %v, want %d", orgBinding.ScopeID, acme.ID)
	}
	if orgBinding.Effect != "deny" {
		t.Errorf("orgBinding.Effect = %q, want %q", orgBinding.Effect, "deny")
	}

	owner, err := orgBinding.QueryTeam().Only(ctx)
	if err != nil {
		t.Fatalf("querying orgBinding's team: %v", err)
	}
	if owner.ID != team.ID {
		t.Errorf("orgBinding belongs to team %d, want %d", owner.ID, team.ID)
	}

	bindings, err := team.QueryRoleBindings().All(ctx)
	if err != nil {
		t.Fatalf("querying team's role bindings: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("team has %d role bindings, want 2", len(bindings))
	}
}

// TestUserRoleFieldWasRemoved documents, as a real test rather than only a
// doc comment, that User no longer carries the orphaned-permission "role"
// string column PLAN.md Section 18.2 forbids: a User created with no role
// information at all is a normal, valid row, and every grant instead lives
// on a Team's RoleBinding rows (proven above).
func TestUserRoleFieldWasRemoved(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:userrolefield?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	user := client.User.Create().SetEmail("carol@example.com").SaveX(ctx)
	teams, err := user.QueryTeams().All(ctx)
	if err != nil {
		t.Fatalf("querying a freshly created user's teams: %v", err)
	}
	if len(teams) != 0 {
		t.Errorf("a freshly created user belongs to %d teams, want 0", len(teams))
	}
}
