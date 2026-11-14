// Package engine: finding the tasks of a compiled runbook that would seed a
// login, so the Controller can refuse such a job before it publishes a
// dispatch.
//
// A login seed (collection.Manifest.SeedsLogin) is at least as sensitive as
// the credential it is derived from: a password hash can be cracked offline,
// and a Windows answer file carries the password itself. The owner decided
// on 2026-11-11 that a seed reaches a Runner only through the claim-time
// fetch of the credential (Phase 105), which is not built yet, so until then
// the Walk tier refuses a seeding task outright. The Runner's engine already
// refuses one (seedLogin), but only after the dispatch has crossed the
// stream. This is the Controller's half, asked before anything is published.
package engine

import (
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// SeededLogins returns one line for each task of dag that would seed a
// login into a machine it creates, sorted by node id, or nil when none
// would. A task counts when its method's manifest (collection.ManifestOf, the
// registry or the installed module catalog) sets SeedsLogin and the task
// either sets that parameter or the parameter is required. An optional
// parameter left out seeds nothing on any tier (seedLogin), so such a task
// does not count. A task's rollback: steps are asked the same question, since
// they run on the Runner too. Each line names the node, the method and the
// device whose login would be seeded, never a secret: none is known here.
//
// Conditional tasks count: a condition that holds on the Runner would run
// the task there.
func SeededLogins(dag *DAG) []string {
	if dag == nil {
		return nil
	}
	ids := make([]string, 0, len(dag.Nodes))
	for id := range dag.Nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var out []string
	for _, id := range ids {
		task := dag.Nodes[id]
		if task == nil {
			continue
		}
		if line, ok := seededLogin(id, *task); ok {
			out = append(out, line)
		}
		for i, step := range task.Rollback {
			if line, ok := seededLogin(fmt.Sprintf("%s.rollback[%d]", id, i), step); ok {
				out = append(out, line)
			}
		}
	}
	return out
}

// seededLogin describes task, at node id, when it would seed a login.
func seededLogin(id string, task Task) (string, bool) {
	if task.FQCN == "" {
		return "", false
	}
	manifest, ok := collection.ManifestOf(task.FQCN)
	if !ok || manifest.SeedsLogin == "" {
		return "", false
	}
	param := manifest.SeedsLogin
	value, set := task.Params[param]
	if s, isString := value.(string); isString && s == "" {
		set = false
	}
	if !set && !manifestRequires(manifest, param) {
		return "", false
	}
	device := "(not set)"
	if set {
		device = fmt.Sprintf("%q", fmt.Sprint(value))
	}
	return fmt.Sprintf("task %q runs %s, which seeds the login of device %s (its %s parameter) into the machine it creates", id, task.FQCN, device, param), true
}
