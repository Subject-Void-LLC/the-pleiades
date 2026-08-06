package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

type fakeAdmissionRule struct {
	effect  auth.Effect
	err     error
	called  bool
	checkFn func()
}

func (f *fakeAdmissionRule) Check(_ context.Context, _ *auth.Identity, _ auth.AdmissionRequest) (auth.Effect, error) {
	f.called = true
	if f.checkFn != nil {
		f.checkFn()
	}
	return f.effect, f.err
}

func TestAdmissionChain_Evaluate_EmptyChainFailsClosed(t *testing.T) {
	var chain auth.AdmissionChain
	if err := chain.Evaluate(context.Background(), &auth.Identity{Subject: "u1"}, auth.AdmissionRequest{}); err == nil {
		t.Fatal("expected an empty admission chain to deny, never allow by default")
	}
}

func TestAdmissionChain_Evaluate_AllMustAllow(t *testing.T) {
	allow := &fakeAdmissionRule{effect: auth.EffectAllow}
	deny := &fakeAdmissionRule{effect: auth.EffectDeny}
	neverReached := &fakeAdmissionRule{effect: auth.EffectAllow}

	chain := auth.AdmissionChain{allow, deny, neverReached}
	err := chain.Evaluate(context.Background(), &auth.Identity{Subject: "u1"}, auth.AdmissionRequest{})
	if err == nil {
		t.Fatal("expected a Deny anywhere in the chain to deny the whole request")
	}
	if !allow.called || !deny.called {
		t.Fatal("expected both rules before the Deny to have run")
	}
	if neverReached.called {
		t.Error("expected the chain to short-circuit at the first Deny, not keep evaluating")
	}
}

func TestAdmissionChain_Evaluate_RuleErrorFailsClosed(t *testing.T) {
	erroring := &fakeAdmissionRule{effect: auth.EffectAllow, err: errors.New("boom")}
	neverReached := &fakeAdmissionRule{effect: auth.EffectAllow}
	chain := auth.AdmissionChain{erroring, neverReached}

	if err := chain.Evaluate(context.Background(), &auth.Identity{Subject: "u1"}, auth.AdmissionRequest{}); err == nil {
		t.Fatal("expected a rule error to deny the request")
	}
	if neverReached.called {
		t.Error("expected the chain to short-circuit on error, not keep evaluating")
	}
}

func TestAdmissionChain_Evaluate_AllAllowSucceeds(t *testing.T) {
	chain := auth.AdmissionChain{
		&fakeAdmissionRule{effect: auth.EffectAllow},
		&fakeAdmissionRule{effect: auth.EffectAllow},
	}
	if err := chain.Evaluate(context.Background(), &auth.Identity{Subject: "u1"}, auth.AdmissionRequest{}); err != nil {
		t.Fatalf("expected an all-Allow chain to succeed, got: %v", err)
	}
}

type fakeRecorder struct {
	decisions []auth.Decision
}

func (f *fakeRecorder) Record(_ context.Context, d auth.Decision) {
	f.decisions = append(f.decisions, d)
}

func TestAdmission_Evaluate_RecordsEveryDecision(t *testing.T) {
	recorder := &fakeRecorder{}
	id := &auth.Identity{Subject: "u1", Role: auth.RoleViewer}
	req := auth.AdmissionRequest{RequiredScope: "inventory:read"}

	allowAdmission := auth.Admission{
		Chain:    auth.AdmissionChain{&fakeAdmissionRule{effect: auth.EffectAllow}},
		Recorder: recorder,
	}
	if err := allowAdmission.Evaluate(context.Background(), id, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	denyAdmission := auth.Admission{
		Chain:    auth.AdmissionChain{&fakeAdmissionRule{effect: auth.EffectDeny}},
		Recorder: recorder,
	}
	if err := denyAdmission.Evaluate(context.Background(), id, req); err == nil {
		t.Fatal("expected the deny chain to return an error")
	}

	if len(recorder.decisions) != 2 {
		t.Fatalf("got %d recorded decisions, want 2", len(recorder.decisions))
	}
	if !recorder.decisions[0].Allowed || recorder.decisions[0].Identity != id {
		t.Errorf("expected the first decision to be allowed and carry the real Identity, got %+v", recorder.decisions[0])
	}
	if recorder.decisions[1].Allowed || recorder.decisions[1].Err == nil {
		t.Errorf("expected the second decision to be denied with a recorded error, got %+v", recorder.decisions[1])
	}
}

func TestTokenScopeRule(t *testing.T) {
	secret := []byte("token-scope-rule-secret-that-is-long-enough")
	eval := newTestEvaluator(t, secret)
	rule := auth.NewTokenScopeRule(eval)
	ctx := context.Background()

	viewer := &auth.Identity{Subject: "u1", Role: auth.RoleViewer, Scopes: []string{"inventory:read"}}

	effect, err := rule.Check(ctx, viewer, auth.AdmissionRequest{RequiredScope: "inventory:read"})
	if err != nil || effect != auth.EffectAllow {
		t.Errorf("expected a matching scope to allow, got effect=%v err=%v", effect, err)
	}

	effect, err = rule.Check(ctx, viewer, auth.AdmissionRequest{RequiredScope: "inventory:write"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if effect != auth.EffectDeny {
		t.Errorf("expected a missing scope to deny, got effect=%v", effect)
	}

	// An empty RequiredScope has nothing for this rule to check.
	effect, err = rule.Check(ctx, viewer, auth.AdmissionRequest{})
	if err != nil || effect != auth.EffectAllow {
		t.Errorf("expected an empty RequiredScope to allow, got effect=%v err=%v", effect, err)
	}
}

type fakeTeamLookup struct {
	teams map[string][]int
	err   error
}

func (f *fakeTeamLookup) TeamIDsForSubject(_ context.Context, subject string) ([]int, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.teams[subject], nil
}

func TestScopeRule(t *testing.T) {
	t.Run("unauthenticated identity fails closed", func(t *testing.T) {
		resolver := auth.NewScopeResolver(&fakeRoleBindingRepository{})
		rule := auth.NewScopeRule(resolver, &fakeTeamLookup{})
		effect, err := rule.Check(context.Background(), nil, auth.AdmissionRequest{})
		if err == nil || effect != auth.EffectDeny {
			t.Errorf("expected a nil identity to deny with an error, got effect=%v err=%v", effect, err)
		}
	})

	t.Run("team lookup failure fails closed", func(t *testing.T) {
		resolver := auth.NewScopeResolver(&fakeRoleBindingRepository{})
		rule := auth.NewScopeRule(resolver, &fakeTeamLookup{err: errors.New("boom")})
		effect, err := rule.Check(context.Background(), &auth.Identity{Subject: "u1"}, auth.AdmissionRequest{})
		if err == nil || effect != auth.EffectDeny {
			t.Errorf("expected a lookup failure to deny with an error, got effect=%v err=%v", effect, err)
		}
	})

	t.Run("resolves through to the real ScopeResolver decision", func(t *testing.T) {
		repo := &fakeRoleBindingRepository{bindings: []auth.RoleBinding{
			{TeamID: 5, Role: auth.RoleAdmin, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow},
		}}
		lookup := &fakeTeamLookup{teams: map[string][]int{"u1": {5}}}
		rule := auth.NewScopeRule(auth.NewScopeResolver(repo), lookup)

		effect, err := rule.Check(context.Background(), &auth.Identity{Subject: "u1"}, auth.AdmissionRequest{})
		if err != nil || effect != auth.EffectAllow {
			t.Errorf("expected the system-level Allow binding to grant, got effect=%v err=%v", effect, err)
		}

		effect, err = rule.Check(context.Background(), &auth.Identity{Subject: "unknown-user"}, auth.AdmissionRequest{})
		if err != nil || effect != auth.EffectDeny {
			t.Errorf("expected a subject with no team membership to deny, got effect=%v err=%v", effect, err)
		}
	})
}
