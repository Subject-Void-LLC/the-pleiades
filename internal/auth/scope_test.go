package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// fakeRoleBindingRepository is an in-memory RoleBindingRepository, used to
// test ScopeResolver's own fold/combine logic in isolation from ent. The
// real ent-backed path is proven separately, against a real SQLite
// database, by TestEntRoleBindingRepository_ListForTeams_RealSQLiteRoundTrip
// and TestScopeResolver_RealSQLite_DeviceDenyOverridesGroupAllow.
type fakeRoleBindingRepository struct {
	bindings []auth.RoleBinding
	err      error
}

func (f *fakeRoleBindingRepository) ListForTeams(_ context.Context, teamIDs []int) ([]auth.RoleBinding, error) {
	if f.err != nil {
		return nil, f.err
	}
	wanted := make(map[int]bool, len(teamIDs))
	for _, id := range teamIDs {
		wanted[id] = true
	}
	var out []auth.RoleBinding
	for _, b := range f.bindings {
		if wanted[b.TeamID] {
			out = append(out, b)
		}
	}
	return out, nil
}

func intPtr(v int) *int { return &v }

func TestScopeResolver_Resolve(t *testing.T) {
	const (
		orgID    = 10
		groupA   = 20
		groupB   = 21
		deviceID = 30
	)

	tests := []struct {
		name       string
		bindings   []auth.RoleBinding
		teamIDs    []int
		target     auth.ScopeTarget
		wantRole   auth.Role
		wantEffect auth.Effect
	}{
		{
			name:       "no bindings at all fails closed to Deny",
			bindings:   nil,
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{OrganizationID: orgID, DeviceID: deviceID},
			wantRole:   "",
			wantEffect: auth.EffectDeny,
		},
		{
			name:       "empty teamIDs fails closed to Deny",
			bindings:   []auth.RoleBinding{{TeamID: 1, Role: auth.RoleAdmin, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow}},
			teamIDs:    nil,
			target:     auth.ScopeTarget{},
			wantRole:   "",
			wantEffect: auth.EffectDeny,
		},
		{
			name:       "a Team with bindings, but none for the queried team, fails closed to Deny",
			bindings:   []auth.RoleBinding{{TeamID: 99, Role: auth.RoleAdmin, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow}},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{},
			wantRole:   "",
			wantEffect: auth.EffectDeny,
		},
		{
			name: "system-level Allow grants at every target",
			bindings: []auth.RoleBinding{
				{TeamID: 1, Role: auth.RoleAdmin, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow},
			},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{OrganizationID: orgID, GroupIDs: []int{groupA}, DeviceID: deviceID},
			wantRole:   auth.RoleAdmin,
			wantEffect: auth.EffectAllow,
		},
		{
			name: "organization-level Allow, no group or device binding",
			bindings: []auth.RoleBinding{
				{TeamID: 1, Role: auth.RoleViewer, ScopeType: auth.ScopeOrganization, ScopeID: intPtr(orgID), Effect: auth.EffectAllow},
			},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{OrganizationID: orgID},
			wantRole:   auth.RoleViewer,
			wantEffect: auth.EffectAllow,
		},
		{
			name: "organization binding for a different organization does not match",
			bindings: []auth.RoleBinding{
				{TeamID: 1, Role: auth.RoleAdmin, ScopeType: auth.ScopeOrganization, ScopeID: intPtr(orgID + 1), Effect: auth.EffectAllow},
			},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{OrganizationID: orgID},
			wantRole:   "",
			wantEffect: auth.EffectDeny,
		},
		{
			name: "PLAN.md Section 18.4 literal example: device Deny overrides group Allow",
			bindings: []auth.RoleBinding{
				{TeamID: 1, Role: auth.RoleOperator, ScopeType: auth.ScopeGroup, ScopeID: intPtr(groupA), Effect: auth.EffectAllow},
				{TeamID: 1, Role: auth.RoleOperator, ScopeType: auth.ScopeDevice, ScopeID: intPtr(deviceID), Effect: auth.EffectDeny},
			},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{GroupIDs: []int{groupA}, DeviceID: deviceID},
			wantRole:   "",
			wantEffect: auth.EffectDeny,
		},
		{
			name: "deliberate judgment call: a broader Deny is sticky against a narrower, later Allow",
			bindings: []auth.RoleBinding{
				{TeamID: 1, Role: auth.RoleOperator, ScopeType: auth.ScopeGroup, ScopeID: intPtr(groupA), Effect: auth.EffectDeny},
				{TeamID: 1, Role: auth.RoleAdmin, ScopeType: auth.ScopeDevice, ScopeID: intPtr(deviceID), Effect: auth.EffectAllow},
			},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{GroupIDs: []int{groupA}, DeviceID: deviceID},
			wantRole:   "",
			wantEffect: auth.EffectDeny,
		},
		{
			name: "device Allow, no Deny anywhere",
			bindings: []auth.RoleBinding{
				{TeamID: 1, Role: auth.RoleAdmin, ScopeType: auth.ScopeDevice, ScopeID: intPtr(deviceID), Effect: auth.EffectAllow},
			},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{DeviceID: deviceID},
			wantRole:   auth.RoleAdmin,
			wantEffect: auth.EffectAllow,
		},
		{
			name: "device binding for a different device does not match",
			bindings: []auth.RoleBinding{
				{TeamID: 1, Role: auth.RoleAdmin, ScopeType: auth.ScopeDevice, ScopeID: intPtr(deviceID + 1), Effect: auth.EffectAllow},
			},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{DeviceID: deviceID},
			wantRole:   "",
			wantEffect: auth.EffectDeny,
		},
		{
			name: "two Allow bindings at the identical scope: the more privileged Role wins the tie",
			bindings: []auth.RoleBinding{
				{TeamID: 1, Role: auth.RoleViewer, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow},
				{TeamID: 1, Role: auth.RoleOperator, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow},
			},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{},
			wantRole:   auth.RoleOperator,
			wantEffect: auth.EffectAllow,
		},
		{
			name: "a device in two groups: an explicit Deny on either group locks the decision",
			bindings: []auth.RoleBinding{
				{TeamID: 1, Role: auth.RoleOperator, ScopeType: auth.ScopeGroup, ScopeID: intPtr(groupA), Effect: auth.EffectAllow},
				{TeamID: 1, Role: auth.RoleOperator, ScopeType: auth.ScopeGroup, ScopeID: intPtr(groupB), Effect: auth.EffectDeny},
			},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{GroupIDs: []int{groupA, groupB}},
			wantRole:   "",
			wantEffect: auth.EffectDeny,
		},
		{
			name: "bindings for a team not in the query are excluded even when otherwise matching",
			bindings: []auth.RoleBinding{
				{TeamID: 2, Role: auth.RoleAdmin, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow},
			},
			teamIDs:    []int{1},
			target:     auth.ScopeTarget{},
			wantRole:   "",
			wantEffect: auth.EffectDeny,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolver := auth.NewScopeResolver(&fakeRoleBindingRepository{bindings: tc.bindings})
			role, effect, err := resolver.Resolve(context.Background(), tc.teamIDs, tc.target)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if role != tc.wantRole || effect != tc.wantEffect {
				t.Errorf("Resolve() = (%q, %q), want (%q, %q)", role, effect, tc.wantRole, tc.wantEffect)
			}
		})
	}
}

func TestScopeResolver_Resolve_RepositoryErrorFailsClosed(t *testing.T) {
	resolver := auth.NewScopeResolver(&fakeRoleBindingRepository{err: errors.New("boom")})
	role, effect, err := resolver.Resolve(context.Background(), []int{1}, auth.ScopeTarget{})
	if err == nil {
		t.Fatal("expected a repository error to propagate")
	}
	if role != "" || effect != auth.EffectDeny {
		t.Errorf("expected a repository error to still resolve to (\"\", Deny), got (%q, %q)", role, effect)
	}
}
