// Package entstore: what the store seals and opens (internal/facts'
// atrest.go, internal/crypto's fact_hook.go). A row's key and value are in
// its sealed payload and nowhere else; this file is the one place either
// leaves or enters it.
package entstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// payloadFactKey is where an opened payload holds the fact's key, beside its
// value under payloadKey.
const payloadFactKey = "key"

// ErrKeysChanged marks a device whose stored facts were tokenized and keyed
// under other keys than the store holds. Changing the facts secret is not a
// rotation this build performs: every token, keyed hash and chain link would
// have to be rewritten, and the history is insert-only. The store refuses
// the device rather than start a second history beside the first that no
// lookup would join to it.
var ErrKeysChanged = errors.New("facts: the device's stored facts were sealed under another facts key than this controller holds; restore the earlier PLEIADES_FACTS_DIGEST_KEY, since a change of it is not a rotation this build performs")

// ErrNotThisDevices marks a row that does not open as the device's own: its
// payload was sealed for another row, was edited, was never sealed, or holds
// a key whose token for this device is not the row's (a row moved from
// another device).
var ErrNotThisDevices = errors.New("facts: a stored fact does not open as this device's")

// ready reports whether the store can seal and open: it has an envelope
// service and usable keys. Without both it does nothing (facts.ErrNoKeys).
func (s *Store) ready() error {
	if s.envelope == nil {
		return fmt.Errorf("%w: the store has no envelope service", facts.ErrNoKeys)
	}
	return s.keys.Ready()
}

// factQuerier is the Fact client of a transaction or of the store's client.
type factQuerier interface {
	Query() *ent.FactQuery
}

// sameKeys refuses device (its database ID) when any of its rows was written
// under other keys than the store's (ErrKeysChanged).
func (s *Store) sameKeys(ctx context.Context, rows factQuerier, device int) error {
	other, err := rows.Query().
		Where(entfact.HasDeviceWith(entdevice.IDEQ(device)), entfact.KeyedByNEQ(s.keys.ID())).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	if other {
		return ErrKeysChanged
	}
	return nil
}

// seal is e's key and value as the row token names holds them: sealed, bound
// to that row.
func (s *Store) seal(token string, e facts.Entry) (map[string]any, error) {
	payload, err := crypto.SealFactPayload(s.envelope, token, map[string]any{payloadFactKey: e.Key, payloadKey: mustCanonical(e.Value)})
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	return payload, nil
}

// open is row as a facts.Entry with its key and value, last confirmed when
// it was first observed (sighted reads its later confirmations). The payload
// must open as the row's own, the key it holds must be the one whose token
// for device the row carries (a row copied from another device opens, since
// its token came with it, and is refused here), and the value it holds must
// be the one the row's keyed hash describes.
func (s *Store) open(device string, row *ent.Fact) (facts.Entry, error) {
	opened, err := crypto.OpenFactPayload(s.envelope, row.KeyToken, row.Payload)
	if err != nil {
		// The reason is the envelope's, which names no stored byte.
		return facts.Entry{}, fmt.Errorf("%w: %w", ErrNotThisDevices, err)
	}
	key, _ := opened[payloadFactKey].(string)
	token, err := s.keys.Token(device, key)
	if err != nil || token != row.KeyToken {
		return facts.Entry{}, fmt.Errorf("%w: it holds a key whose token is not the row's", ErrNotThisDevices)
	}
	e := meta(row, key)
	if text, ok := opened[payloadKey].(string); ok {
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&e.Value); err != nil {
			e.Value = nil
		}
	}
	// The value is the one the row's keyed hash describes: a payload taken
	// from another row of the same key (an older value put back as the
	// newest) carries the right token and opens, and is refused here.
	if hash, err := s.keys.Hash(device, key, e.Value); err != nil || hash != row.Hash {
		return facts.Entry{}, fmt.Errorf("%w: it holds a value its keyed hash does not describe", ErrNotThisDevices)
	}
	return e, nil
}

// openAll is rows as entries, in order (open).
func (s *Store) openAll(device string, rows []*ent.Fact) ([]facts.Entry, error) {
	out := make([]facts.Entry, len(rows))
	for i, row := range rows {
		e, err := s.open(device, row)
		if err != nil {
			return nil, err
		}
		out[i] = e
	}
	return out, nil
}

// valueOf is the value of the one of rows (one key's) whose keyed hash is
// hash, opened: what a record reads when it must write a value again
// (facts.Resumed). Rows of one key with one hash hold one value.
func (s *Store) valueOf(device string, rows []*ent.Fact, hash string) (any, error) {
	for _, row := range rows {
		if row.Hash != hash {
			continue
		}
		e, err := s.open(device, row)
		if err != nil {
			return nil, err
		}
		return e.Value, nil
	}
	return nil, fmt.Errorf("facts: no stored row of %s holds the value a late change resumes", device)
}

// meta is row as a facts.Entry without its value: everything a record
// compares (the keyed hash, the kind, when and by which run it was first
// observed), under key, the key whose token found the row. Nothing is
// opened, so a record costs no decryption for the rows it only compares, and
// one row that does not open cannot stop a device's facts from being stored.
func meta(row *ent.Fact, key string) facts.Entry {
	kind := facts.KindStable
	if row.Kind == entfact.KindVolatile {
		kind = facts.KindVolatile
	}
	by := decodeProvenance(row.Provenance)
	return facts.Entry{Key: key, Hash: row.Hash, ObservedAt: row.CreatedAt.UTC(), LastSeenAt: row.CreatedAt.UTC(), Kind: kind, By: by, LastBy: by}
}

// sightings is facts.Sightings over rows and their entries (in the same
// order), with each entry named by its row's token while the fold runs: a
// coverage lists tokens, never keys, so that is what an observation's
// coverage is matched against.
func sightings(rows []*ent.Fact, entries []facts.Entry, seen []facts.Seen) []facts.Entry {
	stored := make([]facts.Stored, len(rows))
	for i, row := range rows {
		e := entries[i]
		e.Key = row.KeyToken
		stored[i] = facts.Stored{Entry: e, Seq: row.ObservationSeq}
	}
	out := facts.Sightings(stored, seen)
	for i := range out {
		out[i].Key = entries[i].Key
	}
	return out
}
