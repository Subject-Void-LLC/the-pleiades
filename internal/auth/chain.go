package auth

import (
	"context"
	"fmt"
	"log/slog"
)

// AdmissionRequest names what is being requested: a required permission
// scope string (the pre-existing Identity.Scopes/CheckAccess check) and,
// where relevant, the resource it targets (the Section 18.4 Team/
// RoleBinding check).
type AdmissionRequest struct {
	RequiredScope Scope
	Target        ScopeTarget
}

// AdmissionRule is one Chain of Responsibility link: a single, independent
// question about whether id may proceed with req. AdmissionChain composes
// any number of these; a rule never needs to know what else is in the
// chain around it.
type AdmissionRule interface {
	Check(ctx context.Context, id *Identity, req AdmissionRequest) (Effect, error)
}

// AdmissionChain is the Pattern Entry Gate's "Chain of Responsibility...
// admission chain for Step-Up" mechanism. It fails closed on every branch:
// an empty chain, any rule erroring, or any rule not explicitly returning
// EffectAllow all deny access. There is no "no rules ran, so allow by
// default" path.
//
// This ships one real rule today (NewTokenScopeRule, NewScopeRule); Phase
// 49 (The Exception Path) adds a Step-Up rule to a chain built exactly
// like this one, per IMPLEMENTATION.md's own cross-reference ("Phase 8's
// own Pattern Entry Gate already names 'admission chain for Step-Up' as
// an expected pattern and no item in that phase builds it") - Step-Up
// itself is deliberately not implemented here.
type AdmissionChain []AdmissionRule

// Evaluate runs every rule in order, stopping at the first Deny or error.
func (c AdmissionChain) Evaluate(ctx context.Context, id *Identity, req AdmissionRequest) error {
	if len(c) == 0 {
		return fmt.Errorf("access denied: admission chain has no rules configured")
	}
	for _, rule := range c {
		effect, err := rule.Check(ctx, id, req)
		if err != nil {
			return fmt.Errorf("access denied: %w", err)
		}
		if effect != EffectAllow {
			return fmt.Errorf("access denied: admission rule denied access")
		}
	}
	return nil
}

// Decision is one completed admission check, the unit Recorder logs.
type Decision struct {
	Identity *Identity
	Request  AdmissionRequest
	Allowed  bool
	Err      error
}

// Recorder is the Audit Trail pattern (PATTERNS.md): "don't discard that
// Identity after the RBAC check." Every Admission.Evaluate call records
// exactly one Decision, successful or not. This is distinct from the
// DB-backed AuditRecorder port (CODE_SCAFFOLD.md Section M, an append-only
// mutation log written inside the unit of work) - that is Phase 49's own,
// separately owned port, not built here.
type Recorder interface {
	Record(ctx context.Context, d Decision)
}

// slogRecorder is the default Recorder, backed by log/slog.
type slogRecorder struct {
	logger *slog.Logger
}

// NewSlogRecorder builds a Recorder that logs every Decision via logger.
// A nil logger uses slog.Default().
func NewSlogRecorder(logger *slog.Logger) Recorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &slogRecorder{logger: logger}
}

// Record implements Recorder.
func (r *slogRecorder) Record(_ context.Context, d Decision) {
	subject, role := "", Role("")
	if d.Identity != nil {
		subject, role = d.Identity.Subject, d.Identity.Role
	}
	attrs := []any{
		slog.String("subject", subject),
		slog.String("role", string(role)),
		slog.String("required_scope", string(d.Request.RequiredScope)),
		slog.Bool("allowed", d.Allowed),
	}
	if d.Err != nil {
		attrs = append(attrs, slog.String("error", d.Err.Error()))
	}
	if d.Allowed {
		r.logger.Info("admission decision", attrs...)
	} else {
		r.logger.Warn("admission decision", attrs...)
	}
}

// Admission composes an AdmissionChain with a Recorder, so every access
// decision is both enforced and audited in a single call.
type Admission struct {
	Chain    AdmissionChain
	Recorder Recorder
}

// Evaluate runs Chain.Evaluate and records the outcome, successful or
// not, before returning it.
func (a Admission) Evaluate(ctx context.Context, id *Identity, req AdmissionRequest) error {
	err := a.Chain.Evaluate(ctx, id, req)
	if a.Recorder != nil {
		a.Recorder.Record(ctx, Decision{Identity: id, Request: req, Allowed: err == nil, Err: err})
	}
	return err
}

// tokenScopeRule wraps the pre-existing Evaluator.CheckAccess (the
// JWT-carried Identity.Scopes check) as an AdmissionRule, so the chain can
// compose it alongside the Team/RoleBinding check below without either
// needing to know about the other.
type tokenScopeRule struct {
	evaluator Evaluator
}

// NewTokenScopeRule builds an AdmissionRule backed by evaluator's existing
// CheckAccess. A request with an empty RequiredScope always allows: this
// rule has nothing to check.
func NewTokenScopeRule(evaluator Evaluator) AdmissionRule {
	return &tokenScopeRule{evaluator: evaluator}
}

// Check implements AdmissionRule.
func (r *tokenScopeRule) Check(ctx context.Context, id *Identity, req AdmissionRequest) (Effect, error) {
	if req.RequiredScope == "" {
		return EffectAllow, nil
	}
	if err := r.evaluator.CheckAccess(ctx, id, req.RequiredScope); err != nil {
		return EffectDeny, nil
	}
	return EffectAllow, nil
}

// TeamLookup resolves which Teams a caller belongs to. scopeRule depends
// on this port, not on internal/ent directly, mirroring
// RoleBindingRepository's own port/adapter split.
type TeamLookup interface {
	TeamIDsForSubject(ctx context.Context, subject string) ([]int, error)
}

// scopeRule is the Section 18.4 System/Organization/Group/Device check, an
// AdmissionRule wrapping ScopeResolver. It ignores RequiredScope entirely:
// that axis belongs to tokenScopeRule. This rule answers a different
// question - "does this identity's Team membership grant access to this
// specific target" - and the two compose in one chain without either
// implying the other.
type scopeRule struct {
	resolver *ScopeResolver
	lookup   TeamLookup
}

// NewScopeRule builds an AdmissionRule backed by resolver and lookup.
func NewScopeRule(resolver *ScopeResolver, lookup TeamLookup) AdmissionRule {
	return &scopeRule{resolver: resolver, lookup: lookup}
}

// Check implements AdmissionRule. An unauthenticated identity, a team
// lookup failure, and a resolution failure all fail closed to Deny.
func (r *scopeRule) Check(ctx context.Context, id *Identity, req AdmissionRequest) (Effect, error) {
	if id == nil {
		return EffectDeny, fmt.Errorf("auth: unauthenticated identity")
	}
	teamIDs, err := r.lookup.TeamIDsForSubject(ctx, id.Subject)
	if err != nil {
		return EffectDeny, fmt.Errorf("auth: looking up teams for %q: %w", id.Subject, err)
	}
	_, effect, err := r.resolver.Resolve(ctx, teamIDs, req.Target)
	if err != nil {
		return EffectDeny, err
	}
	return effect, nil
}
