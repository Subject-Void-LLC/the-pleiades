// Package record: which of a record's grants of data capabilities count
// toward its device's capabilities, and why each one that does not.
package record

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

// GrantDigestRequiredFrom is the first release line on which a grant that
// carries no definition digest (a legacy grant: 0.3.0 wrote every grant
// that way) grants nothing. On an earlier line (a 0.3.x patch release) it
// still counts, with a warning naming the device and the capability, so
// upgrading within 0.3 never withdraws a grant.
const GrantDigestRequiredFrom = "0.4.0"

// grantPolicyLine is the release line the legacy rule is decided on: this
// source tree's own (buildinfo.CurrentRelease), never a stamped binary
// version, so a development build behaves as the release it becomes. A
// variable so a test can decide it as a 0.3.x tree would.
var grantPolicyLine = buildinfo.CurrentRelease

// withGrants adds to caps the data capabilities rec is granted
// (inventory.GrantedProperty), and returns a note for each grant that
// grants nothing or counts only with a warning. NewBase calls it, so a
// grant counts for every device type whatever builds its capabilities.
//
// A record whose grants are malformed (a name that is not a data
// capability's, an unknown source) was not written by RecordGrant, and
// grants nothing: failing closed. A grant on a bound type holds only while
// the properties it was made against are as they were. Where this process
// holds the capability's definition (a loaded program defined it), a grant
// counts only when it was made against that definition (its digest
// matches), its properties fit it, and caps already has every compiled
// capability it requires. Where it does not (a Controller, which loads no
// program, or a command line whose program was removed), the name is kept
// as recorded and grants nothing there, since capability.Implements answers
// false for a name the vocabulary does not hold; its note says so.
func withGrants(rec Record, caps []capability.Name) ([]capability.Name, []inventory.GrantNote) {
	props := inventory.NewProperties(rec.Properties)
	grants, ok, err := inventory.GrantsFrom(props)
	if err != nil || !ok {
		return caps, nil
	}
	held := make(map[capability.Name]struct{}, len(caps))
	for _, c := range caps {
		held[c] = struct{}{}
	}
	rules, _ := discoveryRules.Get(rec.Type)
	binding := DiscoveryBinding(rec.Type, props)
	var granted []capability.Name
	var notes []inventory.GrantNote
	lapse := func(g inventory.Grant, reason string) {
		notes = append(notes, inventory.GrantNote{Capability: g.Capability, Lapsed: true, Reason: reason})
	}
	for _, g := range grants {
		// A grant whose record could not be read whole (its time is missing,
		// malformed or out of range) is not one: what else it says cannot be
		// trusted to be what a writer wrote.
		if g.Unreadable != "" {
			lapse(g, fmt.Sprintf("device %s's grant of %s cannot be read: %s; %s", rec.Name, g.Capability, g.Unreadable, grantAgain(rec.Name, g)))
			continue
		}
		// A grant on a bound type, probed or asserted, holds only while the
		// properties it was made against are as they were (a repointed
		// device is probed or vouched for again before a program sends its
		// credential anywhere new).
		if binding != "" && g.Binding != binding {
			lapse(g, fmt.Sprintf("device %s's grant of %s was made against other values of %s; %s",
				rec.Name, g.Capability, strings.Join(rules.Bound, ", "), grantAgain(rec.Name, g)))
			continue
		}
		def, defined := capability.DataOf(g.Capability)
		if !defined {
			lapse(g, fmt.Sprintf("device %s's grant of %s grants nothing here: no loaded program defines %s", rec.Name, g.Capability, g.Capability))
			granted = append(granted, g.Capability)
			continue
		}
		if note, counts := definitionHolds(rec.Name, g, def); !counts {
			lapse(g, note)
			continue
		} else if note != "" {
			notes = append(notes, inventory.GrantNote{Capability: g.Capability, Reason: note})
		}
		if err := capability.ValidateGrant(def, g.Properties); err != nil {
			lapse(g, fmt.Sprintf("device %s's grant of %s does not fit program %q's definition of it (%v); %s",
				rec.Name, g.Capability, def.Provider, err, grantAgain(rec.Name, g)))
			continue
		}
		if !requiresHeld(def, held) {
			lapse(g, fmt.Sprintf("device %s's grant of %s needs %s beside it, which the device does not have",
				rec.Name, g.Capability, joinNames(def.Requires)))
			continue
		}
		granted = append(granted, g.Capability)
	}
	return policy.UnionSlices(caps, granted), notes
}

// definitionHolds decides g against def, the definition this process
// holds: whether it counts, and the note that goes with it (why it lapsed,
// or the warning a legacy grant counts with).
func definitionHolds(device string, g inventory.Grant, def capability.DataDefinition) (string, bool) {
	switch {
	case g.Definition == "" && legacyGrantsCount(grantPolicyLine):
		return fmt.Sprintf("device %s's grant of %s predates binding a grant to its capability's definition; it counts on this release line and grants nothing from %s on: %s",
			device, g.Capability, GrantDigestRequiredFrom, grantAgain(device, g)), true
	case g.Definition == "":
		return fmt.Sprintf("device %s's grant of %s predates binding a grant to its capability's definition, which every grant needs from %s on; %s",
			device, g.Capability, GrantDigestRequiredFrom, grantAgain(device, g)), false
	case g.Definition != capability.DefinitionDigest(def):
		return fmt.Sprintf("program %q's definition of %s (revision %d) is not the one device %s's grant was made against; %s",
			def.Provider, g.Capability, def.Revision, device, grantAgain(device, g)), false
	}
	return "", true
}

// grantAgain says how a lapsed grant is made again: a probed one by
// onboarding the device again, an asserted one by asserting it again.
func grantAgain(device string, g inventory.Grant) string {
	if g.Source == inventory.GrantAsserted {
		return fmt.Sprintf("assert it again: `pleiades grant-capability %s %s --reason <why>`", device, g.Capability)
	}
	return fmt.Sprintf("onboard it again: `pleiades onboard %s`", device)
}

// requiresHeld reports whether held has every compiled capability def
// requires.
func requiresHeld(def capability.DataDefinition, held map[capability.Name]struct{}) bool {
	for _, req := range def.Requires {
		if !capability.Resolves(held, req) {
			return false
		}
	}
	return true
}

// joinNames joins capability names with commas.
func joinNames(names []capability.Name) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = string(n)
	}
	return strings.Join(parts, ", ")
}

// legacyGrantsCount reports whether a grant with no definition digest
// still counts on the release line line: only before
// GrantDigestRequiredFrom. A line that does not read as a release counts
// as one at or after it, failing closed.
func legacyGrantsCount(line string) bool {
	have, ok := releaseNumbers(line)
	want, _ := releaseNumbers(GrantDigestRequiredFrom)
	if !ok {
		return false
	}
	for i := range have {
		if have[i] != want[i] {
			return have[i] < want[i]
		}
	}
	return false
}

// releaseNumbers reads "MAJOR.MINOR.PATCH".
func releaseNumbers(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
