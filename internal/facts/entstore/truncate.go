// Package entstore: truncate, the repair that keeps the longest prefix of a
// device's stored history that verifies (inspect.go has the design and why
// it is sound).
package entstore

import (
	"context"
	"errors"
	"fmt"

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
// holds (StateBroken, or StateRefused when the store holds the current key
// too) that it would keep some of (underCheck.keeps); a device it does not
// apply to is
// ErrNothingToRepair or ErrRepairDoesNotApply, and one whose kept part does
// not verify is ErrTruncateUnsound, each changing nothing.
func (s *Store) Truncate(ctx context.Context, name string, opts RepairOptions) (Removal, error) {
	return s.repair(ctx, name, factsreport.RepairTruncate, opts, func(ctx context.Context, r *removal) error {
		check, rekey, err := s.truncateCheck(r.before)
		if err != nil {
			return err
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

// truncateCheck is the check a truncate of a device found as before cuts
// by: the current keys' for a broken history, the previous keys' for a
// refused one, which is then to be re-keyed. Any other state is refused.
func (s *Store) truncateCheck(before inspected) (*underCheck, bool, error) {
	var check *underCheck
	rekey := false
	switch before.report.State {
	case factsreport.StateBroken:
		check = before.current
	case factsreport.StateRefused:
		if s.keys.ID() == "" {
			return nil, false, fmt.Errorf("%w: its history is under the previous key, and what a truncate keeps is re-keyed under the current one, so it needs PLEIADES_FACTS_DIGEST_KEY too", ErrRepairDoesNotApply)
		}
		check, rekey = before.previous, true
	case factsreport.StateEmpty, factsreport.StateHealthy, factsreport.StateRekeyPending:
		return nil, false, fmt.Errorf("%w: it is %s", ErrNothingToRepair, before.report.State)
	case factsreport.StateUnopened:
		return nil, false, fmt.Errorf("%w: no stored row of it opens, which is what a MASTER_ENCRYPTION_KEY other than the one its facts were sealed under looks like; run this with the server's master key settings", ErrRepairDoesNotApply)
	default:
		return nil, false, fmt.Errorf("%w: its rows are %s, so no prefix of them verifies under one key; only discard repairs it", ErrRepairDoesNotApply, before.report.State)
	}
	if !check.keeps(before.report.Rows.Total) {
		return nil, false, fmt.Errorf("%w: a truncate would keep none of its history, since no value verifies before its first failure except in the gather the cut removes; only discard repairs it", ErrRepairDoesNotApply)
	}
	return check, rekey, nil
}

// holds reports whether row opens as the device's own under check's keys,
// on its own.
func (s *Store) holds(check *underCheck, row *ent.Fact) bool {
	_, _, err := s.openUnder(check.keys, check.device, row)
	return err == nil
}
