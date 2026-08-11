// Package session issues and resolves the cookie-backed sessions the web
// UI authenticates with.
//
// It exists because a browser cannot present a Bearer token the way an API
// or CLI client can: an EventSource cannot set an Authorization header at
// all, and a token held in JavaScript is a token exposed to every script
// on the page. A cookie the server issues, and can revoke, solves both.
//
// The store is deliberately shared, never node-local. An in-process
// session map would force Sticky Session, which this platform's own
// pattern register rejects; a row in the PostgreSQL every controller
// replica already queries forces nothing, because any replica resolves any
// session with no coordination and the row itself is the revocation. See
// internal/ent/schema/session.go for the full argument, including why the
// extra read is worth what a signed token structurally cannot offer.
//
// Nothing here caches a resolved session in memory. That is the obvious
// optimization and it is refused on purpose: a cache is exactly what would
// make revocation eventually-consistent, which is the property this whole
// mechanism exists to provide.
package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// ErrNotFound is returned by Resolve when no live session matches, whether
// because none was ever issued under that token, it was deleted, or it has
// expired. The three are deliberately indistinguishable to a caller: a
// response that told an attacker which of the three applied would confirm
// that a guessed token had once been valid.
var ErrNotFound = errors.New("session not found")

// Defaults for the two independent deadlines every session carries.
//
// Both are needed and neither substitutes for the other. Idle expiry
// closes an abandoned session on a shared machine, and slides forward with
// use. Absolute expiry bounds a credential's total life however
// continuously it is exercised -- an idle timeout alone lets a stolen
// cookie live forever simply by being used.
const (
	DefaultIdleTimeout     = 30 * time.Minute
	DefaultAbsoluteTimeout = 8 * time.Hour
)

// tokenBytes is the entropy in a session identifier. 256 bits is not a
// round number chosen for looks: the identifier is a bearer credential
// with no second factor behind it, so it must be infeasible to guess even
// given unlimited online attempts against an endpoint that cannot
// rate-limit per-account, because there is no account name in play.
const tokenBytes = 32

// Session is one live browser session.
type Session struct {
	// Subject, Role and Scopes are the identity this session resolves to,
	// captured at login.
	Subject string
	Role    auth.Role
	Scopes  []string

	// CSRFKey is this session's own HMAC key. It never leaves the server.
	CSRFKey []byte

	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
	LastSeenAt        time.Time
}

// Identity projects the session onto the same auth.Identity the Bearer
// path produces, so both credential kinds feed one evaluator and one
// admission chain. A second identity type here would be a second answer to
// "who is this caller", and the two would eventually disagree.
func (s Session) Identity() *auth.Identity {
	scopes := make([]auth.Scope, 0, len(s.Scopes))
	for _, raw := range s.Scopes {
		scopes = append(scopes, auth.Scope(raw))
	}
	return &auth.Identity{Subject: s.Subject, Role: s.Role, Scopes: scopes}
}

// Store persists sessions. It is a port with an ent adapter behind it,
// matching every other storage boundary in this codebase.
type Store interface {
	// Create persists a new session for id and returns the opaque token
	// the browser will hold. The token is returned exactly once and never
	// stored: only its hash is.
	Create(ctx context.Context, id *auth.Identity, idle, absolute time.Duration) (token string, err error)

	// Resolve returns the live session a token names, or ErrNotFound.
	// An expired session resolves to ErrNotFound rather than being
	// returned with a flag, so no caller can forget to check one.
	Resolve(ctx context.Context, token string) (Session, error)

	// Touch slides a session's idle deadline forward and records the
	// visit. It never extends the absolute deadline.
	Touch(ctx context.Context, token string, idle time.Duration) error

	// Delete removes a session. Logout is a hard delete rather than a
	// revoked_at flag: a revoked row that lingers is a liability with no
	// compensating benefit, and one more condition every future query
	// must remember to include.
	Delete(ctx context.Context, token string) error

	// DeleteExpired removes every session past its absolute deadline and
	// reports how many. It is run periodically by exactly one replica,
	// behind the leader election every other cluster-singleton in this
	// platform already uses.
	DeleteExpired(ctx context.Context, now time.Time) (int, error)
}

// NewToken mints a session identifier: 256 bits of cryptographic
// randomness, URL-safe encoded so it is a valid cookie value with no
// further escaping.
func NewToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		// Refusing is the only safe answer. A fallback to a weaker source
		// would mint a guessable credential at exactly the moment the
		// system is least healthy.
		return "", fmt.Errorf("failed to read session entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken reduces a token to what the database stores.
//
// SHA-256 without a salt or a work factor is correct here and would not be
// for a password. A session token is 256 bits of uniform randomness, so
// there is no dictionary to attack and no rainbow table to precompute; the
// only thing hashing has to defeat is an attacker who has read the table,
// and a single fast digest does that completely. A slow KDF would add a
// per-request cost to the hottest path in the application and buy nothing.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// NewCSRFKey mints a session's own CSRF signing key.
func NewCSRFKey() ([]byte, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("failed to read CSRF key entropy: %w", err)
	}
	return buf, nil
}

// CSRFToken derives the value a form or an HTMX request must echo back.
//
// It is an HMAC over the session token under a key that never leaves the
// server, which is what closes the hole in naive double-submit CSRF: an
// attacker who can set a cookie on this origin still cannot compute a
// matching token, because they do not have the key. Deriving it rather
// than storing a second column also means the pair cannot drift, and
// rotating the session rotates the CSRF secret for free.
func CSRFToken(csrfKey []byte, token string) string {
	mac := hmac.New(sha256.New, csrfKey)
	mac.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCSRF reports whether presented matches the token this session
// should produce, compared in constant time so a mismatch cannot be found
// one byte at a time by timing the comparison.
func VerifyCSRF(csrfKey []byte, token, presented string) bool {
	want := CSRFToken(csrfKey, token)
	return subtle.ConstantTimeCompare([]byte(want), []byte(presented)) == 1
}
