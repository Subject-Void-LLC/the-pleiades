// Package access owns Organizations, Teams, Users and RoleBindings as
// managed objects: the surface an operator uses to say who exists and what
// they may reach.
//
// The split with internal/auth is the point of a separate package.
// internal/auth owns *evaluation*: given a caller and a target, does a
// binding allow it. This owns *administration*: creating the organization,
// putting a user in a team, writing the binding that grants something. One
// answers a question on every request, the other is exercised when somebody
// changes the shape of the deployment, and conflating them would put write
// paths inside the package every request already depends on.
//
// It exists because none of that was possible. Every one of these four
// entities has been in the schema since Phase 1 or Phase 8, with a working
// resolver over them, and nothing anywhere could create one: no endpoint, no
// view, no command, no seeder. They appeared only in test files. The visible
// consequence was an Inventories form whose required organization select had
// no options and refused every submission (FAILURE_PATTERNS.md #101); the
// invisible one was that the whole tenancy axis could never be turned on,
// because a resolver with no bindings to resolve denies everybody.
//
// Scopes are deliberately two, not eight. internal/auth's own
// ScopeInventoryRead comment sets the precedent in writing: a scope names
// what kind of operation a token may perform, and which object it may
// perform it on is the RBAC target's question. Eight scopes here would put
// one decision in two places.
package access

import (
	"context"
	"errors"
)

// ErrNotFound is returned when an id names no record.
var ErrNotFound = errors.New("access: record not found")

// ErrExists is returned when a uniqueness constraint refuses a write. It is
// a caller's mistake rather than a platform failure, which is what makes it
// a 409 rather than a 500.
var ErrExists = errors.New("access: a record with that name already exists")

// ErrInvalidInput is returned when a submission is not a usable record: a
// blank name, an address that is not one, a team belonging to no
// organization.
//
// Distinct from ErrInvalidBinding because it means something different to a
// caller. This is "fix what you sent"; that one is "what you sent could
// never resolve". Both are the caller's, and neither is a platform failure,
// which is the distinction that was missing when a blank name answered 500.
var ErrInvalidInput = errors.New("access: submission is not a usable record")

// ErrInvalidBinding is returned when a role binding could never resolve to
// anything a caller intended. See validateBinding for what that covers and
// why each case is refused at the write.
var ErrInvalidBinding = errors.New("access: role binding is not resolvable")

// ErrLastSystemBinding is returned when deleting a binding would leave the
// deployment with no system-scope Allow at all.
//
// That is the shape of a lockout: system scope is the only level that grants
// across every organization, so removing the last one can leave nobody able
// to administer anything, including the bindings themselves. The refusal is
// recoverable by design, since PLEIADES_BOOTSTRAP_ADMIN still resolves ahead
// of any stored state, but a control plane that lets an operator delete
// their own last key with one click and no warning is one that eventually
// will.
var ErrLastSystemBinding = errors.New("access: refusing to delete the last system-scope grant")

// Query is a list request. It is shared by all four collections because they
// page identically, and a per-entity query type would be four copies of the
// same three fields.
type Query struct {
	// After is a keyset cursor: the highest id already seen. Zero starts at
	// the beginning.
	After int

	// Limit bounds the page. Zero means the store's default.
	Limit int

	// Search narrows by name, case insensitively. Empty means no narrowing.
	Search string
}

// Store is every collection this package manages, composed so a composition
// root wires one value.
//
// The four interfaces stay separate underneath, so a consumer that needs
// only organizations depends only on Organizations. That matters more here
// than usual: the Inventories form needs exactly one method from this whole
// package, and taking a dependency on all four to get it would make the view
// look like it administers access.
type Store interface {
	Organizations
	Teams
	Users
	Bindings
	Contacts
}

// Organizations administers the tenancy boundary itself.
type Organizations interface {
	CreateOrganization(ctx context.Context, org Organization) (Organization, error)
	GetOrganization(ctx context.Context, id int) (Organization, error)
	ListOrganizations(ctx context.Context, q Query) ([]Organization, error)
	UpdateOrganization(ctx context.Context, org Organization) error
	DeleteOrganization(ctx context.Context, id int) error

	// AttestOrganization records that subject confirmed this tenant's
	// ownership information is current, as of now.
	//
	// Its own method rather than two fields on Update, and that is the
	// whole design. An attestation whose subject arrives in the request
	// body is one any caller can write anybody's name into, which makes it
	// a rumour with a date attached rather than a statement anyone is
	// accountable for. Here the subject comes from the caller's identity at
	// the composition root, exactly as an announcement's author does, and
	// there is no path that lets a form supply it.
	AttestOrganization(ctx context.Context, id int, subject string) error
}

// Teams administers the principals roles are granted to.
type Teams interface {
	CreateTeam(ctx context.Context, team Team) (Team, error)
	GetTeam(ctx context.Context, id int) (Team, error)
	ListTeams(ctx context.Context, q TeamQuery) ([]Team, error)
	UpdateTeam(ctx context.Context, team Team) error
	DeleteTeam(ctx context.Context, id int) error

	// AttestTeam is AttestOrganization for a team, and carries the same
	// rule about where the subject comes from.
	AttestTeam(ctx context.Context, id int, subject string) error
}

// Contacts administers accountability: who owns an organization or a team
// and how to reach them.
//
// A separate port rather than methods on Organizations and Teams, because a
// contact is one entity with one set of rules and splitting it in two would
// mean two implementations of the exactly-one-owner invariant. A caller that
// only reads an organization's contacts depends on this and not on the
// administration of organizations themselves.
type Contacts interface {
	CreateContact(ctx context.Context, contact Contact) (Contact, error)
	GetContact(ctx context.Context, id int) (Contact, error)
	ListContacts(ctx context.Context, q ContactQuery) ([]Contact, error)
	UpdateContact(ctx context.Context, contact Contact) error
	DeleteContact(ctx context.Context, id int) error
}

// Users administers the identities that map onto a token's subject.
type Users interface {
	CreateUser(ctx context.Context, user User) (User, error)
	GetUser(ctx context.Context, id int) (User, error)
	ListUsers(ctx context.Context, q Query) ([]User, error)
	UpdateUser(ctx context.Context, user User) error
	DeleteUser(ctx context.Context, id int) error
}

// Bindings administers the grants themselves.
type Bindings interface {
	CreateBinding(ctx context.Context, binding Binding) (Binding, error)
	GetBinding(ctx context.Context, id int) (Binding, error)
	ListBindings(ctx context.Context, q BindingQuery) ([]Binding, error)
	UpdateBinding(ctx context.Context, binding Binding) error
	DeleteBinding(ctx context.Context, id int) error
}
