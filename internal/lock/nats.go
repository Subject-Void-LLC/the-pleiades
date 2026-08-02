package lock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const bucketName = "Pleiades_Locks"

type natsLockManager struct {
	nc *nats.Conn
	kv jetstream.KeyValue
}

// NewNatsLockManager connects to the NATS JetStream KeyValue store.
func NewNatsLockManager(ctx context.Context, url string) (Manager, error) {
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to nats: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("failed to init jetstream: %w", err)
	}

	// Create or update the distributed lock bucket
	kv, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: bucketName,
		TTL:    24 * time.Hour, // Absolute maximum failsafe
	})
	if err != nil {
		return nil, fmt.Errorf("failed to init lock bucket: %w", err)
	}

	return &natsLockManager{nc: nc, kv: kv}, nil
}

func (m *natsLockManager) Close() error {
	m.nc.Close()
	return nil
}

// Acquire implements the atomic Compare-And-Swap lock acquisition using JetStream KV Create.
func (m *natsLockManager) Acquire(ctx context.Context, itemID string, ttl time.Duration) (Lease, error) {
	// NATS JetStream KV's Create method only succeeds if the key DOES NOT exist.
	// This provides our atomic Compare-and-Swap lock mechanism.
	rev, err := m.kv.Create(ctx, itemID, []byte(time.Now().UTC().String()))
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return nil, ErrLockHeld
		}
		return nil, fmt.Errorf("failed to acquire lock for %s: %w", itemID, err)
	}

	return &natsLease{
		kv:       m.kv,
		itemID:   itemID,
		revision: rev,
	}, nil
}

type natsLease struct {
	kv       jetstream.KeyValue
	itemID   string
	revision uint64
}

func (l *natsLease) ID() string {
	return l.itemID
}

func (l *natsLease) KeepAlive(ctx context.Context) error {
	// Update performs a Compare-And-Swap with the existing revision.
	// If someone else somehow modified the lock, this will fail.
	rev, err := l.kv.Update(ctx, l.itemID, []byte(time.Now().UTC().String()), l.revision)
	if err != nil {
		return fmt.Errorf("failed to keep alive lease %s: %w", l.itemID, err)
	}
	l.revision = rev
	return nil
}

func (l *natsLease) Release(ctx context.Context) error {
	// Atomic delete only if we still hold the latest revision.
	err := l.kv.Delete(ctx, l.itemID, jetstream.LastRevision(l.revision))
	if err != nil {
		return fmt.Errorf("failed to release lease %s: %w", l.itemID, err)
	}
	return nil
}
