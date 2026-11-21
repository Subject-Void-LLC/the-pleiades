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
// under other keys than the store holds, current or previous. The store
// refuses the device rather than start a second history beside the first
// that no lookup would join to it. A device under the previous keys is not
// refused: it is re-keyed (rekey.go).
var ErrKeysChanged = errors.New("facts: the device's stored facts were sealed under another facts key than this controller holds; set PLEIADES_FACTS_DIGEST_KEY_PREVIOUS to the key they were sealed under, beside the new PLEIADES_FACTS_DIGEST_KEY, so they are re-keyed")

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

// keyState is how a device's stored rows stand against the store's keys.
type keyState int

const (
	// keysCurrent: every row was written under the current keys, or there is
	// no row.
	keysCurrent keyState = iota
	// keysPrevious: some row names the previous keys and none names any
	// other but the current ones; the device is to be re-keyed (rekey.go).
	keysPrevious
)

// keyState reads which keys device's rows (device is its database ID) name:
// the current keys alone, or the previous ones too. A row naming any other
// keys is ErrKeysChanged, as is one naming the previous keys when the store
// holds none. Every row names some keys: the schema refuses one that does not
// (the fact_keyed check, internal/ent/schema's fact.go), which is what a build
// from before facts were sealed would write.
func (s *Store) keyState(ctx context.Context, rows factQuerier, device int) (keyState, error) {
	others, err := rows.Query().
		Where(entfact.HasDeviceWith(entdevice.IDEQ(device)), entfact.KeyedByNEQ(s.keys.ID())).
		Unique(true).
		Select(entfact.FieldKeyedBy).
		Strings(ctx)
	if err != nil {
		return 0, fmt.Errorf("facts: %w", err)
	}
	if len(others) == 0 {
		return keysCurrent, nil
	}
	if previous := s.previousID(); previous != "" && len(others) == 1 && others[0] == previous {
		return keysPrevious, nil
	}
	return 0, ErrKeysChanged
}

// previousID is the identity of the store's previous keys, "" when it holds
// none usable: then no row is ever taken to be under them.
func (s *Store) previousID() string {
	if s.previous == nil || s.previous.Ready() != nil {
		return ""
	}
	return s.previous.ID()
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
// it was first observed (sighted reads its later confirmations), under the
// store's current keys (openUnder).
func (s *Store) open(device string, row *ent.Fact) (facts.Entry, error) {
	e, _, err := s.openUnder(s.keys, device, row)
	return e, err
}

// openUnder is row opened under keys, as an entry, with its payload as it
// opened. The payload must open as the row's own, the key it holds must be
// the one whose token for device under keys the row carries (a row copied
// from another device opens, since its token came with it, and is refused
// here), and the value it holds must be the one the row's keyed hash under
// keys describes.
func (s *Store) openUnder(keys *facts.Keys, device string, row *ent.Fact) (facts.Entry, map[string]any, error) {
	opened, err := crypto.OpenFactPayload(s.envelope, row.KeyToken, row.Payload)
	if err != nil {
		// The reason is the envelope's, which names no stored byte.
		return facts.Entry{}, nil, fmt.Errorf("%w: %w", ErrNotThisDevices, err)
	}
	key, _ := opened[payloadFactKey].(string)
	token, err := keys.Token(device, key)
	if err != nil || token != row.KeyToken {
		return facts.Entry{}, nil, fmt.Errorf("%w: it holds a key whose token is not the row's", ErrNotThisDevices)
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
	if hash, err := keys.Hash(device, key, e.Value); err != nil || hash != row.Hash {
		return facts.Entry{}, nil, fmt.Errorf("%w: it holds a value its keyed hash does not describe", ErrNotThisDevices)
	}
	return e, opened, nil
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
