// Tests for signing key custody.
//
// The client every test here uses carries the real envelope hook and
// interceptor, because the whole value of this package is that a seed
// goes in through a sealed column and comes back usable. A test against a
// bare client would exercise two string fields.
package meshkey_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/meshsigningkey"
	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/meshkey"

	_ "github.com/mattn/go-sqlite3"
)

// hookedClient returns a client wired exactly as cmd/controller wires
// one, which is what makes these tests representative.
func hookedClient(t *testing.T, name string) *ent.Client {
	t.Helper()
	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService: %v", err)
	}
	client := enttest.Open(t, "sqlite3", "file:"+name+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	client.MeshSigningKey.Use(crypto.MeshSigningKeySeedHook(svc))
	client.MeshSigningKey.Intercept(crypto.MeshSigningKeySeedInterceptor(svc))
	return client
}

// newAccount mints a throwaway operator and account.
func newAccount(t *testing.T, name string) *meshid.Account {
	t.Helper()
	op, err := meshid.NewOperator("custody-test")
	if err != nil {
		t.Fatalf("NewOperator: %v", err)
	}
	acct, err := meshid.NewAccount(op, name)
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	return acct
}

// TestSavedKeyIssuesAWorkingCredential is the round trip, and it asserts
// the outcome that matters rather than the column.
//
// A seed that survives sealing and unsealing but can no longer sign is
// indistinguishable from a working one until a broker refuses a
// connection, so the assertion is that a credential actually comes out.
func TestSavedKeyIssuesAWorkingCredential(t *testing.T) {
	ctx := context.Background()
	client := hookedClient(t, "roundtrip")
	store := meshkey.NewStore(client)
	acct := newAccount(t, "PLEIADES")

	if err := store.Save(ctx, "key-1", acct); err != nil {
		t.Fatalf("Save: %v", err)
	}

	issuer, err := store.Issuer(ctx, acct.Subject)
	if err != nil {
		t.Fatalf("Issuer: %v", err)
	}
	cred, err := issuer.Issue(meshid.FleetRunnerGrant("runner-1"), meshid.DefaultFleetExpiry)
	if err != nil {
		t.Fatalf("issuing from a stored key: %v", err)
	}
	if len(cred.Creds) == 0 {
		t.Error("the stored key issued an empty credential")
	}
}

// TestTheSeedIsNeverStoredInPlaintext is the property the whole entity
// exists for, asserted by reading the column with no interceptor.
func TestTheSeedIsNeverStoredInPlaintext(t *testing.T) {
	ctx := context.Background()
	client := hookedClient(t, "plaintext")
	acct := newAccount(t, "PLEIADES")

	if err := meshkey.NewStore(client).Save(ctx, "key-1", acct); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A second client over the same database with NO interceptor, so this
	// observes what is actually on disk rather than what a read returns.
	raw := enttest.Open(t, "sqlite3", "file:plaintext?mode=memory&cache=shared&_fk=1")
	defer func() { _ = raw.Close() }()
	stored := raw.MeshSigningKey.Query().Where(meshsigningkey.AccountSubjectEQ(acct.Subject)).OnlyX(ctx)

	if stored.Seed == string(acct.SigningKeySeed) {
		t.Fatal("the signing seed is stored in plaintext; anyone who can read the database can mint identities this account's broker trusts")
	}
	if strings.Contains(stored.Seed, string(acct.SigningKeySeed)) {
		t.Fatal("the stored value contains the plaintext seed")
	}
}

// TestSaveRetiresThePreviousActiveKey is the rule the schema says is
// enforced in application code and which, until this package, nothing
// enforced at all.
func TestSaveRetiresThePreviousActiveKey(t *testing.T) {
	ctx := context.Background()
	client := hookedClient(t, "rotate")
	store := meshkey.NewStore(client)

	op, err := meshid.NewOperator("rotate-test")
	if err != nil {
		t.Fatalf("NewOperator: %v", err)
	}
	// Two signing keys for the SAME account, which is what a rotation is.
	first, err := meshid.NewAccount(op, "PLEIADES")
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	second, err := meshid.NewAccount(op, "PLEIADES")
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	second.Subject = first.Subject

	if err := store.Save(ctx, "key-1", first); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	if err := store.Save(ctx, "key-2", second); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	active, err := client.MeshSigningKey.Query().
		Where(meshsigningkey.AccountSubjectEQ(first.Subject), meshsigningkey.ActiveEQ(true)).
		All(ctx)
	if err != nil {
		t.Fatalf("querying active keys: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("%d keys are active for one account, want exactly 1; which key signs is ambiguous", len(active))
	}
	if active[0].KeyID != "key-2" {
		t.Errorf("the active key is %q, want the one just saved", active[0].KeyID)
	}

	// The retired key stays, because its public key must remain in the
	// account JWT until the last credential it signed has expired.
	all, err := client.MeshSigningKey.Query().Where(meshsigningkey.AccountSubjectEQ(first.Subject)).All(ctx)
	if err != nil {
		t.Fatalf("querying every key: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("%d keys recorded, want 2; a retired key must stay so an operator can see what is still inside its credentials' expiry window", len(all))
	}

	id, err := store.ActiveKeyID(ctx, first.Subject)
	if err != nil {
		t.Fatalf("ActiveKeyID: %v", err)
	}
	if id != "key-2" {
		t.Errorf("ActiveKeyID = %q, want key-2", id)
	}
}

// TestNoActiveKeyIsANamedError, because a deployment that has not turned
// mesh authentication on is the ordinary case and must be
// distinguishable from a real failure.
func TestNoActiveKeyIsANamedError(t *testing.T) {
	ctx := context.Background()
	store := meshkey.NewStore(hookedClient(t, "absent"))

	_, err := store.Issuer(ctx, "ACNOTHINGSTOREDHERE")
	if !errors.Is(err, meshkey.ErrNoActiveKey) {
		t.Errorf("Issuer for an unknown account = %v, want ErrNoActiveKey so a caller can tell it from a failure", err)
	}

	if _, err := store.ActiveKeyID(ctx, "ACNOTHINGSTOREDHERE"); !errors.Is(err, meshkey.ErrNoActiveKey) {
		t.Errorf("ActiveKeyID for an unknown account = %v, want ErrNoActiveKey", err)
	}
}

// TestSaveRefusesWhatItCannotName covers the two arguments that would
// otherwise produce a row nobody can act on.
func TestSaveRefusesWhatItCannotName(t *testing.T) {
	ctx := context.Background()
	store := meshkey.NewStore(hookedClient(t, "refuse"))
	acct := newAccount(t, "PLEIADES")

	if err := store.Save(ctx, "", acct); err == nil {
		t.Error("Save accepted an empty key id; an operator retiring it would have no name to use")
	}
	if err := store.Save(ctx, "key-1", nil); err == nil {
		t.Error("Save accepted a nil account")
	}
}

// TestSaveRefusesAnOperatorKey is the protection this package gets for
// free from deriving the public key, and it is worth a test because it is
// easy to lose.
//
// Save calls meshid.SigningKeyPublic, which refuses any seed whose public
// key is not account-kind. So an operator seed cannot be stored here even
// by a caller trying to: the row is never created. That is the difference
// between "we do not store the operator key" as a convention and as a
// property, and it is the reason a Controller compromise stays an account
// compromise.
//
// It was found by trying to plant exactly this leak to falsify a release
// gate's assertion, and discovering the plant would not take.
func TestSaveRefusesAnOperatorKey(t *testing.T) {
	ctx := context.Background()
	store := meshkey.NewStore(hookedClient(t, "operator-refused"))

	op, err := meshid.NewOperator("refusal-test")
	if err != nil {
		t.Fatalf("NewOperator: %v", err)
	}
	opSeed, err := op.Seed()
	if err != nil {
		t.Fatalf("reading the operator seed: %v", err)
	}
	acct := newAccount(t, "PLEIADES")

	// An account in every respect except that its "signing key" is the
	// operator's, which is the shape a mistaken or malicious caller
	// would produce.
	acct.SigningKeySeed = opSeed

	err = store.Save(ctx, "smuggled", acct)
	if err == nil {
		t.Fatal("Save stored an operator key as a signing key; a Controller holding one can mint a new account, and a new account is a new tenant of the mesh")
	}
	if !errors.Is(err, meshid.ErrNotASeed) {
		t.Errorf("Save refused with %v, want the refusal to name the key kind so the mistake is legible", err)
	}
}

// TestActiveAccountFindsTheOneAccountThisDeploymentSignsFor covers the
// lookup that exists so the Controller needs no environment variable
// naming its own account.
//
// A second copy of that name in the environment is a second thing that
// can disagree, and a deployment whose variable names one account while
// its key signs for another authenticates nothing while saying nothing
// useful about why.
func TestActiveAccountFindsTheOneAccountThisDeploymentSignsFor(t *testing.T) {
	ctx := context.Background()
	client := hookedClient(t, "activeaccount")
	store := meshkey.NewStore(client)

	// Nothing stored yet: the ordinary state of a deployment that has not
	// turned mesh authentication on.
	if _, err := store.ActiveAccount(ctx); !errors.Is(err, meshkey.ErrNoActiveKey) {
		t.Errorf("ActiveAccount with nothing stored = %v, want ErrNoActiveKey", err)
	}

	acct := newAccount(t, "PLEIADES")
	if err := store.Save(ctx, "key-1", acct); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.ActiveAccount(ctx)
	if err != nil {
		t.Fatalf("ActiveAccount: %v", err)
	}
	if got != acct.Subject {
		t.Errorf("ActiveAccount = %q, want %q", got, acct.Subject)
	}

	// A retired key must not change the answer. Retired rows stay
	// deliberately, so a lookup that counted rows rather than active ones
	// would start refusing after the first rotation.
	second := newAccount(t, "PLEIADES")
	second.Subject = acct.Subject
	if err := store.Save(ctx, "key-2", second); err != nil {
		t.Fatalf("Save second: %v", err)
	}
	got, err = store.ActiveAccount(ctx)
	if err != nil {
		t.Fatalf("ActiveAccount after a rotation: %v", err)
	}
	if got != acct.Subject {
		t.Errorf("ActiveAccount after a rotation = %q, want %q; a retired key changed the answer", got, acct.Subject)
	}
}

// TestActiveAccountRefusesMoreThanOneAccount is the case that must not be
// resolved by guessing.
//
// Holding keys for several accounts is a real multi-tenant arrangement
// that nothing in this platform builds yet. Picking one arbitrarily would
// mint identities for a tenant the operator did not name, which is
// exactly the kind of quiet wrong answer a default produces.
func TestActiveAccountRefusesMoreThanOneAccount(t *testing.T) {
	ctx := context.Background()
	store := meshkey.NewStore(hookedClient(t, "twoaccounts"))

	first := newAccount(t, "TENANT-A")
	secondAccount := newAccount(t, "TENANT-B")
	if err := store.Save(ctx, "key-a", first); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	if err := store.Save(ctx, "key-b", secondAccount); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	got, err := store.ActiveAccount(ctx)
	if err == nil {
		t.Fatalf("ActiveAccount picked %q out of two accounts rather than refusing; it would mint identities for a tenant nobody named", got)
	}
	if !strings.Contains(err.Error(), "2 accounts") {
		t.Errorf("the refusal does not say how many it found: %v", err)
	}
}

// TestIssuerRefusesAnAmbiguousAccount covers the branch that only a
// writer bypassing Save can produce.
//
// Two active keys for one account means "which key signs" has no answer,
// and the fix is to retire one rather than to retry, so the error has to
// say that instead of reading as a generic query failure.
func TestIssuerRefusesAnAmbiguousAccount(t *testing.T) {
	ctx := context.Background()
	client := hookedClient(t, "ambiguous")
	store := meshkey.NewStore(client)

	acct := newAccount(t, "PLEIADES")
	if err := store.Save(ctx, "key-1", acct); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A second ACTIVE row for the same account, written straight through
	// the client so it bypasses the rule Save enforces. This is what a
	// migration or a hand-edited database looks like.
	pub, err := meshid.SigningKeyPublic(acct.SigningKeySeed)
	if err != nil {
		t.Fatalf("SigningKeyPublic: %v", err)
	}
	if _, err := client.MeshSigningKey.Create().
		SetKeyID("key-smuggled").
		SetAccountSubject(acct.Subject).
		SetPublicKey(pub).
		SetSeed(string(acct.SigningKeySeed)).
		SetActive(true).
		Save(ctx); err != nil {
		t.Fatalf("writing a second active row: %v", err)
	}

	if _, err := store.Issuer(ctx, acct.Subject); err == nil {
		t.Fatal("Issuer picked one of two active keys rather than refusing")
	} else if !strings.Contains(err.Error(), "more than one active signing key") {
		t.Errorf("the error does not name the ambiguity, so the fix is not obvious: %v", err)
	}
}
