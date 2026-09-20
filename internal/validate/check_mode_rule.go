// Package validate: the rule that refuses a check_mode a run could not honor.
package validate

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// CheckModeRule refuses the two ways a runbook's check_mode key can
// promise something a run would not keep.
//
// First, a task marked check_mode (itself, or through a block or an
// import above it) whose action cannot be checked: the author asked for a
// dry run of a step that has none, and reporting it unchecked at run time
// would be the first anyone heard of it. A runbook-level check_mode is
// exempt, since it makes the whole run a check exactly as `--mode check`
// does, and a check names what it could not check rather than refusing.
//
// Second, in a real run, a task that runs for real and whose condition
// reads the registered result of a task that is only checked. That result
// says what the checked task WOULD have done, and acting on it would
// change a device because of work that never happened. A condition that
// reaches stat or nodes in a way no register name can be read off counts
// as reading every checked task's result. The legitimate need, acting
// only when something has drifted, is met by a read-only method, whose
// real run already only reads.
func CheckModeRule(world WorldView) []Finding {
	var findings []Finding
	label := func(id string, task *engine.Task) string {
		if task.Name != "" {
			return fmt.Sprintf("%s (name %q)", id, task.Name)
		}
		return id
	}

	// checkedRegisters maps each register a task carrying check_mode
	// produces to that task's label. Only the key counts here: a task that
	// is a check only because the run is one (--mode check) was never
	// promised a check by its author, and is named unchecked at run time
	// like any other; rule two does not apply to such a run at all.
	checkedRegisters := map[string]string{}
	for id, task := range world.DAG.Nodes {
		if task.FQCN == "" || !task.CheckMode {
			continue
		}
		if task.Register != "" {
			checkedRegisters[task.Register] = label(id, task)
		}
		if world.DAG.CheckMode {
			continue
		}
		if ok, reason := engine.Checkable(task.FQCN, task.Params); !ok {
			findings = append(findings, Finding{
				RuleName: "check_mode",
				Node:     id,
				Message:  fmt.Sprintf("task %s carries check_mode, but it cannot be checked: %s", label(id, task), reason),
			})
		}
	}

	if engine.TaskMode(world.Mode, world.DAG, nil) == collection.ModeCheck || len(checkedRegisters) == 0 {
		return findings
	}
	for id, task := range world.DAG.Nodes {
		if task.FQCN == "" || engine.TaskMode(world.Mode, world.DAG, task) == collection.ModeCheck {
			continue
		}
		names, dynamic, err := engine.ConditionReads(task.Conditional)
		if err != nil {
			// The builder compiled every condition already, so this cannot
			// happen for a DAG it produced; refusing is still the safe side.
			findings = append(findings, Finding{RuleName: "check_mode", Node: id, Message: fmt.Sprintf("task %s: %v", label(id, task), err)})
			continue
		}
		var predicted []string
		for _, name := range names {
			if from, ok := checkedRegisters[name]; ok {
				predicted = append(predicted, fmt.Sprintf("%q (from %s)", name, from))
			}
		}
		switch {
		case len(predicted) > 0:
			findings = append(findings, Finding{
				RuleName: "check_mode",
				Node:     id,
				Message: fmt.Sprintf("task %s runs for real, but its condition reads the result of a task that is only checked: %s. "+
					"That result is a prediction, and a real change must not be decided by one", label(id, task), strings.Join(predicted, ", ")),
			})
		case dynamic:
			findings = append(findings, Finding{
				RuleName: "check_mode",
				Node:     id,
				Message: fmt.Sprintf("task %s runs for real, and its condition reads stat or nodes in a way no register name can be read from, "+
					"so it may read the result of a task that is only checked; name the register it reads", label(id, task)),
			})
		}
	}
	return findings
}

// init registers CheckModeRule with Validate.
func init() {
	Register(CheckModeRule)
}
