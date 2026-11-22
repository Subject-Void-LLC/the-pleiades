// Package engine: the device root, the device a task runs on as a template
// and a condition read it ({{ device.name }}, 'hq' in device.tags). A task
// that reads it is decided and rendered once on each of its devices, as a
// rescue task already is, rather than once for all of them. Which tasks run
// that way, and why, is per_device.go's to say.
package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// deviceRoot is what the device root holds for d: its name, inventory id,
// tags, capabilities, lifecycle state and properties. The properties are an
// allowlist: only the keys d's type declares its accessors read
// (record.DispatchProperties, which internal/archtest holds free of any key
// naming a secret), each a single text, number or boolean, never one whose
// name names a secret (redact.SecretName) or whose text is shaped like one
// (redact.SecretShaped). A property an operator added for themselves is not
// there, whatever its name: a template's output can reach a task's output
// and a device's command line, so what it may read is allowed by what it
// is. Grouping of one's own is a tag. A task with no device reads an empty
// root.
func deviceRoot(d inventory.InventoryItem) map[string]any {
	if d == nil {
		return map[string]any{}
	}
	tags := make([]any, 0, len(d.Tags()))
	for _, t := range d.Tags() {
		tags = append(tags, string(t))
	}
	capabilities := make([]any, 0, len(d.Capabilities()))
	for _, c := range d.Capabilities() {
		capabilities = append(capabilities, string(c))
	}
	properties := map[string]any{}
	if typed, ok := d.(interface{ DeviceType() string }); ok {
		keys, _ := record.DispatchProperties(typed.DeviceType())
		raw := d.Properties().Raw()
		for _, k := range keys {
			v, present := raw[k]
			if !present || inventory.IsReservedProperty(k) || redact.SecretName(k) {
				continue
			}
			switch t := v.(type) {
			case string:
				if !redact.SecretShaped(t) {
					properties[k] = t
				}
			case bool, int, int64, float64:
				properties[k] = t
			}
		}
	}
	return map[string]any{
		"name": d.Name(), "id": string(d.ID()), "tags": tags, "properties": properties,
		"capabilities": capabilities, "state": d.State().String(),
	}
}

// deviceRoots is what an evaluation on the one device d starts from: its
// device root and the variables it brings (deviceVars, the static
// inventory's group_vars and host_vars), which mergedVars reads as their
// own tier. Every path that decides and renders a task on one device builds
// its roots here, so none renders without the device's variables: a block
// with a rescue once built the device root alone, and a task there that
// read a host_vars variable failed to render though the same task at the
// top level did (run 10).
func deviceRoots(d inventory.InventoryItem) map[string]any {
	return map[string]any{TemplateRootDevice: deviceRoot(d), hostVarsRoot: deviceVars(d)}
}

// ParamsReadRoot reports whether any template in params reads root, for the
// executor and validation alike.
func ParamsReadRoot(eng render.Engine, params map[string]any, root string) bool {
	if !HasTemplate(params) {
		return false
	}
	for _, source := range TemplateStrings(params) {
		if tmpl, err := eng.Compile(source); err == nil && readsRoot(tmpl, root) {
			return true
		}
	}
	return false
}

// readsRoot reports whether tmpl has an expression reading root.
func readsRoot(tmpl render.Template, root string) bool {
	for _, e := range tmpl.Expressions() {
		if len(e.Path) > 0 && e.Path[0] == root {
			return true
		}
	}
	return false
}

// TargetTemplateProblems is what a rendered target may not read: the device
// root, since the target is what chooses the device, and a loop's item or
// position, since a target is resolved once for the task, before any item
// exists. task is the task the target belongs to, nil for none.
func TargetTemplateProblems(tmpl render.Template, task *Task) []string {
	var problems []string
	if readsRoot(tmpl, TemplateRootDevice) {
		problems = append(problems, "a target cannot read device: the target is what chooses the device")
	}
	if task != nil {
		for _, root := range task.loopRoots() {
			if readsRoot(tmpl, root) {
				problems = append(problems, fmt.Sprintf("a target cannot read %s: it is resolved once for the whole loop, before any item exists; loop over the devices of hosts: instead, or write one task per target", root))
				break
			}
		}
	}
	return problems
}

// runPerDevice runs task nodeID once on each device its target resolves to,
// each decided and rendered with that device as its device root, or once
// with an empty device root when it resolves to none. errorRoot, when not
// nil, is a unit's failure every device reads as error. cause is why the task
// runs per device (RunsPerDevice), which is the reason it gives when it
// refuses lock_acquisition: all_at_plan_time.
func (r *run) runPerDevice(ctx context.Context, nodeID string, task *Task, started time.Time, errorRoot map[string]any, cause PerDeviceCause) []NodeResult {
	fail := func(stage FailureStage, err error) []NodeResult {
		r.publish(nodeID, task, "", "failed", err.Error())
		return []NodeResult{failedNode(nodeID, started, stage, err)}
	}
	if err := refuseLockAll(nodeID, task, cause); err != nil {
		return fail(FailureStageLockAll, err)
	}
	if r.x.renderer != nil {
		// The target may not read device, and what it does read is held to
		// its rules before anything resolves.
		if err := checkTaskTemplates(r.x.renderer, task); err != nil {
			return fail(FailureStageRender, fmt.Errorf("failed to render the parameters of %s: %w", taskLabel(nodeID, task), err))
		}
	}
	targeted, err := r.renderTarget(task)
	if err != nil {
		return fail(FailureStageRender, fmt.Errorf("failed to render the target of %s: %w", taskLabel(nodeID, task), err))
	}
	devices, err := r.resolveDevices(targeted)
	if err != nil {
		return []NodeResult{r.unresolved(nodeID, task, started, err)}
	}
	on := func(d inventory.InventoryItem) []NodeResult {
		key := ""
		var one []inventory.InventoryItem
		if d != nil {
			key, one = string(d.ID()), []inventory.InventoryItem{d}
		}
		// A handler runs on a device only where it was notified, and taking
		// the notification there is what keeps it to once.
		if task.handler != nil && !r.notified.take(key, task.handler.index) {
			r.publish(nodeID, task, key, "skipped", SkipReasonNotNotified)
			return []NodeResult{finish(NodeResult{
				NodeID: nodeID, Device: key, StartedAt: started, Skipped: true, SkipReason: SkipReasonNotNotified, skipKind: SkipKindNotNotified,
			})}
		}
		roots := deviceRoots(d)
		if errorRoot != nil {
			roots[TemplateRootError] = errorRoot
		}
		return r.runTask(ctx, nodeID, task, started, key, one, roots)
	}
	if len(devices) == 0 {
		return on(nil)
	}
	var results []NodeResult
	for _, out := range runConcurrently(devices, on) {
		results = append(results, out...)
	}
	return results
}

// renderTarget returns task with its target rendered when it is rendered
// from data (within:), so its devices resolve as a run resolves them, and
// task itself otherwise. Nothing but the target is rendered: the rest may
// read device, which is not known until a device is.
func (r *run) renderTarget(task *Task) (*Task, error) {
	target, ok := task.Params[collection.TargetParam]
	if task.Within == "" || !ok || !HasTemplate(target) {
		return task, nil
	}
	if r.x.renderer == nil {
		return nil, fmt.Errorf("this run has no template renderer, so a target holding {{ cannot be rendered")
	}
	ctx, err := r.templateContext()
	if err != nil {
		return nil, err
	}
	rendered, err := renderValue(r.x.renderer, target, ctx)
	if err != nil {
		return nil, err
	}
	out := *task
	out.Params = make(map[string]any, len(task.Params))
	for k, v := range task.Params {
		out.Params[k] = v
	}
	out.Params[collection.TargetParam] = rendered
	return &out, nil
}

// errorRoots is the roots map holding a rescue's error root alone, or nil
// when there is none.
func errorRoots(errorRoot map[string]any) map[string]any {
	if errorRoot == nil {
		return nil
	}
	return map[string]any{TemplateRootError: errorRoot}
}
