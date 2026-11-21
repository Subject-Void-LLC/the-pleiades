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
// updated_at, which nothing calls, and the sealed payload, which a master key
// rotation seals again (internal/crypto's fact_hook.go).
//
// Nothing a row holds names a fact in plaintext (internal/facts' atrest.go):
// a row is found by its key's token, its hash is keyed, and its key and value
// are sealed in its payload (sealed.go). A store with no keys stores nothing
// and reads nothing.
//
// One operation rewrites stored rows in place: a change of the facts key
// with both keys live (rekey.go), which moves a device's every token, keyed
// hash and chain link from the previous keys to the current ones, through
// internal/ent's fact_rekey.go, the one writer of those columns. It changes
// no value, time or provenance and adds no row, and a device is re-keyed at
// most once per change of the key.
package entstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
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
	// keys tokenizes fact keys and keys fact hashes; envelope seals each
	// row's key and value (sealed.go). Without both the store fails closed.
	keys     *facts.Keys
	envelope *crypto.EnvelopeService
	// previous is the keys a change of the facts key moves away from
	// (WithPreviousKeys): a device whose rows name them is re-keyed under
	// keys before it is read or written (rekey.go). Nil re-keys nothing.
	previous *facts.Keys
	// beforeRewrite, when set, is called by a re-key after it has read and
	// checked a device and before it writes anything: a test's way to line
	// two re-keys up. Nil in every store but a test's.
	beforeRewrite func()
	// beforeRemove, when set, is called by a repair (repair.go) once it holds
	// the device and has inspected it, before it removes anything: a test's
	// way to race a gather against it. Nil in every store but a test's.
	beforeRemove func()
}

// Option configures a Store.
type Option func(*Store)

// WithPreviousKeys has the store hold previous beside its keys, for a change
// of the facts key with both keys live: a device whose rows name previous is
// re-keyed under the store's keys, whole and in one transaction, the first
// time it is read or written (and by RekeyAll), and a device under any third
// keys is still refused (ErrKeysChanged). A nil previous holds none.
func WithPreviousKeys(previous *facts.Keys) Option {
	return func(s *Store) { s.previous = previous }
}

// New returns a store over client that tokenizes and keys with keys and
// seals with envelope. With either missing it stores nothing and reads
// nothing (facts.ErrNoKeys): plaintext is never the fallback.
func New(client *ent.Client, keys *facts.Keys, envelope *crypto.EnvelopeService, opts ...Option) *Store {
	s := &Store{client: client, keys: keys, envelope: envelope}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

var _ facts.Store = (*Store)(nil)

// Record implements facts.Store, in one transaction.
func (s *Store) Record(ctx context.Context, device string, obs facts.Observation) error {
	if len(obs.Entries) == 0 {
		return nil
	}
	// A provenance naming a Collection identity no engine writes is never
	// stored (Phase 136b). Unlike the file store, which keeps what its own
	// build's engine made, this store keeps what a Runner of any build made,
	// so it holds the identity only to the part of the rule no build's group
	// list changes (CheckDeliveredCollection): how that Runner routed the
	// method is its build's to say.
	if err := obs.By.CheckDeliveredCollection(); err != nil {
		return fmt.Errorf("%w: %w", facts.ErrInvalid, err)
	}
	// Before anything is read or written: no keys, no record. Each hash is
	// keyed here, so every comparison and every row below holds the keyed one.
	if err := s.ready(); err != nil {
		return err
	}
	keyed, err := s.keys.Rekey(device, obs.Entries)
	if err != nil {
		return err
	}
	obs.Entries = keyed
	for attempt := 1; ; attempt++ {
		err = s.recordOnce(ctx, device, obs)
		// A device under the previous keys is re-keyed in a transaction of
		// its own, holding it exclusively (rekey.go), and the record tried
		// again: the record's own transaction holds it shared, and cannot
		// take it exclusively without waiting on every other record.
		var first *needsRekey
		if !errors.As(err, &first) || attempt == rekeyAttempts {
			return err
		}
		if _, err := s.settle(ctx, first.dev, device); err != nil {
			return err
		}
	}
}

// recordOnce is Record's one transaction, with obs's hashes already keyed.
func (s *Store) recordOnce(ctx context.Context, device string, obs facts.Observation) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	if err := s.record(ctx, tx, device, obs); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

// record is Record inside tx, with obs's hashes already keyed. The device's
// facts are held shared before anything of them is read
// (ent.Tx.LockFactsOfDevice), so a re-key of the device never runs beside
// this record; a device whose rows name the previous keys is not written
// here but answered with needsRekey, before any row is read.
func (s *Store) record(ctx context.Context, tx *ent.Tx, device string, obs facts.Observation) error {
	dev, err := tx.Device.Query().Where(entdevice.DeviceIDEQ(device)).Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("%w: %s", ErrUnknownDevice, device)
	}
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	if err := tx.LockFactsOfDevice(ctx, dev.ID, false); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	state, err := s.keyState(ctx, tx.Fact, dev.ID)
	if err != nil {
		return err
	}
	if state == keysPrevious {
		return &needsRekey{dev: dev.ID}
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
	// Rows are found by token: the table names no key.
	tokens := make([]string, len(obs.Entries))
	tokenOf, keyOf := make(map[string]string, len(obs.Entries)), make(map[string]string, len(obs.Entries))
	for i, e := range obs.Entries {
		token, err := s.keys.Token(device, e.Key)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		tokens[i], tokenOf[e.Key], keyOf[token] = token, token, e.Key
	}
	byKey := map[string][]*ent.Fact{}
	for _, chunk := range chunked(tokens, 500) {
		rows, err := tx.Fact.Query().
			Where(entfact.KeyTokenIn(chunk...), entfact.HasDeviceWith(entdevice.IDEQ(dev.ID))).
			Order(ent.Asc(entfact.FieldID)).
			All(ctx)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		for _, row := range rows {
			key := keyOf[row.KeyToken]
			byKey[key] = append(byKey[key], row)
		}
	}
	w := writer{store: s, tx: tx, dev: dev, device: device, tokenOf: tokenOf}
	last, err := lastSightings(ctx, tx, dev.ID, obs, byKey)
	if err != nil {
		return err
	}
	for _, e := range obs.Entries {
		rows := byKey[e.Key]
		if e.Kind == facts.KindVolatile {
			if err := w.recordVolatile(ctx, rows, last, e, obs, seq, by); err != nil {
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
		if head, err = w.chainStable(ctx, head, e, at, obs.By, seq); err != nil {
			return err
		}
		// A late change keeps the value confirmed after it in the history
		// (FAILURE_PATTERNS 923).
		if back, ok := facts.Resumed(prior, facts.Entry{Key: e.Key, Hash: e.Hash, ObservedAt: at, By: obs.By}); ok {
			// The rows were read without their values (sealed.go's meta);
			// the one value written again is opened now, from a row that
			// holds it.
			if back.Value, err = s.valueOf(device, rows, back.Hash); err != nil {
				return err
			}
			if head, err = w.chainStable(ctx, head, back, back.ObservedAt, back.By, seq); err != nil {
				return err
			}
		}
	}
	return w.writeObservation(ctx, seq, obs, by)
}

// writer is what one record writes rows with: the store's keys, the
// transaction, the device, and each of the observation's keys' tokens.
type writer struct {
	store   *Store
	tx      *ent.Tx
	dev     *ent.Device
	device  string
	tokenOf map[string]string
}

// chainStable writes e as a stable row first observed at at by p, written by
// observation seq and chained after head, and returns the new head.
func (w writer) chainStable(ctx context.Context, head string, e facts.Entry, at time.Time, p facts.Provenance, seq int64) (string, error) {
	by, err := encode(p)
	if err != nil {
		return "", err
	}
	chain, err := w.store.keys.ChainHash(w.device, head, e.Key, e.Hash, at, p)
	if err != nil {
		return "", fmt.Errorf("facts: %w", err)
	}
	row, err := w.create(e, at, by, seq)
	if err != nil {
		return "", err
	}
	if err := row.SetKind(entfact.KindStable).SetChainPrev(head).SetChain(chain).Exec(ctx); err != nil {
		return "", fmt.Errorf("facts: %w", err)
	}
	return chain, nil
}

// recordVolatile keeps e as its key's one volatile row when obs is the
// latest observation of it: a value the same as the one held is confirmed by
// the observation alone, and a different one replaces it; a late observation
// changes nothing.
func (w writer) recordVolatile(ctx context.Context, rows []*ent.Fact, last map[int]facts.Entry, e facts.Entry, obs facts.Observation, seq int64, by map[string]any) error {
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
		if _, err := w.tx.Fact.Delete().Where(entfact.IDIn(ids...)).Exec(ctx); err != nil {
			return fmt.Errorf("facts: %w", err)
		}
	}
	row, err := w.create(e, at, by, seq)
	if err != nil {
		return err
	}
	if err := row.SetKind(entfact.KindVolatile).Exec(ctx); err != nil {
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
// observation seq: found by its key's token, its key and value sealed, its
// hash the keyed one, and named for the keys that made them. The legacy key
// column is left empty.
func (w writer) create(e facts.Entry, at time.Time, by map[string]any, seq int64) (*ent.FactCreate, error) {
	token, ok := w.tokenOf[e.Key]
	if !ok {
		// Every entry's token was made before anything was read.
		return nil, fmt.Errorf("facts: no token for a fact of %s", w.device)
	}
	payload, err := w.store.seal(token, e)
	if err != nil {
		return nil, err
	}
	return w.tx.Fact.Create().
		SetKeyToken(token).
		SetPayload(payload).
		SetHash(e.Hash).
		SetKeyedBy(w.store.keys.ID()).
		SetCreatedAt(at).
		SetProvenance(by).
		SetObservationSeq(seq).
		SetDevice(w.dev), nil
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

// chunked splits keys (or tokens) into runs of at most n, so one IN list stays within
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
