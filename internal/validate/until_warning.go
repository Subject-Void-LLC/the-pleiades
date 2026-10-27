// Package validate: the warning for an until over a method that changes a
// device.
package validate

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// UntilWarnings warns on each task that retries (until) a method that is not
// declared read-only: running it again repeats its change. It is a warning
// and not a refusal, since a method that converges on a second run (a file
// that is already in place) is a fair thing to retry, and the author knows
// which they wrote. Each run is journaled on its own, so a rollback still
// undoes every one.
func UntilWarnings(world WorldView) []Finding {
	var ids []string
	for id, task := range world.DAG.Nodes {
		if len(task.Until) > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []Finding
	for _, id := range ids {
		task := world.DAG.Nodes[id]
		desc, ok := collection.Lookup(task.FQCN)
		if !ok || desc.Manifest.Reversibility.ReadOnly {
			continue
		}
		out = append(out, Finding{
			RuleName: "until",
			Node:     id,
			Message: fmt.Sprintf("task %s retries %s with until, and that method is not read-only, so each retry repeats its change; make sure running it again is safe (each run is journaled, and a rollback undoes them newest first)",
				taskName(id, task), task.FQCN),
		})
	}
	return out
}
