// Package entstore: inspecting and repairing a device whose stored history
// no controller can serve (`controller facts inspect|discard|truncate`).
//
// # Design
//
// A device's history stops being served in four ways: its rows name the
// previous facts key and do not verify under it (ErrRekeyRefused, the state
// a change of the facts key leaves behind), they name both keys at once
// (ErrRekeyRefused), they name a key the controller was not given
// (ErrKeysChanged), or a row under the current key does not open as the
// device's own (ErrNotThisDevices). Each answers the facts routes 503
// (Unavailable). A chain that breaks under the current key while every row
// opens is still served; the chain route reports the break, and inspect
// calls the device broken. Nothing makes such a history verify again
// without the facts it was built from, and rewriting a chain until it
// verifies would defeat what the chain is for. So the tool reads and says,
// and its repairs remove history rather than rewrite it.
//
// A row that does not open is never, by itself, proof of damage. Its
// payload is sealed under MASTER_ENCRYPTION_KEY, and a row sealed under a
// master key, or a key version, the command was not given fails just as an
// edited or moved one does: nothing tells the two apart. One rotation of the
// master key leaves older rows under the old key and newer ones under the
// new, so the environment from before it, or a previous key left out, opens
// part of a history, and the part that opens verifies. So a device with no
// row that opens is factsreport.StateUnopened and one with some is
// StatePartlyUnopened, never broken or refused; any row that does not open,
// in any state, makes the history Unverifiable, as rows under another facts
// key or under both do; and each repair of an unverifiable history takes a
// flag that names it (RepairOptions' DiscardUnverifiable and
// TruncateUnverifiable). The guard reads the count of rows that do not open,
// not the state's name, so it holds however the keys are set. The command
// also refuses a MASTER_ENCRYPTION_KEY the database's key registry does not
// record and warns of what the registry says the other settings leave
// unopened (cmd/controller's facts_repair_open.go), and each device's report
// names the master key versions its rows are sealed as and whether the
// command was given a key for each (crypto.FactPayloadVersion reads it from
// the envelope's head, unopened; a version is a label stored in the clear,
// not part of a key). That is advice; the flag is the guard.
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
// repair) unless told to discard a verified history by name, and one the
// keys given cannot verify either way (a row that does not open, rows under
// another facts key or under both) unless told to discard an unverifiable
// history by name: those point first at the command's own settings.
//
// Truncate (truncate.go) keeps a prefix of the history that verifies and
// removes the rest, then re-keys what it keeps when that is under the
// previous key, through the re-key itself (Store.rekey, under its own
// precondition). It is sound because of where it cuts: at the gather that
// wrote the last stable row the chain verified before the first stable row
// that fails, removing that gather whole, every row it or a later gather
// wrote, and every later gather. A row deleted just before the first failure
// could have been written by that gather or any later one, and whatever
// fails first at a row (its payload, its value, its link) the chain cannot
// prove no row before it is missing; so no gather that might have lost a
// write is kept, and no observation is left to confirm a value its gather
// never saw. A failing row in the middle whose successors verify among
// themselves is cut with its successors: they chain onto it, so keeping them
// would leave a gap. A volatile row is not chained, so one that fails on its
// own is removed alone and its gather kept, observation and coverage too.
// That is sound for a narrower reason than the cut: a volatile key holds one
// row, which a new value replaces, so no older value of it is left for that
// gather's observation or a later one to confirm in its place, and its
// failure says nothing of the stable rows, which the chain holds on their
// own. A volatile value replaced after the cut is not brought back either,
// so its key holds none until the next gather. A truncate that would keep
// nothing (underCheck.keeps: every stable row that verified before the first
// failure was written by the gather the cut removes) is not offered and is
// refused, since it would be a discard under truncate's name.
//
// What it keeps is then checked again, and the truncate is refused, changing
// nothing, when that does not verify (a database whose storage order and
// gather numbers disagree). That check has limits. Gather numbers are not
// covered by the chain, so an edit of them alone is not seen, as it is not
// by the chain route either; and the observations and coverages it keeps are
// counted, not held to the rows it keeps, since nothing chains them. An
// actor who can write the database can edit either so that nothing here
// notices; this tool does not defend against one, and neither does the
// chain. It applies only to a history wholly under one key it was given
// (broken, or refused with the current key given too to re-key into, or
// either partly unopened) that it would keep some of; every other state can
// only be discarded.
//
// Each repair holds the device's facts exclusively for its whole
// transaction (ent.Tx.LockFactsOfDevice, as a re-key does), so a gather of
// the device lands wholly before it or wholly after; on SQLite that hold is
// the database's one write lock. Each prints what it will do: without its
// confirmation flag it runs, under the same hold, and rolls back, so what it
// prints is what it would do, digest included. Each is recorded in the
// activity stream in the same transaction (a "device facts" entry, action
// deleted, naming the device, what was removed by count and a keyed digest
// of the removed rows under the facts key's removal key,
// facts.Keys.RemovalMAC), so a repair is never committed unrecorded and a
// record never names a repair that did not happen. The digest covers what
// the facts key made of each row (token, keyed hash, keys, chain link) and
// its time, kind, gather and provenance, in storage order; never its sealed
// payload, which a master key rotation seals again, nor its ID. A failed
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

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
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

// NameOf is the inventory name of the device whose ID is id, the form the
// controller's log names a device in, or ErrUnknownDevice.
func (s *Store) NameOf(ctx context.Context, id string) (string, error) {
	dev, err := s.client.Device.Query().Where(entdevice.DeviceIDEQ(id)).Select(entdevice.FieldName).Only(ctx)
	if ent.IsNotFound(err) {
		return "", fmt.Errorf("%w: no device has the ID %s", ErrUnknownDevice, termsafe.Quote(id))
	}
	if err != nil {
		return "", fmt.Errorf("facts: %w", err)
	}
	return dev.Name, nil
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
	// keyState is the state the rows' facts keys and the checks alone give,
	// before rows that do not open make it unopened or partly_unopened: the
	// one a truncate reads which check to cut by from.
	keyState factsreport.State
}

// inspectTx inspects dev inside tx, a page of rows at a time.
func (s *Store) inspectTx(ctx context.Context, tx *ent.Tx, dev *ent.Device, opts InspectOptions) (inspected, error) {
	out := inspected{report: factsreport.Device{Device: termsafe.Quote(dev.Name), ID: termsafe.Quote(dev.DeviceID), KeyedBy: []factsreport.KeyedRows{},
		SealedUnder: []factsreport.SealedRows{}, Under: []factsreport.KeyCheck{}, Repairs: []string{}}}
	if s.keys.ID() != "" {
		out.current = newUnderCheck(factsreport.RoleCurrent, s.keys, dev.DeviceID, opts.ShowFactNames)
	}
	if s.previousID() != "" {
		out.previous = newUnderCheck(factsreport.RolePrevious, s.previous, dev.DeviceID, opts.ShowFactNames)
	}
	in := &out.report
	var keyed, sealed tally
	byRole := map[string]int{}
	err := pageFacts(ctx, tx, dev.ID, nil, func(row *ent.Fact, position int) error {
		// Every row is counted, by kind, by the facts keys it names and by
		// the master key version its payload names.
		in.Rows.Total++
		if row.Kind == entfact.KindStable {
			in.Rows.Stable++
		} else {
			in.Rows.Volatile++
		}
		byRole[s.roleOf(row.KeyedBy)]++
		keyed.add(row.KeyedBy)
		if version, ok := crypto.FactPayloadVersion(row.Payload); ok {
			sealed.add(version)
		}
		// One opening serves every set of keys: the payload does not depend
		// on the facts key.
		opened, openErr := s.openPayload(row)
		if kind, _ := faultKind(openErr); kind == factsreport.FaultDoesNotOpen {
			// No facts key changes this: the payload is sealed under a
			// master key, or a version, the store was not given, or damaged.
			in.Rows.Unopened++
		}
		for _, c := range out.checks() {
			c.see(row, position, opened, openErr)
		}
		return nil
	})
	if err != nil {
		return inspected{}, err
	}
	for _, id := range keyed.order {
		in.KeyedBy = append(in.KeyedBy, factsreport.KeyedRows{KeyID: termsafe.Quote(id), Role: s.roleOf(id), Rows: keyed.counts[id]})
	}
	in.KeyedByMore = keyed.more
	for _, version := range sealed.order {
		in.SealedUnder = append(in.SealedUnder, factsreport.SealedRows{Version: termsafe.Quote(version), Role: s.sealedRole(version), Rows: sealed.counts[version]})
	}
	in.SealedUnderMore = sealed.more
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
	out.keyState = stateOf(in.Rows.Total, byRole, out.current, out.previous)
	in.State = out.keyState
	switch {
	case in.Rows.Total > 0 && in.Rows.Unopened == in.Rows.Total:
		// No row opens, whatever keys it names: none of the states the keys
		// give can be judged, and the master key is what to check first.
		in.State = factsreport.StateUnopened
	case in.Rows.Unopened > 0 && (in.State == factsreport.StateBroken || in.State == factsreport.StateRefused):
		// Some rows open and some do not. A row sealed under a master key
		// the store was not given fails just as a damaged one does, so the
		// history cannot be called broken or refused.
		in.State = factsreport.StatePartlyUnopened
	}
	// A row that does not open is never by itself proof of damage, in any
	// state: it is read from the count, not from the state's name, so the
	// repairs' guard holds whatever state the keys give.
	in.Unverifiable = in.Rows.Unopened > 0 || in.State == factsreport.StateOtherKeys || in.State == factsreport.StateMixedKeys
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
