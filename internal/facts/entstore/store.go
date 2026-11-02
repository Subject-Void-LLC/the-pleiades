// Package entstore is the Controller's fact store over the ent Fact table
// (Phase 41), and the consumer that writes what Runners publish into it.
//
// A device's rows for a key are the history of that key's value: a row is
// written only when an observation's hash differs from the value the key held
// at the observation's own time (the newest row created at or before it), and
// an observation of that same value moves the row's last_seen_at instead. The
// current value is the row most recently confirmed, so a batch that arrives
// late (redelivered, or from a Runner whose clock runs behind) adds history
// without replacing a value confirmed after it, and a redelivered batch finds
// its own row and only confirms it. Two different batches for one device
// stored at the same moment by two Controller replicas can each write the
// same new value once; the history then shows it twice. The key, value and
// hash of a row have no setter in the generated client; only last_seen_at
// moves.
package entstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
			Where(entfact.KeyEQ(e.Key), entfact.HasDeviceWith(entdevice.IDEQ(dev.ID)), entfact.CreatedAtLTE(at)).
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
		canon, err := facts.Canonical(e.Value)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		if err := tx.Fact.Create().
			SetKey(e.Key).
			SetPayload(map[string]any{payloadKey: string(canon)}).
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

// Current implements facts.Store: for each of device's keys, the value most
// recently confirmed (the later observation when two were confirmed at once).
func (s *Store) Current(ctx context.Context, device string) ([]facts.Entry, error) {
	rows, err := s.client.Fact.Query().
		Where(entfact.HasDeviceWith(entdevice.DeviceIDEQ(device))).
		Order(ent.Asc(entfact.FieldKey)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	current := map[string]facts.Entry{}
	var keys []string
	for _, row := range rows {
		e := entry(row)
		cur, ok := current[e.Key]
		if !ok {
			keys = append(keys, e.Key)
		}
		if !ok || e.LastSeenAt.After(cur.LastSeenAt) || e.LastSeenAt.Equal(cur.LastSeenAt) && e.ObservedAt.After(cur.ObservedAt) {
			current[e.Key] = e
		}
	}
	out := make([]facts.Entry, 0, len(keys))
	for _, k := range keys {
		out = append(out, current[k])
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

// payloadKey is where a row's payload holds its value: the value's canonical
// JSON as text, so a number reads back exactly as it was stored (a JSON
// column decodes numbers as float64, which rounds an integer past 2^53).
const payloadKey = "json"

// entry is a row as a facts.Entry.
func entry(row *ent.Fact) facts.Entry {
	seen := row.LastSeenAt
	if seen.IsZero() {
		seen = row.CreatedAt
	}
	var value any
	if text, ok := row.Payload[payloadKey].(string); ok {
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			value = nil
		}
	}
	return facts.Entry{Key: row.Key, Value: value, Hash: row.Hash, ObservedAt: row.CreatedAt.UTC(), LastSeenAt: seen.UTC()}
}
