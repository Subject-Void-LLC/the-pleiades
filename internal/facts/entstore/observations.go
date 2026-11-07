// Package entstore: the device's observations (Phase 41h), one immutable
// FactObservation per gather, and the FactCoverage each names, from which a
// stored value's last confirmation is read.
package entstore

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entcoverage "github.com/Subject-Void-LLC/the-pleiades/internal/ent/factcoverage"
	entobs "github.com/Subject-Void-LLC/the-pleiades/internal/ent/factobservation"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/predicate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// nextSeq is the number device's next observation takes: one past its
// newest. Two stores reading the same newest at once both take the same
// number, and the unique index on (seq, device) fails the second to commit.
func nextSeq(ctx context.Context, tx *ent.Tx, device int) (int64, error) {
	row, err := tx.FactObservation.Query().
		Where(entobs.DeviceIDEQ(device)).
		Order(ent.Desc(entobs.FieldSeq)).
		Select(entobs.FieldSeq).
		First(ctx)
	switch {
	case ent.IsNotFound(err):
		return 1, nil
	case err != nil:
		return 0, fmt.Errorf("facts: %w", err)
	}
	return row.Seq + 1, nil
}

// orderColumns is an order as the observation's order columns hold it: a
// token past what a signed 64-bit column holds is stored as an unknown order,
// which every comparison in the database then reads by the clock alone, and
// which earlierThan never leaves out.
func orderColumns(o facts.Order) (string, int64) {
	if !o.Known() || o.Token > math.MaxInt64 {
		return "", 0
	}
	return o.Source, int64(o.Token) // #nosec G115 -- bounded by the check above
}

// writeObservation stores obs as device's observation number seq, and its
// coverage when the device has none with the same digest.
func writeObservation(ctx context.Context, tx *ent.Tx, dev *ent.Device, seq int64, obs facts.Observation, by map[string]any) error {
	covers := facts.CoverageOf(obs.Entries)
	digest := covers.Digest()
	known, err := tx.FactCoverage.Query().
		Where(entcoverage.DigestEQ(digest), entcoverage.HasDeviceWith(entdevice.IDEQ(dev.ID))).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	if !known {
		if err := tx.FactCoverage.Create().SetDigest(digest).SetStable(covers.Stable).SetVolatile(covers.Volatile).SetDevice(dev).Exec(ctx); err != nil {
			return fmt.Errorf("facts: %w", err)
		}
	}
	source, token := orderColumns(obs.By.Order)
	err = tx.FactObservation.Create().
		SetSeq(seq).
		SetObservedAt(facts.StoredTime(obs.At)).
		SetOrderSource(source).
		SetOrderToken(token).
		SetProvenance(by).
		SetCoverage(digest).
		SetDevice(dev).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

// notEarlierThan selects, by the order columns, every observation that
// facts.Later does not place before (order, at), and possibly some it does:
// the ones a confirmation later than (order, at), or tied with it, could come
// from. It never leaves one out, so finding none proves there is none.
func notEarlierThan(order facts.Order, at time.Time) (predicate.FactObservation, bool) {
	if order.Known() && order.Token > math.MaxInt64 {
		// The database cannot compare this token; the caller reads them all.
		return nil, false
	}
	at = facts.StoredTime(at)
	unknown := entobs.Or(entobs.OrderSourceEQ(""), entobs.OrderTokenEQ(0))
	if !order.Known() {
		return entobs.Or(entobs.ObservedAtGTE(at), unknown), true
	}
	token := int64(order.Token) // #nosec G115 -- bounded by the check above
	return entobs.Or(
		// Ordered after it by the same counter.
		entobs.And(entobs.OrderSourceEQ(order.Source), entobs.OrderTokenGT(token)),
		// The same run, or another counter, or an unknown order: the
		// clock decides.
		entobs.And(entobs.OrderSourceEQ(order.Source), entobs.OrderTokenEQ(token), entobs.ObservedAtGTE(at)),
		entobs.And(entobs.OrderSourceNEQ(order.Source), entobs.ObservedAtGTE(at)),
		// A token the columns could not hold reads as unknown; it is kept
		// whatever its time, since its real token may still compare.
		unknown,
	), true
}

// anyNotEarlier reports whether device has an observation after seq that
// notEarlierThan (order, at) selects.
func anyNotEarlier(ctx context.Context, tx *ent.Tx, device int, seq int64, order facts.Order, at time.Time) (bool, error) {
	p, ok := notEarlierThan(order, at)
	if !ok {
		return true, nil
	}
	found, err := tx.FactObservation.Query().
		Where(entobs.DeviceIDEQ(device), entobs.SeqGT(seq), p).
		Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("facts: %w", err)
	}
	return found, nil
}

// observationsAfter is device's observations numbered after seq, with what
// each covered, read through client (a transaction's or the store's).
func observationsAfter(ctx context.Context, client queryClient, device int, seq int64) ([]facts.Seen, error) {
	rows, err := client.observations().Query().
		Where(entobs.DeviceIDEQ(device), entobs.SeqGT(seq)).
		Order(ent.Asc(entobs.FieldSeq)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	digests := map[string]bool{}
	for _, row := range rows {
		digests[row.Coverage] = true
	}
	covers, err := coverages(ctx, client, device, digests)
	if err != nil {
		return nil, err
	}
	out := make([]facts.Seen, 0, len(rows))
	for _, row := range rows {
		out = append(out, facts.Seen{Seq: row.Seq, At: row.ObservedAt.UTC(), By: decodeProvenance(row.Provenance), Covers: covers[row.Coverage]})
	}
	return out, nil
}

// coverages reads device's coverages named by digests.
func coverages(ctx context.Context, client queryClient, device int, digests map[string]bool) (map[string]*facts.Coverage, error) {
	names := make([]string, 0, len(digests))
	for d := range digests {
		names = append(names, d)
	}
	out := map[string]*facts.Coverage{}
	for _, chunk := range chunked(names, 500) {
		rows, err := client.coverages().Query().
			Where(entcoverage.DigestIn(chunk...), entcoverage.HasDeviceWith(entdevice.IDEQ(device))).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("facts: %w", err)
		}
		for _, row := range rows {
			out[row.Digest] = &facts.Coverage{Stable: row.Stable, Volatile: row.Volatile}
		}
	}
	return out, nil
}

// queryClient is what reading observations needs, from a transaction or a
// client alike.
type queryClient interface {
	observations() *ent.FactObservationClient
	coverages() *ent.FactCoverageClient
}

// txClient reads through a transaction.
type txClient struct{ tx *ent.Tx }

// observations is the transaction's FactObservation client.
func (c txClient) observations() *ent.FactObservationClient { return c.tx.FactObservation }

// coverages is the transaction's FactCoverage client.
func (c txClient) coverages() *ent.FactCoverageClient { return c.tx.FactCoverage }

// storeClient reads through the store's client.
type storeClient struct{ client *ent.Client }

// observations is the client's FactObservation client.
func (c storeClient) observations() *ent.FactObservationClient { return c.client.FactObservation }

// coverages is the client's FactCoverage client.
func (c storeClient) coverages() *ent.FactCoverageClient { return c.client.FactCoverage }
