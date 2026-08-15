// Package localauth verifies locally stored passwords, per PLAN.md Section
// 18.1's "Hashed passwords stored in the Postgres DB".
//
// # The one thing to understand about this package
//
// A password enters only as a function ARGUMENT and never comes to rest on
// an exported type. Account, the projection every read method here returns,
// HAS NO FIELD a password or a hash could occupy, so a caller holding one
// cannot leak either by forgetting something. That is internal/credstore's
// boundary applied a second time, for the reason its own package doc gives:
// a rule of the form "handlers must remember not to select that column"
// holds until the day somebody adds a handler, and the failure is silent
// and permanent, because a password that reached a response reached a log,
// a proxy and a browser history.
//
// The asymmetry is deliberate and is the whole shape of the package.
// Passwords go IN through Authenticate, SetPassword and ChangePassword.
// Nothing comes back out: there is no method here that returns a password
// or a hash, and adding one would defeat the boundary the package exists to
// draw. internal/archtest keeps that structural rather than aspirational.
//
// # Authentication has exactly one failure value, on purpose
//
// Authenticate returns ErrInvalidCredentials for a wrong password, for an
// email with no user row, for a user with no local credential, and for a
// locked account. A caller cannot tell those apart, because the alternative
// is an account-existence oracle that anybody can query from a login form,
// and "the handler must remember to answer identically" is the class of
// rule this package already refuses everywhere else. The real reason goes
// to the store's logger, where an operator can read it and an attacker
// cannot. The timing is equalized too: see DecoyHash.
//
// # What this package does NOT decide
//
// It does not decide what an authenticated caller may DO. Authenticate
// proves a subject and returns that subject; deriving an auth.Identity's
// Role and Scopes from the subject's RoleBindings happens above this
// package, through auth.ScopeResolver, so there is exactly one place that
// answers "what may this person reach" rather than two that can drift.
package localauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidCredentials is the single authentication failure.
//
// See the package doc: a wrong password, an unknown email, an account with
// no local credential and a locked account all produce this, and a caller
// has no way to tell which. That is the point.
var ErrInvalidCredentials = errors.New("localauth: invalid credentials")

// ErrNoSuchAccount is returned by the administrative methods, which are
// reached from a host shell rather than from an HTTP request.
//
// Distinguishing "no such user" is correct THERE and wrong on the login
// path, and the split between this error and ErrInvalidCredentials is where
// that distinction lives. An operator running a reset needs to know they
// typed the address wrong; an unauthenticated caller does not.
var ErrNoSuchAccount = errors.New("localauth: no such account")

// ErrWeakPassword is returned when a proposed password is refused before
// it is ever hashed.
var ErrWeakPassword = errors.New("localauth: password refused")

// MinPasswordLen is the shortest password this package will store.
//
// Length is the only property enforced here. Composition rules (an upper,
// a digit, a symbol) are deliberately absent: they measurably push people
// toward predictable substitutions without adding entropy, and NIST SP
// 800-63B withdrew the recommendation. Password history, expiry and
// breached-password checks are named out of scope by the phase that adds
// this package rather than silently omitted.
const MinPasswordLen = 12

// Account is the read projection of one local credential.
//
// It carries what an operator or a handler needs to know ABOUT a
// credential and nothing that could authenticate as one. There is no
// PasswordHash field and there is no field a hash could be assigned to,
// which is the property internal/archtest and the package boundary exist to
// preserve. internal/auth's ScopeCredentialRead doc makes the same point
// about credentials in the identical words: granting sight of this is
// granting a catalog, not a keyring.
type Account struct {
	// Subject is the normalized email, which is also the join key against
	// a token's subject and against User.email.
	Subject string

	// UserID is the owning User's row id.
	//
	// Present because the audit trail needs it: an activity entry names an
	// object by kind and id, and a credential change is a change to a USER
	// as far as an operator reading the stream is concerned. It is not a
	// credential and discloses nothing, unlike every field this type
	// deliberately omits.
	UserID int

	// FailedAttempts is the count since the last success. It is here so an
	// operator can see an account under attack, not so a caller can make a
	// decision with it; the store owns the lockout decision.
	FailedAttempts int

	// LockedUntil is nil when the account is not locked. Nillable rather
	// than a zero time, because "not locked" and "locked until long ago"
	// are different facts and the zero time reads as the second.
	LockedUntil *time.Time

	// PasswordChangedAt is when this credential was last written.
	PasswordChangedAt time.Time

	// MustChange marks a credential an administrator wrote rather than one
	// its owner chose, so a bootstrap or reset password can be required to
	// be replaced instead of silently becoming permanent.
	MustChange bool
}

// Locked reports whether the account is locked as of now.
func (a *Account) Locked(now time.Time) bool {
	return a.LockedUntil != nil && a.LockedUntil.After(now)
}

// LockoutPolicy bounds online guessing against ONE account.
//
// This is a different axis from internal/api's rate limiter and does not
// overlap with it. That limiter keys on the CALLER (an authenticated
// subject, else the source address) and therefore bounds how fast one
// client may ask; it never keys on the account being attacked, so a
// distributed attacker gets a fresh budget per source address, all aimed at
// one victim. This keys on the TARGET. internal/ui/session's own tokenBytes
// comment already conceded the gap in writing, noting that a session
// identifier must survive unlimited online guessing precisely because there
// is "no account name in play". On a login form there is.
type LockoutPolicy struct {
	// MaxAttempts is the number of consecutive failures that locks the
	// account. Zero disables lockout entirely, which is a real choice an
	// operator can make and not the default.
	MaxAttempts int

	// Duration is how long a lock lasts.
	//
	// A lock is ALWAYS time bounded. A permanent lock on the break-glass
	// account PLAN.md Section 18.1 exists to harden is a remote off switch
	// an unauthenticated attacker can throw, and the recovery path would
	// then require a second administrator who may not exist. Recovery is
	// also always available out of band through the controller's own
	// unlock subcommand, which runs on the host rather than over HTTP.
	Duration time.Duration
}

// DefaultLockoutPolicy is ten consecutive failures, then fifteen minutes.
//
// The expiry does NOT reset the counter, so the next burst re-locks
// immediately rather than handing an attacker ten fresh attempts every
// fifteen minutes. Only a successful authentication clears it.
//
// On by default, following the reasoning internal/api's rate limiter
// already records for its own defaults: a protection nobody remembers to
// enable does not defend against a mistake nobody meant to make. No account
// is exempt, because exempting the highest-value account makes it the only
// one an attacker may grind without limit.
var DefaultLockoutPolicy = LockoutPolicy{
	MaxAttempts: 10,
	Duration:    15 * time.Minute,
}

// Store is the local credential port.
//
// The ent adapter is the only implementation, and callers depend on this
// rather than on internal/ent so the lockout and verification logic is
// testable without a database and so the persistence choice stays
// replaceable. auth.RoleBindingRepository and auth.TeamLookup draw the same
// boundary for the same reason.
type Store interface {
	// Authenticate proves that password belongs to email and returns the
	// resulting account.
	//
	// It returns ErrInvalidCredentials for every failure, without
	// distinction. On success it resets the failure counter, clears any
	// lock, and transparently upgrades a hash derived at stale parameters.
	Authenticate(ctx context.Context, email, password string) (*Account, error)

	// Account reports what is known about one credential, without
	// authenticating. It returns ErrNoSuchAccount when the user has no
	// local credential. Administrative surfaces use it; the login path
	// does not, because a caller that can ask "does this account exist"
	// is the oracle Authenticate exists to avoid.
	Account(ctx context.Context, email string) (*Account, error)

	// SetPassword writes a credential for an existing user, creating one
	// if the user has none and replacing it if they do. It is the
	// administrative path: bootstrap and reset. mustChange marks the
	// result as needing replacement at first use.
	SetPassword(ctx context.Context, email, password string, mustChange bool) error

	// ChangePassword is the self-service path. It requires the current
	// password, so possession of a session is not by itself enough to
	// replace the credential that session was minted from.
	ChangePassword(ctx context.Context, email, oldPassword, newPassword string) error

	// Unlock clears a lockout without changing the password. It is the
	// out-of-band recovery LockoutPolicy.Duration's doc refers to.
	Unlock(ctx context.Context, email string) error
}

// NormalizeEmail trims and lowercases a submitted address.
//
// It matches internal/access's own normalization deliberately: that
// package's rule is that the address is the join key against a token's
// subject, so two rows differing only in case would be two identities for
// one person. A credential keyed by a differently-normalized address than
// the user row it belongs to would be a credential that authenticates
// nobody, or worse, one that authenticates a second identity.
func NormalizeEmail(email string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(email))
	if trimmed == "" {
		return "", fmt.Errorf("%w: an account needs an email address", ErrNoSuchAccount)
	}
	if !strings.Contains(trimmed, "@") {
		return "", fmt.Errorf("%w: %q is not an email address", ErrNoSuchAccount, trimmed)
	}
	return trimmed, nil
}

// ValidatePassword refuses a proposed password before it is hashed.
//
// It runs on the way in only. Nothing here inspects a stored credential,
// so tightening the rule later cannot lock out an account whose existing
// password no longer satisfies it; that account is asked to change at its
// next change, which is a policy decision for whichever phase adds password
// expiry rather than something to smuggle in here.
func ValidatePassword(email, password string) error {
	switch {
	case len(password) < MinPasswordLen:
		return fmt.Errorf("%w: needs at least %d characters", ErrWeakPassword, MinPasswordLen)
	case len(password) > maxPasswordLen:
		return fmt.Errorf("%w: is over the %d character limit", ErrWeakPassword, maxPasswordLen)
	case strings.EqualFold(strings.TrimSpace(password), strings.TrimSpace(email)):
		// Checked because bootstrap and reset are typed at a terminal by
		// somebody who already has the address in front of them, which is
		// exactly the situation that produces this.
		return fmt.Errorf("%w: cannot be the account's own email address", ErrWeakPassword)
	}
	return nil
}
