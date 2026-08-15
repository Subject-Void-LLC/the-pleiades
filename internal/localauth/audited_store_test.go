// Tests for the audit decorator.
//
// The properties worth proving are what it records, what it deliberately
// does NOT record, and that nothing it writes could carry a password.
package localauth_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
)

// recordingSpy captures what the decorator appended.
type recordingSpy struct {
	entries []activity.Entry
	err     error
}

func (r *recordingSpy) Record(_ context.Context, e activity.Entry) error {
	if r.err != nil {
		return r.err
	}
	r.entries = append(r.entries, e)
	return nil
}

func newAuditedStore(t *testing.T) (localauth.Store, *recordingSpy, *ent.Client) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	spy := &recordingSpy{}
	store := localauth.NewAuditedStore(
		localauth.NewEntStore(client, quietLogger()), spy,
		func(context.Context) string { return "operator@example.test" },
		quietLogger(),
	)
	return store, spy, client
}

func TestAuditedStore_RecordsEveryWrite(t *testing.T) {
	store, spy, client := newAuditedStore(t)
	ctx := context.Background()
	seedUser(t, client, "target@example.test")

	if err := store.SetPassword(ctx, "target@example.test", testPassword, true); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}
	if err := store.ChangePassword(ctx, "target@example.test", testPassword, "a-chosen-password"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if err := store.Unlock(ctx, "target@example.test"); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}

	// ChangePassword is implemented over SetPassword, so it records twice.
	// Asserted on content rather than count, because the count is an
	// implementation detail and the content is the contract.
	var notes []string
	for _, e := range spy.entries {
		notes = append(notes, e.ObjectName)
		if e.Actor == "" {
			t.Error("an entry was recorded with no actor")
		}
		if e.ObjectKind != activity.KindUser {
			t.Errorf("ObjectKind = %q, want %q", e.ObjectKind, activity.KindUser)
		}
	}
	joined := strings.Join(notes, " | ")
	for _, want := range []string{"password set", "password changed by its owner", "account unlocked"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no entry recorded %q; got %s", want, joined)
		}
	}
}

// TestAuditedStore_NeverRecordsAPassword is the property the whole package
// is shaped around, checked at the one place a password could plausibly
// reach a durable row.
func TestAuditedStore_NeverRecordsAPassword(t *testing.T) {
	store, spy, client := newAuditedStore(t)
	ctx := context.Background()
	seedUser(t, client, "target@example.test")

	const distinctive = "a-very-distinctive-passphrase"
	if err := store.SetPassword(ctx, "target@example.test", distinctive, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	for _, e := range spy.entries {
		blob := fmt.Sprintf("%+v", e)
		if strings.Contains(blob, distinctive) {
			t.Fatalf("an activity entry carries the plaintext password: %s", blob)
		}
		if strings.Contains(blob, "$argon2id$") {
			t.Fatalf("an activity entry carries the password hash: %s", blob)
		}
	}
}

// TestAuditedStore_DoesNotRecordSignInAttempts is the deliberate omission.
//
// A failed sign-in is attacker-triggerable and unbounded, so recording one
// per attempt would hand an unauthenticated caller the ability to append
// rows to a durable table until the disk filled. Those go to the log.
func TestAuditedStore_DoesNotRecordSignInAttempts(t *testing.T) {
	store, spy, client := newAuditedStore(t)
	ctx := context.Background()
	seedUser(t, client, "target@example.test")
	if err := store.SetPassword(ctx, "target@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}
	before := len(spy.entries)

	// One success and several failures.
	if _, err := store.Authenticate(ctx, "target@example.test", testPassword); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	for range 3 {
		_, _ = store.Authenticate(ctx, "target@example.test", "wrong")
	}

	if len(spy.entries) != before {
		t.Errorf("sign-in attempts appended %d entries; an unauthenticated caller can grow the audit table",
			len(spy.entries)-before)
	}
}

// TestAuditedStore_ARecordingFailureDoesNotFailTheWrite pins the decision
// the decorator makes in the open: the write already happened, so reporting
// it as failed would be a worse lie than a missing audit line.
func TestAuditedStore_ARecordingFailureDoesNotFailTheWrite(t *testing.T) {
	store, spy, client := newAuditedStore(t)
	ctx := context.Background()
	seedUser(t, client, "target@example.test")
	spy.err = errors.New("the activity table is unreachable")

	if err := store.SetPassword(ctx, "target@example.test", testPassword, false); err != nil {
		t.Fatalf("SetPassword() error = %v; a recording failure must not fail the write", err)
	}
	// And the password really was written.
	if _, err := store.Authenticate(ctx, "target@example.test", testPassword); err != nil {
		t.Errorf("the password was not written: %v", err)
	}
}

// TestAuditedStore_RecordsNothingWhenTheWriteFails is the other direction:
// an audit line for a change that did not happen is worse than none.
func TestAuditedStore_RecordsNothingWhenTheWriteFails(t *testing.T) {
	store, spy, _ := newAuditedStore(t)

	// No user row, so SetPassword refuses.
	if err := store.SetPassword(context.Background(), "ghost@example.test", testPassword, false); err == nil {
		t.Fatal("SetPassword() for a nonexistent user returned nil")
	}
	if len(spy.entries) != 0 {
		t.Errorf("%d entries recorded for a write that failed", len(spy.entries))
	}
}

// TestAuditedStore_ReadsPassThroughUnchanged covers the two methods that
// record nothing, so the decorator is proven to be transparent rather than
// only proven to be noisy.
//
// A decorator that quietly changed a read would be the worst kind of bug
// here: every write test would still pass.
func TestAuditedStore_ReadsPassThroughUnchanged(t *testing.T) {
	store, spy, client := newAuditedStore(t)
	ctx := context.Background()
	seedUser(t, client, "target@example.test")
	if err := store.SetPassword(ctx, "target@example.test", testPassword, true); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}
	before := len(spy.entries)

	account, err := store.Account(ctx, "target@example.test")
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if account.Subject != "target@example.test" {
		t.Errorf("Subject = %q", account.Subject)
	}
	if !account.MustChange {
		t.Error("MustChange was lost through the decorator")
	}

	authenticated, err := store.Authenticate(ctx, "target@example.test", testPassword)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if authenticated.Subject != account.Subject {
		t.Errorf("Authenticate returned %q, Account returned %q", authenticated.Subject, account.Subject)
	}

	if len(spy.entries) != before {
		t.Errorf("reads appended %d entries, want none", len(spy.entries)-before)
	}

	// And an unknown account still reports itself as unknown through the
	// decorator, which is the distinction the administrative commands rely
	// on and the login path must never see.
	if _, err := store.Account(ctx, "nobody@example.test"); !errors.Is(err, localauth.ErrNoSuchAccount) {
		t.Errorf("Account(unknown) error = %v, want ErrNoSuchAccount", err)
	}
}
