// Package entstore: one inspection's check of a device's rows under one set
// of keys, what it advises, and an inspection of every device (inspect.go).
package entstore

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// underCheck is a device's rows checked, in the order they were stored,
// under one set of keys.
type underCheck struct {
	report factsreport.KeyCheck
	keys   *facts.Keys
	device string
	// show names a failing row's fact when its payload opened.
	show bool
	// walk is the chain over the stable rows; stable counts the stable rows
	// seen, each one's index in the chain.
	walk   *facts.ChainWalk
	stable int
	// cut is the first stable row that fails, where a truncate cuts; nil
	// while none has. A volatile row that fails does not stop the chain.
	cut *factsreport.RowFailure
}

// newUnderCheck starts a check of device's rows under keys, in role.
func newUnderCheck(role string, keys *facts.Keys, device string, show bool) *underCheck {
	return &underCheck{report: factsreport.KeyCheck{Role: role, KeyID: keys.ID()}, keys: keys, device: device, show: show, walk: facts.NewChainWalk(device, keys)}
}

// see checks row, at position among the device's rows, whose payload opened
// as opened (or failed to, openErr), under the check's keys.
func (c *underCheck) see(row *ent.Fact, position int, opened map[string]any, openErr error) {
	stable := row.Kind == entfact.KindStable
	index := -1
	if stable {
		index = c.stable
		c.stable++
	}
	if openErr != nil {
		kind, _ := faultKind(openErr)
		c.report.Failed++
		c.fail(row, position, index, kind, "")
		return
	}
	// The fact's name, which a failure names only when asked.
	name, _ := opened[payloadFactKey].(string)
	e, err := heldUnder(c.keys, c.device, row, opened)
	if err != nil {
		kind, _ := faultKind(err)
		c.report.Failed++
		c.fail(row, position, index, kind, name)
		return
	}
	c.report.Opened++
	// The chain is walked only while every stable row before has held: a
	// link after a failing one cannot be judged.
	if stable && c.cut == nil {
		link := facts.Link{Key: e.Key, Value: e.Value, Hash: e.Hash, ObservedAt: e.ObservedAt, By: e.By, Chain: row.Chain}
		if row.ChainPrev != nil {
			link.Prev = *row.ChainPrev
		}
		if b := c.walk.Next(link); b != nil {
			c.fail(row, position, index, factsreport.FailureKind(b.Kind), name)
			return
		}
	}
	if c.report.Failure == nil {
		c.report.Checked++
	}
}

// fail records row as failing for kind: the check's first failure, and the
// cut when it is the first stable row to fail. index is its place in the
// chain, -1 for a volatile row.
func (c *underCheck) fail(row *ent.Fact, position, index int, kind factsreport.FailureKind, name string) {
	stable := index >= 0
	if c.report.Failure != nil && (!stable || c.cut != nil) {
		return
	}
	f := &factsreport.RowFailure{Row: row.ID, Position: position, Kind: kind, Stable: stable, KeyID: termsafe.Quote(row.KeyedBy),
		ObservedAt: row.CreatedAt.UTC(), Observation: row.ObservationSeq}
	if stable {
		at := index
		f.ChainIndex = &at
	}
	if c.show && name != "" {
		f.Fact = termsafe.Quote(name)
	}
	if c.report.Failure == nil {
		c.report.Failure = f
	}
	if stable && c.cut == nil {
		c.cut = f
	}
}

// advise fills in's repairs, the gather a truncate would cut at, and what
// the operator can do, from its state and its checks. haveCurrent says
// whether the current keys were given, which a truncate of a history under
// the previous keys re-keys into.
func advise(in *factsreport.Device, checks inspected, haveCurrent bool) {
	both := []string{factsreport.RepairTruncate, factsreport.RepairDiscard}
	var cut *factsreport.RowFailure
	switch in.State {
	case factsreport.StateEmpty:
		in.Advice = "Nothing is stored for this device; there is nothing to repair."
	case factsreport.StateHealthy:
		in.Advice = "Every stored fact opens as this device's and its chain verifies under the current key; there is nothing to repair."
	case factsreport.StateRekeyPending:
		in.Advice = "Every stored fact verifies under the previous key. A controller given the new key as PLEIADES_FACTS_DIGEST_KEY and this one as PLEIADES_FACTS_DIGEST_KEY_PREVIOUS re-keys it at its next read or start; there is nothing to repair."
	case factsreport.StateBroken:
		in.Repairs, cut = both, checks.current.cut
		in.Advice = "Its stored facts are under the current key and do not verify, and no key makes them verify again. `controller facts truncate` keeps the history before the first failure; `controller facts discard` removes it all, and the next gather starts a new history."
	case factsreport.StateRefused:
		in.Repairs = []string{factsreport.RepairDiscard}
		in.Advice = "Its stored facts are under the previous key and do not verify under it, so the controller refuses to re-key them and serves none of them. Nothing makes them verify again; the backup taken before the change of key still holds them. "
		if haveCurrent {
			in.Repairs, cut = both, checks.previous.cut
			in.Advice += "`controller facts truncate` keeps the history before the first failure and re-keys it under the current key; `controller facts discard` removes it all, and the next gather starts a new history."
		} else {
			in.Advice += "`controller facts discard` removes it all; `controller facts truncate`, which keeps the history before the first failure, needs the new key as PLEIADES_FACTS_DIGEST_KEY too, to re-key what it keeps."
		}
	case factsreport.StateMixedKeys:
		in.Repairs = []string{factsreport.RepairDiscard}
		in.Advice = "Its rows name the current key and the previous one at once, which no build of this release writes: the database was edited or restored in part. Only `controller facts discard` repairs it; the backup taken before the change of key still holds the history."
	default:
		in.Repairs = []string{factsreport.RepairDiscard}
		in.Advice = "Some of its rows name a facts key this command was not given. If you still hold the key they were stored under, run this again with it as PLEIADES_FACTS_DIGEST_KEY_PREVIOUS; if not, only `controller facts discard` repairs it."
	}
	if cut != nil {
		in.TruncateFrom = cut.Observation
	}
}

// fleetPage is how many devices one page of a fleet inspection reads.
const fleetPage = 200

// InspectAll inspects every device holding a stored fact, a page of devices
// at a time in database ID order, each in a read-only transaction of its
// own, and reports every one that is not empty or healthy, or all of them
// with opts.All.
func (s *Store) InspectAll(ctx context.Context, opts InspectOptions) (factsreport.Report, error) {
	report := factsreport.Report{SchemaVersion: factsreport.Version, Keys: s.Keys(), Scope: factsreport.ScopeFleet, Devices: []factsreport.Device{}}
	after := 0
	for {
		ids, err := s.client.Device.Query().
			Where(entdevice.IDGT(after), entdevice.HasFacts()).
			Order(ent.Asc(entdevice.FieldID)).
			Limit(fleetPage).
			IDs(ctx)
		if err != nil {
			return report, fmt.Errorf("facts: listing devices to inspect: %w", err)
		}
		for _, id := range ids {
			after = id
			// By database ID, which a rename meanwhile does not change.
			found, err := s.inspectOne(ctx, entdevice.IDEQ(id), opts)
			if ent.IsNotFound(err) {
				// Removed since the page was read: nothing left to inspect.
				continue
			}
			if err != nil {
				return report, err
			}
			report.Scanned++
			if found.State.NeedsRepair() {
				report.NeedsRepair++
			}
			if opts.All || (found.State != factsreport.StateHealthy && found.State != factsreport.StateEmpty) {
				report.Devices = append(report.Devices, found)
			}
		}
		if len(ids) < fleetPage {
			return report, nil
		}
	}
}
