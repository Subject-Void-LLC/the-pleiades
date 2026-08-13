package access

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// MapWriteErrorForTest exposes the driver-error classification to this
// package's external test, which is the only way to assert the foreign-key
// branch: producing a real one requires reaching past the store to the ent
// client, and the classification itself is unexported by design.
func MapWriteErrorForTest(err error, kind string, id int, name string) error {
	return mapWriteError(err, kind, id, name)
}

// RollbackForTest exposes the transaction unwind. Reaching its second
// branch through DeleteTeam would mean arranging for a rollback to fail
// during a write that has already failed, which no store method can be
// driven into on purpose; calling it directly against a real transaction
// is the only honest way to see what it returns.
func RollbackForTest(tx *ent.Tx, cause error) error {
	return rollback(tx, cause)
}

// ResolveScopeNamesForTest exposes the batched scope-target resolution.
//
// Its per-scope-type error branches cannot be reached from outside: they
// only run after the binding query itself has succeeded, so any failure
// injected before the call is caught by the outer query instead. Calling it
// directly against a broken client is the only way to see what each branch
// returns.
func ResolveScopeNamesForTest(ctx context.Context, s Store, bindings []Binding) error {
	return s.(*entStore).resolveScopeNames(ctx, bindings)
}
