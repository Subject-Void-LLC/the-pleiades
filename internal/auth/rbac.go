// Package auth provides token evaluation and role-based access control.
package auth

import "context"

// Evaluator checks identity tokens against internal authorization policies.
type Evaluator interface {
	// IsAuthorized verifies if the identity context holds the required role
	// for the target resource scope.
	IsAuthorized(ctx context.Context, resourceScope string, requiredRole string) (bool, error)
}

// HATEOASGenerator inspects a user token and returns the available REST methods.
type HATEOASGenerator interface {
	// GetAllowedMethods returns a list of HTTP verbs (GET, POST) the user
	// is permitted to execute against the specified endpoint.
	GetAllowedMethods(ctx context.Context, endpoint string) ([]string, error)
}
