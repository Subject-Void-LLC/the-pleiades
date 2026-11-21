// Package entstore: what an inspection advises (inspect.go has the design):
// the repairs that apply to a device and what to do about it, the one rule
// for which check a truncate cuts by that the advice and Truncate both read,
// and what a fleet inspection says of the fleet as a whole.
package entstore

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
)

// errTruncateNeedsCurrent marks a truncate of a history under the previous
// facts key with no current key to re-key what it keeps into.
var errTruncateNeedsCurrent = fmt.Errorf("%w: its history is under the previous key, and what a truncate keeps is re-keyed under the current one, so it needs PLEIADES_FACTS_DIGEST_KEY too", ErrRepairDoesNotApply)

// errTruncateKeepsNothing marks a truncate that would keep none of the
// history (underCheck.keeps), which is a discard under truncate's name.
var errTruncateKeepsNothing = fmt.Errorf("%w: a truncate would keep none of its history, since no value verifies before its first failure except in the gather the cut removes; only discard repairs it", ErrRepairDoesNotApply)

// checkMasterKeys is what to do first about rows that do not open.
const checkMasterKeys = "Run this with the server's MASTER_ENCRYPTION_KEY, MASTER_ENCRYPTION_KEY_VERSION, MASTER_ENCRYPTION_KEY_PREVIOUS and MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION first."

// truncateBy is the check a truncate of a device found as before cuts by,
// and whether what it keeps is then re-keyed, or why no truncate applies.
// haveCurrent says whether the current facts keys were given. It applies to
// a history wholly under one facts key the store holds (broken, refused, or
// partly_unopened over either) that it would keep some of.
func truncateBy(before inspected, haveCurrent bool) (*underCheck, bool, error) {
	switch st := before.report.State; st {
	case factsreport.StateEmpty, factsreport.StateHealthy, factsreport.StateRekeyPending:
		return nil, false, fmt.Errorf("%w: it is %s", ErrNothingToRepair, st)
	case factsreport.StateUnopened:
		return nil, false, fmt.Errorf("%w: no stored row of it opens, which is what a MASTER_ENCRYPTION_KEY or key version other than the ones its facts were sealed under looks like; run this with the server's master key settings", ErrRepairDoesNotApply)
	case factsreport.StateBroken, factsreport.StateRefused, factsreport.StatePartlyUnopened:
	default:
		return nil, false, fmt.Errorf("%w: its rows are %s, so no prefix of them verifies under one key; only discard repairs it", ErrRepairDoesNotApply, st)
	}
	// Each of those is a history wholly under one facts key: the key state
	// says which, and so which check to cut by.
	check, rekey := before.current, false
	if before.keyState == factsreport.StateRefused {
		check, rekey = before.previous, true
	}
	if !check.keeps(before.report.Rows.Total) {
		return nil, false, errTruncateKeepsNothing
	}
	if rekey && !haveCurrent {
		return nil, false, errTruncateNeedsCurrent
	}
	return check, rekey, nil
}

// advise fills in's repairs, the gather a truncate would cut at, and what
// the operator can do, from its state and its checks. haveCurrent says
// whether the current keys were given, which a truncate of a history under
// the previous keys re-keys into.
func advise(in *factsreport.Device, checks inspected, haveCurrent bool) {
	by, rekey, why := truncateBy(checks, haveCurrent)
	if in.State.NeedsRepair() {
		in.Repairs = []string{factsreport.RepairDiscard}
		if why == nil {
			in.Repairs = []string{factsreport.RepairTruncate, factsreport.RepairDiscard}
			if by.cut != nil {
				in.TruncateFrom = by.lastGood
			}
		}
	}
	switch in.State {
	case factsreport.StateEmpty:
		in.Advice = "Nothing is stored for this device; there is nothing to repair."
	case factsreport.StateHealthy:
		in.Advice = "Every stored fact opens as this device's and its chain verifies under the current key; there is nothing to repair."
	case factsreport.StateRekeyPending:
		in.Advice = "Every stored fact verifies under the previous key. A controller given the new key as PLEIADES_FACTS_DIGEST_KEY and this one as PLEIADES_FACTS_DIGEST_KEY_PREVIOUS re-keys it at its next read or start; there is nothing to repair."
	case factsreport.StateBroken:
		in.Advice = "Its stored facts are under the current key, every one opens, and they do not verify, and no key makes them verify again. " + repairsText(why, rekey, false)
	case factsreport.StateRefused:
		in.Advice = "Its stored facts are under the previous key, every one opens, and they do not verify under it, so the controller refuses to re-key them and serves none of them. Nothing makes them verify again; the backup taken before the change of key still holds them. " + repairsText(why, rekey, false)
	case factsreport.StatePartlyUnopened:
		in.Advice = fmt.Sprintf("%d of its %d stored rows do not open, and the rest do. %s A row sealed under a master key or key version this command was not given fails just as a damaged one does, and nothing tells the two apart, so this history is not called broken. %s Repair it only if the key those rows were sealed under is lost. %s",
			in.Rows.Unopened, in.Rows.Total, unopenedWhy(in), checkMasterKeys, repairsText(why, rekey, true))
	case factsreport.StateMixedKeys:
		in.Advice = "Its rows name the current key and the previous one at once, which no build of this release writes: the database was edited or restored in part. Only `controller facts discard --discard-unverifiable-history` repairs it; the backup taken before the change of key still holds the history."
	case factsreport.StateUnopened:
		in.Advice = "No stored row of it opens, whatever key it names. " + unopenedWhy(in) + " That is what a MASTER_ENCRYPTION_KEY other than the one its facts were sealed under looks like. " + checkMasterKeys + " Only if that key is lost does `controller facts discard --discard-unverifiable-history` repair it."
	default:
		in.Advice = "Some of its rows name a facts key this command was not given. If you still hold the key they were stored under, run this again with it as PLEIADES_FACTS_DIGEST_KEY_PREVIOUS; if not, only `controller facts discard --discard-unverifiable-history` repairs it."
	}
	if in.Rows.Unopened > 0 && (in.State == factsreport.StateOtherKeys || in.State == factsreport.StateMixedKeys) {
		in.Advice += fmt.Sprintf(" Besides, %d of its rows do not open. %s", in.Rows.Unopened, unopenedWhy(in))
	}
}

// repairsText says which repairs apply, from why no truncate does (nil when
// one does): rekey when a truncate re-keys what it keeps, flagged when each
// repair takes the flag that names an unverifiable history.
func repairsText(why error, rekey, flagged bool) string {
	truncate, discard := "`controller facts truncate`", "`controller facts discard`"
	if flagged {
		truncate, discard = "`controller facts truncate --truncate-unverifiable-history`", "`controller facts discard --discard-unverifiable-history`"
	}
	switch {
	case errors.Is(why, errTruncateKeepsNothing):
		return "Every value that verifies before the first failure was written by the gather a truncate removes, or none does, so a truncate would keep none of it: only " + discard + " repairs it, and the next gather starts a new history."
	case errors.Is(why, errTruncateNeedsCurrent):
		return discard + " removes it all; " + truncate + ", which keeps the history before the gather that wrote the last value that verifies, needs the new key as PLEIADES_FACTS_DIGEST_KEY too, to re-key what it keeps."
	}
	kept := " keeps the history before the gather that wrote the last value that verifies"
	if rekey {
		kept += " and re-keys it under the current key"
	}
	return truncate + kept + "; " + discard + " removes it all, and the next gather starts a new history."
}

// unopenedWhy says what the master key versions in's rows name make of the
// rows that do not open: the versions the command was given no key for, or
// that every version is one it holds a key for.
func unopenedWhy(in *factsreport.Device) string {
	if missing := versionsNotGiven(in.SealedUnder); len(missing) > 0 {
		return notGivenText(missing)
	}
	return "Every row names a master key version this command holds a key for, so a key given under the wrong version, or damaged rows, can each be why."
}

// notGivenText says, as a sentence, that rows sealed under versions, which
// the command holds no key for, cannot open.
func notGivenText(versions []string) string {
	clause := notGivenClause(versions)
	return strings.ToUpper(clause[:1]) + clause[1:] + "."
}

// notGivenClause is notGivenText as a clause, for an error.
func notGivenClause(versions []string) string {
	pronoun := "it"
	if len(versions) > 1 {
		pronoun = "them"
	}
	return "rows sealed under master key " + versionsText(versions) + " cannot open, since this command was not given a key for " + pronoun
}

// versionsNotGiven are the versions of sealed the command holds no key for.
func versionsNotGiven(sealed []factsreport.SealedRows) []string {
	var out []string
	for _, s := range sealed {
		if s.Role == factsreport.RoleOther {
			out = append(out, s.Version)
		}
	}
	return out
}

// versionsText names versions in a sentence: "version v2", "versions v2 and
// v3", "versions v2, v3 and v4".
func versionsText(versions []string) string {
	if len(versions) == 1 {
		return "version " + versions[0]
	}
	return "versions " + strings.Join(versions[:len(versions)-1], ", ") + " and " + versions[len(versions)-1]
}

// fleetAdvice is what a fleet inspection says of the fleet as a whole, ""
// when it has nothing to say: when every one of the scanned devices is
// under a facts key the command was not given, or has no row that opens,
// the fault is far likelier the command's own keys than every device's
// history; and when any device holds rows that do not open (unopened, the
// devices that do), the master key settings are what to check before
// believing them damaged. notGiven are the master key versions those rows
// name that the command holds no key for.
func fleetAdvice(scanned int, byState map[factsreport.State]int, unopened int, notGiven []string) string {
	if scanned == 0 {
		return ""
	}
	var said []string
	if byState[factsreport.StateOtherKeys] == scanned {
		said = append(said, "Every device's stored facts name a facts key this command was not given. That is what a wrong PLEIADES_FACTS_DIGEST_KEY looks like: run this with the keys the server runs with before repairing anything.")
	}
	missing := ""
	if len(notGiven) > 0 {
		missing = " " + notGivenText(notGiven)
	}
	switch {
	case byState[factsreport.StateUnopened] == scanned:
		said = append(said, "No device has a stored fact that opens. That is what a wrong MASTER_ENCRYPTION_KEY looks like: run this with the server's MASTER_ENCRYPTION_KEY, MASTER_ENCRYPTION_KEY_PREVIOUS and their versions before repairing anything."+missing)
	case unopened > 0:
		said = append(said, fmt.Sprintf("%d of the %d devices read hold rows that do not open.%s A row sealed under a master key or key version this command was not given fails just as a damaged one does: check MASTER_ENCRYPTION_KEY, its version and MASTER_ENCRYPTION_KEY_PREVIOUS with its version before believing those rows damaged. Repairing such a device takes the flag that names an unverifiable history.", unopened, scanned, missing))
	}
	return strings.Join(said, " ")
}
