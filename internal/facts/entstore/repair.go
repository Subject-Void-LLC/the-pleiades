// Package entstore: the repairs of a device's stored history, discard here
// and truncate in truncate.go (inspect.go has the design), and what both
// share: one transaction holding the device's facts, the digest of what is
// removed, and the activity record written in the same transaction.
package entstore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// ErrNothingToRepair marks a repair refused because the device's stored
// history needs none: it is empty, it verifies, or a re-key is pending.
var ErrNothingToRepair = errors.New("facts: the device's stored facts need no repair")

// ErrRepairDoesNotApply marks a repair the device's state does not allow;
// the error says which repair does.
var ErrRepairDoesNotApply = errors.New("facts: this repair does not apply to the device's stored facts")

// ErrUnverifiable marks a discard refused because nothing the command was
// given can say whether the history is broken: no row of it opens (a
// MASTER_ENCRYPTION_KEY other than the one it was sealed under looks just
// so), or its rows name a facts key the command was not given, or both
// facts keys at once. Discarding it takes the flag that names it.
var ErrUnverifiable = errors.New("facts: the device's stored facts cannot be verified with the keys this command was given")

// RepairOptions says how a repair runs.
type RepairOptions struct {
	// Confirm commits the repair. Without it the repair runs inside its
	// transaction and is rolled back, so what it reports is what it would
	// do and nothing is changed.
	Confirm bool
	// DiscardVerified has discard remove a history that verifies, or one a
	// re-key is pending for. Truncate never removes one.
	DiscardVerified bool
	// DiscardUnverifiable has discard remove a history the keys given
	// cannot verify either way (ErrUnverifiable): one no row of which opens,
	// or one under another facts key or under both. Truncate never removes
	// one.
	DiscardUnverifiable bool
	// Actor is who the activity stream records the repair as.
	Actor string
}

// Removal is what one repair removed, or with RepairOptions.Confirm unset
// would remove. It counts rows and names a digest, never a fact.
type Removal struct {
	// Repair is factsreport.RepairDiscard or RepairTruncate.
	Repair string
	// Committed is true when the repair was committed.
	Committed bool
	// Before is the device as the repair found it, under its lock.
	Before factsreport.Device
	// Facts, Coverages and Observations count the rows removed.
	Facts, Coverages, Observations int
	// Kept counts the stored facts a truncate kept, and Rekeyed the ones
	// it re-keyed from the previous key to the current one.
	Kept, Rekeyed int
	// Digest is the keyed digest of every removed row, by what the facts
	// key made of it and its time, kind, gather and provenance, never its
	// sealed payload or its ID (digestFact), under the removal key of the
	// keys DigestKey names.
	Digest, DigestKey string
	// Recorded is the activity stream's sentence for the repair.
	Recorded string
}

// Discard removes every stored fact, coverage and observation of the
// device named name, so its next gather starts a new history under the
// current keys. It refuses a device with nothing stored, one whose history
// verifies or is pending a re-key unless opts.DiscardVerified, and one the
// keys given cannot verify either way unless opts.DiscardUnverifiable.
func (s *Store) Discard(ctx context.Context, name string, opts RepairOptions) (Removal, error) {
	return s.repair(ctx, name, factsreport.RepairDiscard, opts, func(ctx context.Context, r *removal) error {
		switch st := r.before.report.State; st {
		case factsreport.StateEmpty:
			return fmt.Errorf("%w: it has no stored facts", ErrNothingToRepair)
		case factsreport.StateHealthy, factsreport.StateRekeyPending:
			if !opts.DiscardVerified {
				return fmt.Errorf("%w: its history verifies (%s); discarding a history that verifies takes a second flag that names it", ErrNothingToRepair, st)
			}
		case factsreport.StateUnopened, factsreport.StateOtherKeys, factsreport.StateMixedKeys:
			if !opts.DiscardUnverifiable {
				return fmt.Errorf("%w (%s): set the keys the server runs with first; discarding a history no key given can verify takes a second flag that names it", ErrUnverifiable, st)
			}
		}
		if err := r.removeFacts(ctx, nil, nil); err != nil {
			return err
		}
		if err := r.removeObservations(ctx, nil); err != nil {
			return err
		}
		return r.removeCoverages(ctx, false)
	})
}

// removal is one repair under way: its transaction, its device, what it
// found, and the digest and counts of what it has removed.
type removal struct {
	tx     *ent.Tx
	dev    *ent.Device
	before inspected
	mac    hash.Hash
	out    Removal
}

// repair runs do on the device named name in one transaction that holds
// the device's facts exclusively before anything of them is read, after
// inspecting the device there, and records it in the activity stream in
// the same transaction. It commits only with opts.Confirm.
func (s *Store) repair(ctx context.Context, name, what string, opts RepairOptions, do func(ctx context.Context, r *removal) error) (Removal, error) {
	if opts.Actor == "" {
		return Removal{}, errors.New("facts: a repair needs an actor for its record")
	}
	mac, keyID, err := s.removalMAC()
	if err != nil {
		return Removal{}, err
	}
	// The device's database ID, read before the transaction: on SQLite a
	// transaction that has read cannot then wait for the write lock the
	// device's hold takes (ent.Tx.LockFactsOfDevice).
	id, err := s.client.Device.Query().Where(entdevice.NameEQ(name)).OnlyID(ctx)
	if ent.IsNotFound(err) {
		return Removal{}, fmt.Errorf("%w: %s", ErrUnknownDevice, termsafe.Quote(name))
	}
	if err != nil {
		return Removal{}, fmt.Errorf("facts: %w", err)
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return Removal{}, fmt.Errorf("facts: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := tx.LockFactsOfDevice(ctx, id, true); err != nil {
		return Removal{}, fmt.Errorf("facts: %w", err)
	}
	dev, err := tx.Device.Get(ctx, id)
	if err != nil || dev.Name != name {
		// Removed or renamed between the two reads: nothing is repaired.
		return Removal{}, fmt.Errorf("%w: %s", ErrUnknownDevice, termsafe.Quote(name))
	}
	before, err := s.inspectTx(ctx, tx, dev, InspectOptions{})
	if err != nil {
		return Removal{}, err
	}
	r := &removal{tx: tx, dev: dev, before: before, mac: mac,
		out: Removal{Repair: what, Before: before.report, DigestKey: keyID}}
	// The digest is of this device's rows under this repair alone.
	_, _ = fmt.Fprintf(r.mac, "pleiades facts removal v1\x00%s\x00%s\n", what, dev.DeviceID)
	if s.beforeRemove != nil {
		s.beforeRemove()
	}
	if err := do(ctx, r); err != nil {
		return Removal{}, err
	}
	r.out.Digest = "hmac-sha256:" + hex.EncodeToString(r.mac.Sum(nil))
	r.out.Recorded = r.sentence()
	// In the same transaction: a repair is never committed unrecorded.
	if err := activity.NewEntStore(tx.Client()).Record(ctx, activity.Entry{
		Actor: opts.Actor, Action: activity.ActionDeleted, ObjectKind: activity.KindDeviceFacts,
		ObjectID: dev.ID, ObjectName: r.out.Recorded,
	}); err != nil {
		return Removal{}, fmt.Errorf("facts: %w", err)
	}
	if !opts.Confirm {
		return r.out, nil
	}
	if err := tx.Commit(); err != nil {
		return Removal{}, fmt.Errorf("facts: %w", err)
	}
	committed, r.out.Committed = true, true
	return r.out, nil
}

// removalMAC is a removal digest under the current keys, or the previous
// ones when the store holds no current keys, and the identity of the keys.
func (s *Store) removalMAC() (hash.Hash, string, error) {
	keys := s.keys
	if keys.ID() == "" {
		keys = s.previous
	}
	mac, err := keys.RemovalMAC()
	if err != nil {
		return nil, "", fmt.Errorf("%w: a repair digests what it removes under the facts key", err)
	}
	return mac, keys.ID(), nil
}

// sentence is the repair as the activity stream records it: the device,
// what was removed and kept, and the digest. It names no fact.
func (r *removal) sentence() string {
	o := r.out
	text := fmt.Sprintf("%s: %s removed %d stored facts, %d coverages and %d observations", r.dev.Name, o.Repair, o.Facts, o.Coverages, o.Observations)
	if o.Repair == factsreport.RepairTruncate {
		text += fmt.Sprintf(", kept %d and re-keyed %d", o.Kept, o.Rekeyed)
	}
	return text + fmt.Sprintf(" (was %s; removed rows %s under %s)", o.Before.State, o.Digest, o.DigestKey)
}
