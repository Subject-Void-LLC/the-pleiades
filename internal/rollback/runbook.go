// Package rollback: the runbook a plan runs as.
package rollback

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"go.yaml.in/yaml/v3"
)

// Runbook writes levels as the runbook a rollback runs, named id: one
// task per step, and a parallel group where a level holds several
// devices. With targets, each task targets the device it undoes a change
// on (the Crawl tier, one run over many devices); without, the tasks name
// no device, for a Runner that runs every task on the one device it was
// dispatched. It returns which of the runbook's nodes undoes what, for
// engine.WithRollback.
//
// The runbook is encoded, never joined as text, so no recorded value can
// change its shape.
func Runbook(id string, levels [][]Step, targets bool) ([]byte, map[string]engine.Undo, error) {
	undoes := map[string]engine.Undo{}
	stepTask := func(s Step) map[string]any {
		params := map[string]any{}
		for k, v := range s.Params {
			params[k] = v
		}
		name := s.Name
		if targets && s.DeviceName != "" {
			params[collection.TargetParam] = s.DeviceName
			name += " on " + s.DeviceName
		}
		task := map[string]any{"name": name, s.FQCN: params}
		if s.Become != "" {
			// The undo runs as the account the change ran as.
			task["become"] = true
			task["become_user"] = s.Become
		}
		return task
	}
	var tasks []map[string]any
	for i, level := range levels {
		if len(level) == 1 {
			tasks = append(tasks, stepTask(level[0]))
			undoes[NodeID(levels, i, 0)] = engine.Undo{Node: level[0].Node, Step: level[0].Index}
			continue
		}
		children := make([]map[string]any, 0, len(level))
		for j, s := range level {
			children = append(children, stepTask(s))
			undoes[NodeID(levels, i, j)] = engine.Undo{Node: s.Node, Step: s.Index}
		}
		tasks = append(tasks, map[string]any{"name": fmt.Sprintf("undo %s on %d devices", level[0].Node, len(level)), "parallel": children})
	}
	payload, err := yaml.Marshal(map[string]any{"id": id, "tasks": tasks})
	if err != nil {
		return nil, nil, fmt.Errorf("writing the rollback runbook: %w", err)
	}
	return payload, undoes, nil
}

// NodeID is the node id Runbook gives level i's step j.
func NodeID(levels [][]Step, i, j int) string {
	if len(levels[i]) == 1 {
		return fmt.Sprintf("tasks[%d]", i)
	}
	return fmt.Sprintf("tasks[%d].parallel[%d]", i, j)
}

// Provisional returns, by the node id Runbook gives it, each step on a
// device an earlier level of the same plan changes first, with why, for
// engine.WithProvisionalChecks: a check of it meets the device before that
// step has run.
func Provisional(levels [][]Step) map[string]string {
	out := map[string]string{}
	earlier := map[string]Step{}
	for i, level := range levels {
		for j, s := range level {
			if first, ok := earlier[s.DeviceID]; ok {
				out[NodeID(levels, i, j)] = fmt.Sprintf("an earlier step of this rollback (%s) changes %s first", first.Name, s.DeviceName)
			}
		}
		for _, s := range level {
			if _, ok := earlier[s.DeviceID]; !ok {
				earlier[s.DeviceID] = s
			}
		}
	}
	return out
}
