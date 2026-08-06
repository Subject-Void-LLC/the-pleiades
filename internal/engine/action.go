package engine

import (
	"context"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// TargetResolver resolves a task's Params["target"] string into the
// inventory devices it names: an exact device Name match, or every device
// carrying target as a Tag. This is exactly what validate.WorldView.Resolve
// (internal/validate/validate.go) already does; engine declares this port
// rather than importing validate directly, since validate already imports
// engine for *DAG and Task, and the reverse import would be a cycle. The
// composition root (cmd/pleiades/run.go) already builds a
// validate.WorldView to run capability and blast-radius checks before
// execution, and that same value's Resolve method satisfies this
// interface with no adapter code and no duplicated logic (AGENTS.md's DRY
// rule): one Resolve implementation, two consumers.
type TargetResolver interface {
	Resolve(target string) []inventory.InventoryItem
}

// ActionResult is what one fqcn action reports after running once,
// either against one resolved device or, for a controller-side task with
// no target, against none at all (PLAN.md Section 14's Execution
// Contexts).
type ActionResult struct {
	// Changed reports whether this action altered real state, mirroring
	// Ansible's changed/ok distinction and PLAN.md Section 14's
	// Convergence principle: report changed only when something changed.
	Changed bool

	// Stats is this action's optional output, merged into the
	// WorkflowContext under the task's Register name (if set) so a later
	// task's when_cel can reference it. This mirrors
	// sdk.RunbookContext.SetStat at the Collection-author level (pkg/sdk's
	// doc comment) and PLAN.md Section 27's device.state_changed stats
	// payload; Executor is its Walk-tier in-process carrier.
	Stats map[string]interface{}

	// IsMetadata reports whether Stats is runbook-level custom automation
	// statistics (fqcn "set_metadata") meant for a final run report,
	// rather than an ordinary registered result meant only for a later
	// when_cel to read. Executor uses this to decide whether a Register'd
	// result also belongs in RunResult.Metadata; it never inspects
	// Task.FQCN itself to make that decision, keeping Executor fqcn
	// agnostic (it contains no fqcn string literal anywhere else).
	IsMetadata bool
}

// ActionExecutor runs one task's fqcn action. It is the seam Phase W6
// replaces with a real transport-backed implementation (dispatch over
// SSHTransportCapable, once a device is resolved and its capability
// checked); the Walk-tier default (NewBuiltinActionExecutor) has no
// transport at all, so only fqcn values with no real device dependency
// can genuinely run in-process today.
type ActionExecutor interface {
	// Execute runs task once. device is nil for a controller-side task
	// (Params["target"] is empty); otherwise it is one of the devices
	// TargetResolver.Resolve returned for that task's target.
	Execute(ctx context.Context, task *Task, device inventory.InventoryItem) (ActionResult, error)
}

// builtinActionExecutor is the Walk-tier default ActionExecutor.
type builtinActionExecutor struct{}

// NewBuiltinActionExecutor returns the Walk-tier default ActionExecutor.
// It knows exactly two actions, both requiring nothing per
// capability_rule.go's actionCapability map:
//
//   - "noop", the scaffolded sample runbook's own fqcn
//     (internal/inventory/project.go's starterRunbook). It never fails and
//     never claims to have changed anything unless its task explicitly
//     says so: it echoes task.Params verbatim into ActionResult.Stats and
//     reads an optional boolean task.Params["changed"], the same "let a
//     task publish literal authored data for a later condition to read"
//     role Ansible's own debug module plays, and the only way to prove
//     conditional branching end to end (Phase W5's Release Gate) before
//     Phase W6 adds a real transport to produce a real value to branch on.
//   - "set_metadata", also reachable as "pleiades.builtin.set_metadata"
//     (both spellings dispatch identically, indefinitely), mirroring
//     Ansible's set_stats module: requires a non-empty task.Params["data"]
//     map, reported verbatim as ActionResult.Stats with IsMetadata set,
//     for RunResult.Metadata's final run report. Never reports Changed:
//     setting metadata never alters device state. Deliberately does not
//     implement set_stats' aggregate/per_host flags (overwrite semantics
//     only): nothing asked for them, and top-level keys only, matching
//     every ActionResult.Stats shape this codebase produces today. The
//     dotted spelling exists so a runbook can namespace every genuinely
//     native (non-Ansible-ported) fqcn under "pleiades.builtin.", the same
//     namespace internal/validate/collection_rule.go's exemption list
//     names; see that file's doc comment for why this one builtin stays a
//     hardcoded switch case rather than a real pkg/collection method.
//
// Every other fqcn returns an explicit "not implemented" error rather than
// a fake success, the same honesty rule the Forge catalog's stub decision
// already established for an unimplemented Collection method
// (HANDOFF_DOCUMENT.md's Forge of Hephaestus session, decision 4): a stub
// must say so, not pretend.
func NewBuiltinActionExecutor() ActionExecutor {
	return builtinActionExecutor{}
}

func (builtinActionExecutor) Execute(_ context.Context, task *Task, _ inventory.InventoryItem) (ActionResult, error) {
	switch task.FQCN {
	case "noop":
		result := ActionResult{}
		if changed, ok := task.Params["changed"].(bool); ok {
			result.Changed = changed
		}
		if len(task.Params) > 0 {
			result.Stats = task.Params
		}
		return result, nil
	case "set_metadata", "pleiades.builtin.set_metadata":
		data, ok := task.Params["data"].(map[string]interface{})
		if !ok || len(data) == 0 {
			return ActionResult{}, fmt.Errorf("fqcn %q requires a non-empty params.data map", task.FQCN)
		}
		return ActionResult{Stats: data, IsMetadata: true}, nil
	default:
		return ActionResult{}, fmt.Errorf("fqcn %q has no in-process implementation yet (Phase W6 adds a real transport)", task.FQCN)
	}
}
