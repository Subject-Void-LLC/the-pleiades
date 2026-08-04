package validate

import "fmt"

// SecretMaskRule checks that every task's secret_mask.register names a
// register some task in the DAG actually uses, catching an obvious typo
// before it silently masks nothing at runtime.
//
// This is deliberately existence-only, not ordering-aware: world.DAG.Nodes
// is an unordered map, and engine.TopologicalOrder/LevelIterator exclude
// Rescue and Always descendants from any total order entirely, so "is this
// register produced by a strictly earlier task" is undefined for a large,
// legitimate class of runbooks (a Rescue task referencing a register its
// own guarded Block produced). This is the same fidelity this codebase
// already accepts for the analogous when_cel/stat.<register> reference,
// which has no static check at all today. A reference that passes this
// check but is not actually populated by the time its secret_mask task
// runs (registered by a task later in execution order, or one that was
// skipped) is still a hard runtime error, never a silent no-op: see
// internal/engine's applySecretMask.
func SecretMaskRule(world WorldView) []Finding {
	registered := make(map[string]struct{})
	for _, task := range world.DAG.Nodes {
		if task.Register != "" {
			registered[task.Register] = struct{}{}
		}
	}

	var findings []Finding
	for id, task := range world.DAG.Nodes {
		if task.SecretMask == nil {
			continue
		}
		if _, ok := registered[task.SecretMask.Register]; ok {
			continue
		}

		label := id
		if task.Name != "" {
			label = fmt.Sprintf("%s (name %q)", id, task.Name)
		}
		findings = append(findings, Finding{
			RuleName: "secret_mask",
			Node:     id,
			Message:  fmt.Sprintf("secret_mask.register %q (task %s) is not registered by any task in this runbook", task.SecretMask.Register, label),
		})
	}

	return findings
}

func init() {
	Register(SecretMaskRule)
}
