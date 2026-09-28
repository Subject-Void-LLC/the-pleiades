// The ent-backed Store, and the lockout arithmetic that has to be a row
// rather than a variable.
//
// Every method here goes through the same two-step lookup: normalize the
// address, then resolve User -> LocalCredential. The credential is never
// queried by anything but its owning user, so there is no path that
// produces a credential without knowing whose it is.
package localauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/localcredential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/user"
)

// entStore is the ent implementation of Store.
type entStore struct {
	client *ent.Client
	policy LockoutPolicy
	logger *slog.Logger

	// gate bounds concurrent Argon2id derivations. Every path in this file
	// that derives goes through it, including the decoy burn: a gate the
	// unknown-account path skipped would be a gate an attacker could walk
	// straight past by guessing addresses that do not exist.
	gate *Gate

	// decoy is the derivation a refusal with no real hash to verify runs,
	// so that path costs what a real verification costs. It is BurnDecoy in
	// every store this package builds. It is a field only so a test can
	// observe WHICH path a call took, by counting, the same seam as now
	// below; a test that wraps it still runs the real derivation.
	decoy func(password string)

	// now is injectable so lockout expiry is testable without sleeping.
	// The rest of the codebase uses the same seam (internal/ui/session's
	// entStore holds an identical field).
	now func() time.Time
}

// NewEntStore builds a Store over client with the default lockout policy
// and the default derivation gate.
func NewEntStore(client *ent.Client, logger *slog.Logger) Store {
	return NewEntStoreWithPolicy(client, logger, DefaultLockoutPolicy)
}

// NewEntStoreWithPolicy builds a Store with an explicit lockout policy, for
// a deployment that has chosen different thresholds and for tests that
// cannot afford ten real derivations.
func NewEntStoreWithPolicy(client *ent.Client, logger *slog.Logger, policy LockoutPolicy) Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &entStore{
		client: client,
		policy: policy,
		logger: logger,
		gate:   NewGate(DefaultMaxConcurrentDerivations),
		decoy:  BurnDecoy,
		now:    time.Now,
	}
}

// burnDecoy runs a derivation that cannot succeed, through the gate.
//
// Gate saturation is swallowed here on purpose. This call exists only to
// spend time, and if it could not get a slot it spent that time waiting
// instead, which serves the same end. Returning an error would also hand
// the caller a way to distinguish this path from a real verification, which
// is precisely what it exists to prevent.
func (s *entStore) burnDecoy(ctx context.Context, password string) {
	_ = s.gate.Do(ctx, func() { s.decoy(password) })
}

// verify runs a real verification through the gate.
//
// Gate saturation IS surfaced here, unlike in burnDecoy, and the asymmetry
// is deliberate. A verification that never ran proved nothing, so reporting
// it as a wrong password would be a false negative that locks out a
// legitimate user under load and counts against their lockout threshold.
func (s *entStore) verify(ctx context.Context, encoded, password string) (bool, error) {
	var (
		ok     bool
		verErr error
	)
	if err := s.gate.Do(ctx, func() { ok, verErr = Verify(encoded, password) }); err != nil {
		return false, err
	}
	return ok, verErr
}

// hash derives a new credential through the gate.
func (s *entStore) hash(ctx context.Context, password string) (string, error) {
	var (
		encoded string
		hashErr error
	)
	if err := s.gate.Do(ctx, func() { encoded, hashErr = Hash(password) }); err != nil {
		return "", err
	}
	return encoded, hashErr
}

// Authenticate proves that password belongs to email.
//
// Every failure path returns ErrInvalidCredentials, and every failure path
// that depends on the address performs one Argon2id derivation, whether or
// not there is a hash to verify against. That half is easy to omit and
// expensive to omit: a short circuit on "no such user" skips the derivation
// and makes the unknown-account case tens of milliseconds faster, which
// turns the login form into an account-existence oracle anyone can query
// with a stopwatch. The real reason for each refusal goes to the logger.
//
// The one refusal made before the address is read is an oversized
// password, and it is made there precisely so it depends on nothing about
// the account. Verify would refuse it without deriving on every path, which
// takes away the derivation's tens of milliseconds, and what remained was
// the failure counter's write, which only an existing account performs.
func (s *entStore) Authenticate(ctx context.Context, email, password string) (*Account, error) {
	if len(password) > maxPasswordLen {
		// Nothing is looked up, derived or counted: a password that no
		// stored credential can hold is not a guess at one, and every
		// address gets this same answer in the same time.
		s.logger.InfoContext(ctx, "local authentication refused",
			slog.String("reason", "password over the length limit"))
		return nil, ErrInvalidCredentials
	}

	subject, err := NormalizeEmail(email)
	if err != nil {
		// A malformed address never matches a row, so it costs the same as
		// one that simply does not exist. It is refused here rather than
		// passed to the query: a NUL, for one, is a query error on Postgres,
		// and that error came back fast and without the decoy. The address
		// is not logged, since it is attacker text that failed the check
		// for being unsafe to print.
		s.burnDecoy(ctx, password)
		s.logger.InfoContext(ctx, "local authentication refused",
			slog.String("reason", "malformed address"))
		return nil, ErrInvalidCredentials
	}

	cred, err := s.credentialFor(ctx, subject)
	if err != nil {
		if errors.Is(err, ErrNoSuchAccount) {
			s.burnDecoy(ctx, password)
			s.logger.InfoContext(ctx, "local authentication refused",
				slog.String("subject", subject), slog.String("reason", "no local credential"))
			return nil, ErrInvalidCredentials
		}
		// Logged here because nothing above logs it: the login handler
		// answers every error with the same page, so a database failure
		// during sign-in was otherwise visible to nobody.
		s.logger.ErrorContext(ctx, "local authentication could not load a credential",
			slog.String("subject", subject), slog.String("error", err.Error()))
		return nil, fmt.Errorf("localauth: failed to load credential: %w", err)
	}

	now := s.now()
	if cred.LockedUntil != nil && cred.LockedUntil.After(now) {
		// Burned before returning, so a locked account is not detectable by
		// being fast. Without this, lockout itself becomes the oracle that
		// the indistinguishable error above exists to prevent.
		s.burnDecoy(ctx, password)
		s.logger.WarnContext(ctx, "local authentication refused",
			slog.String("subject", subject), slog.String("reason", "account locked"),
			slog.Time("locked_until", *cred.LockedUntil))
		return nil, ErrInvalidCredentials
	}

	ok, err := s.verify(ctx, cred.PasswordHash, password)
	if errors.Is(err, ErrBusy) {
		// The derivation never ran, so nothing was proved and nothing is
		// counted against the account. Surfaced as itself rather than as an
		// authentication failure, per verify's own doc.
		return nil, err
	}
	if err != nil {
		// A corrupted row. Distinguished in the log because an operator
		// needs to know a credential is unusable rather than merely wrong,
		// and not distinguished in the return value for the usual reason.
		s.logger.ErrorContext(ctx, "local authentication refused",
			slog.String("subject", subject), slog.String("reason", "stored hash is unusable"),
			slog.String("error", err.Error()))
		return nil, ErrInvalidCredentials
	}
	if !ok {
		s.recordFailure(ctx, cred, subject, now)
		return nil, ErrInvalidCredentials
	}

	return s.recordSuccess(ctx, cred, subject, password)
}

// recordFailure increments the counter and locks the account once it
// crosses the threshold.
//
// The increment is ent's AddFailedAttempts, which is a single "SET col =
// col + 1" statement rather than a read in Go followed by a write. A
// read-modify-write here would let a parallel burst all read the same
// starting value and count as one attempt, which is precisely the attack
// shape lockout exists to stop.
func (s *entStore) recordFailure(ctx context.Context, cred *ent.LocalCredential, subject string, now time.Time) {
	updated, err := s.client.LocalCredential.UpdateOne(cred).
		AddFailedAttempts(1).
		Save(ctx)
	if err != nil {
		// Logged rather than returned: the authentication has already
		// failed, and turning a bookkeeping error into a different response
		// would be the oracle again.
		s.logger.ErrorContext(ctx, "failed to record a login failure",
			slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}

	s.logger.WarnContext(ctx, "local authentication refused",
		slog.String("subject", subject), slog.String("reason", "wrong password"),
		slog.Int("failed_attempts", updated.FailedAttempts))

	if s.policy.MaxAttempts <= 0 || updated.FailedAttempts < s.policy.MaxAttempts {
		return
	}

	// Setting the deadline is idempotent, so it is safe that this is a
	// second statement rather than part of the increment above: two
	// concurrent failures that both cross the threshold write the same
	// kind of lock, and the later one simply extends it.
	until := now.Add(s.policy.Duration)
	if _, err := s.client.LocalCredential.UpdateOne(updated).SetLockedUntil(until).Save(ctx); err != nil {
		s.logger.ErrorContext(ctx, "failed to lock an account after repeated failures",
			slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}
	s.logger.WarnContext(ctx, "account locked after repeated login failures",
		slog.String("subject", subject), slog.Int("failed_attempts", updated.FailedAttempts),
		slog.Time("locked_until", until))
}

// recordSuccess clears the failure state and upgrades a stale hash.
func (s *entStore) recordSuccess(
	ctx context.Context, cred *ent.LocalCredential, subject, password string,
) (*Account, error) {
	update := s.client.LocalCredential.UpdateOne(cred).
		SetFailedAttempts(0).
		ClearLockedUntil()

	// Rehash on login, only after a successful verify, because that is the
	// only moment the plaintext exists to re-derive from.
	stale, err := NeedsRehash(cred.PasswordHash)
	if err == nil && stale {
		rehashed, hashErr := s.hash(ctx, password)
		if hashErr != nil {
			// Logged and continued. A stale cost is worse than the current
			// hash; it is not worse than refusing an authentication that
			// has already succeeded.
			s.logger.ErrorContext(ctx, "failed to upgrade a password hash to current parameters",
				slog.String("subject", subject), slog.String("error", hashErr.Error()))
		} else {
			update = update.SetPasswordHash(rehashed)
			s.logger.InfoContext(ctx, "upgraded a password hash to current parameters",
				slog.String("subject", subject))
		}
	}

	updated, err := update.Save(ctx)
	if err != nil {
		// Same reasoning: the password was correct, so the caller is
		// authenticated. Report the bookkeeping failure and hand back what
		// was proven, built from the pre-update row.
		s.logger.ErrorContext(ctx, "failed to clear login failure state after a success",
			slog.String("subject", subject), slog.String("error", err.Error()))
		return projectAccount(subject, cred), nil
	}

	// The row an update hands back carries no edges, so the owner that
	// credentialFor eager-loaded is carried across. Without it every
	// successful authentication reported UserID 0, which nothing read until
	// a fuzz target needed to know which row a sign-in had matched.
	updated.Edges.User = cred.Edges.User
	return projectAccount(subject, updated), nil
}

// Account reports what is known about one credential without
// authenticating.
func (s *entStore) Account(ctx context.Context, email string) (*Account, error) {
	subject, err := NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	cred, err := s.credentialFor(ctx, subject)
	if err != nil {
		return nil, err
	}
	return projectAccount(subject, cred), nil
}

// SetPassword writes a credential for an existing user.
//
// It refuses to create the user, deliberately. A password is proof of
// identity for a subject the deployment already knows; creating the subject
// is internal/access's job, and doing both here would give this package a
// second way to add an identity that the access audit trail never sees.
func (s *entStore) SetPassword(ctx context.Context, email, password string, mustChange bool) error {
	subject, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	if err := ValidatePassword(subject, password); err != nil {
		return err
	}

	owner, err := s.client.User.Query().Where(user.EmailEQ(subject)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("%w: %q has no user record; create the user first", ErrNoSuchAccount, subject)
		}
		return fmt.Errorf("localauth: failed to load user: %w", err)
	}

	hash, err := s.hash(ctx, password)
	if err != nil {
		return fmt.Errorf("localauth: failed to hash password: %w", err)
	}
	now := s.now()

	existing, err := s.credentialFor(ctx, subject)
	switch {
	case err == nil:
		// Replacing a credential also clears the lockout state, since the
		// secret being guessed no longer exists.
		_, err = s.client.LocalCredential.UpdateOne(existing).
			SetPasswordHash(hash).
			SetPasswordChangedAt(now).
			SetMustChange(mustChange).
			SetFailedAttempts(0).
			ClearLockedUntil().
			Save(ctx)
	case errors.Is(err, ErrNoSuchAccount):
		_, err = s.client.LocalCredential.Create().
			SetUser(owner).
			SetPasswordHash(hash).
			SetPasswordChangedAt(now).
			SetMustChange(mustChange).
			Save(ctx)
	}
	if err != nil {
		return fmt.Errorf("localauth: failed to write credential: %w", err)
	}

	s.logger.InfoContext(ctx, "local password set",
		slog.String("subject", subject), slog.Bool("must_change", mustChange))
	return nil
}

// ChangePassword is the self-service path and requires the current
// password.
//
// Holding a session is not enough on its own: a session is a bearer
// credential with an eight hour ceiling, and letting one replace the
// password it was minted from would turn a stolen cookie into permanent
// account takeover.
func (s *entStore) ChangePassword(ctx context.Context, email, oldPassword, newPassword string) error {
	if _, err := s.Authenticate(ctx, email, oldPassword); err != nil {
		return err
	}
	// mustChange is cleared here: the owner has now chosen this one.
	return s.SetPassword(ctx, email, newPassword, false)
}

// Unlock clears a lockout without touching the password.
func (s *entStore) Unlock(ctx context.Context, email string) error {
	subject, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	cred, err := s.credentialFor(ctx, subject)
	if err != nil {
		return err
	}
	if _, err := s.client.LocalCredential.UpdateOne(cred).
		SetFailedAttempts(0).
		ClearLockedUntil().
		Save(ctx); err != nil {
		return fmt.Errorf("localauth: failed to unlock account: %w", err)
	}
	s.logger.InfoContext(ctx, "local account unlocked", slog.String("subject", subject))
	return nil
}

// credentialFor resolves one subject's credential row, or ErrNoSuchAccount.
//
// A missing user and a user with no credential are the same answer here on
// purpose: both mean "this address cannot authenticate locally", and
// Authenticate must not be able to tell them apart.
func (s *entStore) credentialFor(ctx context.Context, subject string) (*ent.LocalCredential, error) {
	cred, err := s.client.LocalCredential.Query().
		Where(localcredential.HasUserWith(user.EmailEQ(subject))).
		// The owner is eager-loaded because Account carries its id for the
		// audit trail, and a lazy edge would mean a second query on every
		// authentication rather than only on the writes that record one.
		WithUser().
		Only(ctx)
	switch {
	case ent.IsNotFound(err):
		return nil, fmt.Errorf("%w: %q", ErrNoSuchAccount, subject)
	case err != nil:
		return nil, err
	}
	return cred, nil
}

// projectAccount builds the read projection.
//
// This is the one place a row becomes an Account, and Account has no field
// for cred.PasswordHash, so the hash cannot leave this package by being
// copied into a struct somebody later marshals.
func projectAccount(subject string, cred *ent.LocalCredential) *Account {
	account := &Account{
		Subject:           subject,
		FailedAttempts:    cred.FailedAttempts,
		LockedUntil:       cred.LockedUntil,
		PasswordChangedAt: cred.PasswordChangedAt,
		MustChange:        cred.MustChange,
	}
	// Guarded rather than dereferenced: the edge is loaded on the read path
	// and not on a row this package just wrote, so an unloaded owner is a
	// zero id rather than a panic. The audit decorator re-reads through
	// Account for exactly that reason.
	if cred.Edges.User != nil {
		account.UserID = cred.Edges.User.ID
	}
	return account
}
