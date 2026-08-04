package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type jwtEvaluator struct {
	secretKey []byte
}

// NewJWTEvaluator creates an Evaluator backed by symmetric HMAC signatures.
// In a full production system, asymmetric RS256 with JWKS is typically preferred.
func NewJWTEvaluator(secret []byte) Evaluator {
	return &jwtEvaluator{
		secretKey: secret,
	}
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
		// Enforce HMAC-SHA256 signature algorithm
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return j.secretKey, nil
	})

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
