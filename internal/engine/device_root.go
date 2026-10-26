// Package engine: the device root, the device a task runs on as a template
// and a condition read it ({{ device.name }}, device.properties.site ==
// 'hq'). A task that reads it is decided and rendered once on each of its
// devices, as a rescue task already is, rather than once for all of them.
package engine

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// deviceRoot is what the device root holds for d: its name, inventory id,
// tags, capabilities, lifecycle state and properties. Only a property that
// is a single text, number or boolean and whose name names no secret
// (redact.SecretName) is there: a template's output can reach a task's
// output and a device's command line, so what it may read is allowed by
// what it is, never everything not known to be secret. The reserved
// discovery and grant maps are not. A task with no device reads an empty
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
	for k, v := range d.Properties().Raw() {
		if inventory.IsReservedProperty(k) || redact.SecretName(k) {
			continue
		}
		switch v.(type) {
		case string, bool, int, int64, float64:
			properties[k] = v
		}
	}
	return map[string]any{
		"name": d.Name(), "id": string(d.ID()), "tags": tags, "properties": properties,
		"capabilities": capabilities, "state": d.State().String(),
	}
}

// deviceInCondition finds the device root in a condition: the identifier
// device, not a field of something else (error.device).
var deviceInCondition = regexp.MustCompile(`(^|[^.\w])device\b`)

// readsDevice reports whether task (node nodeID) reads the device root, in
// its condition or in a parameter's template.
func (r *run) readsDevice(nodeID string, task *Task) bool {
	if r.dag.Conditions[nodeID] != nil {
		for _, expr := range append(append([]string{task.WhenCEL}, task.When...), task.WhenOr...) {
			if deviceInCondition.MatchString(expr) {
				return true
			}
		}
	}
	if r.x.renderer == nil || !HasTemplate(task.Params) {
		return false
	}
	for _, source := range TemplateStrings(task.Params) {
		if tmpl, err := r.x.renderer.Compile(source); err == nil && readsRoot(tmpl, TemplateRootDevice) {
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
// root, since the target is what chooses the device.
func TargetTemplateProblems(tmpl render.Template) []string {
	if readsRoot(tmpl, TemplateRootDevice) {
		return []string{"a target cannot read device: the target is what chooses the device"}
	}
	return nil
}

// runPerDevice runs task nodeID once on each device its authored target
// resolves to, each decided and rendered with that device as its device
// root, or once with an empty device root when it resolves to none.
func (r *run) runPerDevice(ctx context.Context, nodeID string, task *Task, started time.Time) []NodeResult {
	// Its devices come from its authored target, so a target it renders is
	// held to its rules (never device) before anything resolves.
	if r.x.renderer != nil {
		if err := checkTaskTemplates(r.x.renderer, task); err != nil {
			wrapped := fmt.Errorf("failed to render the parameters of %s: %w", taskLabel(nodeID, task), err)
			r.publish(nodeID, task, "", "failed", wrapped.Error())
			return []NodeResult{failedNode(nodeID, started, FailureStageRender, wrapped)}
		}
	}
	devices, err := r.resolveDevices(task)
	if err != nil {
		wrapped := fmt.Errorf("failed to resolve target for %s: %w", taskLabel(nodeID, task), err)
		r.publish(nodeID, task, "", "failed", wrapped.Error())
		return []NodeResult{failedNode(nodeID, started, FailureStageResolveTarget, wrapped)}
	}
	mask := func(s string) string { return redact.Text(r.secrets.Snapshot(), s) }
	if len(devices) == 0 {
		return r.rescueOn(ctx, nodeID, task, nil, started, mask, false)
	}
	var results []NodeResult
	for _, out := range runConcurrently(devices, func(d inventory.InventoryItem) []NodeResult {
		return r.rescueOn(ctx, nodeID, task, d, started, mask, false)
	}) {
		results = append(results, out...)
	}
	return results
}

// errorRoots is the roots map holding a rescue's error root alone, or nil
// when there is none.
func errorRoots(errorRoot map[string]any) map[string]any {
	if errorRoot == nil {
		return nil
	}
	return map[string]any{TemplateRootError: errorRoot}
}
