// Package entstore is the Controller's fact store over the ent Fact table
// (Phase 41, 41b, 41h), and the consumer that writes what Runners publish
// into it.
//
// It keeps the rules the Crawl CLI's file store keeps (internal/facts). A
// stable key's rows are the history of its value: a row is written only when
// an observation's hash differs from the value the key held as of the
// observation's run order. No row is ever updated. Each gather writes one
// FactObservation instead (observations.go), and when a value was last
// confirmed is read from those (facts.Sightings): the current value is the
// one most recently confirmed, by run order where two runs' orders compare
// (facts.Order) and by the clock where they do not, so a batch that arrives
// late (redelivered, or from a Runner whose clock runs behind) adds history
// without replacing a value confirmed after it. A volatile key keeps one row,
// replaced when its value changes.
//
// Stable rows form a per-device hash chain (chain_prev, chain). A unique
// index on (chain_prev, device) keeps it a line, and one on an observation's
// (seq, device) makes two replicas storing for one device at once take turns:
// the loser's batch is redelivered against the new head. No field of a row
// has a setter in the generated client but the shared timestamp mixin's
// updated_at, which nothing calls.
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
	at := facts.StoredTime(obs.At)
	by, err := encode(obs.By)
	if err != nil {
		return err
	}
	seq, err := nextSeq(ctx, tx, dev.ID)
	if err != nil {
		return err
	}
	head, err := chainHead(ctx, tx, dev.ID)
	if err != nil {
		return err
	}
	// Every row of the device's keys this observation names, read once: a
	// host's packages are thousands of keys (Phase 41c), and one query per
	// key made a batch thousands of round trips. An observation holds each
	// key once, so what one entry writes is never read by another.
	keys := make([]string, len(obs.Entries))
	for i, e := range obs.Entries {
		keys[i] = e.Key
	}
	byKey := map[string][]*ent.Fact{}
	for _, chunk := range chunked(keys, 500) {
		rows, err := tx.Fact.Query().
			Where(entfact.KeyIn(chunk...), entfact.HasDeviceWith(entdevice.IDEQ(dev.ID))).
			Order(ent.Asc(entfact.FieldID)).
			All(ctx)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		for _, row := range rows {
			byKey[row.Key] = append(byKey[row.Key], row)
		}
	}
	last, err := lastSightings(ctx, tx, dev.ID, obs, byKey)
	if err != nil {
		return err
	}
	for _, e := range obs.Entries {
		rows := byKey[e.Key]
		if e.Kind == facts.KindVolatile {
			if err := recordVolatile(ctx, tx, dev, rows, last, e, obs, seq, by); err != nil {
				return err
			}
			continue
		}
		prior := make([]facts.Entry, len(rows))
		for i, row := range rows {
			prior[i] = last[row.ID]
		}
		if cur, ok := facts.ValueAt(prior, obs.By.Order, at); ok && cur.Hash == e.Hash {
			// A confirmation: the observation written below is all of it.
			continue
		}
		if head, err = chainStable(ctx, tx, dev, device, head, e, at, obs.By, seq); err != nil {
			return err
		}
		// A late change keeps the value confirmed after it in the history
		// (FAILURE_PATTERNS 923).
		if back, ok := facts.Resumed(prior, facts.Entry{Key: e.Key, Hash: e.Hash, ObservedAt: at, By: obs.By}); ok {
			if head, err = chainStable(ctx, tx, dev, device, head, back, back.ObservedAt, back.By, seq); err != nil {
				return err
			}
		}
	}
	return writeObservation(ctx, tx, dev, seq, obs, by)
}

// chainStable writes e as a stable row first observed at at by p, written by
// observation seq and chained after head, and returns the new head.
func chainStable(ctx context.Context, tx *ent.Tx, dev *ent.Device, device, head string, e facts.Entry, at time.Time, p facts.Provenance, seq int64) (string, error) {
	by, err := encode(p)
	if err != nil {
		return "", err
	}
	chain, err := facts.ChainHash(device, head, e.Key, e.Hash, at, p)
	if err != nil {
		return "", fmt.Errorf("facts: %w", err)
	}
	if err := create(tx, dev, e, at, by, seq).SetKind(entfact.KindStable).SetChainPrev(head).SetChain(chain).Exec(ctx); err != nil {
		return "", fmt.Errorf("facts: %w", err)
	}
	return chain, nil
}

// recordVolatile keeps e as its key's one volatile row when obs is the
// latest observation of it: a value the same as the one held is confirmed by
// the observation alone, and a different one replaces it; a late observation
// changes nothing.
func recordVolatile(ctx context.Context, tx *ent.Tx, dev *ent.Device, rows []*ent.Fact, last map[int]facts.Entry, e facts.Entry, obs facts.Observation, seq int64, by map[string]any) error {
	held := volatileRows(rows)
	at := facts.StoredTime(obs.At)
	if len(held) > 0 {
		newest := held[len(held)-1]
		if !laterThan(facts.Entry{By: obs.By, ObservedAt: at}, lastOf(last, newest)) {
			return nil
		}
		if newest.Hash == e.Hash && len(held) == 1 {
			return nil
		}
		ids := make([]int, len(held))
		for i, row := range held {
			ids[i] = row.ID
		}
		if _, err := tx.Fact.Delete().Where(entfact.IDIn(ids...)).Exec(ctx); err != nil {
			return fmt.Errorf("facts: %w", err)
		}
	}
	if err := create(tx, dev, e, at, by, seq).SetKind(entfact.KindVolatile).Exec(ctx); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

// lastOf is row's last confirmation as an entry whose first sighting is it,
// for laterThan.
func lastOf(last map[int]facts.Entry, row *ent.Fact) facts.Entry {
	e := last[row.ID]
	return facts.Entry{By: e.LastBy, ObservedAt: e.LastSeenAt}
}

// create starts a row for e, first observed at at by by, written by
// observation seq.
func create(tx *ent.Tx, dev *ent.Device, e facts.Entry, at time.Time, by map[string]any, seq int64) *ent.FactCreate {
	return tx.Fact.Create().
		SetKey(e.Key).
		SetPayload(map[string]any{payloadKey: mustCanonical(e.Value)}).
		SetHash(e.Hash).
		SetCreatedAt(at).
		SetProvenance(by).
		SetObservationSeq(seq).
		SetDevice(dev)
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

// chunked splits keys into runs of at most n, so one IN list stays within
// what every dialect accepts as bound parameters.
func chunked(keys []string, n int) [][]string {
	var out [][]string
	for len(keys) > n {
		out = append(out, keys[:n])
		keys = keys[n:]
	}
	if len(keys) > 0 {
		out = append(out, keys)
	}
	return out
}
