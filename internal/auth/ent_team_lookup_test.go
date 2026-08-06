package auth_test

import (
	"context"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

func TestEntTeamLookup_TeamIDsForSubject_RealSQLiteRoundTrip(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:teamlookup?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	alice := client.User.Create().SetEmail("alice@example.com").SaveX(ctx)
	bob := client.User.Create().SetEmail("bob@example.com").SaveX(ctx)

	platform := client.Team.Create().SetName("platform").SetOrganization(org).AddUsers(alice).SaveX(ctx)
	security := client.Team.Create().SetName("security").SetOrganization(org).AddUsers(alice, bob).SaveX(ctx)

	lookup := auth.NewEntTeamLookup(client)

	aliceTeams, err := lookup.TeamIDsForSubject(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("TeamIDsForSubject: %v", err)
	}
	if len(aliceTeams) != 2 {
		t.Fatalf("alice belongs to %d teams, want 2", len(aliceTeams))
	}

	bobTeams, err := lookup.TeamIDsForSubject(ctx, "bob@example.com")
	if err != nil {
		t.Fatalf("TeamIDsForSubject: %v", err)
	}
	if len(bobTeams) != 1 || bobTeams[0] != security.ID {
		t.Errorf("bob's teams = %v, want [%d]", bobTeams, security.ID)
	}

	t.Run("unknown subject returns empty, not an error", func(t *testing.T) {
		teams, err := lookup.TeamIDsForSubject(ctx, "nobody@example.com")
		if err != nil {
			t.Fatalf("TeamIDsForSubject: %v", err)
		}
		if len(teams) != 0 {
			t.Errorf("got %d teams for an unknown subject, want 0", len(teams))
		}
	})

	t.Run("empty subject returns empty, not an error", func(t *testing.T) {
		teams, err := lookup.TeamIDsForSubject(ctx, "")
		if err != nil {
			t.Fatalf("TeamIDsForSubject: %v", err)
		}
		if len(teams) != 0 {
			t.Errorf("got %d teams for an empty subject, want 0", len(teams))
		}
	})

	_ = platform
}

// TestAdmission_RealSQLite_FullChain proves the composed AdmissionChain
// (tokenScopeRule + scopeRule) end to end against a real SQLite database:
// a Team's Device-level Deny blocks even a caller whose token carries the
// right permission scope.
func TestAdmission_RealSQLite_FullChain(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:admissionfullchain?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	device := client.Device.Create().SetName("core-switch").SetType("cisco_router").SaveX(ctx)
	netops := client.User.Create().SetEmail("netops@example.com").SaveX(ctx)
	team := client.Team.Create().SetName("netops").SetOrganization(org).AddUsers(netops).SaveX(ctx)

	client.RoleBinding.Create().
		SetRole(string(auth.RoleOperator)).
		SetScopeType(string(auth.ScopeDevice)).
		SetScopeID(device.ID).
		SetEffect(string(auth.EffectDeny)).
		SetTeam(team).
		SaveX(ctx)

	secret := []byte("full-chain-secret-that-is-long-enough-for-hs256")
	eval := newTestEvaluator(t, secret)

	resolver := auth.NewScopeResolver(auth.NewEntRoleBindingRepository(client))
	admission := auth.Admission{
		Chain: auth.AdmissionChain{
			auth.NewTokenScopeRule(eval),
			auth.NewScopeRule(resolver, auth.NewEntTeamLookup(client)),
		},
		Recorder: auth.NewSlogRecorder(nil),
	}

	// The token carries the right permission scope, but the Team's
	// binding for this specific device is an explicit Deny.
	id := &auth.Identity{Subject: "netops@example.com", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeRunbookExecute}}
	req := auth.AdmissionRequest{
		RequiredScope: auth.ScopeRunbookExecute,
		Target:        auth.ScopeTarget{OrganizationID: org.ID, DeviceID: device.ID},
	}

	if err := admission.Evaluate(ctx, id, req); err == nil {
		t.Fatal("expected the device-level Deny to block access despite a matching token scope")
	}
}
