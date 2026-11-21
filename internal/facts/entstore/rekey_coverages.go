// Package entstore: a re-key's rewrite of the tokens a device's coverages
// list, and of the coverage digest each of its observations names (rekey.go).
package entstore

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entcoverage "github.com/Subject-Void-LLC/the-pleiades/internal/ent/factcoverage"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// rekeyCoverages rewrites, inside tx, each of the device's coverages (dev is
// its database ID) with every token mapped through tokens and its digest
// made again over them, and points each observation at its coverage's new
// digest. A token no row held is dropped. Two coverages that come out the
// same, which only a dropped token can make, are kept as one.
func rekeyCoverages(ctx context.Context, tx *ent.Tx, dev int, tokens map[string]string) (rekeyed, error) {
	covers, err := tx.FactCoverage.Query().
		Where(entcoverage.HasDeviceWith(entdevice.IDEQ(dev))).
		Order(ent.Asc(entcoverage.FieldID)).
		All(ctx)
	if err != nil {
		return rekeyed{}, fmt.Errorf("facts: %w", err)
	}
	var done rekeyed
	old := make(map[string]bool, len(covers))
	for _, c := range covers {
		old[c.Digest] = true
	}
	digests := make(map[string]string, len(covers))
	held := make(map[string]bool, len(covers))
	for _, c := range covers {
		stable, lostStable := mapTokens(c.Stable, tokens)
		volatile, lostVolatile := mapTokens(c.Volatile, tokens)
		done.dropped += lostStable + lostVolatile
		digest := facts.Coverage{Stable: stable, Volatile: volatile}.Digest()
		if old[digest] && digest != c.Digest {
			// Only a hash collision makes a new digest another coverage's old
			// one; observations of both would then be joined. Refused.
			return rekeyed{}, errors.New("facts: a re-keyed coverage's digest is another coverage's; nothing was re-keyed")
		}
		digests[c.Digest] = digest
		if held[digest] {
			// The same set as a coverage already rewritten: its observations
			// name that one from now on.
			deleted, err := tx.FactCoverage.Delete().Where(entcoverage.IDEQ(c.ID), entcoverage.DigestEQ(c.Digest)).Exec(ctx)
			if err != nil {
				return rekeyed{}, fmt.Errorf("facts: %w", err)
			}
			if deleted != 1 {
				return rekeyed{}, fmt.Errorf("%w (a coverage)", ent.ErrFactRekeyRaced)
			}
			continue
		}
		held[digest] = true
		if err := tx.RekeyFactCoverage(ctx, c.ID, dev, c.Digest, digest, stable, volatile); err != nil {
			return rekeyed{}, err
		}
		done.coverages++
	}
	// In digest order, so every re-key writes in the same order.
	was := make([]string, 0, len(digests))
	for d := range digests {
		was = append(was, d)
	}
	sort.Strings(was)
	for _, d := range was {
		if digests[d] == d {
			continue
		}
		n, err := tx.RekeyFactObservations(ctx, dev, d, digests[d])
		if err != nil {
			return rekeyed{}, err
		}
		done.observations += int(n)
	}
	return done, nil
}

// mapTokens is list with each token replaced by its new one in tokens,
// sorted as a coverage keeps them (nil when none is left, as
// facts.CoverageOf leaves a kind it saw nothing of), and how many it dropped
// for having none.
func mapTokens(list []string, tokens map[string]string) ([]string, int) {
	var out []string
	dropped := 0
	for _, t := range list {
		if now, ok := tokens[t]; ok {
			out = append(out, now)
			continue
		}
		dropped++
	}
	sort.Strings(out)
	return out, dropped
}
