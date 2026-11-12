// Package engine: journaling a task's authored rollback: steps as the run
// rendered them (JournalEntry.AuthoredSteps).
//
// A rollback: step may read what only the run had: a variable a set_fact
// set, a register's result, the device it ran on. A later pleiades rollback
// (or a Controller's rollback job) runs a new runbook with none of that, so
// it cannot render the step again and get what the task meant. So when a
// task changes a device, each of its templated steps is rendered there with
// exactly what the task ran with (the variables undo_vars.go keeps, the
// registered results as they stand at the level's end, the device root), and
// the values of its templated parameters are journaled under the same rules
// a recorded undo's are (projectInverse): only a key the task's own method
// declares recordable for an undo through the step's method
// (sdk.InverseSpec.Record), only a value recordedValue admits, and never one
// holding a secret the run knows. A step that cannot meet them is journaled
// with no value and the reason, which the planner refuses by name.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// AuthoredStepDigest is the digest of one rollback: step as its runbook
// writes it: its method and its parameters as authored, canonical JSON
// (encoding/json sorts map keys). The projection records it and the planner
// computes it again over the runbook it reads, so a step edited since the
// run is never given the values rendered for another.
func AuthoredStepDigest(step Task) string {
	raw, err := json.Marshal(struct {
		FQCN   string         `json:"fqcn"`
		Params map[string]any `json:"params"`
	}{step.FQCN, step.Params})
	if err != nil {
		// A step's params came from YAML, which json can always encode; a
		// digest of nothing matches no step, so the planner refuses it.
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// renderedSteps returns the journal's record of task's templated rollback:
// steps for n, a result of nodeID on one device: nil unless the task changed
// the device and has such a step. emitter is the task's method as
// projectResult resolved it, nil for an engine action.
func (r *run) renderedSteps(nodeID string, task *Task, n NodeResult, emitter *collection.Descriptor) []AuthoredStep {
	if len(task.Rollback) == 0 || n.Undo != nil || !(n.journalChanged || n.Changed) {
		return nil
	}
	var device inventory.InventoryItem
	if n.Device != "" {
		r.itemsMu.Lock()
		device = r.items[n.Device]
		r.itemsMu.Unlock()
	}
	var out []AuthoredStep
	for i := range task.Rollback {
		step := task.Rollback[i]
		if !HasTemplate(step.Params) {
			continue
		}
		out = append(out, r.renderedStep(nodeID, i, step, emitter, device, n.Device))
	}
	return out
}

// renderedStep renders step, the index-th of the task nodeID ran with
// emitter's method, on device, and records it under projectInverse's rules.
func (r *run) renderedStep(nodeID string, index int, step Task, emitter *collection.Descriptor, device inventory.InventoryItem, deviceID string) AuthoredStep {
	inverse := resolveFQCN(step.FQCN)
	rec := AuthoredStep{Index: index, FQCN: inverse.Name, StepDigest: AuthoredStepDigest(step)}
	// The same declaration a recorded undo is held to: an external program's,
	// an undo the emitter never declared, or an unresolved method keeps
	// nothing.
	spec, declared := declaredUndo(emitter, inverse)
	if !declared || inverse.engineAction {
		rec.Withheld = WithheldUndeclared
		return rec
	}
	roots := map[string]any{TemplateRootDevice: deviceRoot(device), hostVarsRoot: deviceVars(device)}
	if kept, ok := r.forward.of(nodeID, deviceID); ok {
		roots[forwardVarsRoot] = kept
	}
	rendered, err := r.renderTask(&step, roots)
	if err != nil {
		rec.Withheld = WithheldRender
		return rec
	}
	secrets := r.secrets.Snapshot()
	var params []InverseParam
	for _, key := range sortedParamKeys(step.Params) {
		if !HasTemplate(step.Params[key]) {
			// A literal is the runbook's, and the digest holds it there.
			continue
		}
		value := rendered.Params[key]
		p, ok := recordedValue(key, value, ListParam(inverse.Name, key))
		if !ok || collection.IsReservedParam(key) || !slices.Contains(spec.Record, key) || !inverse.paramKeys[key] {
			rec.Withheld = WithheldValue
			return rec
		}
		if holdsKnownSecret(p, secrets) {
			rec.Withheld = WithheldSecret
			return rec
		}
		params = append(params, p)
	}
	rec.Params, rec.Complete = params, true
	return rec
}

// holdsKnownSecret reports whether p's text holds any of secrets.
func holdsKnownSecret(p InverseParam, secrets []string) bool {
	var texts []string
	switch {
	case p.Text != nil:
		texts = []string{*p.Text}
	case p.Number != nil:
		texts = []string{p.Number.String()}
	default:
		texts = p.Texts
	}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		for _, t := range texts {
			if strings.Contains(t, s) {
				return true
			}
		}
	}
	return false
}
