// Tests for the ent-backed Store.
//
// Every one runs against a real in-memory SQLite database through the same
// entStore production uses, per RULE 0: the atomic increment is SQL, the
// unique constraint on the owning user is SQL, and the cascade on user
// delete is SQL, so a double standing in for the client would be testing
// the double rather than any of the three claims that matter.
package localauth_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/argon2"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
)

// testPassword is over MinPasswordLen, so ValidatePassword accepts it and
// the tests exercise the real path rather than the refusal.
const testPassword = "a-real-test-password"

// quietLogger discards output. The store logs the real refusal reason on
// every failure, which is the point of the design, and which would
// otherwise bury the test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newStore opens a fresh database and returns a store with a lockout
// threshold a test can reach without paying for ten real derivations. It
// takes testing.TB so a fuzz target can build its store once per worker.
func newStore(t testing.TB, policy localauth.LockoutPolicy) (localauth.Store, *ent.Client) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	return localauth.NewEntStoreWithPolicy(client, quietLogger(), policy), client
}

// seedUser creates the User row a credential must hang off. Creating the
// identity is internal/access's job, which is why the store refuses to do
// it and why a test has to.
func seedUser(t testing.TB, client *ent.Client, email string) *ent.User {
	t.Helper()

	u, err := client.User.Create().SetEmail(email).Save(context.Background())
	if err != nil {
		t.Fatalf("failed to seed user %q: %v", email, err)
	}
	return u
}

func TestSetPassword_ThenAuthenticate(t *testing.T) {
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	seedUser(t, client, "operator@example.test")

	if err := store.SetPassword(ctx, "operator@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	account, err := store.Authenticate(ctx, "operator@example.test", testPassword)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if account.Subject != "operator@example.test" {
		t.Errorf("Subject = %q, want %q", account.Subject, "operator@example.test")
	}
	if account.LockedUntil != nil {
		t.Errorf("LockedUntil = %v, want nil on a fresh credential", account.LockedUntil)
	}
	if account.FailedAttempts != 0 {
		t.Errorf("FailedAttempts = %d, want 0", account.FailedAttempts)
	}
}

func TestAuthenticate_ReportsTheOwnerItMatched(t *testing.T) {
	// Account.UserID is how the audit trail names the account a change
	// happened to, so a successful authentication must report the real
	// owner. The row an update returns carries no edges, and for a while
	// every success reported 0 here.
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	seedUser(t, client, "first@example.test")
	owner := seedUser(t, client, "second@example.test")
	for _, addr := range []string{"first@example.test", "second@example.test"} {
		if err := store.SetPassword(ctx, addr, testPassword, false); err != nil {
			t.Fatalf("SetPassword(%q) error = %v", addr, err)
		}
	}

	account, err := store.Authenticate(ctx, "second@example.test", testPassword)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if account.UserID != owner.ID {
		t.Errorf("UserID = %d, want %d, the user the credential belongs to", account.UserID, owner.ID)
	}
}

func TestAuthenticate_StoresNoPlaintext(t *testing.T) {
	// The claim the whole package rests on, checked against the column
	// rather than against the API: read the row straight out of the
	// database and prove the password is not in it under any encoding this
	// code could have used.
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	seedUser(t, client, "operator@example.test")

	if err := store.SetPassword(ctx, "operator@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	cred, err := client.LocalCredential.Query().Only(ctx)
	if err != nil {
		t.Fatalf("failed to read the credential row: %v", err)
	}
	if cred.PasswordHash == testPassword {
		t.Fatal("the stored hash IS the password")
	}
	if got := cred.PasswordHash; len(got) < 20 || got[:11] != "$argon2id$v" {
		t.Errorf("stored hash = %q, want a PHC-encoded Argon2id string", got)
	}
	// And it must be verifiable, or the column is merely unreadable rather
	// than correct.
	ok, err := localauth.Verify(cred.PasswordHash, testPassword)
	if err != nil || !ok {
		t.Errorf("Verify(stored hash) = %v, %v; want true, nil", ok, err)
	}
}

func TestAuthenticate_EveryFailureIsIndistinguishable(t *testing.T) {
	// The package's central promise: a caller cannot tell a wrong password
	// from an unknown address from an account with no credential. If any of
	// these returned a different error, the login form would be an
	// account-existence oracle.
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()

	seedUser(t, client, "has-password@example.test")
	seedUser(t, client, "no-password@example.test")
	if err := store.SetPassword(ctx, "has-password@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	tests := []struct {
		name     string
		email    string
		password string
	}{
		{"wrong password for a real account", "has-password@example.test", "not-the-password"},
		{"user exists but has no credential", "no-password@example.test", testPassword},
		{"no user row at all", "nobody@example.test", testPassword},
		{"not an email address", "nonsense", testPassword},
		{"empty address", "", testPassword},
		{"empty password", "has-password@example.test", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := store.Authenticate(ctx, tt.email, tt.password)
			if account != nil {
				t.Errorf("Authenticate() returned an account: %+v", account)
			}
			if !errors.Is(err, localauth.ErrInvalidCredentials) {
				t.Errorf("Authenticate() error = %v, want ErrInvalidCredentials", err)
			}
			// Exactly equal, not merely wrapping: a wrapped error carries a
			// distinguishing message, and a handler that logs err would
			// then hand the distinction to whatever reads the logs.
			if err.Error() != localauth.ErrInvalidCredentials.Error() {
				t.Errorf("Authenticate() error message = %q, want the bare sentinel %q",
					err.Error(), localauth.ErrInvalidCredentials.Error())
			}
		})
	}
}

func TestAuthenticate_UnknownAccountIsNotFasterThanAKnownOne(t *testing.T) {
	// The timing half of username enumeration. A store that short circuits
	// on "no such user" skips the derivation entirely, making the unknown
	// case roughly a thousand times faster, which is measurable from
	// anywhere with a stopwatch.
	//
	// The band is deliberately wide. This asserts the derivation HAPPENED,
	// not that the two are indistinguishable to a statistical attacker; the
	// second belongs in the phase's own release gate, against the real
	// handler, with many more samples.
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	seedUser(t, client, "known@example.test")
	if err := store.SetPassword(ctx, "known@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	measure := func(email string) time.Duration {
		const samples = 5
		var total time.Duration
		for range samples {
			start := time.Now()
			_, _ = store.Authenticate(ctx, email, "a-wrong-password-here")
			total += time.Since(start)
		}
		return total / samples
	}

	known := measure("known@example.test")
	unknown := measure("nobody@example.test")

	if unknown < known/4 {
		t.Errorf("an unknown account took %v and a known one took %v; the unknown path is short circuiting "+
			"past the derivation, which makes the login form an account-existence oracle", unknown, known)
	}
}

func TestAuthenticate_OversizedPasswordIsRefusedBeforeTheLookup(t *testing.T) {
	// A password over the KDF's input cap is refused by Verify before any
	// derivation, on the real path and the decoy path alike, which removes
	// the tens of milliseconds that otherwise hide everything else. What was
	// left was the failure counter's UPDATE, which only an existing account
	// runs: an account-existence oracle readable in one or two requests,
	// and a lockout an attacker could run up for free. So it is refused
	// before the address is even looked at, and the two addresses must
	// behave identically: the bare sentinel, no derivation, no row touched.
	policy := localauth.LockoutPolicy{MaxAttempts: 3, Duration: time.Hour}
	store, client := newStore(t, policy)
	ctx := context.Background()
	seedUser(t, client, "known@example.test")
	if err := store.SetPassword(ctx, "known@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	var decoys atomic.Int64
	localauth.ObserveDecoys(store, func() { decoys.Add(1) })
	oversized := strings.Repeat("x", 1025)

	for _, email := range []string{"known@example.test", "nobody@example.test"} {
		before := decoys.Load()
		_, err := store.Authenticate(ctx, email, oversized)
		if err != localauth.ErrInvalidCredentials {
			t.Errorf("Authenticate(%q, oversized) error = %v, want the bare ErrInvalidCredentials", email, err)
		}
		if ran := decoys.Load() - before; ran != 0 {
			t.Errorf("Authenticate(%q, oversized) ran %d decoy derivations; the refusal must come before "+
				"anything that differs between a known and an unknown address", email, ran)
		}
	}

	account, err := store.Account(ctx, "known@example.test")
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if account.FailedAttempts != 0 {
		t.Errorf("FailedAttempts = %d after an oversized password; a password that can never match "+
			"must not count toward the lockout, and counting it is the write that told the two addresses apart",
			account.FailedAttempts)
	}
}

func TestAuthenticate_LocksAfterRepeatedFailures(t *testing.T) {
	policy := localauth.LockoutPolicy{MaxAttempts: 3, Duration: 15 * time.Minute}
	store, client := newStore(t, policy)
	ctx := context.Background()
	seedUser(t, client, "target@example.test")
	if err := store.SetPassword(ctx, "target@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	for i := range policy.MaxAttempts {
		if _, err := store.Authenticate(ctx, "target@example.test", "wrong"); !errors.Is(err, localauth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: error = %v, want ErrInvalidCredentials", i+1, err)
		}
	}

	account, err := store.Account(ctx, "target@example.test")
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if !account.Locked(time.Now()) {
		t.Fatalf("account is not locked after %d failures; LockedUntil = %v, FailedAttempts = %d",
			policy.MaxAttempts, account.LockedUntil, account.FailedAttempts)
	}

	// The correct password must now be refused, and refused
	// indistinguishably. A lock that answers differently is the oracle the
	// single error value exists to prevent, one layer down.
	if _, err := store.Authenticate(ctx, "target@example.test", testPassword); !errors.Is(err, localauth.ErrInvalidCredentials) {
		t.Errorf("a locked account accepted the correct password: error = %v", err)
	}
}

func TestUnlock_RestoresAccess(t *testing.T) {
	policy := localauth.LockoutPolicy{MaxAttempts: 2, Duration: time.Hour}
	store, client := newStore(t, policy)
	ctx := context.Background()
	seedUser(t, client, "locked@example.test")
	if err := store.SetPassword(ctx, "locked@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}
	for range policy.MaxAttempts {
		_, _ = store.Authenticate(ctx, "locked@example.test", "wrong")
	}

	if err := store.Unlock(ctx, "locked@example.test"); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}

	if _, err := store.Authenticate(ctx, "locked@example.test", testPassword); err != nil {
		t.Errorf("Authenticate() after Unlock() error = %v, want success", err)
	}
	account, err := store.Account(ctx, "locked@example.test")
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if account.FailedAttempts != 0 {
		t.Errorf("FailedAttempts = %d after unlock and a success, want 0", account.FailedAttempts)
	}
}

func TestAuthenticate_SuccessResetsTheFailureCounter(t *testing.T) {
	policy := localauth.LockoutPolicy{MaxAttempts: 5, Duration: time.Hour}
	store, client := newStore(t, policy)
	ctx := context.Background()
	seedUser(t, client, "operator@example.test")
	if err := store.SetPassword(ctx, "operator@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	for range 3 {
		_, _ = store.Authenticate(ctx, "operator@example.test", "wrong")
	}
	account, err := store.Authenticate(ctx, "operator@example.test", testPassword)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if account.FailedAttempts != 0 {
		t.Errorf("FailedAttempts = %d after a success, want 0", account.FailedAttempts)
	}
}

func TestSetPassword_RefusesAUserThatDoesNotExist(t *testing.T) {
	// A password proves who a known subject is. Creating the subject is
	// internal/access's job, and doing it here would be a second way to add
	// an identity that the access audit trail never sees.
	store, _ := newStore(t, localauth.DefaultLockoutPolicy)

	err := store.SetPassword(context.Background(), "ghost@example.test", testPassword, false)
	if !errors.Is(err, localauth.ErrNoSuchAccount) {
		t.Errorf("SetPassword() error = %v, want ErrNoSuchAccount", err)
	}
}

func TestSetPassword_ReplacesAndClearsLockoutState(t *testing.T) {
	policy := localauth.LockoutPolicy{MaxAttempts: 2, Duration: time.Hour}
	store, client := newStore(t, policy)
	ctx := context.Background()
	seedUser(t, client, "reset-me@example.test")
	if err := store.SetPassword(ctx, "reset-me@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}
	for range policy.MaxAttempts {
		_, _ = store.Authenticate(ctx, "reset-me@example.test", "wrong")
	}

	const replacement = "a-brand-new-password"
	if err := store.SetPassword(ctx, "reset-me@example.test", replacement, true); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	// Exactly one row: a replace must not leave a second credential behind.
	if n, err := client.LocalCredential.Query().Count(ctx); err != nil || n != 1 {
		t.Errorf("credential count = %d (err %v), want exactly 1", n, err)
	}
	account, err := store.Authenticate(ctx, "reset-me@example.test", replacement)
	if err != nil {
		t.Fatalf("Authenticate() with the replacement error = %v", err)
	}
	if !account.MustChange {
		t.Error("MustChange = false on an administratively set password, want true")
	}
	// The old password must be dead.
	if _, err := store.Authenticate(ctx, "reset-me@example.test", testPassword); !errors.Is(err, localauth.ErrInvalidCredentials) {
		t.Error("the previous password still authenticates after a reset")
	}
}

func TestChangePassword_RequiresTheCurrentPassword(t *testing.T) {
	// Holding a session must not be enough on its own. A session is a
	// bearer credential with an eight hour ceiling, and letting one replace
	// the password it was minted from turns a stolen cookie into permanent
	// account takeover.
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	seedUser(t, client, "self@example.test")
	if err := store.SetPassword(ctx, "self@example.test", testPassword, true); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	const replacement = "chosen-by-the-owner"
	if err := store.ChangePassword(ctx, "self@example.test", "the-wrong-old-one", replacement); !errors.Is(err, localauth.ErrInvalidCredentials) {
		t.Errorf("ChangePassword() with a wrong current password error = %v, want ErrInvalidCredentials", err)
	}
	// And it must not have taken effect anyway.
	if _, err := store.Authenticate(ctx, "self@example.test", replacement); err == nil {
		t.Fatal("the new password works after a refused change")
	}

	if err := store.ChangePassword(ctx, "self@example.test", testPassword, replacement); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	account, err := store.Authenticate(ctx, "self@example.test", replacement)
	if err != nil {
		t.Fatalf("Authenticate() with the changed password error = %v", err)
	}
	if account.MustChange {
		t.Error("MustChange = true after the owner chose their own password, want false")
	}
}

func TestSetPassword_EnforcesTheLengthFloorAndTheEmailRule(t *testing.T) {
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	seedUser(t, client, "user@example.test")

	tests := []struct {
		name     string
		password string
	}{
		{"too short", "short"},
		{"exactly one under the floor", "12345678901"},
		{"the account's own address", "user@example.test"},
		{"the address in a different case", "USER@example.test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := store.SetPassword(ctx, "user@example.test", tt.password, false); !errors.Is(err, localauth.ErrWeakPassword) {
				t.Errorf("SetPassword(%q) error = %v, want ErrWeakPassword", tt.password, err)
			}
		})
	}
}

func TestAuthenticate_UpgradesAStaleHashOnLogin(t *testing.T) {
	// Rehash-on-login, proven against the stored column rather than
	// asserted. The upgrade must happen only after a successful verify,
	// because that is the only moment the plaintext exists.
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	user := seedUser(t, client, "stale@example.test")

	// Write a credential at a deliberately weaker cost, the way a row
	// created before a parameter bump would look.
	stale := localauth.Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}
	salt := []byte("0123456789abcdef")
	staleHash := staleHashFor(t, stale, salt, testPassword)
	if _, err := client.LocalCredential.Create().
		SetUser(user).
		SetPasswordHash(staleHash).
		SetPasswordChangedAt(time.Now()).
		Save(ctx); err != nil {
		t.Fatalf("failed to seed a stale credential: %v", err)
	}

	if _, err := store.Authenticate(ctx, "stale@example.test", testPassword); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	cred, err := client.LocalCredential.Query().Only(ctx)
	if err != nil {
		t.Fatalf("failed to reread the credential: %v", err)
	}
	if cred.PasswordHash == staleHash {
		t.Error("the stale hash was not upgraded after a successful login")
	}
	needs, err := localauth.NeedsRehash(cred.PasswordHash)
	if err != nil {
		t.Fatalf("NeedsRehash() error = %v", err)
	}
	if needs {
		t.Error("the upgraded hash still reports as needing a rehash")
	}
	// And the password must still work through the new hash.
	if _, err := store.Authenticate(ctx, "stale@example.test", testPassword); err != nil {
		t.Errorf("Authenticate() after the upgrade error = %v", err)
	}
}

// staleHashFor derives a hash at explicit parameters, so the rehash test
// can produce the "written before the last cost bump" shape without
// exporting a weaker hashing entry point from the package itself.
func staleHashFor(t *testing.T, p localauth.Params, salt []byte, password string) string {
	t.Helper()

	// Derived through the same public Encode the package writes with, so
	// the fixture cannot drift from the real format.
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return localauth.Encode(p, salt, key)
}

func TestAuthenticate_RefusesACorruptedHashWithoutCrashing(t *testing.T) {
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	user := seedUser(t, client, "corrupt@example.test")

	if _, err := client.LocalCredential.Create().
		SetUser(user).
		SetPasswordHash("$argon2id$v=19$m=4294967295,t=2,p=1$c2FsdA$a2V5").
		SetPasswordChangedAt(time.Now()).
		Save(ctx); err != nil {
		t.Fatalf("failed to seed a corrupted credential: %v", err)
	}

	// The row claims four terabytes of memory. It must be refused by the
	// parser rather than handed to the KDF, which would hang the process.
	done := make(chan error, 1)
	go func() {
		_, err := store.Authenticate(ctx, "corrupt@example.test", testPassword)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, localauth.ErrInvalidCredentials) {
			t.Errorf("Authenticate() error = %v, want ErrInvalidCredentials", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Authenticate() did not return within 30s; the memory parameter reached the KDF")
	}
}

func TestDeletingAUserDeletesItsCredential(t *testing.T) {
	// The cascade is a SQL constraint, so this is the only way to check it.
	// A credential whose owner is gone is a verifiable password for an
	// identity that no longer exists.
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	user := seedUser(t, client, "departing@example.test")
	if err := store.SetPassword(ctx, "departing@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	if err := client.User.DeleteOne(user).Exec(ctx); err != nil {
		t.Fatalf("failed to delete the user: %v", err)
	}

	if n, err := client.LocalCredential.Query().Count(ctx); err != nil || n != 0 {
		t.Errorf("credential count = %d (err %v) after deleting its owner, want 0", n, err)
	}
	if _, err := store.Authenticate(ctx, "departing@example.test", testPassword); !errors.Is(err, localauth.ErrInvalidCredentials) {
		t.Error("a deleted user's password still authenticates")
	}
}

func TestOneUserCannotHoldTwoCredentials(t *testing.T) {
	// Enforced by the unique index on the owning user, not by application
	// code, so a second write path added later cannot bypass it.
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	user := seedUser(t, client, "single@example.test")
	if err := store.SetPassword(ctx, "single@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	_, err := client.LocalCredential.Create().
		SetUser(user).
		SetPasswordHash("$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U").
		SetPasswordChangedAt(time.Now()).
		Save(ctx)
	if err == nil {
		t.Error("a second credential was accepted for one user; the unique constraint is not doing its job")
	}
}

func TestAccount_ReportsNoSuchAccountForAnUnknownAddress(t *testing.T) {
	// Account is the administrative read and MAY distinguish, which is the
	// deliberate counterpart to Authenticate's single error: an operator
	// running a reset needs to know they typed the address wrong.
	store, _ := newStore(t, localauth.DefaultLockoutPolicy)

	if _, err := store.Account(context.Background(), "nobody@example.test"); !errors.Is(err, localauth.ErrNoSuchAccount) {
		t.Errorf("Account() error = %v, want ErrNoSuchAccount", err)
	}
}

func TestNormalizeEmail_MakesTheAddressTheJoinKey(t *testing.T) {
	// A credential keyed by a differently normalized address than its user
	// row would be a credential that authenticates nobody.
	store, client := newStore(t, localauth.DefaultLockoutPolicy)
	ctx := context.Background()
	seedUser(t, client, "mixed@example.test")
	if err := store.SetPassword(ctx, "  MiXeD@Example.TEST  ", testPassword, false); err != nil {
		t.Fatalf("SetPassword() with a differently cased address error = %v", err)
	}

	if _, err := store.Authenticate(ctx, "mixed@example.test", testPassword); err != nil {
		t.Errorf("Authenticate() error = %v; the address did not normalize to the same key", err)
	}
}
