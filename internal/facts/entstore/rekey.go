// Package entstore: a change of the facts key with both keys live (the
// approved Phase 41 item: "the hash key's rotation re-tokenizes and
// re-hashes, a key change, not drift").
//
// A Controller given PLEIADES_FACTS_DIGEST_KEY_PREVIOUS beside a new
// PLEIADES_FACTS_DIGEST_KEY holds both sets of keys (WithPreviousKeys). The
// first time the store reads or writes a device whose rows name the previous
// keys, it re-keys the device's whole stored history in one transaction
// first, so no lookup ever needs two tokens: every place a token or a keyed
// hash is kept for the device is rewritten under the current keys.
//
//   - Each Fact row: its key_token, its keyed hash, its chain link (for a
//     stable row, chain_prev and chain, recomputed in the history's own
//     order), its keyed_by, and its payload, which is bound to the token and
//     so is opened under the old token and sealed again under the new one
//     through internal/crypto, the one sealing path.
//   - Each FactCoverage: its token lists and the digest over them.
//   - Each FactObservation: the coverage digest it names.
//
// Nothing else changes: an observation's times and provenance, a row's
// created_at, provenance and observation_seq, and every value stay exactly
// as they were, no row is added, and no sighting is written. A re-key is a
// key change, not drift. A value that itself holds a keyed file digest
// (facts.KeyedDigestField, made by the digest keyer from the same secret) is
// kept as it is: the raw digest it was made from is not stored, so it cannot
// be keyed again, and it reads as changed once, the next time the file is
// gathered under the new key.
//
// A device is re-keyed only when everything it holds verifies under the
// previous keys: every row opens as the device's own, and the chain over its
// stable rows is whole. Re-keying a history that does not verify would make
// an edited one read as whole under the new keys, so such a device is
// refused (ErrRekeyRefused) and left exactly as it was.
//
// # Two writers at once
//
// Two things can write a device while it is re-keyed: another replica's
// re-key, and a gather stored by a replica that still holds only the
// previous key (one not yet restarted). The second matters most: what it
// writes names the previous keys and chains to a head the re-key rewrites,
// so a gather committed beside a re-key would leave the device under both
// keys, which no store can verify and every store then refuses.
//
// The re-key reads the device's rows inside its transaction and decides
// again there: rows already under the current keys mean another transaction
// finished first, and nothing is written. Every write is a compare-and-swap
// on what was read (internal/ent's fact_rekey.go), and a lost one fails the
// whole transaction (ent.ErrFactRekeyRaced), which is rolled back and the
// device read again.
//
// That is not enough on PostgreSQL (READ COMMITTED): rows are read without
// locks, and a gather only inserts, so nothing it writes meets the re-key's
// compare-and-swap. So every record holds the device's facts shared and a
// re-key holds them exclusively (ent.Tx.LockFactsOfDevice, a row lock on the
// device's own row), each before reading anything of them: a re-key waits for
// the records already writing and then reads what they committed, and a
// record waits for the re-key and then finds the device under the current
// keys. SQLite has one writer at a time, and in WAL mode (the Controller's)
// a transaction whose read another commit overtook cannot then write, so a
// gather beside a re-key fails at its first write and its batch comes again;
// a re-key there takes the write lock before it reads, so a second re-key
// waits for the first and reads what it committed, rather than failing at
// once as a transaction that has read does. The compare-and-swap stays
// behind both as a second line: two re-keys that did both read the previous
// keys write their Fact rows in id order, first, so on PostgreSQL the second
// blocks on the first's row lock, then changes no row and rolls back.
package entstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// ErrRekeyRefused marks a device whose stored facts name the previous facts
// key but do not verify under it, or name both keys at once. The store does
// not re-key it, since that would make a broken or edited history read as
// whole under the new key, and serves none of it. A Controller started with
// the previous key as PLEIADES_FACTS_DIGEST_KEY reads it, and its chain
// check says where it breaks.
var ErrRekeyRefused = errors.New("facts: the device's stored facts name the previous facts key but do not verify under it, so they are not re-keyed")

// rekeyed is what one re-key rewrote.
type rekeyed struct {
	// facts, coverages and observations count the rows rewritten.
	facts, coverages, observations int
	// dropped counts the tokens a coverage listed that no row of the device
	// held: they matched nothing before and would match nothing after.
	dropped int
}

// rekeyRow is one Fact row's rewrite: the row as read, its keyed columns
// under the current keys, and its payload sealed for its new token.
type rekeyRow struct {
	row     *ent.Fact
	now     ent.FactKeyed
	payload map[string]any
}

// needsRekey is a record that found its device under the previous keys and
// wrote nothing: the device (dev, its database ID) is re-keyed first.
type needsRekey struct{ dev int }

// Error implements error.
func (e *needsRekey) Error() string {
	return "facts: the device's stored facts are under the previous facts key and are re-keyed first"
}

// rekeyAttempts bounds how often settle re-keys one device when another
// transaction overtakes it: once, and once more after a writer that did not
// re-key (a gather stored under the previous key) committed first.
const rekeyAttempts = 2

// settle re-keys device (dev is its database ID) in a transaction of its
// own when its rows name the previous keys, and reports what it rewrote.
// When the re-key loses to another transaction (a compare-and-swap that
// found its row changed, or SQLite refusing a write from a read another
// commit overtook), the device is read again: under the current keys now,
// that is success, and still under the previous ones it is tried again.
//
// Every read calls it (Current, History and Verify, through bound), not
// only a write, so a caller holding only inventory:read, a GET of the
// device's facts or its fact chain, is what performs a device's one-time
// re-key after a change of the facts key, and waits for it. While it runs
// it holds the device's facts exclusively (ent.Tx.LockFactsOfDevice: on
// PostgreSQL the device's own row, FOR NO KEY UPDATE; on SQLite the
// database's one write lock), so every record of the device waits for it,
// and on SQLite every write to the database does too; its transaction
// rewrites every row, coverage and observation of the device. A second read
// of the device meanwhile finds it still under the previous keys and waits
// to re-key it in turn, then finds nothing left to do; reads of other
// devices go on beside it. That is by design: a read under the new keys
// cannot find a row under the previous ones, whose token differs, so it
// re-keys the device first or would serve nothing, and the re-key happens
// once per device per change of the key, which RekeyAll does at start for
// every device anyway. When there is nothing to do, settle costs keyState
// alone: one query over the device's rows through the device index.
func (s *Store) settle(ctx context.Context, dev int, device string) (rekeyed, error) {
	state, err := s.keyState(ctx, s.client.Fact, dev)
	if err != nil || state == keysCurrent {
		return rekeyed{}, err
	}
	for attempt := 1; ; attempt++ {
		done, err := s.rekeyTx(ctx, dev, device)
		if err == nil || errors.Is(err, ErrKeysChanged) || errors.Is(err, ErrRekeyRefused) || ctx.Err() != nil {
			return done, err
		}
		again, aerr := s.keyState(ctx, s.client.Fact, dev)
		if aerr != nil {
			return rekeyed{}, aerr
		}
		if again == keysCurrent {
			return rekeyed{}, nil
		}
		if attempt == rekeyAttempts {
			return rekeyed{}, err
		}
	}
}

// rekeyTx is rekey in a transaction of its own, committed only when the
// whole device was rewritten.
func (s *Store) rekeyTx(ctx context.Context, dev int, device string) (rekeyed, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return rekeyed{}, fmt.Errorf("facts: %w", err)
	}
	done, err := s.rekey(ctx, tx, dev, device)
	if err != nil {
		_ = tx.Rollback()
		return rekeyed{}, err
	}
	if err := tx.Commit(); err != nil {
		return rekeyed{}, fmt.Errorf("facts: %w", err)
	}
	return done, nil
}

// rekey rewrites, inside tx, every token and keyed hash device holds under
// the previous keys as the current keys make them (this file's doc). The
// rows are read and judged again here, inside the transaction: already
// under the current keys is nothing to do, any third keys is
// ErrKeysChanged, and both keys at once is ErrRekeyRefused.
func (s *Store) rekey(ctx context.Context, tx *ent.Tx, dev int, device string) (rekeyed, error) {
	// Before anything of the device is read: no record of it runs beside
	// this, and none begun before it is still writing (this file's doc).
	if err := tx.LockFactsOfDevice(ctx, dev, true); err != nil {
		return rekeyed{}, fmt.Errorf("facts: %w", err)
	}
	rows, err := tx.Fact.Query().
		Where(entfact.HasDeviceWith(entdevice.IDEQ(dev))).
		Order(ent.Asc(entfact.FieldID)).
		All(ctx)
	if err != nil {
		return rekeyed{}, fmt.Errorf("facts: %w", err)
	}
	current, previous := s.keys.ID(), s.previousID()
	under := 0
	for _, row := range rows {
		switch {
		case previous != "" && row.KeyedBy == previous:
			under++
		case row.KeyedBy != current:
			return rekeyed{}, ErrKeysChanged
		}
	}
	if under == 0 {
		// Another transaction re-keyed it first.
		return rekeyed{}, nil
	}
	if under != len(rows) {
		// A history is under one key or the other. Both at once is a writer
		// that kept the previous key alone storing beside a re-key; its rows
		// chain to a head the re-key rewrote, so nothing verifies the join.
		return rekeyed{}, fmt.Errorf("%w: %d of its %d stored rows name the current key and the rest the previous one", ErrRekeyRefused, len(rows)-under, len(rows))
	}
	plan, tokens, err := s.planRekey(device, rows)
	if err != nil {
		return rekeyed{}, err
	}
	if s.beforeRewrite != nil {
		s.beforeRewrite()
	}
	// The Fact rows first, in id order (this file's doc says why).
	for _, r := range plan {
		was := ent.FactKeyed{KeyToken: r.row.KeyToken, Hash: r.row.Hash, KeyedBy: r.row.KeyedBy}
		if err := tx.RekeyFact(ctx, r.row.ID, dev, was, r.now); err != nil {
			return rekeyed{}, err
		}
		// The payload through its own setter, which crypto.FactPayloadHook
		// guards: it takes only a sealed envelope.
		if err := tx.Fact.UpdateOneID(r.row.ID).SetPayload(r.payload).Exec(ctx); err != nil {
			return rekeyed{}, fmt.Errorf("facts: %w", err)
		}
	}
	done, err := rekeyCoverages(ctx, tx, dev, tokens)
	if err != nil {
		return rekeyed{}, err
	}
	done.facts = len(plan)
	return done, nil
}

// planRekey is the rewrite of device's rows (all of them, in id order, every
// one under the previous keys): each opened and held to the previous keys,
// the chain over the stable rows verified under them, and each row's token,
// keyed hash, chain link and payload made again under the current keys. The
// map is each old token's new one. Anything that does not verify is
// ErrRekeyRefused, and nothing is written.
func (s *Store) planRekey(device string, rows []*ent.Fact) ([]rekeyRow, map[string]string, error) {
	plan := make([]rekeyRow, 0, len(rows))
	tokens := make(map[string]string, len(rows))
	var links []facts.Link
	head := ""
	for i, row := range rows {
		e, opened, err := s.openUnder(s.previous, device, row)
		if err != nil {
			// The reason is the store's, which names no stored byte.
			return nil, nil, fmt.Errorf("%w: stored row %d does not open as the device's under the previous key (%w)", ErrRekeyRefused, i, err)
		}
		token, err := s.keys.Token(device, e.Key)
		if err != nil {
			return nil, nil, fmt.Errorf("facts: %w", err)
		}
		// The value as it stands, a keyed file digest in it included: that
		// digest's raw form is not stored, so it is kept, not keyed again.
		hash, err := s.keys.Hash(device, e.Key, e.Value)
		if err != nil {
			return nil, nil, fmt.Errorf("facts: %w", err)
		}
		now := ent.FactKeyed{KeyToken: token, Hash: hash, KeyedBy: s.keys.ID()}
		if row.Kind == entfact.KindStable {
			// The link as Verify reads it, under the previous keys.
			link := facts.Link{Key: e.Key, Value: e.Value, Hash: e.Hash, ObservedAt: e.ObservedAt, By: e.By, Chain: row.Chain}
			if row.ChainPrev != nil {
				link.Prev = *row.ChainPrev
			}
			links = append(links, link)
			// The new link covers the new keyed hash, from the same time and
			// provenance, after the new link before it, keyed under the
			// current chain key.
			chain, err := s.keys.ChainHash(device, head, e.Key, hash, e.ObservedAt, e.By)
			if err != nil {
				return nil, nil, fmt.Errorf("facts: %w", err)
			}
			now.Chained, now.ChainPrev, now.Chain = true, head, chain
			head = chain
		}
		// The same key and value text, sealed for the new token: the payload's
		// associated data is the token, so it cannot stay as it was.
		payload, err := crypto.SealFactPayload(s.envelope, token, opened)
		if err != nil {
			return nil, nil, fmt.Errorf("facts: %w", err)
		}
		plan = append(plan, rekeyRow{row: row, now: now, payload: payload})
		tokens[row.KeyToken] = token
	}
	if v := facts.VerifyChain(device, links, s.previous); v.Break != nil {
		return nil, nil, fmt.Errorf("%w: under the previous key its chain breaks at stored value %d: %s", ErrRekeyRefused, v.Break.Index, v.Break.Reason)
	}
	return plan, tokens, nil
}
