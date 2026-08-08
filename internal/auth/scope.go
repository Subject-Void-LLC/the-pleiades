package auth

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

// Effect is whether a RoleBinding permits or forbids access at its scope.
type Effect string

// The two Section 18.4 effects. Deny exists specifically so a more
// specific scope can revoke what a broader one granted.
const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// ScopeType names which of the four PLAN.md Section 18.4 scopes a
// RoleBinding applies at.
type ScopeType string

// The four Section 18.4 scopes, broadest to narrowest.
const (
	ScopeSystem       ScopeType = "system"
	ScopeOrganization ScopeType = "organization"
	ScopeGroup        ScopeType = "group"
	ScopeDevice       ScopeType = "device"
)

// ScopeTarget names the resource an access check is against. GroupIDs is a
// slice, not a single value, because a device can belong to any number of
// overlapping groups at once (PLAN.md Section 3). Zero-value fields
// (OrganizationID == 0, DeviceID == 0) never match a real RoleBinding: ent
// primary keys are auto-increment starting at 1, so a check that does not
// apply to an Organization or a Device (e.g. a purely System/Group-scoped
// check) needs no special-casing here.
type ScopeTarget struct {
	OrganizationID int
	GroupIDs       []int
	DeviceID       int
}

// RoleBinding is a Team's grant, or explicit denial, of a Role at one
// scope: the Go-side mirror of the ent RoleBinding entity, decoupled from
// ent so ScopeResolver's own logic has no generated-code dependency.
type RoleBinding struct {
	TeamID    int
	Role      Role
	ScopeType ScopeType
	ScopeID   *int // nil for ScopeSystem, which has no target
	Effect    Effect
}

// RoleBindingRepository loads the RoleBindings belonging to a set of
// Teams. ScopeResolver depends on this port, not on internal/ent directly,
// so its resolution logic is testable without a database.
type RoleBindingRepository interface {
	ListForTeams(ctx context.Context, teamIDs []int) ([]RoleBinding, error)
}

// ScopeDecision is one scope level's contribution to a resolution. Both
// fields nil means "no binding applies at this level, inherit whatever a
// less specific level already decided" - the pkg/policy per-field-Override
// idiom internal/classification.combineRule already established in this
// codebase. Role and Effect always move together (never independently):
// they are one indivisible fact about one binding, so a resolved decision
// can never pair one level's Role with a different level's Effect.
type ScopeDecision struct {
	Role   *Role
	Effect *Effect
}

// combineScopeDecision is the pkg/policy combine function ScopeResolver
// folds System -> Organization -> Group -> Device (least to most specific)
// through. It is a deliberate, stated departure from plain policy.Override:
// once any level sets an explicit Deny, that Deny is terminal, and no
// later, more specific level, even an explicit Allow, can undo it.
//
// Plain Override already satisfies PLAN.md 18.4's literal worked example
// for free (a Device-level Deny beats a Group-level Allow, since Device
// folds last), but it would also let a later, more specific Allow override
// an earlier Deny. Section 18.4 frames device-level RBAC as "overridable"
// specifically for the case where nothing more specific has decided yet,
// or where a broader level only granted an Allow, not for overriding a
// broader explicit Deny - the fail-closed posture this whole design
// commits to (see ScopeResolver.Resolve's own doc comment) treats an
// explicit Deny at any level as final.
func combineScopeDecision(acc, next ScopeDecision) ScopeDecision {
	if acc.Effect != nil && *acc.Effect == EffectDeny {
		return acc
	}
	if next.Role != nil || next.Effect != nil {
		acc.Role, acc.Effect = next.Role, next.Effect
	}
	return acc
}

// rolePriority orders the three roles by privilege, most permissive last,
// so decisionFor has an explicit, deterministic tie-break when a Team
// holds more than one Allow RoleBinding at the identical scope level
// (e.g. both Viewer and Operator granted on the same Group). An unknown
// role name sorts lowest, never winning a tie against a recognized one.
func rolePriority(r Role) int {
	switch r {
	case RoleAdmin:
		return 2
	case RoleOperator:
		return 1
	case RoleViewer:
		return 0
	default:
		return -1
	}
}

// decisionFor folds every binding matching scopeType/scopeID into one
// ScopeDecision. An explicit Deny at this level always wins over any
// Allow at the same level, matching Section 18.4's own framing of Deny as
// the stronger statement; among multiple Allow bindings at the same level,
// the most privileged Role wins via rolePriority, rather than an
// arbitrary, slice-order-dependent choice.
func decisionFor(bindings []RoleBinding, scopeType ScopeType, scopeID *int) ScopeDecision {
	var decision ScopeDecision
	for _, b := range bindings {
		if b.ScopeType != scopeType || !scopeIDMatches(b.ScopeID, scopeID) {
			continue
		}
		if b.Effect == EffectDeny {
			role, effect := b.Role, EffectDeny
			return ScopeDecision{Role: &role, Effect: &effect}
		}
		if decision.Role == nil || rolePriority(b.Role) > rolePriority(*decision.Role) {
			role, effect := b.Role, EffectAllow
			decision = ScopeDecision{Role: &role, Effect: &effect}
		}
	}
	return decision
}

func scopeIDMatches(bindingScopeID, targetScopeID *int) bool {
	if bindingScopeID == nil || targetScopeID == nil {
		return bindingScopeID == targetScopeID
	}
	return *bindingScopeID == *targetScopeID
}

// ScopeResolver implements PLAN.md Section 18.4: System, Organization,
// Group, and Device scoped RoleBindings folded into one effective
// decision via pkg/policy.Resolve (Phase 6's shared hierarchical policy
// resolver, reused here rather than a fifth hand-rolled walk-and-merge
// loop), most specific applicable level winning, with an explicit Deny at
// any level locking out every less specific Allow.
//
// Deliberately not wired into Evaluator.ValidateToken: no token-issuance
// path exists anywhere in this codebase to bake a caller's Team
// memberships into a JWT, and a per-request database lookup inside
// stateless token validation would undermine the Stateless Session
// pattern this phase hardens rather than replaces (PATTERNS.md). Resolve
// takes team IDs the caller already knows, ready for a future
// issuance-time or per-request caller to supply them - the same "inert
// until a real caller exists" boundary this codebase already draws
// elsewhere (e.g. pkg/collection.Manifest's PlatformTarget).
type ScopeResolver struct {
	repo RoleBindingRepository
}

// NewScopeResolver builds a ScopeResolver backed by repo.
func NewScopeResolver(repo RoleBindingRepository) *ScopeResolver {
	return &ScopeResolver{repo: repo}
}

// Resolve folds every RoleBinding belonging to teamIDs that applies to
// target and reports the winning Role and Effect. Every branch fails
// closed: an empty teamIDs, a repository error, or no matching binding at
// any level all resolve to (zero Role, EffectDeny), never to an implicit
// Allow.
//
// A device belonging to more than one Group (GroupIDs has more than one
// element) folds every matching group-scoped binding at the same
// specificity; an explicit Deny on any one of them locks the whole
// decision via combineScopeDecision, but among several non-Deny group
// bindings the one folded last wins, since a device's group membership is
// an unordered set with no other tie-break this codebase defines yet - a
// stated, honest limitation, not an oversight.
func (r *ScopeResolver) Resolve(ctx context.Context, teamIDs []int, target ScopeTarget) (Role, Effect, error) {
	if len(teamIDs) == 0 {
		return "", EffectDeny, nil
	}

	bindings, err := r.repo.ListForTeams(ctx, teamIDs)
	if err != nil {
		return "", EffectDeny, fmt.Errorf("auth: listing role bindings: %w", err)
	}

	layers := []policy.Layer[ScopeDecision]{
		{Name: "system", Value: decisionFor(bindings, ScopeSystem, nil)},
		{Name: "organization", Value: decisionFor(bindings, ScopeOrganization, &target.OrganizationID)},
	}
	for _, gid := range target.GroupIDs {
		gid := gid
		layers = append(layers, policy.Layer[ScopeDecision]{
			Name:  "group",
			Value: decisionFor(bindings, ScopeGroup, &gid),
		})
	}
	layers = append(layers, policy.Layer[ScopeDecision]{
		Name:  "device",
		Value: decisionFor(bindings, ScopeDevice, &target.DeviceID),
	})

	result := policy.Resolve(policy.ModeOverride, ScopeDecision{}, layers, combineScopeDecision)
	if result.Value.Role == nil || result.Value.Effect == nil || *result.Value.Effect != EffectAllow {
		return "", EffectDeny, nil
	}
	return *result.Value.Role, EffectAllow, nil
}
