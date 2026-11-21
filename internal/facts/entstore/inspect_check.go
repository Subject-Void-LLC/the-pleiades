// Package entstore: one inspection's check of a device's rows under one set
// of keys, the state it adds up to, and an inspection of every device
// (inspect.go; inspect_advice.go has what each state advises).
package entstore

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
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
	// cut is the first stable row that fails; nil while none has. A volatile
	// row that fails does not stop the chain.
	cut *factsreport.RowFailure
	// lastGood is the gather that wrote the last stable row the chain
	// verified before cut, where a truncate cuts (truncate.go), and minGood
	// the earliest gather that wrote one; haveGood is false while no stable
	// row has verified.
	lastGood, minGood int64
	haveGood          bool
}

// keeps reports whether a truncate by this check keeps any of a history of
// total rows. With no stable row failing it removes only the failing
// volatile rows, which Failed then counts. Otherwise it removes the gather
// lastGood and every later one, so it keeps something only when a stable
// row the chain verified was written by an earlier gather: a first failure
// inside the first gather that wrote a verified row leaves nothing, and is
// a discard under another name.
func (c *underCheck) keeps(total int) bool {
	if c.cut == nil {
		return total > c.report.Failed
	}
	return c.haveGood && c.minGood < c.lastGood
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
		if !c.haveGood || row.ObservationSeq < c.minGood {
			c.minGood = row.ObservationSeq
		}
		c.lastGood, c.haveGood = row.ObservationSeq, true
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
	// Which master key version it names: for a row that does not open, the
	// first thing to hold to the keys the command was given.
	if version, ok := crypto.FactPayloadVersion(row.Payload); ok {
		f.SealedVersion = termsafe.Quote(version)
	}
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

// roleOf is the role the key identity id has for the store.
func (s *Store) roleOf(id string) string {
	switch {
	case id != "" && id == s.keys.ID():
		return factsreport.RoleCurrent
	case id != "" && id == s.previousID():
		return factsreport.RolePrevious
	}
	return factsreport.RoleOther
}

// sealedRole is the role the master key version version has for the store's
// envelope service: current, previous, or other when it holds no key for it.
func (s *Store) sealedRole(version string) string {
	switch {
	case s.envelope == nil:
		return factsreport.RoleOther
	case version == s.envelope.CurrentVersion():
		return factsreport.RoleCurrent
	case version != "" && version == s.envelope.PreviousVersion():
		return factsreport.RolePrevious
	}
	return factsreport.RoleOther
}

// tally counts distinct names (key identities, master key versions) in the
// order first seen, naming at most maxKeyIDs and counting the rest as more.
type tally struct {
	counts map[string]int
	order  []string
	more   int
}

// add counts one row naming name.
func (t *tally) add(name string) {
	if t.counts == nil {
		t.counts = map[string]int{}
	}
	if _, ok := t.counts[name]; !ok && len(t.counts) >= maxKeyIDs {
		t.more++
		return
	}
	if _, ok := t.counts[name]; !ok {
		t.order = append(t.order, name)
	}
	t.counts[name]++
}

// stateOf is the state rows rows add up to, split by the role of the keys
// each names, and the checks under the current and previous keys.
func stateOf(rows int, byRole map[string]int, current, previous *underCheck) factsreport.State {
	switch {
	case rows == 0:
		return factsreport.StateEmpty
	case byRole[factsreport.RoleOther] > 0:
		return factsreport.StateOtherKeys
	case byRole[factsreport.RoleCurrent] > 0 && byRole[factsreport.RolePrevious] > 0:
		return factsreport.StateMixedKeys
	case byRole[factsreport.RoleCurrent] == rows:
		// Rows under the current keys mean the store holds them.
		if current.report.Failure == nil {
			return factsreport.StateHealthy
		}
		return factsreport.StateBroken
	}
	// Every row is under the previous keys, which the store holds.
	if previous.report.Failure == nil {
		return factsreport.StateRekeyPending
	}
	return factsreport.StateRefused
}

// fleetPage is how many devices one page of a fleet inspection reads.
const fleetPage = 200

// InspectAll inspects every device holding a stored fact, a page of devices
// at a time in database ID order, each in a read-only transaction of its
// own, and reports every one that is not empty or healthy, or all of them
// with opts.All.
func (s *Store) InspectAll(ctx context.Context, opts InspectOptions) (factsreport.Report, error) {
	report := factsreport.Report{SchemaVersion: factsreport.Version, Keys: s.Keys(), Scope: factsreport.ScopeFleet, Devices: []factsreport.Device{}}
	// byState counts the devices read in each state, unopened those holding
	// a row that does not open, and notGiven the master key versions such
	// rows name that the store holds no key for: the fleet's advice.
	byState := map[factsreport.State]int{}
	unopened := 0
	var notGiven tally
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
			byState[found.State]++
			if found.Rows.Unopened > 0 {
				unopened++
				for _, version := range versionsNotGiven(found.SealedUnder) {
					notGiven.add(version)
				}
			}
			if found.State.NeedsRepair() {
				report.NeedsRepair++
			}
			if opts.All || (found.State != factsreport.StateHealthy && found.State != factsreport.StateEmpty) {
				report.Devices = append(report.Devices, found)
			}
		}
		if len(ids) < fleetPage {
			report.Advice = fleetAdvice(report.Scanned, byState, unopened, notGiven.order)
			return report, nil
		}
	}
}
