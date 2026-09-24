// Package playbook: finding where each task landed in a written runbook.
package playbook

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// locate walks the written runbook's list at prefix beside the output
// tasks it came from, giving each converted or placeholder task its node
// ID (the engine's own scheme, "tasks[1].block[0]") and the line it landed
// on, and giving each finding a task cites the first place it landed.
func (t *translator) locate(seq *yaml.Node, tasks []*outTask, prefix, file string) []*TaskResult {
	seq = deref(seq)
	var out []*TaskResult
	for i, o := range tasks {
		id := fmt.Sprintf("%s[%d]", prefix, i)
		pos := Position{File: file}
		if seq != nil && i < len(seq.Content) {
			pos.Line, pos.Column = seq.Content[i].Line, seq.Content[i].Column
		}
		for _, cite := range o.cites {
			if f := t.finding(cite); f != nil && f.Emitted == nil {
				at := pos
				f.Emitted = &at
			}
		}
		if o.result != nil {
			o.result.NodeID, o.result.Emitted, o.result.Runbook = id, pos, file
			if o.result.Outcome != OutcomeBlocked {
				ok, why := engine.Checkable(o.fqcn, o.params)
				o.result.CanCheck, o.result.CheckReason = &ok, why
			}
			out = append(out, o.result)
		}
		var node *yaml.Node
		if seq != nil && i < len(seq.Content) {
			node = seq.Content[i]
		}
		for _, sub := range []struct {
			key   string
			tasks []*outTask
		}{{"block", o.block}, {"rescue", o.rescue}, {"always", o.always}} {
			out = append(out, t.locate(lookup(node, sub.key), sub.tasks, id+"."+sub.key, file)...)
		}
	}
	return out
}
