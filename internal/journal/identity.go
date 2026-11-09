// Package journal: what the Controller stores of the Collection identity and
// undo digest a Runner's journal rows carry (Phase 136b).
//
// The Controller writes a Runner's rows into its own store, so it is their
// writer, and a writer writes only what the engine's projection writes
// (LESSONS 413): an identity that passes wire.CollectionIdentity.Valid, a
// program's identity on an entry a program ran and no other, and an undo
// digest of a digest's shape. What a batch carries beyond that (a Runner
// out of step with itself, or a batch made by hand) is stored as none, and
// the entry is kept, as the facts subscriber keeps a fact whose identity it
// clears: the row is the device's history, and an absent identity reads as
// unknown, as a row from before the field does. Valid's Unicode rule is
// applied here, at the write, and never by a reader of what was stored, so
// a later Go release's tables cannot make a stored row unreadable (LESSONS
// 415).
package journal

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// identityProblems returns entry with any Collection identity and undo
// digest the engine's projection would not have written cleared, and why
// each was cleared. An entry with nothing to clear comes back as it was,
// with no reasons.
func identityProblems(entry engine.JournalEntry) (engine.JournalEntry, []string) {
	var reasons []string
	if id := entry.Collection; !id.IsZero() {
		var err error
		switch {
		case id.Valid() != nil:
			err = id.Valid()
		case id.Program && entry.ProviderProgram == "":
			err = fmt.Errorf("it names the external program %q, and the entry names no program that ran it", id.Group)
		case !id.Program && entry.ProviderProgram != "":
			err = fmt.Errorf("it names the first-party %q Collection, and the entry was run by the external program %q", id.Group, entry.ProviderProgram)
		}
		if err != nil {
			reasons = append(reasons, "Collection identity: "+err.Error())
			entry.Collection = wire.CollectionIdentity{}
		}
	}
	if entry.InverseSpecDigest != "" && !digestPattern.MatchString(entry.InverseSpecDigest) {
		reasons = append(reasons, fmt.Sprintf("undo digest %q is not a digest", entry.InverseSpecDigest))
		entry.InverseSpecDigest = ""
	}
	return entry, reasons
}
