package auth

import (
	"context"
	"fmt"
)

// minStaticSecretLen is HS256's RFC 7518 SS3.2 recommendation: a key at
// least as long as the hash output (32 bytes for SHA-256) so the signature
// cannot be brute forced faster than the hash itself.
const minStaticSecretLen = 32

// KeyProvider resolves the verification key a token's signature should be
// checked against. This is the Federated Identity port: an evaluator that
// depends only on KeyProvider never needs to know whether a key came from a
// local, symmetric secret or a remote Identity Provider's JWKS endpoint.
type KeyProvider interface {
	// Key returns the verification key for the given key ID (a JWT "kid"
	// header claim). kid is empty when the token carries no "kid" header,
	// which a provider with exactly one key (e.g. a static secret) is free
	// to treat as "the only key I have."
	Key(ctx context.Context, kid string) (interface{}, error)

	// Algorithms returns the JWT "alg" values this provider's keys are
	// valid for, so a caller can pin jwt.WithValidMethods before parsing
	// rather than accepting an entire algorithm family.
	Algorithms() []string
}

// staticKeyProvider is a single symmetric HMAC secret. PLAN.md Section 32.1:
// "A shared symmetric secret is acceptable only for local development."
type staticKeyProvider struct {
	secret []byte
}

// NewStaticKeyProvider builds a KeyProvider backed by one symmetric HMAC
// secret, for local development only. It rejects a nil, empty, or
// shorter-than-32-byte secret at construction, so a forgeable key fails the
// process at startup instead of silently reaching production traffic.
func NewStaticKeyProvider(secret []byte) (KeyProvider, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("auth: static signing key must not be nil or empty")
	}
	if len(secret) < minStaticSecretLen {
		return nil, fmt.Errorf("auth: static signing key must be at least %d bytes, got %d", minStaticSecretLen, len(secret))
	}
	// Copied so a caller mutating the slice it passed in cannot mutate the
	// key this provider verifies against afterward.
	owned := make([]byte, len(secret))
	copy(owned, secret)
	return &staticKeyProvider{secret: owned}, nil
}

// Key implements KeyProvider. kid is ignored: a static provider has exactly
// one key, regardless of what (if anything) the token's "kid" header names.
func (p *staticKeyProvider) Key(_ context.Context, _ string) (interface{}, error) {
	return p.secret, nil
}

// Algorithms implements KeyProvider.
func (p *staticKeyProvider) Algorithms() []string {
	return []string{"HS256"}
}
