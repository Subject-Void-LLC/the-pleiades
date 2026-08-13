package access

import (
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// Organization is the tenancy boundary. Everything ownable belongs to
// exactly one, which is the single structural decision AWX made that is
// worth copying wholesale.
type Organization struct {
	ID   int
	Name string

	// Description is what this tenant is, in a sentence. A list of names is
	// unreadable at the point where four of them are a variation of
	// "platform".
	Description string

	// Classification is this tenant's own marking, distinct from the
	// deployment-wide banner. One installation can hold tenants at
	// different levels, and the banner alone cannot say whose data is on
	// screen. Empty means unmarked, which is not the same as unclassified.
	Classification Classification

	// ChangeWindow is when this tenant permits automation to run, written
	// for a human rather than parsed. Nothing enforces it yet and the
	// field's help text says so.
	ChangeWindow string

	// Frozen is an operator-declared stop, separate from an absent change
	// window because "no window is declared" and "there is a window and we
	// are deliberately not running" are different facts, and only the
	// second is a decision somebody can be asked about.
	Frozen       bool
	FreezeReason string

	// External reference ids reconcile this row against whatever system of
	// record the customer already runs. Opaque on purpose: validating
	// somebody else's key format is a promise about their system that this
	// one cannot keep.
	CostCentre string
	TicketKey  string
	CMDBID     string

	// Attested is who last confirmed this tenant's ownership and escalation
	// information is current, and when. Set through Attest, never through
	// Update, so the subject comes from the caller's identity rather than
	// from a form.
	Attested Attestation

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Team is a group of users, and the only thing a role is ever granted to.
//
// Never a user directly. PLAN.md Section 18.2 states the rule and the reason
// in one line: granting to a person produces orphaned permissions the moment
// that person leaves, because the grant outlives the reason it was made. A
// team is a durable statement about a function, and membership is the
// revocable part.
type Team struct {
	ID             int
	Name           string
	OrganizationID int

	// OrganizationName is the tenant's name, carried beside its id so a
	// list can render "Network" rather than "1".
	//
	// It costs nothing: ListTeams already eager-loads the organization row
	// to populate OrganizationID, and the hydrator was reading the id off
	// it and discarding the rest. Empty when the caller did not load the
	// edge, never a fallback to the id.
	OrganizationName string

	// Description is what this team is responsible for, as opposed to who
	// is currently in it. The name is a noun and the grants hanging off it
	// are consequences; neither says why the team exists, which is what a
	// reviewer needs to judge whether its permissions are proportionate.
	Description string

	// Attested carries the same meaning it does on an Organization, and
	// matters more here. A team is what a role is granted to, so a team
	// with no attested owner is a live set of permissions with nobody
	// accountable for it, which is exactly the finding an access review
	// exists to produce.
	Attested Attestation

	// UserIDs is the membership, replaced wholesale on update rather than
	// merged, for the reason inventory.Set gives about its own members: a
	// merge makes removing the last member inexpressible, since an empty
	// submission is indistinguishable from "no change".
	UserIDs []int

	CreatedAt time.Time
	UpdatedAt time.Time
}

// User is an identity. It is deliberately almost nothing: an email, and the
// teams it belongs to.
//
// The email is the join key against a token's subject, which is what
// internal/auth's entTeamLookup already does. There is no password here and
// no phase owns building one, so a User is a statement that a subject is
// known to this deployment rather than a credential this deployment issues.
type User struct {
	ID    int
	Email string

	TeamIDs []int

	// TeamNames parallels TeamIDs, in the same order, for the reason
	// Team.OrganizationName exists. ListUsers already eager-loads the
	// teams.
	TeamNames []string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Binding is one grant: a team, a role, a place in the containment
// hierarchy, and whether it allows or denies.
type Binding struct {
	ID     int
	TeamID int
	Role   auth.Role

	// ScopeType and ScopeID name what this grant is about. ScopeID is zero
	// for a system-scope binding and positive for every other level, and
	// that invariant is enforced at the write rather than assumed.
	//
	// It has to be, because the column carries no foreign key and the
	// resolver compares values: before FAILURE_PATTERNS.md #99 was fixed, a
	// stored zero at device scope matched every check whose target named no
	// device, which is every organization-level question the platform asks.
	// The resolver now skips a level the target does not name; this refuses
	// to write the row that made it dangerous.
	ScopeType auth.ScopeType
	ScopeID   int

	// TeamName and ScopeName are the two references resolved to something
	// a reader can act on. TeamName is free: every binding query already
	// eager-loads its team. ScopeName costs one query per scope type on a
	// page, because scope_id is polymorphic and no eager load can reach it.
	//
	// ScopeName is empty for a system-scope grant, which names no target,
	// and also empty when the target has been deleted. Those are different
	// facts, and SystemWide distinguishes them.
	TeamName  string
	ScopeName string

	Effect auth.Effect

	CreatedAt time.Time
	UpdatedAt time.Time
}

// SystemWide reports whether this binding grants across every organization.
func (b Binding) SystemWide() bool { return b.ScopeType == auth.ScopeSystem }

// TeamQuery narrows a team listing to particular organizations.
type TeamQuery struct {
	Query

	// OrganizationIDs restricts the result. Empty means no restriction, and
	// that is the caller's decision rather than this store's, matching the
	// posture inventory.SetQuery already documents.
	OrganizationIDs []int
}

// BindingQuery narrows a binding listing.
type BindingQuery struct {
	Query

	// TeamIDs restricts to grants held by particular teams, which is how a
	// team's own page answers "what does this team reach".
	TeamIDs []int

	// ScopeTypes restricts to particular levels, which is how an auditor
	// answers the only question worth asking first: who holds system scope.
	ScopeTypes []auth.ScopeType

	// ScopeID restricts to one target, which is how a record's own page
	// answers "who reaches this".
	//
	// Applied only alongside a single entry in ScopeTypes, and that is a
	// correctness rule rather than an ergonomic one. scope_id carries no
	// foreign key and its values are per level, so organization 7 and
	// device 7 are unrelated records sharing an integer. Matching the id
	// alone would put another tenant's grants on this record's page, which
	// is FAILURE_PATTERNS.md #99 in a different direction: there a zero
	// matched every level, here a positive id would match the wrong one.
	ScopeID int
}

// validRoles is the closed set a binding may carry.
//
// Listed here rather than derived from internal/auth, because auth exposes
// the three constants without a set: a table there would be a second place
// to keep in step, and the compiler cannot check either way. The test
// asserts this covers every declared Role.
var validRoles = map[auth.Role]bool{
	auth.RoleViewer:   true,
	auth.RoleOperator: true,
	auth.RoleAdmin:    true,
}

// validScopeTypes is the containment hierarchy, broadest to narrowest.
var validScopeTypes = map[auth.ScopeType]bool{
	auth.ScopeSystem:       true,
	auth.ScopeOrganization: true,
	auth.ScopeInventory:    true,
	auth.ScopeGroup:        true,
	auth.ScopeDevice:       true,
}

// validEffects is the closed set of decisions a binding may express.
var validEffects = map[auth.Effect]bool{
	auth.EffectAllow: true,
	auth.EffectDeny:  true,
}

// validateBinding refuses a grant that could never resolve to what its
// author meant.
//
// Every case here is a write that would otherwise store successfully and
// then behave wrongly at evaluation time, which is the worst combination
// available: the operator sees a row in a table saying what they intended,
// and the resolver reads something else.
func validateBinding(b Binding) error {
	if b.TeamID <= 0 {
		return fmt.Errorf("%w: a binding must name a team", ErrInvalidBinding)
	}
	if !validRoles[b.Role] {
		return fmt.Errorf("%w: %q is not a role", ErrInvalidBinding, b.Role)
	}
	if !validScopeTypes[b.ScopeType] {
		return fmt.Errorf("%w: %q is not a scope type", ErrInvalidBinding, b.ScopeType)
	}
	if !validEffects[b.Effect] {
		return fmt.Errorf("%w: %q is not an effect", ErrInvalidBinding, b.Effect)
	}

	// The two halves of the #99 invariant. A system binding names no row, so
	// carrying an id would suggest it were narrower than it is; every other
	// level names exactly one row, and ent primary keys start at 1, so a
	// non-positive id can only ever be a mistake or an attack.
	if b.ScopeType == auth.ScopeSystem && b.ScopeID != 0 {
		return fmt.Errorf("%w: a system-scope binding names no target, but carries id %d",
			ErrInvalidBinding, b.ScopeID)
	}
	if b.ScopeType != auth.ScopeSystem && b.ScopeID <= 0 {
		return fmt.Errorf("%w: a %s-scope binding must name a positive target id",
			ErrInvalidBinding, b.ScopeType)
	}
	return nil
}

// normalizeName trims a submitted name and refuses an empty one.
func normalizeName(kind, name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("%w: a %s needs a name", ErrInvalidInput, kind)
	}
	return trimmed, nil
}

// normalizeEmail trims and lowercases a submitted address.
//
// Lowercased because it is the join key against a token's subject, and two
// rows differing only in case would be two identities to one person, with
// whichever one the token happened to match deciding what they could reach.
func normalizeEmail(email string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(email))
	if trimmed == "" {
		return "", fmt.Errorf("%w: a user needs an email address", ErrInvalidInput)
	}
	if !strings.Contains(trimmed, "@") {
		return "", fmt.Errorf("%w: %q is not an email address", ErrInvalidInput, trimmed)
	}
	return trimmed, nil
}
