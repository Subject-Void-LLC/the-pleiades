// Package entstore: truncate, the repair that keeps the longest prefix of a
// device's stored history that verifies (inspect.go has the design and why
// it is sound).
package entstore

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/predicate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
)

// ErrTruncateUnsound marks a truncate refused because what it would keep
// does not verify, which only a database whose storage order and gather
// numbers disagree gives. Nothing is changed; discard still applies.
var ErrTruncateUnsound = errors.New("facts: what a truncate would keep does not verify, since the history's storage order and its gather numbers disagree; nothing was changed, and only discard repairs it")

// Truncate keeps the part of the device named name's stored history before
// the gather that wrote the last stable row that verifies ahead of the first
// that fails, and removes the rest: every row that gather or a later one
// wrote, every such gather, the coverages no gather left names, and each
// volatile row that fails on its own, alone, its gather kept. A history
// under the previous key is then re-keyed under the current one, by the
// re-key itself. It applies to a history wholly under one key the store
// holds (StateBroken, StateRefused when the store holds the current key
// too, or StatePartlyUnopened over either) that it would keep some of
// (truncateBy); a device it does not apply to is ErrNothingToRepair or
// ErrRepairDoesNotApply, one with a row that does not open is ErrUnverifiable
// unless opts.TruncateUnverifiable, and one whose kept part does not verify
// is ErrTruncateUnsound, each changing nothing.
func (s *Store) Truncate(ctx context.Context, name string, opts RepairOptions) (Removal, error) {
	return s.repair(ctx, name, factsreport.RepairTruncate, opts, func(ctx context.Context, r *removal) error {
		check, rekey, err := truncateBy(r.before, s.keys.ID() != "")
		if err != nil {
			return err
		}
		// A row that does not open is never by itself proof of damage: it
		// may be sealed under a key this command was not given, and what the
		// cut removes after it may be intact.
		if r.before.report.Unverifiable && !opts.TruncateUnverifiable {
			return unverifiable(r.before.report, "--truncate-unverifiable-history")
		}
		if check.cut != nil {
			// The gather that wrote the last stable row that verified, and
			// every later one, whole: a row missing between that row and the
			// first that fails could be any of theirs, so none of their
			// observations can be kept to confirm what is left.
			from := check.lastGood
			if err := r.removeFacts(ctx, []predicate.Fact{entfact.ObservationSeqGTE(from)}, nil); err != nil {
				return err
			}
			if err := r.removeObservations(ctx, &from); err != nil {
				return err
			}
		}
		// A volatile row that fails on its own is not chained, so nothing
		// cuts it: it is removed by itself, and its gather kept. That is
		// sound because a volatile key holds one row (a new value replaces
		// it), so no older value is left for that gather's or a later one's
		// observation to confirm in its place, and its failure says nothing
		// of the stable rows, which the chain holds on their own.
		volatile := []predicate.Fact{entfact.KindEQ(entfact.KindVolatile)}
		if err := r.removeFacts(ctx, volatile, func(row *ent.Fact) bool { return !s.holds(check, row) }); err != nil {
			return err
		}
		if err := r.removeCoverages(ctx, true); err != nil {
			return err
		}
		// What is kept is checked again, as a whole, before anything is
		// re-keyed or committed.
		after, err := s.inspectTx(ctx, r.tx, r.dev, InspectOptions{})
		if err != nil {
			return err
		}
		if after.report.State.NeedsRepair() {
			return ErrTruncateUnsound
		}
		r.out.Kept = after.report.Rows.Total
		if rekey && r.out.Kept > 0 {
			done, err := s.rekey(ctx, r.tx, r.dev.ID, r.dev.DeviceID)
			if err != nil {
				return err
			}
			r.out.Rekeyed = done.facts
		}
		return nil
	})
}

// holds reports whether row opens as the device's own under check's keys,
// on its own.
func (s *Store) holds(check *underCheck, row *ent.Fact) bool {
	_, _, err := s.openUnder(check.keys, check.device, row)
	return err == nil
}
