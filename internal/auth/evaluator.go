package auth

import "context"

// Role represents a system role that grants certain baseline privileges.
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

// Identity represents an authenticated caller.
type Identity struct {
	Subject string
	Role    Role
	Scopes  []Scope
}

// impliedBy lists, for a scope, the wider scopes that carry it. It is the
// one place implication is decided, read by HasScope, which every scope
// check reaches.
var impliedBy = map[Scope][]Scope{
	// A check can only change less than a real run, so whoever may run a
	// template for real may check it.
	ScopeRunbookCheck: {ScopeRunbookExecute},
}

// HasScope checks if the identity possesses a required scope, directly or
// through a wider scope that implies it (impliedBy).
func (id *Identity) HasScope(required Scope) bool {
	for _, s := range id.Scopes {
		if s == required || s == scopeWildcard {
			return true
		}
		for _, wider := range impliedBy[required] {
			if s == wider {
				return true
			}
		}
	}
	// Admin role bypasses scope checks
	if id.Role == RoleAdmin {
		return true
	}
	return false
}

// Evaluator defines the interface for evaluating authentication tokens.
type Evaluator interface {
	// ValidateToken parses a raw string token and extracts the verified Identity.
	ValidateToken(ctx context.Context, rawToken string) (*Identity, error)

	// CheckAccess enforces that the provided identity meets the required scopes.
	CheckAccess(ctx context.Context, id *Identity, requiredScopes ...Scope) error
}
