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
	Scopes  []string // e.g. "inventory:read", "inventory:write", "runbook:execute"
}

// HasScope checks if the identity possesses a required scope.
func (id *Identity) HasScope(required string) bool {
	for _, s := range id.Scopes {
		if s == required || s == "*" {
			return true
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
	CheckAccess(ctx context.Context, id *Identity, requiredScopes ...string) error
}

// HATEOASGenerator inspects a user token and returns the available REST methods.
type HATEOASGenerator interface {
	// GetAllowedMethods returns a list of HTTP verbs (GET, POST) the user
	// is permitted to execute against the specified endpoint.
	GetAllowedMethods(ctx context.Context, endpoint string) ([]string, error)
}
