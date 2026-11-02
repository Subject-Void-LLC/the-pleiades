// Package entstore is the Controller's fact store over the ent Fact table
// (Phase 41), and the consumer that writes what Runners publish into it.
//
// A device's rows for a key are the history of that key's value: a row is
// written only when an observation's hash differs from the newest row's, and
// an observation of the same value moves that row's last_seen_at instead. The
// key, value and hash of a row are immutable in the schema itself; only
// last_seen_at moves.
package entstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// ErrUnknownDevice marks a batch naming a device the database does not hold.
var ErrUnknownDevice = errors.New("facts: no such device")

// Store is facts.Store over an ent client.
type Store struct {
	client *ent.Client
}

// New returns a store over client.
func New(client *ent.Client) *Store { return &Store{client: client} }

var _ facts.Store = (*Store)(nil)

// Record implements facts.Store, in one transaction.
func (s *Store) Record(ctx context.Context, device string, entries []facts.Entry, at time.Time) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	if err := record(ctx, tx, device, entries, at.UTC()); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

// record is Record inside tx.
func record(ctx context.Context, tx *ent.Tx, device string, entries []facts.Entry, at time.Time) error {
	dev, err := tx.Device.Query().Where(entdevice.DeviceIDEQ(device)).Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("%w: %s", ErrUnknownDevice, device)
	}
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	for _, e := range entries {
		newest, err := tx.Fact.Query().
			Where(entfact.KeyEQ(e.Key), entfact.HasDeviceWith(entdevice.IDEQ(dev.ID))).
			Order(ent.Desc(entfact.FieldCreatedAt), ent.Desc(entfact.FieldID)).
			First(ctx)
		switch {
		case err == nil && newest.Hash == e.Hash:
			if at.After(newest.LastSeenAt) {
				if err := tx.Fact.UpdateOne(newest).SetLastSeenAt(at).Exec(ctx); err != nil {
					return fmt.Errorf("facts: %w", err)
				}
			}
			continue
		case err != nil && !ent.IsNotFound(err):
			return fmt.Errorf("facts: %w", err)
		}
		if err := tx.Fact.Create().
			SetKey(e.Key).
			SetPayload(map[string]any{"value": e.Value}).
			SetHash(e.Hash).
			SetCreatedAt(at).
			SetLastSeenAt(at).
			SetDevice(dev).
			Exec(ctx); err != nil {
			return fmt.Errorf("facts: %w", err)
		}
	}
	return nil
}

// Current implements facts.Store: the newest row of each of device's keys.
func (s *Store) Current(ctx context.Context, device string) ([]facts.Entry, error) {
	rows, err := s.client.Fact.Query().
		Where(entfact.HasDeviceWith(entdevice.DeviceIDEQ(device))).
		Order(ent.Asc(entfact.FieldKey), ent.Desc(entfact.FieldCreatedAt), ent.Desc(entfact.FieldID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	out := make([]facts.Entry, 0, len(rows))
	for _, row := range rows {
		if len(out) > 0 && out[len(out)-1].Key == row.Key {
			continue
		}
		out = append(out, entry(row))
	}
	return out, nil
}

// History implements facts.Store: every row of device's key, oldest first.
func (s *Store) History(ctx context.Context, device, key string) ([]facts.Entry, error) {
	rows, err := s.client.Fact.Query().
		Where(entfact.KeyEQ(key), entfact.HasDeviceWith(entdevice.DeviceIDEQ(device))).
		Order(ent.Asc(entfact.FieldCreatedAt), ent.Asc(entfact.FieldID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	out := make([]facts.Entry, 0, len(rows))
	for _, row := range rows {
		out = append(out, entry(row))
	}
	return out, nil
}

// entry is a row as a facts.Entry.
func entry(row *ent.Fact) facts.Entry {
	seen := row.LastSeenAt
	if seen.IsZero() {
		seen = row.CreatedAt
	}
	return facts.Entry{Key: row.Key, Value: row.Payload["value"], Hash: row.Hash, ObservedAt: row.CreatedAt.UTC(), LastSeenAt: seen.UTC()}
}
