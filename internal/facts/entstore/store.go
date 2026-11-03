// Package entstore is the Controller's fact store over the ent Fact table
// (Phase 41, 41b), and the consumer that writes what Runners publish into it.
//
// It keeps the rules the Crawl CLI's file store keeps (internal/facts). A
// stable key's rows are the history of its value: a row is written only when
// an observation's hash differs from the value the key held as of the
// observation's run order, and an observation of that same value moves the
// row's last_seen_at and last_provenance instead. The current value is the
// one most recently confirmed, by run order where two runs' orders compare
// (facts.Order) and by the clock where they do not, so a batch that arrives
// late (redelivered, or from a Runner whose clock runs behind) adds history
// without replacing a value confirmed after it. A volatile key keeps one row,
// replaced when its value changes.
//
// Stable rows form a per-device hash chain (chain_prev, chain). A unique
// index on (chain_prev, device) keeps it a line: two replicas storing for one
// device at once cannot both extend the same row, and the loser's batch is
// redelivered against the new head. The key, value, hash, kind, first
// provenance and chain of a row have no setter in the generated client.
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
func (s *Store) Record(ctx context.Context, device string, obs facts.Observation) error {
	if len(obs.Entries) == 0 {
		return nil
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	if err := record(ctx, tx, device, obs); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

// record is Record inside tx.
func record(ctx context.Context, tx *ent.Tx, device string, obs facts.Observation) error {
	dev, err := tx.Device.Query().Where(entdevice.DeviceIDEQ(device)).Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("%w: %s", ErrUnknownDevice, device)
	}
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	at := obs.At.UTC()
	by, err := encode(obs.By)
	if err != nil {
		return err
	}
	head, err := chainHead(ctx, tx, dev.ID)
	if err != nil {
		return err
	}
	for _, e := range obs.Entries {
		rows, err := tx.Fact.Query().
			Where(entfact.KeyEQ(e.Key), entfact.HasDeviceWith(entdevice.IDEQ(dev.ID))).
			Order(ent.Asc(entfact.FieldID)).
			All(ctx)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		if e.Kind == facts.KindVolatile {
			if err := recordVolatile(ctx, tx, dev, rows, e, obs, by); err != nil {
				return err
			}
			continue
		}
		cur, ok := valueAt(rows, obs.By.Order, at)
		if ok && cur.Hash == e.Hash {
			if err := confirm(ctx, tx, cur, obs, by); err != nil {
				return err
			}
			continue
		}
		chain, err := facts.ChainHash(head, e.Key, e.Hash, at, obs.By)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		if err := create(tx, dev, e, at, by).SetKind(entfact.KindStable).SetChainPrev(head).SetChain(chain).Exec(ctx); err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		head = chain
	}
	return nil
}

// recordVolatile keeps e as its key's one volatile row when obs is the
// latest observation of it: the row is confirmed when its value is the
// same, and replaced when it is not; a late observation changes nothing.
func recordVolatile(ctx context.Context, tx *ent.Tx, dev *ent.Device, rows []*ent.Fact, e facts.Entry, obs facts.Observation, by map[string]any) error {
	var held []*ent.Fact
	for _, row := range rows {
		if row.Kind == entfact.KindVolatile {
			held = append(held, row)
		}
	}
	at := obs.At.UTC()
	if len(held) > 0 {
		newest := held[len(held)-1]
		last := entry(newest)
		if after, _ := facts.Later(obs.By.Order, at, last.LastBy.Order, last.LastSeenAt); !after {
			return nil
		}
		if newest.Hash == e.Hash && len(held) == 1 {
			return confirm(ctx, tx, newest, obs, by)
		}
		ids := make([]int, len(held))
		for i, row := range held {
			ids[i] = row.ID
		}
		if _, err := tx.Fact.Delete().Where(entfact.IDIn(ids...)).Exec(ctx); err != nil {
			return fmt.Errorf("facts: %w", err)
		}
	}
	if err := create(tx, dev, e, at, by).SetKind(entfact.KindVolatile).Exec(ctx); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

// confirm moves row's last confirmation to obs when obs came later.
func confirm(ctx context.Context, tx *ent.Tx, row *ent.Fact, obs facts.Observation, by map[string]any) error {
	last := entry(row)
	if after, _ := facts.Later(obs.By.Order, obs.At.UTC(), last.LastBy.Order, last.LastSeenAt); !after {
		return nil
	}
	if err := tx.Fact.UpdateOne(row).SetLastSeenAt(obs.At.UTC()).SetLastProvenance(by).Exec(ctx); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

// create starts a row for e, first observed and last confirmed by obs.
func create(tx *ent.Tx, dev *ent.Device, e facts.Entry, at time.Time, by map[string]any) *ent.FactCreate {
	return tx.Fact.Create().
		SetKey(e.Key).
		SetPayload(map[string]any{payloadKey: mustCanonical(e.Value)}).
		SetHash(e.Hash).
		SetCreatedAt(at).
		SetLastSeenAt(at).
		SetProvenance(by).
		SetLastProvenance(by).
		SetDevice(dev)
}

// valueAt is the stable row a key held as of (order, at): of its rows that
// came at or before it, the latest, the later row on a tie.
func valueAt(rows []*ent.Fact, order facts.Order, at time.Time) (*ent.Fact, bool) {
	var found *ent.Fact
	var foundEntry facts.Entry
	for _, row := range rows {
		if row.Kind != entfact.KindStable {
			continue
		}
		e := entry(row)
		if after, _ := facts.Later(e.By.Order, e.ObservedAt, order, at); after {
			continue
		}
		if found == nil || !laterThan(foundEntry, e) {
			found, foundEntry = row, e
		}
	}
	return found, found != nil
}

// laterThan reports whether a was first observed after b.
func laterThan(a, b facts.Entry) bool {
	after, _ := facts.Later(a.By.Order, a.ObservedAt, b.By.Order, b.ObservedAt)
	return after
}

// chainHead is the chain hash of device's newest chained row, "" when none
// is chained yet.
func chainHead(ctx context.Context, tx *ent.Tx, device int) (string, error) {
	row, err := tx.Fact.Query().
		Where(entfact.HasDeviceWith(entdevice.IDEQ(device)), entfact.ChainNotNil(), entfact.ChainNEQ("")).
		Order(ent.Desc(entfact.FieldID)).
		First(ctx)
	switch {
	case ent.IsNotFound(err):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("facts: %w", err)
	}
	return row.Chain, nil
}
