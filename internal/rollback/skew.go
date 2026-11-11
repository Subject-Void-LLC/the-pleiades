// Package rollback: holding a recorded undo to the declarations it was
// recorded under (Phase 136b).
//
// A journal entry names the Collection its method came from
// (JournalEntry.Collection: the group, the build, a digest of the group's
// declarations) and the digest of the undo's own declaration
// (InverseSpecDigest). A later build compares each with its own. The undo's
// digest decides, since it covers exactly what decides whether a recorded
// undo replays as it was recorded (collection.UndoDigest, each parameter's
// type, requirement, default, alias and choices among it): a corrected
// sentence elsewhere in the group moves the group's digest and not the
// undo's, and needs no flag. Only an entry that recorded no undo digest
// falls back to a group's (comparedGroup says which).
//
// What the comparison may relax is narrow. Every recorded value is still
// held to this build's declarations (checkRecorded): declared, recordable,
// of the declared type and inside the declared choices. A parameter this
// build's undo method requires that the run did not record is refused
// whatever the operator names. What remains is a digest difference that adds
// only optional declarations or changes ones the entry does not record,
// which --allow-skew (the API's allow_skew) accepts by node.
//
// Every stored identity is read through wire.CollectionIdentity.Shape,
// never Valid, since Valid depends on the Unicode tables of the Go release
// a build was made with (LESSONS 415), and every group, version and digest
// is printed quoted: Shape lets a group hold a format character, such as a
// direction override, that would otherwise make a line read as something
// else. Until identities are signed the digests are public, so a journal's
// writer can name this build's digests and suppress the comparison; it is
// advisory, and the checks above are what hold.
package rollback

import (
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// coreVersion is this build's version, as every binary reports it, read
// once: it reads the binary's build information on every call.
var coreVersion = sync.OnceValue(buildinfo.Version)

// skew is how one recorded undo's declarations compare with this build's.
type skew struct {
	// recorded and recordedUndo are what the entry recorded.
	recorded     wire.CollectionIdentity
	recordedUndo string
	// group is the first-party group this build routes the emitter to ("" for
	// none), and groups where its digest is read (groupDigest).
	group  string
	groups groupDigests
	// undoDigest is this build's collection.UndoDigest of the undo.
	undoDigest string
}

// groupDigests holds collection.GroupDigest per group for one plan. A
// digest encodes every manifest of its group, about a millisecond for a
// large one, and a Walk job's journal can hold one entry per node per
// device, so the planner digests each group once per Build. A nil one
// digests on every call: Verify passes nil, on the Controller for every
// step it plans and on a Runner for every step it runs, and a skew reads a
// group's digest only when it is needed (groupDigest), so the common step,
// whose undo digest was recorded and is this build's, never computes one.
type groupDigests map[string]string

// of returns group's digest in this build, "" when it holds none.
func (c groupDigests) of(group string) string {
	if digest, ok := c[group]; ok {
		return digest
	}
	// An error leaves the digest empty, which no recorded digest equals.
	digest, _ := collection.GroupDigest(group)
	if c != nil {
		c[group] = digest
	}
	return digest
}

// skewOf compares an undo of emitter recorded under id and recordedUndo with
// this build's declaration of it, spec, and its method's manifest, target,
// reading group digests through groups when it needs one.
func skewOf(emitter string, id wire.CollectionIdentity, recordedUndo string, spec sdk.InverseSpec, target collection.Manifest, groups groupDigests) skew {
	k := skew{recorded: id, recordedUndo: recordedUndo, groups: groups,
		// The parameters as the engine digested them when it recorded the
		// undo (engine's inverseSpecDigest), fragments included.
		undoDigest: collection.UndoDigest(spec, fragment.ByNameIn(target))}
	if g, ok := collection.FirstPartyGroupOf(emitter); ok {
		k.group = g.Name
	}
	return k
}

// comparedGroup is the group whose digest in this build a recorded identity
// is compared with, and that digest ("" for none). It is the group the
// entry recorded, whenever this build holds methods of it: a stored identity
// is held to its own group's declarations, not to how this build routes the
// method, which a later release may change (FAILURE_PATTERNS 988). Only when
// this build holds no method of the recorded group (one a later release
// left out, or an entry naming no group, which an entry with no identity
// does) is it the group this build routes the emitter to. A method moved
// out of a group leaves that group's declarations changed, so an entry
// recorded before the move and with no undo digest reads as a skew either
// way, which only --allow-skew accepts.
func (k skew) comparedGroup() (string, string) {
	if !k.recorded.IsZero() {
		if digest := k.groups.of(k.recorded.Group); digest != "" {
			return k.recorded.Group, digest
		}
	}
	if k.group == "" {
		return "", ""
	}
	return k.group, k.groups.of(k.group)
}

// groupDigest is comparedGroup's digest.
func (k skew) groupDigest() string {
	_, digest := k.comparedGroup()
	return digest
}

// unknown reports whether the entry recorded neither digest, so nothing
// tells its declarations from this build's.
func (k skew) unknown() bool { return k.recorded.IsZero() && k.recordedUndo == "" }

// undoMatches reports whether the entry recorded an undo digest and it is
// this build's: the undo's declaration is the one it was recorded under.
func (k skew) undoMatches() bool { return k.recordedUndo != "" && k.recordedUndo == k.undoDigest }

// skewed reports whether the undo was recorded under other declarations
// than this build's: by the undo's own digest when one was recorded, else
// by its group's.
func (k skew) skewed() bool {
	switch {
	case k.recordedUndo != "":
		return !k.undoMatches()
	case !k.recorded.IsZero():
		return k.recorded.Manifests != k.groupDigest()
	}
	return false
}

// missingRequired refuses an undo that does not give a parameter target
// requires in this build, whatever its digests: replaying it cannot work,
// so no name accepts it. Under this build's own undo declaration only a
// forged entry, or a declaration whose Record leaves a required parameter
// out (which no built-in's does without withholding it, and a withheld value
// already makes the undo one not recorded in full), reaches it.
func (k skew) missingRequired(emitter, method string, target collection.Manifest, have map[string]bool) error {
	missing := requiredMissing(target, have)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%s requires %s in this build, which the undo of %s through %s does not give, so it cannot be replayed whatever is accepted: %s",
		Printable(method), quoted(missing), Printable(emitter), Printable(method), k.describe())
}

// describe says what the undo was recorded under and what this build has,
// with each name and digest quoted.
func (k skew) describe() string {
	recorded := "with no Collection identity"
	if !k.recorded.IsZero() {
		recorded = fmt.Sprintf("by the %s Collection at %s (manifests %s)", termsafe.QuoteBounded(k.recorded.Group), termsafe.QuoteBounded(k.recorded.Version), termsafe.QuoteBounded(k.recorded.Manifests))
	}
	undo := "none"
	if k.recordedUndo != "" {
		undo = termsafe.QuoteBounded(k.recordedUndo)
	}
	here := "holds no first-party group to compare it with"
	if group, digest := k.comparedGroup(); group != "" {
		here = fmt.Sprintf("has the %q Collection's manifests %q", group, digest)
	}
	return fmt.Sprintf("it was recorded %s with undo digest %s, and this build is %q, %s and undo digest %q",
		recorded, undo, coreVersion(), here, k.undoDigest)
}

// routedTo names, quoted, the group this build routes an emitter to, for
// a sentence.
func (k skew) routedTo() string {
	if k.group == "" {
		return "no first-party group"
	}
	return fmt.Sprintf("%q", k.group)
}

// versions names the build that recorded an entry and this one, for a
// refusal of a recorded value: empty for an entry that recorded no
// identity, whose build nobody knows.
func versions(id wire.CollectionIdentity) string {
	if id.IsZero() {
		return ""
	}
	return fmt.Sprintf(" (recorded by the %s Collection at %s; this build is %q)", termsafe.QuoteBounded(id.Group), termsafe.QuoteBounded(id.Version), coreVersion())
}

// requiredMissing returns, sorted, every parameter target requires (its own
// or a fragment's) that have gives no value, by its name or an alias's.
func requiredMissing(target collection.Manifest, have map[string]bool) []string {
	params := fragment.ByNameIn(target)
	var missing []string
	for name, p := range params {
		if !p.Required || have[name] {
			continue
		}
		given := false
		for alias, a := range params {
			given = given || (a.AliasOf == name && have[alias])
		}
		if !given {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// judgeSkew decides one recorded undo's skew for the planner, after every
// recorded value passed checkRecorded: the step's SkewAccepted, the
// warnings to show, or the refusal. have names the parameters recorded.
func judgeSkew(req Request, node string, k skew, emitter, method string, target collection.Manifest, have map[string]bool) (accepted bool, warnings []string, err error) {
	named := req.AllowSkew[node]
	if !k.recorded.IsZero() && k.recorded.Group != k.group {
		// A minor release may move a method to another group, so a stored
		// group is never held to this build's routing (FAILURE_PATTERNS 988).
		warnings = append(warnings, fmt.Sprintf("the journal records %s under the %s Collection, and this build routes it to %s; its undo is held to this build's declarations", Printable(emitter), termsafe.QuoteBounded(k.recorded.Group), k.routedTo()))
	}
	if err := k.missingRequired(emitter, method, target, have); err != nil {
		return false, nil, err
	}
	if !k.skewed() {
		switch {
		case k.unknown():
			warnings = append(warnings, fmt.Sprintf("%s was recorded with no Collection identity, so its undo was checked against this build's declarations only", Printable(emitter)))
		case k.recorded.IsZero():
			warnings = append(warnings, fmt.Sprintf("%s was recorded with no Collection identity, and its undo's declaration is this build's (undo digest %q)", Printable(emitter), k.undoDigest))
		case k.recorded.Manifests != k.groupDigest():
			warnings = append(warnings, fmt.Sprintf("%s's Collection declarations changed since the run, and its undo's own declaration is unchanged, so it replays as recorded: %s", Printable(emitter), k.describe()))
		}
		if named && !req.ExecutorIsPlanner {
			// A Runner of another build may hold other declarations than this
			// Controller's, and only this mark lets it run the step then. The
			// warning says what this Controller compared, which for an entry
			// that recorded neither digest is nothing.
			warnings = append(warnings, namedUnskewed(emitter, k))
			return true, warnings, nil
		}
		return false, warnings, nil
	}
	if !named {
		return false, nil, &stepError{
			reason: fmt.Sprintf("the undo of %s through %s was recorded under other declarations than this build's, and every value it recorded fits this build's: %s",
				Printable(emitter), Printable(method), k.describe()),
			accept: "--allow-skew " + node,
		}
	}
	// Named as the operator named it: the flag on the CLI, the request
	// field on the Controller.
	by := "--allow-skew"
	if !req.ExecutorIsPlanner {
		by = "allow_skew"
	}
	warnings = append(warnings, fmt.Sprintf("%s accepted the undo of %s through %s under other declarations: %s", by, Printable(emitter), Printable(method), k.describe()))
	return true, warnings, nil
}

// namedUnskewed is the warning for a node allow_skew names whose undo this
// Controller finds no skew in, saying what it compared.
func namedUnskewed(emitter string, k skew) string {
	switch {
	case k.recordedUndo != "":
		return fmt.Sprintf("allow_skew names this node, and the undo digest %s's entry recorded (%s) is this Controller's; the step is marked so that a Runner whose own declarations differ replays it", Printable(emitter), termsafe.QuoteBounded(k.recordedUndo))
	case !k.recorded.IsZero():
		return fmt.Sprintf("allow_skew names this node, and %s's Collection digest as recorded (%s) is this Controller's; the step is marked so that a Runner whose own declarations differ replays it", Printable(emitter), termsafe.QuoteBounded(k.recorded.Manifests))
	}
	return fmt.Sprintf("allow_skew names this node, and %s was recorded with no Collection identity or undo digest, so nothing was compared, on this Controller or on a Runner; the name changes nothing", Printable(emitter))
}

// quoted is names, each quoted, joined for a sentence.
func quoted(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%q", n)
	}
	return out
}

// needlessSkew is why --allow-skew accepts nothing for a node whose steps
// the planner built, or "" when it accepted a skew there.
func needlessSkew(node string, steps []Step) string {
	if slices.ContainsFunc(steps, func(s Step) bool { return s.SkewAccepted }) {
		return ""
	}
	why := "the undo it recorded matches this build's declarations"
	switch {
	case len(steps) == 0:
		why = "its rollback: list says there is nothing to undo"
	case steps[0].Source == SourceAuthored:
		why = "its undo is its runbook's rollback: list, which is held to the runbook rather than to a Collection's declarations"
	case steps[0].Collection.IsZero() && steps[0].UndoDigest == "":
		why = "it was recorded with no Collection identity, so nothing tells its declarations from this build's"
	}
	return fmt.Sprintf("node %s has no skew to accept: %s; drop --allow-skew %s", Printable(node), why, Printable(node))
}

// unplannedSkews refuses, on either tier, each node AllowSkew names that
// has no change in the run to undo: one no entry of its journal names, and
// one whose entries ran without changing anything or failed with an effect
// nobody knows (changed holds the run's changes, changesOf's). Nothing of
// such a node is replayed by the planner or by a Runner, so the name
// accepts nothing, and it is most likely mistyped or carried from another
// run's rollback.
func unplannedSkews(req Request, changed []engine.JournalEntry) []Problem {
	journaled, hasChange := map[string]bool{}, map[string]bool{}
	for _, e := range req.Target.Entries {
		journaled[e.NodeID] = true
	}
	for _, e := range changed {
		hasChange[e.NodeID] = true
	}
	var problems []Problem
	for node, named := range req.AllowSkew {
		var reason string
		switch {
		case !named || hasChange[node]:
			continue
		case journaled[node]:
			reason = fmt.Sprintf("a skew is accepted for node %q, which changed nothing in run %s or failed with an effect nobody knows, so it has no undo to compare and the name accepts nothing", node, req.Target.ID)
		default:
			reason = fmt.Sprintf("a skew is accepted for node %q, which no entry of run %s's journal names, so it accepts nothing", node, req.Target.ID)
		}
		problems = append(problems, Problem{Node: node, Reason: reason})
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Node < problems[j].Node })
	return problems
}

// SkewNote describes how a recorded step's declarations differ from this
// build's, for a Runner to log when it replays one the plan accepted by
// name; "" when they do not differ, nothing was recorded to compare, or the
// step is not a recorded undo this build declares.
func SkewNote(s Step) string {
	if s.Source != SourceRecorded {
		return ""
	}
	spec, target, err := declaredUndo(s.Emitter, s.FQCN)
	if err != nil {
		return ""
	}
	k := skewOf(s.Emitter, s.Collection, s.UndoDigest, *spec, target, nil)
	if !k.skewed() {
		return ""
	}
	return fmt.Sprintf("the undo of %s through %s replays under other declarations, accepted by name: %s", Printable(s.Emitter), Printable(s.FQCN), k.describe())
}
