// Package entstore: inspecting and repairing a device whose stored history
// no controller can serve (`controller facts inspect|discard|truncate`).
//
// # Design
//
// A device's history stops being served in four ways: its rows name the
// previous facts key and do not verify under it (ErrRekeyRefused, the state
// a change of the facts key leaves behind), they name both keys at once
// (ErrRekeyRefused), they name a key the controller was not given
// (ErrKeysChanged), or they name the current key and a row does not open as
// the device's own or the chain breaks (ErrNotThisDevices on a read, a
// break on the chain route). Nothing makes such a history verify again
// without the facts it was built from, and rewriting a chain until it
// verifies would defeat what the chain is for. So the tool reads and says,
// and its repairs remove history rather than rewrite it.
//
// Inspect (InspectDevice, InspectAll) is read-only. Each device is read in a
// transaction of its own that is read-only and REPEATABLE READ (PostgreSQL
// gives one snapshot of the device's rows, coverages and observations even
// while a re-key commits; SQLite's transactions already read one snapshot,
// and its driver takes no options), holds no lock beyond what a plain read
// takes, and never goes through Current, History or Verify, each of which
// would re-key a device under the previous key. Rows are read a page at a
// time and the chain is walked one link at a time (facts.ChainWalk), so
// memory per device is one page; devices are paged by ID. Each payload is
// opened once (it is sealed under MASTER_ENCRYPTION_KEY and bound to the
// row's token, whatever the facts key) and held to each set of keys given:
// the current, the previous, or both. What it reports (internal/facts'
// factsreport, the published --json model) is counts, row IDs, key
// identities, times, gather numbers and failure kinds, and the state they
// add up to; for each set of keys, whether the history verifies under it
// and the first row that does not, with why: a payload that is not sealed
// or does not open, a fact whose token is not the row's, a value its hash
// does not describe, a stable row with no chain link, a link that names
// another predecessor (a gap or a reordering), or a chain hash that does not
// match. It names a failing row's fact only when asked, and only when the
// row opened.
//
// Discard (repair.go) removes every stored fact, coverage and observation of
// one device in one transaction, so its next gather starts a new history
// under the current keys. It refuses a device that verifies (nothing to
// repair) unless told to discard a verified history by name.
//
// Truncate (truncate.go) keeps the longest prefix of the history that
// verifies and removes the rest, then re-keys what it keeps when that is
// under the previous key, through the re-key itself (Store.rekey, under its
// own precondition). It is sound because of where it cuts: at the gather
// that wrote the first stable row that fails, removing that gather, every
// row it or a later gather wrote, and every later gather, so no confirmation
// from after the cut is left to confirm a value it never saw; volatile rows
// before the cut that fail on their own are removed too, since they are not
// chained. A failing row in the middle whose successors verify among
// themselves is cut with its successors: they chain onto it, so keeping
// them would leave a gap, and the chain cannot say whether the failing row
// or everything after it is the edit. What it keeps is then checked again,
// and the truncate is refused, changing nothing, when that does not verify
// (a database whose storage order and gather numbers disagree). It applies
// only to a history wholly under one key it was given (broken, or refused
// with the current key given too to re-key into); every other state can
// only be discarded.
//
// Each repair holds the device's facts exclusively for its whole
// transaction (ent.Tx.LockFactsOfDevice, as a re-key does), so a gather of
// the device lands wholly before it or wholly after. Each prints what it
// will do: without its confirmation flag it runs and rolls back, so what it
// prints is what it would do, digest included. Each is recorded in the
// activity stream in the same transaction (a "device facts" entry, action
// deleted, naming the device, what was removed by count and a keyed digest
// of the removed rows as stored under the facts key's removal key,
// facts.Keys.RemovalMAC, never a value), so a repair is never committed
// unrecorded and a record never names a repair that did not happen. A failed
// write of the record therefore fails the repair, unlike the other audited
// stores, which log it and let their write stand: here the write is the
// destruction of evidence, and it must not happen unrecorded. Only the repairs, a
// volatile value's replacement and a re-key's coverage merge delete from
// the fact tables (internal/archtest's TestOnlyTheRepairsDeleteFactHistory).
//
// Not built: completing a re-key of a device under both keys. The lock that
// keeps a gather from storing beside a re-key landed with the re-key itself,
// so no build ever wrote that state; it means the database was edited or
// restored in part, and re-keying it would make the edit read as whole. Nor
// a repair of rows under a key the command was not given: inspected with
// that key as the previous one, they are a refused or a pending history,
// and without it nothing can tell an intact history from an edited one.
package entstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	entcoverage "github.com/Subject-Void-LLC/the-pleiades/internal/ent/factcoverage"
	entobs "github.com/Subject-Void-LLC/the-pleiades/internal/ent/factobservation"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/predicate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// inspectPage is how many of a device's rows one query of an inspection or
// a repair reads.
const inspectPage = 500

// maxKeyIDs bounds the key identities an inspection names for one device.
const maxKeyIDs = 8

// InspectOptions says what an inspection reports.
type InspectOptions struct {
	// ShowFactNames names the fact a failing row holds, when its payload
	// opens. Off by default: a fact's name can say what it is about.
	ShowFactNames bool
	// All reports every device a fleet inspection reads, healthy ones too.
	All bool
}

// readOnly is the transaction an inspection reads one device in.
var readOnly = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}

// Keys names the identities of the keys the store holds.
func (s *Store) Keys() factsreport.Keys {
	return factsreport.Keys{Current: s.keys.ID(), Previous: s.previousID()}
}

// InspectDevice inspects the device named name (its inventory name), in a
// read-only transaction of its own. A name the database does not hold is
// ErrUnknownDevice.
func (s *Store) InspectDevice(ctx context.Context, name string, opts InspectOptions) (factsreport.Device, error) {
	found, err := s.inspectOne(ctx, entdevice.NameEQ(name), opts)
	if ent.IsNotFound(err) {
		return factsreport.Device{}, fmt.Errorf("%w: %s", ErrUnknownDevice, termsafe.Quote(name))
	}
	return found, err
}

// inspectOne inspects the one device which selects, in a read-only
// transaction of its own. No such device is ent's not-found error.
func (s *Store) inspectOne(ctx context.Context, which predicate.Device, opts InspectOptions) (factsreport.Device, error) {
	tx, err := s.client.BeginTx(ctx, readOnly)
	if err != nil {
		return factsreport.Device{}, fmt.Errorf("facts: %w", err)
	}
	// Nothing is written, so the transaction always ends in a rollback.
	defer func() { _ = tx.Rollback() }()
	dev, err := tx.Device.Query().Where(which).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return factsreport.Device{}, err
		}
		return factsreport.Device{}, fmt.Errorf("facts: %w", err)
	}
	found, err := s.inspectTx(ctx, tx, dev, opts)
	return found.report, err
}

// inspected is one inspection's report and the checks it was made from,
// which a repair reads its cut from.
type inspected struct {
	report  factsreport.Device
	current *underCheck
	// previous is nil when the store holds no previous keys.
	previous *underCheck
}

// inspectTx inspects dev inside tx, a page of rows at a time.
func (s *Store) inspectTx(ctx context.Context, tx *ent.Tx, dev *ent.Device, opts InspectOptions) (inspected, error) {
	out := inspected{report: factsreport.Device{Device: termsafe.Quote(dev.Name), ID: termsafe.Quote(dev.DeviceID), KeyedBy: []factsreport.KeyedRows{}, Under: []factsreport.KeyCheck{}, Repairs: []string{}}}
	if s.keys.ID() != "" {
		out.current = newUnderCheck(factsreport.RoleCurrent, s.keys, dev.DeviceID, opts.ShowFactNames)
	}
	if s.previousID() != "" {
		out.previous = newUnderCheck(factsreport.RolePrevious, s.previous, dev.DeviceID, opts.ShowFactNames)
	}
	in := &out.report
	keyed := map[string]int{}
	order := []string{}
	byRole := map[string]int{}
	err := pageFacts(ctx, tx, dev.ID, nil, func(row *ent.Fact, position int) error {
		// Every row is counted, by kind and by the keys it names.
		in.Rows.Total++
		if row.Kind == entfact.KindStable {
			in.Rows.Stable++
		} else {
			in.Rows.Volatile++
		}
		role := s.roleOf(row.KeyedBy)
		byRole[role]++
		if _, ok := keyed[row.KeyedBy]; ok || len(keyed) < maxKeyIDs {
			if !ok {
				order = append(order, row.KeyedBy)
			}
			keyed[row.KeyedBy]++
		} else {
			in.KeyedByMore++
		}
		// One opening serves every set of keys: the payload does not depend
		// on the facts key.
		opened, openErr := s.openPayload(row)
		for _, c := range out.checks() {
			c.see(row, position, opened, openErr)
		}
		return nil
	})
	if err != nil {
		return inspected{}, err
	}
	for _, id := range order {
		in.KeyedBy = append(in.KeyedBy, factsreport.KeyedRows{KeyID: termsafe.Quote(id), Role: s.roleOf(id), Rows: keyed[id]})
	}
	if in.Coverages, err = tx.FactCoverage.Query().Where(entcoverage.HasDeviceWith(entdevice.IDEQ(dev.ID))).Count(ctx); err != nil {
		return inspected{}, fmt.Errorf("facts: %w", err)
	}
	if in.Observations, err = tx.FactObservation.Query().Where(entobs.DeviceIDEQ(dev.ID)).Count(ctx); err != nil {
		return inspected{}, fmt.Errorf("facts: %w", err)
	}
	for _, c := range out.checks() {
		c.report.Verifies = c.report.Failure == nil
		in.Under = append(in.Under, c.report)
	}
	in.State = stateOf(in.Rows.Total, byRole, out.current, out.previous)
	advise(in, out, s.keys.ID() != "")
	return out, nil
}

// checks are the inspection's checks, current first.
func (i inspected) checks() []*underCheck {
	var out []*underCheck
	for _, c := range []*underCheck{i.current, i.previous} {
		if c != nil {
			out = append(out, c)
		}
	}
	return out
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

// pageFacts calls each with every row of the device dev (its database ID)
// that where selects, in id order, a page at a time, and each row's place
// among them from 0.
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
