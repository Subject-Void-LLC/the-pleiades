// Building an auth.Identity from stored state rather than from a token.
//
// Everything else in this package answers "is this token telling the
// truth". This file answers a question nothing had to answer before: given
// that we already know WHO somebody is, what may they do? The two are
// different jobs and it is worth keeping them visibly different, because
// conflating them is how a login ends up trusting a claim it should have
// derived.
//
// The pieces this composes all existed and none of them had a production
// caller. ScopeResolver's own doc calls it "inert until a real caller
// exists" and says its arguments are "ready for a future issuance-time or
// per-request caller to supply them". This is that caller.
package auth

import (
	"context"
	"fmt"
)

// IdentityBuilder turns a proven subject into an Identity, by reading the
// RoleBindings on the Teams that subject belongs to.
//
// It performs no authentication of its own and must never be given the
// means to. Proving the subject is internal/localauth's job for a password,
// and an external issuer's job for a token; this type starts after that is
// settled. Keeping it ignorant of credentials is what stops a second notion
// of "who is this caller" growing next to the first.
type IdentityBuilder struct {
	teams    TeamLookup
	resolver *ScopeResolver
}

// NewIdentityBuilder composes the two ports an identity is derived from.
func NewIdentityBuilder(teams TeamLookup, resolver *ScopeResolver) *IdentityBuilder {
	return &IdentityBuilder{teams: teams, resolver: resolver}
}

// Build derives the Identity for an already-proven subject.
//
// The three steps, and why each is the shape it is:
//
// Subject to Teams, through the existing TeamLookup, which already joins a
// subject against User.email. That join is not new here and is not being
// invented: it is the same one the admission chain's Team rule uses, which
// is what keeps a locally derived identity and a token-derived one pointing
// at the same User row.
//
// Teams to Role, through ScopeResolver against an EMPTY ScopeTarget. An
// empty target folds the system-scope layer alone, because every other
// layer is skipped when its id is unset, so this asks exactly "what is this
// caller's baseline role across the whole deployment". That is the only
// question an Identity can answer, because an Identity carries one Role and
// no target: it is minted once at login and then presented against routes
// that have no Group or Device in hand. Narrower, per-target authority is
// real and is deliberately NOT collapsed into this value; see the note on
// the residual gap below.
//
// Role to Scopes, through ScopesForRole. See rolescopes.go for that
// decision and for why admin gets an enumeration rather than a wildcard.
//
// Fails closed at every step. A resolver error, an explicit Deny, or no
// binding at all yields an Identity with an empty role and no scopes rather
// than an error, because "authenticated but authorized for nothing" is a
// real and correct state: refusing the login instead would conflate
// authentication with authorization and would lock out a correctly scoped
// operator whose grants happen to be group-scoped.
//
// # The residual gap, recorded rather than left to be found
//
// A subject whose only RoleBindings are organization, group or device
// scoped resolves here to no role and no scopes, and can therefore sign in
// and reach nothing. That is not a bug in this function; it is the visible
// edge of a gap that predates it. The admission chain in the composition
// root carries only the token-scope rule, because the RoleBinding rule
// needs a ScopeTarget and an HTTP route has none to give it. Until that
// rule joins the chain, system-scope is the only level that decides
// anything at request time, and FAILURE_PATTERNS.md #98 stays open. A
// caller in this state should be shown a truthful empty state, not a
// broken-looking one.
func (b *IdentityBuilder) Build(ctx context.Context, subject string) (*Identity, error) {
	if subject == "" {
		return nil, fmt.Errorf("auth: cannot build an identity for an empty subject")
	}

	teamIDs, err := b.teams.TeamIDsForSubject(ctx, subject)
	if err != nil {
		return nil, fmt.Errorf("auth: resolving teams for %q: %w", subject, err)
	}

	// ScopeResolver already fails closed on an empty team list and on a
	// repository error, returning ("", EffectDeny). Calling it anyway
	// rather than short-circuiting here keeps one implementation of that
	// policy instead of two that can drift.
	role, effect, err := b.resolver.Resolve(ctx, teamIDs, ScopeTarget{})
	if err != nil {
		return nil, fmt.Errorf("auth: resolving role for %q: %w", subject, err)
	}

	if effect != EffectAllow {
		// An explicit Deny and "no binding matched" arrive here the same
		// way, deliberately: both mean this caller has been granted nothing
		// at system scope, and inventing a distinction would imply a
		// negative grant that the resolver does not model.
		return &Identity{Subject: subject, Scopes: []Scope{}}, nil
	}

	return &Identity{
		Subject: subject,
		Role:    role,
		Scopes:  ScopesForRole(role),
	}, nil
}
