// Tests for the Role-to-Scope mapping and the identity derivation.
//
// This is the one decision Phase 79 had to make rather than assemble, so it
// gets tested for the properties that would be expensive to discover later:
// that the tables cover the vocabulary, that they nest, that no role gets
// the wildcard, and that a caller with no system-scope grant is authorized
// for nothing rather than for everything.
package auth_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

func TestScopesForRole_CoversTheWholeVocabulary(t *testing.T) {
	// The assertion that catches the real mistake: adding a Scope constant
	// and forgetting to place it in any role. That would compile, pass every
	// other test, and present as a capability no local login can ever hold,
	// which looks like a broken feature rather than a missing table entry.
	admin := auth.ScopesForRole(auth.RoleAdmin)

	for _, scope := range auth.AllScopes() {
		if !slices.Contains(admin, scope) {
			t.Errorf("scope %q is in the vocabulary but no role grants it; a local login can never hold it", scope)
		}
	}
}

func TestScopesForRole_RolesNest(t *testing.T) {
	// viewer is a subset of operator is a subset of admin. If they ever stop
	// nesting, an "upgrade" from viewer to operator would silently REMOVE a
	// capability, which is the kind of regression nobody looks for.
	var (
		viewer   = auth.ScopesForRole(auth.RoleViewer)
		operator = auth.ScopesForRole(auth.RoleOperator)
		admin    = auth.ScopesForRole(auth.RoleAdmin)
	)

	for _, scope := range viewer {
		if !slices.Contains(operator, scope) {
			t.Errorf("operator lacks %q, which viewer has", scope)
		}
	}
	for _, scope := range operator {
		if !slices.Contains(admin, scope) {
			t.Errorf("admin lacks %q, which operator has", scope)
		}
	}
	if len(operator) <= len(viewer) || len(admin) <= len(operator) {
		t.Errorf("roles do not strictly widen: viewer=%d operator=%d admin=%d",
			len(viewer), len(operator), len(admin))
	}
}

func TestScopesForRole_NeverGrantsTheWildcard(t *testing.T) {
	// The wildcard is unexported precisely because granting it is a
	// token-issuance decision. The scope list is also PERSISTED on the
	// session row, so a wildcard there would outlive any later narrowing of
	// what admin means.
	for _, role := range []auth.Role{auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin, ""} {
		for _, scope := range auth.ScopesForRole(role) {
			if scope == "*" {
				t.Errorf("role %q was granted the wildcard scope", role)
			}
		}
	}
}

func TestScopesForRole_OnlyAdminMayGrantAccess(t *testing.T) {
	// The privilege-escalation boundary. An operator who can write access
	// bindings can grant themselves admin, which makes the role split
	// decorative.
	if slices.Contains(auth.ScopesForRole(auth.RoleOperator), auth.ScopeAccessWrite) {
		t.Error("operator holds access:write, so an operator can promote themselves to admin")
	}
	if slices.Contains(auth.ScopesForRole(auth.RoleViewer), auth.ScopeAccessWrite) {
		t.Error("viewer holds access:write")
	}
	if !slices.Contains(auth.ScopesForRole(auth.RoleAdmin), auth.ScopeAccessWrite) {
		t.Error("admin does not hold access:write, so nobody can administer this deployment")
	}
}

func TestScopesForRole_ViewerHoldsNoWrite(t *testing.T) {
	writes := []auth.Scope{
		auth.ScopeInventoryWrite,
		auth.ScopeAnnouncementWrite,
		auth.ScopeTemplateWrite,
		auth.ScopeCredentialWrite,
		auth.ScopeAccessWrite,
		auth.ScopeRunbookExecute,
	}
	for _, scope := range writes {
		if slices.Contains(auth.ScopesForRole(auth.RoleViewer), scope) {
			t.Errorf("viewer holds %q, which changes something", scope)
		}
	}
}

func TestScopesForRole_UnknownRoleGrantsNothing(t *testing.T) {
	// Fail closed, and reachable in normal operation: a subject with only
	// group-scoped bindings resolves to no system role at all.
	for _, role := range []auth.Role{"", "superuser", "Admin", "ADMIN"} {
		got := auth.ScopesForRole(role)
		if len(got) != 0 {
			t.Errorf("role %q granted %v, want nothing", role, got)
		}
		if got == nil {
			t.Errorf("role %q returned a nil slice; return an empty one so callers need no nil check", role)
		}
	}
}

// fakeBindings is an in-memory RoleBindingRepository, the same double
// scope_test.go already uses for the resolver's own fold logic.
type fakeBindings struct {
	byTeam map[int][]auth.RoleBinding
	err    error
}

func (f fakeBindings) ListForTeams(_ context.Context, teamIDs []int) ([]auth.RoleBinding, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]auth.RoleBinding, 0)
	for _, id := range teamIDs {
		out = append(out, f.byTeam[id]...)
	}
	return out, nil
}

// fakeTeams is an in-memory TeamLookup.
type fakeTeams struct {
	bySubject map[string][]int
	err       error
}

func (f fakeTeams) TeamIDsForSubject(_ context.Context, subject string) ([]int, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.bySubject[subject], nil
}

func systemBinding(teamID int, role auth.Role, effect auth.Effect) auth.RoleBinding {
	return auth.RoleBinding{
		TeamID:    teamID,
		Role:      role,
		ScopeType: auth.ScopeSystem,
		Effect:    effect,
	}
}

func TestIdentityBuilder_DerivesRoleAndScopesFromBindings(t *testing.T) {
	// The whole point of the phase: an identity built from STORED STATE, with
	// no token anywhere in the picture.
	builder := auth.NewIdentityBuilder(
		fakeTeams{bySubject: map[string][]int{"admin@example.test": {7}}},
		auth.NewScopeResolver(fakeBindings{byTeam: map[int][]auth.RoleBinding{
			7: {systemBinding(7, auth.RoleAdmin, auth.EffectAllow)},
		}}),
	)

	identity, err := builder.Build(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if identity.Subject != "admin@example.test" {
		t.Errorf("Subject = %q", identity.Subject)
	}
	if identity.Role != auth.RoleAdmin {
		t.Errorf("Role = %q, want admin", identity.Role)
	}
	if !slices.Contains(identity.Scopes, auth.ScopeAccessWrite) {
		t.Error("an admin identity did not carry access:write")
	}
	if !identity.HasScope(auth.ScopeRunbookExecute) {
		t.Error("the derived identity cannot execute a runbook")
	}
}

func TestIdentityBuilder_RespectsTheRoleTheResolverReturns(t *testing.T) {
	tests := []struct {
		name string
		role auth.Role
	}{
		{"viewer", auth.RoleViewer},
		{"operator", auth.RoleOperator},
		{"admin", auth.RoleAdmin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := auth.NewIdentityBuilder(
				fakeTeams{bySubject: map[string][]int{"user@example.test": {1}}},
				auth.NewScopeResolver(fakeBindings{byTeam: map[int][]auth.RoleBinding{
					1: {systemBinding(1, tt.role, auth.EffectAllow)},
				}}),
			)

			identity, err := builder.Build(context.Background(), "user@example.test")
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if identity.Role != tt.role {
				t.Errorf("Role = %q, want %q", identity.Role, tt.role)
			}
			want := auth.ScopesForRole(tt.role)
			if !slices.Equal(identity.Scopes, want) {
				t.Errorf("Scopes = %v, want %v", identity.Scopes, want)
			}
		})
	}
}

func TestIdentityBuilder_AuthorizesNothingWithoutASystemGrant(t *testing.T) {
	// The residual gap, asserted so it is a known state rather than a
	// surprise. A subject whose grants are all group-scoped signs in and
	// reaches nothing, because the RoleBinding rule is not in the running
	// admission chain (it needs a ScopeTarget an HTTP route cannot supply).
	groupID := 3
	builder := auth.NewIdentityBuilder(
		fakeTeams{bySubject: map[string][]int{"scoped@example.test": {2}}},
		auth.NewScopeResolver(fakeBindings{byTeam: map[int][]auth.RoleBinding{
			2: {{
				TeamID:    2,
				Role:      auth.RoleOperator,
				ScopeType: auth.ScopeGroup,
				ScopeID:   &groupID,
				Effect:    auth.EffectAllow,
			}},
		}}),
	)

	identity, err := builder.Build(context.Background(), "scoped@example.test")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if identity.Subject != "scoped@example.test" {
		t.Errorf("Subject = %q; the login still identifies them", identity.Subject)
	}
	if identity.Role != "" {
		t.Errorf("Role = %q, want empty: a group-scoped grant is not a system-scope role", identity.Role)
	}
	if len(identity.Scopes) != 0 {
		t.Errorf("Scopes = %v, want none", identity.Scopes)
	}
}

func TestIdentityBuilder_DenyBeatsNoBindingAndBothGrantNothing(t *testing.T) {
	builder := auth.NewIdentityBuilder(
		fakeTeams{bySubject: map[string][]int{"denied@example.test": {4}}},
		auth.NewScopeResolver(fakeBindings{byTeam: map[int][]auth.RoleBinding{
			4: {systemBinding(4, auth.RoleAdmin, auth.EffectDeny)},
		}}),
	)

	identity, err := builder.Build(context.Background(), "denied@example.test")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if identity.Role != "" || len(identity.Scopes) != 0 {
		t.Errorf("an explicit Deny produced role %q and scopes %v", identity.Role, identity.Scopes)
	}
}

func TestIdentityBuilder_UnknownSubjectGetsNothing(t *testing.T) {
	// A subject with no User row has no Teams and therefore no grants. It is
	// NOT an error: authentication already succeeded by the time this runs,
	// and refusing here would conflate the two questions.
	builder := auth.NewIdentityBuilder(
		fakeTeams{bySubject: map[string][]int{}},
		auth.NewScopeResolver(fakeBindings{byTeam: map[int][]auth.RoleBinding{}}),
	)

	identity, err := builder.Build(context.Background(), "stranger@example.test")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(identity.Scopes) != 0 || identity.Role != "" {
		t.Errorf("an unknown subject was authorized: role %q scopes %v", identity.Role, identity.Scopes)
	}
}

func TestIdentityBuilder_FailsClosedOnPortErrors(t *testing.T) {
	boom := errors.New("database is on fire")

	t.Run("team lookup fails", func(t *testing.T) {
		builder := auth.NewIdentityBuilder(
			fakeTeams{err: boom},
			auth.NewScopeResolver(fakeBindings{}),
		)
		if _, err := builder.Build(context.Background(), "user@example.test"); err == nil {
			t.Error("Build() = nil error when the team lookup failed; a login must not succeed on a broken read")
		}
	})

	t.Run("binding repository fails", func(t *testing.T) {
		// The resolver itself fails closed to Deny on a repository error,
		// so this must produce an unauthorized identity rather than an
		// authorized one. Either outcome is safe; being authorized is not.
		builder := auth.NewIdentityBuilder(
			fakeTeams{bySubject: map[string][]int{"user@example.test": {1}}},
			auth.NewScopeResolver(fakeBindings{err: boom}),
		)
		identity, err := builder.Build(context.Background(), "user@example.test")
		if err == nil && len(identity.Scopes) != 0 {
			t.Errorf("a failed binding read produced an authorized identity: %v", identity.Scopes)
		}
	})
}

func TestIdentityBuilder_RefusesAnEmptySubject(t *testing.T) {
	builder := auth.NewIdentityBuilder(fakeTeams{}, auth.NewScopeResolver(fakeBindings{}))

	if _, err := builder.Build(context.Background(), ""); err == nil {
		t.Error("Build(\"\") = nil error; an empty subject is a caller bug, not an anonymous user")
	}
}
