package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type jwtEvaluator struct {
	keyProvider KeyProvider
	issuer      string
	audience    string
}

// NewJWTEvaluator creates an Evaluator that verifies tokens through the
// given KeyProvider (Stateless Session, backed by Federated Identity or a
// local development secret) and pins issuer, audience, expiry, and
// algorithm (PLAN.md Section 32.1). It fails closed at construction: a nil
// provider is rejected here, at startup, rather than surfacing as every
// later token failing to validate for an unclear reason.
func NewJWTEvaluator(provider KeyProvider, issuer, audience string) (Evaluator, error) {
	if provider == nil {
		return nil, fmt.Errorf("auth: KeyProvider must not be nil")
	}
	if issuer == "" {
		return nil, fmt.Errorf("auth: issuer must not be empty")
	}
	if audience == "" {
		return nil, fmt.Errorf("auth: audience must not be empty")
	}
	return &jwtEvaluator{keyProvider: provider, issuer: issuer, audience: audience}, nil
}

// claims defines our custom JWT payload structure.
type claims struct {
	Role   string   `json:"role"`
	Scopes []string `json:"scopes"`
	jwt.RegisteredClaims
}

func (j *jwtEvaluator) ValidateToken(ctx context.Context, rawToken string) (*Identity, error) {
	// Strip Bearer prefix if present
	rawToken = strings.TrimPrefix(rawToken, "Bearer ")

	token, err := jwt.ParseWithClaims(rawToken, &claims{}, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		return j.keyProvider.Key(ctx, kid)
	},
		// Pin the algorithm to exactly what this KeyProvider's keys are
		// valid for, replacing "any HMAC family" with a closed set. This is
		// what defeats an algorithm-confusion attack (e.g. an RS256-signed
		// token presented to an HMAC-only validator, or vice versa): the
		// keyfunc above and the algorithm check below both have to agree,
		// and each KeyProvider implementation only returns keys of the type
		// its own Algorithms() names.
		jwt.WithValidMethods(j.keyProvider.Algorithms()),
		jwt.WithIssuer(j.issuer),
		jwt.WithAudience(j.audience),
		// A token with no exp claim is otherwise accepted forever; require
		// one explicitly rather than relying on it being present by
		// convention. Not-before is already checked by the default
		// Validator whenever the claim is present; nothing here needs to
		// change to enforce that.
		jwt.WithExpirationRequired(),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if customClaims, ok := token.Claims.(*claims); ok && token.Valid {
		sub, _ := customClaims.GetSubject()
		return &Identity{
			Subject: sub,
			Role:    Role(customClaims.Role),
			Scopes:  customClaims.Scopes,
		}, nil
	}

	return nil, fmt.Errorf("invalid token claims")
}

func (j *jwtEvaluator) CheckAccess(ctx context.Context, id *Identity, requiredScopes ...string) error {
	if id == nil {
		return fmt.Errorf("access denied: unauthenticated identity")
	}

	for _, req := range requiredScopes {
		if !id.HasScope(req) {
			return fmt.Errorf("access denied: missing required scope '%s'", req)
		}
	}

	return nil
}
