// ResidencyRule: the plan-time check that a use tagged local sends nothing
// to a hosted language model provider.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

func init() { Register(ResidencyRule) }

// ResidencyRule refuses, before anything runs, a model call that would
// take a local use's data to a provider whose record says hosted. The
// methods refuse it again at run time, before contact; this is the same
// rule earlier, where a mistake costs nothing:
//
//   - an ai task whose residency is literally local, against every device
//     it targets that is a provider;
//   - ai.route's literal routing table: every use's providers, whichever
//     use the task routes, since a table is the project's data and a
//     local use naming a hosted provider is wrong wherever it is used.
//     A provider the table names must be an ai_provider in the inventory.
//
// A value that holds a template is left to run time, which checks it once
// it renders.
func ResidencyRule(world WorldView) []Finding {
	ids := make([]string, 0, len(world.DAG.Nodes))
	for id := range world.DAG.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	byName := map[string]inventory.InventoryItem{}
	for _, item := range world.Items {
		byName[item.Name()] = item
	}
	var findings []Finding
	add := func(id string, task *engine.Task, format string, a ...any) {
		findings = append(findings, Finding{RuleName: "residency", Node: id, Message: fmt.Sprintf("task %s: ", taskName(id, task)) + fmt.Sprintf(format, a...)})
	}
	for _, id := range ids {
		task := world.DAG.Nodes[id]
		if !strings.HasPrefix(task.FQCN, "ai.") {
			continue
		}
		if tag, ok := task.Params[aitask.ParamResidency].(string); ok && !engine.HasTemplate(tag) {
			_, devices := world.taskDevices(task)
			for _, d := range devices {
				if p, isProvider := d.(capability.AIProviderCapable); isProvider {
					if err := aitask.CheckResidency(tag, d.Name(), p.AIResidency()); err != nil {
						add(id, task, "%v", err)
					}
				}
			}
		}
		raw, ok := task.Params[aitask.ParamRoutes]
		if !ok || engine.HasTemplate(raw) {
			continue
		}
		uses, err := aitask.ParseRoutes(raw)
		if err != nil {
			add(id, task, "%v", err)
			continue
		}
		names := make([]string, 0, len(uses))
		for n := range uses {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			for _, step := range uses[n].Steps {
				item, found := byName[step.Provider]
				p, isProvider := item.(capability.AIProviderCapable)
				switch {
				case !found:
					add(id, task, "use %s names provider %s, which is not in the inventory", n, step.Provider)
				case !isProvider:
					add(id, task, "use %s names %s, which is not an ai_provider", n, step.Provider)
				default:
					if err := aitask.CheckResidency(uses[n].Residency, step.Provider, p.AIResidency()); err != nil {
						add(id, task, "use %s: %v", n, err)
					}
				}
			}
		}
	}
	return findings
}
