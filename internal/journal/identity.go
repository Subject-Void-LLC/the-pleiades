// Package journal: what the Controller stores of the Collection identity and
// undo digest a Runner's journal rows carry (Phase 136b).
//
// The Controller writes a Runner's rows into its own store, so it is their
// writer, and a writer writes only what the engine's projection writes
// (LESSONS 413): an identity that passes wire.CollectionIdentity.Deliverable,
// a program's identity on an entry a program ran and no other, and an undo
// digest of a digest's shape. What a batch carries beyond that (a Runner
// out of step with itself, or a batch made by hand) is stored as none, and
// the entry is kept, as the facts subscriber keeps a fact whose identity it
// clears: the row is the device's history, and an absent identity reads as
// unknown, as a row from before the field does.
//
// Deliverable is Shape and a frozen list of misleading characters, and reads
// no Unicode table, so whether a row keeps its identity cannot depend on the
// toolchain this Controller was built with. Valid, which reads the tables,
// is the Runner's rule for what it makes; where it would differ here, the
// subscriber says so at info (validDiffers) and stores the identity as sent.
// A reader of what was stored holds it to Shape alone (LESSONS 415, 422).
package journal

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// identityProblems returns entry with any Collection identity and undo
// digest the engine's projection would not have written cleared, and why
// each was cleared. An entry with nothing to clear comes back as it was,
// with no reasons. Each reason is fixed text: the group, the program and the
// digest are delivered bytes of any length, so none is repeated.
func identityProblems(entry engine.JournalEntry) (engine.JournalEntry, []string) {
	var reasons []string
	if id := entry.Collection; !id.IsZero() {
		reason := ""
		switch err := id.Deliverable(); {
		case err != nil:
			reason = err.Error()
		case id.Program && entry.ProviderProgram == "":
			reason = "it names an external program, and the entry names no program that ran it"
		case !id.Program && entry.ProviderProgram != "":
			reason = "it names a first-party Collection, and the entry was run by an external program"
		}
		if reason != "" {
			reasons = append(reasons, "Collection identity: "+reason)
			entry.Collection = wire.CollectionIdentity{}
		}
	}
	if entry.InverseSpecDigest != "" && !digestPattern.MatchString(entry.InverseSpecDigest) {
		reasons = append(reasons, "undo digest: it is not sha256:<64 lowercase hex digits>")
		entry.InverseSpecDigest = ""
	}
	return entry, reasons
}

// validDiffers returns why this Controller's own Valid refuses the identity
// entry kept, which only its toolchain's Unicode tables decide, or "" when
// it would not. The answer is fixed text, so it repeats none of the group.
func validDiffers(entry engine.JournalEntry) string {
	if id := entry.Collection; !id.IsZero() {
		if err := id.Valid(); err != nil {
			return err.Error()
		}
	}
	return ""
}
