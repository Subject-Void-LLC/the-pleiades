// This file holds ReadSnapshot to one moment against the embedded database
// the Controller opens, and to joining a write's transaction.
package storage_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/storage"
)

// openFile opens the database the Controller's embedded tier opens: a file
// in WAL mode with a connection pool, where each statement outside a
// transaction takes its own snapshot.
func openFile(t *testing.T) *ent.Client {
	t.Helper()
	client, err := ent.OpenEmbedded(context.Background(), filepath.Join(t.TempDir(), "snapshot.db"))
	if err != nil {
		t.Fatalf("OpenEmbedded() = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestReadSnapshot_ReadsOneMoment commits a write between two reads inside
// one ReadSnapshot, which neither sees, while a read after it does.
func TestReadSnapshot_ReadsOneMoment(t *testing.T) {
	ctx := context.Background()
	client := openFile(t)
	client.Device.Create().SetName("first").SetType("linux_server").SaveX(ctx)

	err := storage.ReadSnapshot(ctx, client, func(ctx context.Context, db *ent.Client) error {
		before := db.Device.Query().CountX(ctx)
		client.Device.Create().SetName("second").SetType("linux_server").SaveX(context.Background())
		if after := ent.FromContext(ctx).Device.Query().CountX(ctx); after != before {
			t.Errorf("the second read saw %d devices and the first %d: they were not one snapshot", after, before)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadSnapshot() = %v", err)
	}
	if got := client.Device.Query().CountX(ctx); got != 2 {
		t.Errorf("after the snapshot ended a read saw %d devices, want 2", got)
	}
}

// TestReadSnapshot_ARootClientInTheContextIsNotATransaction places the root
// client, not a transaction's, in the context: it is no transaction to join,
// so the read still gets one snapshot of its own.
func TestReadSnapshot_ARootClientInTheContextIsNotATransaction(t *testing.T) {
	client := openFile(t)
	client.Device.Create().SetName("first").SetType("linux_server").SaveX(context.Background())

	err := storage.ReadSnapshot(ent.NewContext(context.Background(), client), client, func(ctx context.Context, db *ent.Client) error {
		before := db.Device.Query().CountX(ctx)
		client.Device.Create().SetName("second").SetType("linux_server").SaveX(context.Background())
		if after := db.Device.Query().CountX(ctx); after != before {
			t.Errorf("the second read saw %d devices and the first %d: a root client in the context skipped the snapshot", after, before)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadSnapshot() = %v", err)
	}
}

// TestReadSnapshot_RunsInsideATransactionItIsGiven reads, inside a write's
// transaction, the row that transaction has not committed yet.
func TestReadSnapshot_RunsInsideATransactionItIsGiven(t *testing.T) {
	ctx := context.Background()
	client := openFile(t)
	err := storage.NewEntUnitOfWork(client).WithTx(ctx, func(txCtx context.Context) error {
		ent.FromContext(txCtx).Device.Create().SetName("uncommitted").SetType("linux_server").SaveX(txCtx)
		return storage.ReadSnapshot(txCtx, client, func(ctx context.Context, db *ent.Client) error {
			if got := db.Device.Query().CountX(ctx); got != 1 {
				t.Errorf("the read inside the write's transaction saw %d devices, want its own 1", got)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("WithTx() = %v", err)
	}
}

// TestReadSnapshot_ReturnsWhatReadReturns passes read's error back as it
// is, and a client that cannot begin a transaction says so.
func TestReadSnapshot_ReturnsWhatReadReturns(t *testing.T) {
	ctx := context.Background()
	client := openFile(t)
	refused := errors.New("refused")
	if err := storage.ReadSnapshot(ctx, client, func(context.Context, *ent.Client) error { return refused }); !errors.Is(err, refused) {
		t.Errorf("ReadSnapshot() = %v, want read's own error", err)
	}

	_ = client.Close()
	err := storage.ReadSnapshot(ctx, client, func(context.Context, *ent.Client) error {
		t.Error("read ran on a client that could not begin a transaction")
		return nil
	})
	if err == nil {
		t.Error("ReadSnapshot() on a closed client = nil, want the failure to begin")
	}
}
