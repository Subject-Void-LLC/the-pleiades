// Package validate: the refusal of lock_acquisition: all_at_plan_time on a
// task that runs on each device on its own, asked of the engine's own
// decision.
package validate

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// lockAllFindings refuses lock_acquisition: all_at_plan_time on every task
// the run would send down its per-device path, where the lock cannot be
// taken: the run refuses such a task only when it reaches it, after earlier
// tasks have changed devices.
//
// It holds no list of which tasks those are. It asks engine.RunsPerDevice,
// the function the run chooses the path by, and takes the reason from the
// cause that function names, so a cause the engine adds is refused here
// without a change to this package, in the run's own words. This is the only
// file of the package that names engine.AcquisitionAllAtPlanTime, which a
// test holds true, so a second, hand-written copy of the run's questions
// cannot be added beside it unnoticed.
//
// The devices it hands the engine are those the task's target names in the
// whole inventory (varsDevices), so the answer is definite and the engine's
// "decided at run time" answer, for a caller with no inventory, never comes
// back here. A rollback: step is not asked about: the builder refuses
// lock_acquisition on one.
func lockAllFindings(world WorldView, eng render.Engine) []Finding {
	ids := make([]string, 0, len(world.DAG.Nodes))
	for id, task := range world.DAG.Nodes {
		if task.LockAcquisition == engine.AcquisitionAllAtPlanTime {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var findings []Finding
	for _, id := range ids {
		task := world.DAG.Nodes[id]
		cause, _ := engine.RunsPerDevice(world.DAG, id, task, eng, func() []inventory.InventoryItem { return varsDevices(world, task) })
		if cause == engine.NotPerDevice {
			continue
		}
		findings = append(findings, Finding{RuleName: "template", Node: id, Message: fmt.Sprintf("task %s: %s", taskName(id, task), cause.LockAllReason())})
	}
	return findings
}

// varsDevices is the devices whose variables task could read: those its
// target names in the inventory, or none for a target rendered from data, as
// the run decides (engine's perDevice).
func varsDevices(world WorldView, task *engine.Task) []inventory.InventoryItem {
	target, items := world.taskDevices(task)
	if target == "" || engine.HasTemplate(target) {
		return nil
	}
	return items
}
