package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/storage"
	_ "github.com/mattn/go-sqlite3"
)

// newTestClient opens an in-memory ent client for exercising UnitOfWork's
// own commit/rollback mechanics. enttest's auto-migration shortcut is the
// right tool here (unlike internal/ent's own Release Gate test): the
// claim under test is "a transaction commits or rolls back correctly,"
// not "the versioned-migration mechanism produces a working schema,"
// which internal/ent/client_test.go already proves against the real path.
func newTestClient(t *testing.T) *ent.Client {
	t.Helper()
	// _journal_mode=WAL matches internal/ent's own production DSN
	// (embeddedDSN): without it, SQLite's shared-cache mode enforces
	// table-level locking across connections, and a concurrent read from
	// a second connection while a write transaction is open on another
	// fails with "database table is locked" instead of returning a clean
	// isolated result. WAL lets readers proceed without blocking on (or
	// seeing) a writer's uncommitted transaction, which is what
	// TestEntUnitOfWork_TransactionScopedClientIsIsolatedUntilCommit
	// below actually needs.
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1&_journal_mode=WAL")
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestEntUnitOfWork_CommitsOnSuccess(t *testing.T) {
	client := newTestClient(t)
	uow := storage.NewEntUnitOfWork(client)
	ctx := context.Background()

	err := uow.WithTx(ctx, func(txCtx context.Context) error {
		_, err := ent.FromContext(txCtx).Device.Create().SetName("committed-device").SetType("linux_server").Save(txCtx)
		return err
	})
	if err != nil {
		t.Fatalf("WithTx returned an error on the success path: %v", err)
	}

	count, err := client.Device.Query().Count(ctx)
	if err != nil {
		t.Fatalf("counting devices: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 committed device, got %d", count)
	}
}

func TestEntUnitOfWork_RollsBackOnError(t *testing.T) {
	client := newTestClient(t)
	uow := storage.NewEntUnitOfWork(client)
	ctx := context.Background()

	sentinel := errors.New("boom")
	err := uow.WithTx(ctx, func(txCtx context.Context) error {
		if _, err := ent.FromContext(txCtx).Device.Create().SetName("rolled-back-device").SetType("linux_server").Save(txCtx); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the sentinel error to propagate, got: %v", err)
	}

	count, err := client.Device.Query().Count(ctx)
	if err != nil {
		t.Fatalf("counting devices: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected the device create to be rolled back, but %d devices exist", count)
	}
}

func TestEntUnitOfWork_RollsBackOnPanic(t *testing.T) {
	client := newTestClient(t)
	uow := storage.NewEntUnitOfWork(client)
	ctx := context.Background()

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected WithTx to re-raise the panic after rolling back")
			}
		}()
		_ = uow.WithTx(ctx, func(txCtx context.Context) error {
			if _, err := ent.FromContext(txCtx).Device.Create().SetName("panicked-device").SetType("linux_server").Save(txCtx); err != nil {
				t.Fatalf("creating device before panic: %v", err)
			}
			panic("simulated failure mid-transaction")
		})
	}()

	count, err := client.Device.Query().Count(ctx)
	if err != nil {
		t.Fatalf("counting devices: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected the device create to be rolled back after the panic, but %d devices exist", count)
	}
}

func TestEntUnitOfWork_FailsToStartTxOnClosedClient(t *testing.T) {
	client := newTestClient(t)
	if err := client.Close(); err != nil {
		t.Fatalf("closing client: %v", err)
	}

	uow := storage.NewEntUnitOfWork(client)
	ranFn := false
	err := uow.WithTx(context.Background(), func(txCtx context.Context) error {
		ranFn = true
		return nil
	})
	if err == nil {
		t.Fatalf("expected WithTx to fail starting a transaction on a closed client")
	}
	if ranFn {
		t.Fatalf("fn must not run when the transaction itself never started")
	}
}

func TestEntUnitOfWork_TransactionScopedClientIsIsolatedUntilCommit(t *testing.T) {
	// A real temp file, not the shared in-memory DB newTestClient uses
	// elsewhere in this file: SQLite does not support WAL mode for
	// :memory: databases (it silently falls back to a mode where a write
	// transaction on one connection locks the whole shared-cache database
	// against reads from another), so proving that an uncommitted write
	// on the transaction-scoped connection is genuinely invisible to a
	// concurrent read on the top-level connection needs the same real,
	// on-disk, WAL-mode shape internal/ent's own production DSN
	// (embeddedDSN) uses.
	dir := t.TempDir()
	dsn := "file:" + dir + "/uow-isolation.sqlite?_fk=1&_journal_mode=WAL&_busy_timeout=5000"
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	uow := storage.NewEntUnitOfWork(client)
	ctx := context.Background()

	err := uow.WithTx(ctx, func(txCtx context.Context) error {
		txClient := ent.FromContext(txCtx)
		if txClient == client {
			t.Fatalf("ent.FromContext(txCtx) returned the top-level client, not a transaction-scoped one")
		}
		if _, err := txClient.Device.Create().SetName("in-flight-device").SetType("linux_server").Save(txCtx); err != nil {
			return err
		}
		// The write is visible through the transaction-scoped client...
		count, err := txClient.Device.Query().Count(txCtx)
		if err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("expected the in-flight write to be visible inside its own transaction, got count %d", count)
		}
		// ...but not yet through the top-level client, since nothing has
		// committed.
		outsideCount, err := client.Device.Query().Count(ctx)
		if err != nil {
			return err
		}
		if outsideCount != 0 {
			t.Fatalf("expected the uncommitted write to be invisible outside the transaction, got count %d", outsideCount)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx returned an error on the success path: %v", err)
	}

	count, err := client.Device.Query().Count(ctx)
	if err != nil {
		t.Fatalf("counting devices: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected the device to be visible after commit, got count %d", count)
	}
}
