// Package entstore: how a repair removes rows (repair.go): each row digested
// (digestFact says by what), then deleted by ID a page at a time, inside the repair's
// transaction, which holds the device's facts exclusively, so every count
// is checked against what was read.
package entstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	entcoverage "github.com/Subject-Void-LLC/the-pleiades/internal/ent/factcoverage"
	entobs "github.com/Subject-Void-LLC/the-pleiades/internal/ent/factobservation"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/predicate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// errRemovalRaced marks a delete that removed another number of rows than
// were read, which the repair's hold on the device's facts should make
// impossible. The whole repair is rolled back.
var errRemovalRaced = errors.New("facts: a stored row changed while it was being removed; nothing was repaired")

// pageFacts calls each with every row of the device dev (its database ID)
// that where selects, in id order, a page at a time, and each row's place
// among them from 0: how an inspection and a repair read a device's rows.
func pageFacts(ctx context.Context, tx *ent.Tx, dev int, where []predicate.Fact, each func(row *ent.Fact, position int) error) error {
	after, position := 0, 0
	for {
		filter := append([]predicate.Fact{entfact.HasDeviceWith(entdevice.IDEQ(dev)), entfact.IDGT(after)}, where...)
		rows, err := tx.Fact.Query().Where(filter...).Order(ent.Asc(entfact.FieldID)).Limit(inspectPage).All(ctx)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		for _, row := range rows {
			after = row.ID
			if err := each(row, position); err != nil {
				return err
			}
			position++
		}
		if len(rows) < inspectPage {
			return nil
		}
	}
}

// removeFacts digests and removes each of the device's facts that where
// selects and drop (when not nil) chooses, a page at a time.
func (r *removal) removeFacts(ctx context.Context, where []predicate.Fact, drop func(*ent.Fact) bool) error {
	var ids []int
	flush := func() error {
		if len(ids) == 0 {
			return nil
		}
		n, err := r.tx.Fact.Delete().Where(entfact.IDIn(ids...)).Exec(ctx)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		if n != len(ids) {
			return errRemovalRaced
		}
		r.out.Facts += n
		ids = ids[:0]
		return nil
	}
	err := pageFacts(ctx, r.tx, r.dev.ID, where, func(row *ent.Fact, _ int) error {
		if drop != nil && !drop(row) {
			return nil
		}
		if err := r.digestFact(row); err != nil {
			return err
		}
		ids = append(ids, row.ID)
		// Rows already read are deleted as the walk goes; the next page
		// starts past them, so it never reads one twice.
		if len(ids) == inspectPage {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

// removeObservations digests and removes the device's observations, every
// one, or with from set every one numbered from *from on.
func (r *removal) removeObservations(ctx context.Context, from *int64) error {
	after := 0
	for {
		filter := []predicate.FactObservation{entobs.DeviceIDEQ(r.dev.ID), entobs.IDGT(after)}
		if from != nil {
			filter = append(filter, entobs.SeqGTE(*from))
		}
		rows, err := r.tx.FactObservation.Query().Where(filter...).Order(ent.Asc(entobs.FieldID)).Limit(inspectPage).All(ctx)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		ids := make([]int, 0, len(rows))
		for _, o := range rows {
			after = o.ID
			if err := r.digestObservation(o); err != nil {
				return err
			}
			ids = append(ids, o.ID)
		}
		if len(ids) > 0 {
			n, err := r.tx.FactObservation.Delete().Where(entobs.IDIn(ids...)).Exec(ctx)
			if err != nil {
				return fmt.Errorf("facts: %w", err)
			}
			if n != len(ids) {
				return errRemovalRaced
			}
			r.out.Observations += n
		}
		if len(rows) < inspectPage {
			return nil
		}
	}
}

// removeCoverages digests and removes the device's coverages: every one,
// or with orphans only those no observation of the device still names.
func (r *removal) removeCoverages(ctx context.Context, orphans bool) error {
	after := 0
	for {
		rows, err := r.tx.FactCoverage.Query().
			Where(entcoverage.HasDeviceWith(entdevice.IDEQ(r.dev.ID)), entcoverage.IDGT(after)).
			Order(ent.Asc(entcoverage.FieldID)).Limit(inspectPage).All(ctx)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		ids := make([]int, 0, len(rows))
		for _, c := range rows {
			after = c.ID
			if orphans {
				named, err := r.tx.FactObservation.Query().Where(entobs.DeviceIDEQ(r.dev.ID), entobs.CoverageEQ(c.Digest)).Exist(ctx)
				if err != nil {
					return fmt.Errorf("facts: %w", err)
				}
				if named {
					continue
				}
			}
			if err := r.digestCoverage(c); err != nil {
				return err
			}
			ids = append(ids, c.ID)
		}
		if len(ids) > 0 {
			n, err := r.tx.FactCoverage.Delete().Where(entcoverage.IDIn(ids...)).Exec(ctx)
			if err != nil {
				return fmt.Errorf("facts: %w", err)
			}
			if n != len(ids) {
				return errRemovalRaced
			}
			r.out.Coverages += n
		}
		if len(rows) < inspectPage {
			return nil
		}
	}
}

// digestFact writes row into the removal digest: one canonical JSON array
// per row and a newline after it, which canonical JSON never holds, so no
// two sequences of rows give the same bytes. A row is named by what the
// facts key made of it (its token, keyed hash, keys and chain link) and its
// time, kind, gather and provenance, in the order rows are stored. Its
// sealed payload is left out, since a master key rotation seals it again
// and the digest should still match a backup taken before; so is its ID,
// which the database assigns and a table rebuild need not keep.
func (r *removal) digestFact(row *ent.Fact) error {
	var prev any
	if row.ChainPrev != nil {
		prev = *row.ChainPrev
	}
	return r.digest([]any{"fact", row.KeyToken, row.Hash, row.KeyedBy, string(row.Kind),
		storedTime(row.CreatedAt), row.ObservationSeq, prev, row.Chain, row.Provenance})
}

// digestCoverage writes c into the removal digest, without its ID.
func (r *removal) digestCoverage(c *ent.FactCoverage) error {
	return r.digest([]any{"coverage", c.Digest, c.Stable, c.Volatile})
}

// digestObservation writes o into the removal digest, without its ID.
func (r *removal) digestObservation(o *ent.FactObservation) error {
	return r.digest([]any{"observation", o.Seq, storedTime(o.ObservedAt), o.OrderSource, o.OrderToken, o.Provenance, o.Coverage})
}

// digest writes fields as one canonical line into the removal digest.
func (r *removal) digest(fields []any) error {
	line, err := facts.Canonical(fields)
	if err != nil {
		return fmt.Errorf("facts: digesting a removed row: %w", err)
	}
	_, _ = r.mac.Write(append(line, '\n'))
	return nil
}

// storedTime is t as a row's digest line holds it.
func storedTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
