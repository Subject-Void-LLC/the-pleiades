// Package rollback: holding a recorded undo to the declarations it was
// recorded under (Phase 136b).
//
// A journal entry names the Collection its method came from
// (JournalEntry.Collection: the group, the build, a digest of the group's
// declarations) and the digest of the undo's own declaration
// (InverseSpecDigest). A later build compares each with its own. The undo's
// digest decides, since it covers exactly what decides whether a recorded
// undo replays as it was recorded (collection.UndoDigest): a corrected
// sentence elsewhere in the group moves the group's digest and not the
// undo's, and needs no flag. Only an entry that recorded no undo digest
// falls back to the group's.
//
// What the comparison may relax is narrow. Every recorded value is still
// held to this build's declarations (checkRecorded): declared, recordable,
// and of the declared type. A parameter this build's undo method requires
// that the run did not record is refused whatever the operator names.
// What remains is a digest difference that adds only optional declarations
// or changes ones the entry does not record, which --allow-skew (the API's
// allow_skew) accepts by node.
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
	// none), and groupDigest its digest here ("" when this build holds none).
	group       string
	groupDigest string
	// undoDigest is this build's collection.UndoDigest of the undo.
	undoDigest string
}

// groupDigests holds collection.GroupDigest per group for one plan: a
// digest encodes every manifest of its group, and a Walk job's journal can
// hold one entry per node per device, so each group is digested once per
// Build rather than once per entry. A nil one digests on every call, which
// a Runner's handful of steps can afford.
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
// reading group digests through groups.
func skewOf(emitter string, id wire.CollectionIdentity, recordedUndo string, spec sdk.InverseSpec, target collection.Manifest, groups groupDigests) skew {
	k := skew{recorded: id, recordedUndo: recordedUndo,
		// The parameters as the engine digested them when it recorded the
		// undo (engine's inverseSpecDigest), fragments included.
		undoDigest: collection.UndoDigest(spec, fragment.ByNameIn(target))}
	if g, ok := collection.FirstPartyGroupOf(emitter); ok {
		k.group, k.groupDigest = g.Name, groups.of(g.Name)
	}
	return k
}

// unknown reports whether the entry recorded neither digest, so nothing
// tells its declarations from this build's.
func (k skew) unknown() bool { return k.recorded.IsZero() && k.recordedUndo == "" }

// skewed reports whether the undo was recorded under other declarations
// than this build's: by the undo's own digest when one was recorded, else
// by its group's.
func (k skew) skewed() bool {
	switch {
	case k.recordedUndo != "":
		return k.recordedUndo != k.undoDigest
	case !k.recorded.IsZero():
		return k.recorded.Manifests != k.groupDigest
	}
	return false
}

// describe says what the undo was recorded under and what this build has,
// with each name and digest quoted.
func (k skew) describe() string {
	recorded := "with no Collection identity"
	if !k.recorded.IsZero() {
		recorded = fmt.Sprintf("by the %q Collection at %q (manifests %q)", k.recorded.Group, k.recorded.Version, k.recorded.Manifests)
	}
	undo := "none"
	if k.recordedUndo != "" {
		undo = fmt.Sprintf("%q", k.recordedUndo)
	}
	here := "routes it to no first-party group"
	if k.group != "" {
		here = fmt.Sprintf("has the %q Collection's manifests %q", k.group, k.groupDigest)
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
	return fmt.Sprintf(" (recorded by the %q Collection at %q; this build is %q)", id.Group, id.Version, coreVersion())
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
		warnings = append(warnings, fmt.Sprintf("the journal records %s under the %q Collection, and this build routes it to %s; its undo is held to this build's declarations", emitter, k.recorded.Group, k.routedTo()))
	}
	if !k.skewed() {
		switch {
		case k.unknown():
			warnings = append(warnings, fmt.Sprintf("%s was recorded with no Collection identity, so its undo was checked against this build's declarations only", emitter))
		case k.recorded.IsZero():
			warnings = append(warnings, fmt.Sprintf("%s was recorded with no Collection identity, and its undo's declaration is this build's (undo digest %q)", emitter, k.undoDigest))
		case k.recorded.Manifests != k.groupDigest:
			warnings = append(warnings, fmt.Sprintf("%s's Collection declarations changed since the run, and its undo's own declaration is unchanged, so it replays as recorded: %s", emitter, k.describe()))
		}
		if named && !req.ExecutorIsPlanner {
			// A Runner of another build may hold other declarations than this
			// Controller's, and only this mark lets it run the step then.
			warnings = append(warnings, fmt.Sprintf("allow_skew names this node, and %s's undo matches this Controller's declarations; the step is marked so that a Runner whose declarations differ replays it: %s", emitter, k.describe()))
			return true, warnings, nil
		}
		return false, warnings, nil
	}
	if missing := requiredMissing(target, have); len(missing) > 0 {
		return false, nil, fmt.Errorf("the undo of %s through %s now requires %s, which the run did not record, so it cannot be replayed whatever is accepted: %s",
			emitter, method, quoted(missing), k.describe())
	}
	if !named {
		return false, nil, &stepError{
			reason: fmt.Sprintf("the undo of %s through %s was recorded under other declarations than this build's, and every value it recorded fits this build's: %s",
				emitter, method, k.describe()),
			accept: "--allow-skew " + node,
		}
	}
	// Named as the operator named it: the flag on the CLI, the request
	// field on the Controller.
	by := "--allow-skew"
	if !req.ExecutorIsPlanner {
		by = "allow_skew"
	}
	warnings = append(warnings, fmt.Sprintf("%s accepted the undo of %s through %s under other declarations: %s", by, emitter, method, k.describe()))
	return true, warnings, nil
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
	return fmt.Sprintf("node %s has no skew to accept: %s; drop --allow-skew %s", node, why, node)
}

// unjournaledSkews refuses each node AllowSkew names that no entry of the
// run's journal names, on either tier: it accepts nothing, and is most
// likely a mistyped name or one carried from another run's rollback.
func unjournaledSkews(req Request) []Problem {
	journaled := map[string]bool{}
	for _, e := range req.Target.Entries {
		journaled[e.NodeID] = true
	}
	var problems []Problem
	for node, named := range req.AllowSkew {
		if named && !journaled[node] {
			problems = append(problems, Problem{Node: node,
				Reason: fmt.Sprintf("a skew is accepted for node %q, which no entry of run %s's journal names, so it accepts nothing", node, req.Target.ID)})
		}
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
	return fmt.Sprintf("the undo of %s through %s replays under other declarations, accepted by name: %s", s.Emitter, s.FQCN, k.describe())
}
