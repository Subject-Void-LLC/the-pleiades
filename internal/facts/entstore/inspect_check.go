// Package entstore: one inspection's check of a device's rows under one set
// of keys, what it advises, and an inspection of every device (inspect.go).
package entstore

import (
	"context"
	"fmt"
	"slices"

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
	// verified before cut, where a truncate cuts (truncate.go); haveGood is
	// false while no stable row has verified.
	lastGood int64
	haveGood bool
}

// truncatable reports whether a truncate has a prefix to keep: no stable
// row fails, or one verified before the first that does.
func (c *underCheck) truncatable() bool {
	return c.cut == nil || c.haveGood
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
	// by is the check a truncate would cut by, nil when none applies.
	var by *underCheck
	switch in.State {
	case factsreport.StateEmpty:
		in.Advice = "Nothing is stored for this device; there is nothing to repair."
	case factsreport.StateHealthy:
		in.Advice = "Every stored fact opens as this device's and its chain verifies under the current key; there is nothing to repair."
	case factsreport.StateRekeyPending:
		in.Advice = "Every stored fact verifies under the previous key. A controller given the new key as PLEIADES_FACTS_DIGEST_KEY and this one as PLEIADES_FACTS_DIGEST_KEY_PREVIOUS re-keys it at its next read or start; there is nothing to repair."
	case factsreport.StateBroken:
		in.Repairs, by = []string{factsreport.RepairDiscard}, checks.current
		in.Advice = "Its stored facts are under the current key and do not verify, and no key makes them verify again. "
		if by.truncatable() {
			in.Repairs = both
			in.Advice += "`controller facts truncate` keeps the history before the gather that wrote the last value that verifies; "
		}
		in.Advice += "`controller facts discard` removes it all, and the next gather starts a new history."
	case factsreport.StateRefused:
		in.Repairs = []string{factsreport.RepairDiscard}
		in.Advice = "Its stored facts are under the previous key and do not verify under it, so the controller refuses to re-key them and serves none of them. Nothing makes them verify again; the backup taken before the change of key still holds them. "
		switch {
		case !checks.previous.truncatable():
			in.Advice += "No value before the first failure verifies, so only `controller facts discard` repairs it; the next gather then starts a new history."
		case haveCurrent:
			in.Repairs, by = both, checks.previous
			in.Advice += "`controller facts truncate` keeps the history before the gather that wrote the last value that verifies and re-keys it under the current key; `controller facts discard` removes it all, and the next gather starts a new history."
		default:
			in.Advice += "`controller facts discard` removes it all; `controller facts truncate`, which keeps the history before the gather that wrote the last value that verifies, needs the new key as PLEIADES_FACTS_DIGEST_KEY too, to re-key what it keeps."
		}
	case factsreport.StateMixedKeys:
		in.Repairs = []string{factsreport.RepairDiscard}
		in.Advice = "Its rows name the current key and the previous one at once, which no build of this release writes: the database was edited or restored in part. Only `controller facts discard --discard-unverifiable-history` repairs it; the backup taken before the change of key still holds the history."
	case factsreport.StateUnopened:
		in.Repairs = []string{factsreport.RepairDiscard}
		in.Advice = "No stored row of it opens, whatever key it names. That is what a MASTER_ENCRYPTION_KEY other than the one its facts were sealed under looks like: run this with the server's MASTER_ENCRYPTION_KEY, MASTER_ENCRYPTION_KEY_PREVIOUS and their versions first. Only if that key is lost does `controller facts discard --discard-unverifiable-history` repair it."
	default:
		in.Repairs = []string{factsreport.RepairDiscard}
		in.Advice = "Some of its rows name a facts key this command was not given. If you still hold the key they were stored under, run this again with it as PLEIADES_FACTS_DIGEST_KEY_PREVIOUS; if not, only `controller facts discard --discard-unverifiable-history` repairs it."
	}
	if by != nil && by.cut != nil && by.haveGood && slices.Contains(in.Repairs, factsreport.RepairTruncate) {
		in.TruncateFrom = by.lastGood
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
	// byState counts the devices read in each state, for the fleet's advice.
	byState := map[factsreport.State]int{}
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
			if found.State.NeedsRepair() {
				report.NeedsRepair++
			}
			if opts.All || (found.State != factsreport.StateHealthy && found.State != factsreport.StateEmpty) {
				report.Devices = append(report.Devices, found)
			}
		}
		if len(ids) < fleetPage {
			report.Advice = fleetAdvice(report.Scanned, byState)
			return report, nil
		}
	}
}

// fleetAdvice is what a fleet inspection says of the fleet as a whole: when
// every one of the scanned devices is under a facts key the command was not
// given, or has no row that opens, the fault is far likelier the command's
// own keys than every device's history. "" otherwise.
func fleetAdvice(scanned int, byState map[factsreport.State]int) string {
	switch {
	case scanned == 0:
		return ""
	case byState[factsreport.StateOtherKeys] == scanned:
		return "Every device's stored facts name a facts key this command was not given. That is what a wrong PLEIADES_FACTS_DIGEST_KEY looks like: run this with the keys the server runs with before repairing anything."
	case byState[factsreport.StateUnopened] == scanned:
		return "No device has a stored fact that opens. That is what a wrong MASTER_ENCRYPTION_KEY looks like: run this with the server's MASTER_ENCRYPTION_KEY, MASTER_ENCRYPTION_KEY_PREVIOUS and their versions before repairing anything."
	}
	return ""
}
