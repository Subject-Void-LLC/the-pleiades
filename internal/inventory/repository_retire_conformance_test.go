package inventory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// This file extends the Repository conformance suite
// (repository_conformance_test.go) to Retire, the write path Phase 13 adds
// so the API's DELETE verb has something real behind it.
//
// It is a conformance test rather than two adapter-specific ones for the
// reason that file's own header gives: a caller must not be able to tell
// which adapter it is talking to from behavior alone. Retire in particular
// has three behaviors that are easy to implement two different ways by
// accident, and each gets its own test below: what an unknown name does,
// whether a second call is an error, and whether the transition lands in
// the audit trail rather than only in a status column.

func TestRepositoryConformance_RetireArchivesAndRecordsRevision(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			repo := backend.newRepo(t)

			before, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}
			if before.State() == pkginventory.StateArchived {
				t.Fatal("seeded host is already archived, so this test would prove nothing")
			}
			startVersion := before.Version()
			startHistory := len(before.History())

			if err := repo.Retire(ctx, conformanceHostName); err != nil {
				t.Fatalf("Retire: %v", err)
			}

			after, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName after Retire: %v", err)
			}

			// The item still exists. Retire is a lifecycle transition, not a
			// row removal, precisely so the audit trail below survives it.
			if got := after.State(); got != pkginventory.StateArchived {
				t.Errorf("state after Retire = %v, want %v", got, pkginventory.StateArchived)
			}

			// An archived device is no longer a valid runbook target, which
			// is the operational point of the transition and is enforced by
			// the lifecycle type rather than by any caller remembering to
			// check.
			if after.State().CanExecute() {
				t.Error("an archived device must not be executable")
			}

			if got, want := after.Version(), startVersion+1; got != want {
				t.Errorf("version after Retire = %d, want %d", got, want)
			}

			history := after.History()
			if len(history) != startHistory+1 {
				t.Fatalf("history length after Retire = %d, want %d: %+v", len(history), startHistory+1, history)
			}
			rev := history[len(history)-1]
			if rev.Field != "state" {
				t.Errorf("retirement revision field = %q, want %q", rev.Field, "state")
			}
			if rev.NewValue != pkginventory.StateArchived.String() {
				t.Errorf("retirement revision NewValue = %v, want %q", rev.NewValue, pkginventory.StateArchived.String())
			}
			if rev.OldValue == nil || rev.OldValue == pkginventory.StateArchived.String() {
				t.Errorf("retirement revision OldValue = %v, want the pre-retirement state", rev.OldValue)
			}
			if rev.Version != after.Version() {
				t.Errorf("retirement revision version = %d, want %d", rev.Version, after.Version())
			}
			if rev.ChangedAt.IsZero() {
				t.Error("retirement revision carries no timestamp, so 'when was this retired' is unanswerable")
			}
		})
	}
}

func TestRepositoryConformance_RetireIsIdempotent(t *testing.T) {
	// DELETE is idempotent per RFC 9110, so the port backing it must be
	// too. The sharper half of this assertion is the audit trail: a second
	// retirement must record no second Revision, because a trail claiming a
	// device was archived twice describes something that never happened.
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			repo := backend.newRepo(t)

			if err := repo.Retire(ctx, conformanceHostName); err != nil {
				t.Fatalf("first Retire: %v", err)
			}
			once, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName after first Retire: %v", err)
			}

			if err := repo.Retire(ctx, conformanceHostName); err != nil {
				t.Fatalf("second Retire returned an error; DELETE is idempotent: %v", err)
			}
			twice, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName after second Retire: %v", err)
			}

			if twice.Version() != once.Version() {
				t.Errorf("second Retire moved the version from %d to %d; it must change nothing", once.Version(), twice.Version())
			}
			if len(twice.History()) != len(once.History()) {
				t.Errorf("second Retire appended a revision: history went from %d to %d entries", len(once.History()), len(twice.History()))
			}
			if twice.State() != pkginventory.StateArchived {
				t.Errorf("state after second Retire = %v, want %v", twice.State(), pkginventory.StateArchived)
			}
		})
	}
}

func TestRepositoryConformance_RetireUnknownNameReportsNotFound(t *testing.T) {
	// The sentinel matters more than the failure. A caller (api.DeviceHandler)
	// maps ErrItemNotFound to 404 and everything else to 500, so an adapter
	// returning a bare formatted error here would turn a missing device into
	// a server error on one backend and a 404 on the other.
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			repo := backend.newRepo(t)

			err := repo.Retire(ctx, "no-such-host")
			if err == nil {
				t.Fatal("retiring an unknown host returned nil")
			}
			if !errors.Is(err, inventory.ErrItemNotFound) {
				t.Fatalf("error does not wrap ErrItemNotFound, so a caller cannot distinguish it from a backend failure: %v", err)
			}
		})
	}
}

func TestRepositoryConformance_ReadOnlyRepositoryRefusesRetire(t *testing.T) {
	// NewReadOnlyRepository's argument is that a wrapper cannot be forgotten
	// in one branch of one adapter. Retire is the least reversible operation
	// on this port, so a read-only run that performed one silently would
	// break the exact promise the wrapper exists to make.
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			inner := backend.newRepo(t)
			readOnly := inventory.NewReadOnlyRepository(inner)

			err := readOnly.Retire(ctx, conformanceHostName)
			if err == nil {
				t.Fatal("read-only repository performed a retirement")
			}
			if !errors.Is(err, inventory.ErrInventoryReadOnly) {
				t.Fatalf("error does not wrap ErrInventoryReadOnly: %v", err)
			}

			// The refusal must be real, not merely reported: read through the
			// unwrapped repository and confirm nothing moved.
			after, err := inner.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName after refused Retire: %v", err)
			}
			if after.State() == pkginventory.StateArchived {
				t.Error("read-only repository reported a refusal but the item was archived anyway")
			}
		})
	}
}
