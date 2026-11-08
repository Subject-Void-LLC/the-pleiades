// Package native: comparing the Collections a dispatch names with this
// Runner's own (Phase 136b, wire.DispatchPayload.Collections).
//
// The Controller names, for each first-party group the dispatch's methods
// belong to, the identity its installed catalog gives the group: its build's
// version and the digest of the group's manifests. This Runner compares each
// with its own and, in this release, warns and runs. Running is safe: the
// Runner runs its own linked code under its own checks, and journals its own
// identity, which is what a later rollback compares. A refusal waits for a
// release that can say which versions a group may differ by.
package native

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// runnerVersion is this Runner's build version (buildinfo.Version), read
// once: it reads the binary's build information on every call, and it
// cannot change while the process runs.
var runnerVersion = sync.OnceValue(buildinfo.Version)

// compareCollections compares each group payload names with this Runner's
// own, logs what differs through logger, and returns each warning as a
// sentence for the job's log, which Execute publishes as a task.warning
// event so an operator reading the job sees it. It never refuses.
//
// The cases, each its own line so a reader can tell them apart:
//   - Nothing named (an older Controller, or a playbook): nothing to compare,
//     and nothing is logged.
//   - The same digest: the same declarations, so no warning, since only the
//     digests decide whether the declarations differ: two development builds
//     of different trees can share a version, and a rebuild of one tree
//     changes none of its declarations. A version that differs is logged at
//     debug.
//   - Another digest under another version: two builds; warned, naming the
//     group and both versions and digests.
//   - Another digest under the same version: no real deployment makes it, so
//     it is warned as a stale module catalog or a corrupt build. The
//     versions choose between this wording and the one above; they never
//     decide whether to warn.
//   - A group this Runner holds nothing of: warned; its tasks then fail as
//     unregistered, as they would have without the comparison.
//   - An entry marked as a program's, or one Shape refuses: ignored and
//     warned, since a Controller loads no program and an entry no build
//     writes compares to nothing. Shape, never Valid, so the rule a reader
//     holds an entry to does not move with this Runner's Unicode tables.
//   - A method of dag this Runner runs from an external program while the
//     Controller named a first-party group for it: warned on its own, since
//     it is not a digest that differs but a different Collection.
//   - A built-in method of dag whose group the Controller did not name, when
//     it named others: its catalog does not hold the method (a Runner newer
//     than its Controller), or it could not identify the group. Logged at
//     info and not warned: nothing was named to compare, and the Runner's
//     own preflight decides whether the method runs.
func compareCollections(logger *slog.Logger, payload wire.DispatchPayload, dag *engine.DAG) []string {
	if len(payload.Collections) == 0 {
		return nil
	}
	log := logger.With(slog.String("job", payload.JobID), slog.String("device", payload.DeviceName))
	version := runnerVersion()
	var warnings []string
	// warn logs msg with attrs and keeps job, the same warning as a sentence
	// for the job's log.
	warn := func(job, msg string, attrs ...any) {
		log.Warn(msg, attrs...)
		warnings = append(warnings, job)
	}
	// accepted are the keys of the entries well formed enough to say which
	// group the Controller placed a method in: every one the loop below does
	// not refuse as a program's or as a shape no build writes.
	accepted := map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(payload.Collections)) {
		named := payload.Collections[key]
		if named.Program {
			warn(fmt.Sprintf("the Controller named an external program's identity for the Collection %q; a Controller loads no program, so it was not compared", key),
				"the Controller named an external program's identity for a Collection; a Controller loads no program, so it is not compared",
				slog.String("group", key))
			continue
		}
		if err := named.Shape(); err != nil || named.Group != key {
			reason := shapeReason(err, named, key)
			warn(fmt.Sprintf("the Controller named an identity for the Collection %q that no build writes (%v), so it was not compared", key, reason),
				"the Controller named a Collection identity no build writes; it is not compared",
				slog.String("group", key), slog.Any("reason", reason))
			continue
		}
		accepted[key] = true
		own, err := collection.GroupDigest(key)
		if err != nil {
			warn(fmt.Sprintf("the Controller (%s) planned with the Collection %s, which this Runner (%s) does not include; its tasks will fail as unregistered", named.Version, key, version),
				"the Controller named a Collection this Runner does not include; its tasks will fail as unregistered",
				slog.String("group", key), slog.String("controller_version", named.Version), slog.Any("reason", err))
			continue
		}
		attrs := []any{slog.String("group", key),
			slog.String("controller_version", named.Version), slog.String("runner_version", version),
			slog.String("controller_manifests", named.Manifests), slog.String("runner_manifests", own)}
		both := fmt.Sprintf("the Controller's Collection %s at %s declares %s, and this Runner's at %s declares %s", key, named.Version, named.Manifests, version, own)
		switch {
		case own == named.Manifests:
			if named.Version != version {
				log.Debug("the Controller and this Runner are different builds with the same declarations for a Collection", attrs...)
			}
		case named.Version == version:
			warn(both+": one version with different declarations, which only a stale module catalog or a corrupt build makes; the Runner runs its own",
				"the Controller and this Runner report one build and different declarations for a Collection, which only a stale module catalog or a corrupt build makes; the Runner runs its own", attrs...)
		default:
			warn(both+": the Controller planned against other declarations than this Runner runs; the Runner runs its own",
				"the Controller planned against other declarations of a Collection than this Runner runs; the Runner runs its own", attrs...)
		}
	}
	for _, fqcn := range dagMethods(dag) {
		desc, ok := collection.Lookup(fqcn)
		if !ok {
			continue
		}
		group, named := namedGroupOf(accepted, fqcn)
		switch {
		case desc.Provider != nil && named:
			warn(fmt.Sprintf("the Controller expects %s from the first-party Collection %s, and this Runner runs it from the external program %s", fqcn, group, desc.Provider.Program),
				"the Controller expects this method from a first-party Collection, and this Runner runs it from an external program",
				slog.String("fqcn", fqcn), slog.String("group", group), slog.String("program", desc.Provider.Program))
		case desc.Provider == nil && !named:
			if own, firstParty := collection.FirstPartyGroupOf(fqcn); firstParty {
				log.Info("the Controller named no Collection for this built-in method, so there was nothing to compare",
					slog.String("fqcn", fqcn), slog.String("group", own.Name))
			}
		}
	}
	return warnings
}

// shapeReason says why an entry is not compared: Shape's refusal, or a key
// that is not the group the entry names.
func shapeReason(err error, named wire.CollectionIdentity, key string) any {
	if err != nil {
		return err
	}
	return "it is keyed " + key + " and names group " + named.Group
}

// namedGroupOf is the group, among the accepted keys of the entries the
// Controller sent, whose name is the longest prefix of fqcn ending at a dot:
// the group the Controller placed fqcn in, read from what it sent rather
// than from this Runner's own list of groups, which an older Runner may lack
// a newer group of. An entry refused as a program's or for its shape places
// nothing, so its key is not in accepted.
func namedGroupOf(accepted map[string]bool, fqcn string) (string, bool) {
	best := ""
	for key := range accepted {
		if !strings.HasPrefix(fqcn, key+".") {
			continue
		}
		if len(key) > len(best) {
			best = key
		}
	}
	return best, best != ""
}

// dagMethods returns, sorted and deduplicated, every method a node of dag
// names, conditional ones included: a condition that holds runs its task.
func dagMethods(dag *engine.DAG) []string {
	seen := map[string]bool{}
	for _, task := range dag.Nodes {
		if task.FQCN != "" {
			seen[task.FQCN] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}
