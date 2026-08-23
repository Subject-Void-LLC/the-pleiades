// This file covers EnsureManagedType's lost-race branch: the window
// between its namespace lookup and its insert, where another process can
// claim the same namespace.
//
// The branch is unreachable by any ordinary sequential test, and that is
// structural rather than an oversight. EnsureManagedType queries the exact
// namespace it is about to write, so a single-threaded caller that reaches
// the create arm has already proven no row holds it, and the only unique
// index that could fire is the one it just checked. The other index,
// (name, organization), cannot collide here either: a managed row has a
// NULL organization and both dialects treat NULL as distinct in a unique
// index, which internal/ent/schema/credential_type.go says outright.
//
// So the collision has to be manufactured, and the two tests below
// manufacture it two different ways on purpose. The first is deterministic
// and pins the branch; the second is a real race and pins the property.
// Neither stubs the database: both take a genuine constraint violation
// from the same real SQLite the store uses everywhere else, which is what
// RULE 0 asks for on a test whose entire subject is what the database does
// under contention.
package credstore_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credentialtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/hook"
)

// lostRaceType is the managed type both tests install. Its namespace is
// distinctive so a failure names something greppable.
func lostRaceType() credtype.CredentialType {
	return credtype.CredentialType{
		Name:      "Contested Cloud",
		Kind:      credtype.KindCloud,
		Namespace: "contested",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "access_key", Label: "Access Key"},
		}},
	}
}

// TestEnsureManagedType_LosingTheRaceToAManagedTypeSucceeds is the branch
// under test, reached deterministically.
//
// A CredentialType hook fires once, on the store's own insert, and writes
// the row first through the same client before letting the original
// mutation through. That is exactly what a second controller cold-starting
// against a shared database does, compressed into one process: the store's
// namespace lookup found nothing, and by the time its insert reaches the
// database the namespace is taken.
//
// What must happen is that the reconcile SUCCEEDS. The row is correct, it
// simply was not written by this caller, and a managed type installed by
// somebody else is the outcome this method exists to converge on. Before
// the fix it returned ErrExists, which ReconcileManaged reads as "a custom
// type holds this namespace" and reports with an action naming a type that
// does not exist.
func TestEnsureManagedType_LosingTheRaceToAManagedTypeSucceeds(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, _, _ := fixture(t)

	// CompareAndSwap rather than sync.Once, and set BEFORE the rival's own
	// write rather than around it: that write goes through this same client
	// and therefore re-enters this hook, and sync.Once deadlocks when its
	// Do is re-entered from inside itself.
	var raced atomic.Bool
	client.CredentialType.Use(func(next ent.Mutator) ent.Mutator {
		return hook.CredentialTypeFunc(func(ctx context.Context, m *ent.CredentialTypeMutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) && raced.CompareAndSwap(false, true) {
				// The rival's write. Managed, like ours, and deliberately
				// carrying a different Name so the assertions below can
				// tell whose row survived.
				client.CredentialType.Create().
					SetName("Installed By The Other Controller").
					SetKind(string(credtype.KindCloud)).
					SetNamespace("contested").
					SetManaged(true).
					SetInputs(lostRaceType().Inputs).
					SaveX(ctx)
			}
			return next.Mutate(ctx, m)
		})
	})

	got, err := store.EnsureManagedType(ctx, lostRaceType())
	if err != nil {
		t.Fatalf("EnsureManagedType() error = %v, want the lost race to be reconciled rather than reported", err)
	}
	if !got.Managed {
		t.Error("the adopted row is not marked managed")
	}
	if got.Namespace != "contested" {
		t.Errorf("Namespace = %q, want %q", got.Namespace, "contested")
	}
	// The loser adopts the row and reconciles its own definition onto it,
	// which is the same thing it would have done had it merely been second
	// rather than unlucky.
	if got.Name != "Contested Cloud" {
		t.Errorf("Name = %q, want the caller's own definition to have been reconciled onto the winning row", got.Name)
	}

	// Exactly one row IN THIS NAMESPACE, which is the property the unique
	// index guarantees and the reason losing the race is safe at all. The
	// count is scoped because fixture seeds a credential type of its own.
	if n := contestedRows(ctx, t, client); n != 1 {
		t.Errorf("rows in the contested namespace = %d, want exactly 1", n)
	}
}

// TestEnsureManagedType_LosingTheRaceToACustomTypeIsStillRefused is the
// control on the test above, and the reason the fix re-reads instead of
// simply treating every constraint error as success.
//
// The rival here writes a CUSTOM type into the namespace. That is the one
// case an operator genuinely has to act on -- this platform must never
// overwrite something a person authored -- so it must still come back as
// ErrExists even though it arrived through the identical code path. A fix
// that swallowed the constraint error would silently replace an operator's
// credential type with a shipped one.
func TestEnsureManagedType_LosingTheRaceToACustomTypeIsStillRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, _ := fixture(t)

	// See the note on the sibling test: the flag is set before the rival's
	// write because that write re-enters this hook.
	var raced atomic.Bool
	client.CredentialType.Use(func(next ent.Mutator) ent.Mutator {
		return hook.CredentialTypeFunc(func(ctx context.Context, m *ent.CredentialTypeMutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) && raced.CompareAndSwap(false, true) {
				client.CredentialType.Create().
					SetName("An Operator Wrote This").
					SetKind(string(credtype.KindCloud)).
					SetNamespace("contested").
					SetManaged(false).
					SetOrganizationID(orgID).
					SetInputs(lostRaceType().Inputs).
					SaveX(ctx)
			}
			return next.Mutate(ctx, m)
		})
	})

	if _, err := store.EnsureManagedType(ctx, lostRaceType()); !errors.Is(err, credstore.ErrExists) {
		t.Fatalf("EnsureManagedType() error = %v, want ErrExists: a custom type holding the namespace is the one case an operator must resolve", err)
	}

	row := client.CredentialType.Query().
		Where(credentialtype.NamespaceEQ("contested")).
		OnlyX(ctx)
	if row.Managed || row.Name != "An Operator Wrote This" {
		t.Errorf("the operator's custom type was overwritten: managed = %v, name = %q", row.Managed, row.Name)
	}
}

// TestEnsureManagedType_ConcurrentReconcilesConvergeOnOneRow is the
// property the deterministic tests above cannot state: that the real thing
// works when several callers genuinely contend, with no hook involved.
//
// It is the starting-gate shape internal/schedule/scanner_test.go uses for
// the same class of question. Most goroutines will see the row through
// their own namespace lookup and take the ordinary update path rather than
// the create collision, which is exactly why this test is a complement to
// the two above rather than a replacement for them -- it cannot pin the
// branch, and it is the only one that proves the branch was worth pinning.
//
// The fixture is a real file-backed SQLite in WAL mode with a busy timeout
// (ent.OpenEmbedded), so the contention is real. That matters:
// internal/schedule/ent_store_test.go records that a shared-cache
// in-memory database answers a contending writer with SQLITE_LOCKED, which
// no busy timeout retries, and a test written on one would pass for a
// reason unrelated to what it claims.
func TestEnsureManagedType_ConcurrentReconcilesConvergeOnOneRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, _, _ := fixture(t)

	const racers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, racers)

	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = store.EnsureManagedType(ctx, lostRaceType())
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("racer %d: EnsureManagedType() error = %v, want every concurrent reconcile of the same managed type to succeed", i, err)
		}
	}
	if n := contestedRows(ctx, t, client); n != 1 {
		t.Errorf("rows in the contested namespace = %d, want exactly 1 no matter how many callers installed it", n)
	}
}

// contestedRows counts the rows holding the namespace these tests contend
// over. Scoped rather than a bare count because fixture seeds a credential
// type of its own, so a total would answer a different question.
func contestedRows(ctx context.Context, t *testing.T, client *ent.Client) int {
	t.Helper()
	return client.CredentialType.Query().
		Where(credentialtype.NamespaceEQ("contested")).
		CountX(ctx)
}

// TestEnsureManagedType_ANonConstraintCreateFailureIsNotTreatedAsARace is
// the boundary of the branch above.
//
// Only a CONSTRAINT error means somebody else got there first. Any other
// write failure -- a closed database, a disk error, a driver fault -- must
// still be reported, because re-reading after one of those would either
// find nothing or fail again, and reporting it as a lost race would tell
// an operator their reconcile succeeded when the row was never written.
func TestEnsureManagedType_ANonConstraintCreateFailureIsNotTreatedAsARace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, _, _ := fixture(t)

	wantErr := errors.New("the disk went away")
	client.CredentialType.Use(func(next ent.Mutator) ent.Mutator {
		return hook.CredentialTypeFunc(func(ctx context.Context, m *ent.CredentialTypeMutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) {
				return nil, wantErr
			}
			return next.Mutate(ctx, m)
		})
	})

	_, err := store.EnsureManagedType(ctx, lostRaceType())
	if err == nil {
		t.Fatal("EnsureManagedType() = nil, want a write failure that is not a constraint to be reported")
	}
	if errors.Is(err, credstore.ErrExists) {
		t.Errorf("error = %v, want it not to claim the namespace already exists", err)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want it to wrap the underlying write failure", err)
	}
	if n := contestedRows(ctx, t, client); n != 0 {
		t.Errorf("rows in the contested namespace = %d, want 0: nothing was written", n)
	}
}
