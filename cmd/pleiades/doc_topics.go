// The doc topics that are not a method or a device type: `pleiades doc
// filters`, every filter a task parameter's {{ }} template may use, and
// `pleiades doc conditions`, what a when, when_or or when_cel condition may
// read and call. The filters and functions come from the engine itself; the
// lists of roots below are written here, and a test holds them to the
// engine's.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// The topic names doc takes in place of a method.
const (
	docTopicFilters    = "filters"
	docTopicConditions = "conditions"
)

// templateRoots are the names a task parameter's template may read, each of
// engine.TemplateRoots (TestDocTopics holds the two equal).
var templateRoots = []docRoot{
	{"device", "the device the task runs on: device.name, device.id, device.tags, device.capabilities, device.state and device.properties.<key> (only the keys its type reads, such as host, and never a secret); a task reading it renders once per device, and a target may not read it"},
	{"vars", "the run's variables (pleiades run -e key=value, a launch's extra variables); never a secret"},
	{"result", "result.<register>: the result an earlier task registered, when one device (or no device) wrote it"},
	{"nodes", "nodes.<register>[\"<device id>\"]: an earlier task's registered result on each device, by the device's inventory id"},
	{"error", "in a rescue task only: the failure it answers (error.message, error.task, error.name, error.method, error.device, error.unreached)"},
	{"secret", "secret.<name>.<field>: a named secret (pleiades add-secret), only in a parameter whose method resolves one; never rendered into the runbook or its output"},
}

// conditionRoots are the names a condition may read.
var conditionRoots = []docRoot{
	{"device", "the device the task decides on, as in templates: 'hq' in device.tags runs the task only on the devices tagged hq"},
	{"result", "result.<register>: an earlier task's registered result, as in templates"},
	{"nodes", "nodes.<register>[\"<device id>\"]: each device's registered result"},
	{"vars", "the run's variables"},
	{"stat", "the same registered results as nodes, kept for older runbooks"},
	{"error", "in a rescue task only: the failure it answers (error.message, error.task, error.name, error.method, error.device, error.unreached)"},
}

// docRoot is one name a template or condition reads, and what it holds.
type docRoot struct {
	Name  string `json:"name"`
	Holds string `json:"holds"`
}

// filtersTopic is `pleiades doc filters --json`'s model.
type filtersTopic struct {
	Roots    []docRoot               `json:"roots"`
	Filters  []render.FilterDoc      `json:"filters"`
	Excluded []render.ExcludedFilter `json:"excluded"`
}

// printTemplateFilters is `pleiades doc filters`.
func printTemplateFilters(asJSON bool) error {
	topic := filtersTopic{Roots: templateRoots, Filters: render.Filters(), Excluded: render.ExcludedFilters()}
	if asJSON {
		return writeJSON(os.Stdout, topic)
	}
	fmt.Println("A string in a task's params holding {{ }} is a template, rendered when the task runs. It reads:")
	for _, r := range topic.Roots {
		fmt.Printf("  %-8s %s\n", r.Name, r.Holds)
	}
	fmt.Println("\nA name nobody supplied is an error, never empty text. Its filters, the whole closed set:")
	for _, f := range topic.Filters {
		fmt.Printf("  %-36s %s\n", f.Usage, f.Description)
	}
	fmt.Println("\nLeft out on purpose:")
	for _, e := range topic.Excluded {
		fmt.Printf("  %-14s %s\n", e.Name, e.Why)
	}
	fmt.Println("\nThere are no {% %} statements, no loops and no arithmetic. A condition (when:) is CEL, not a template: pleiades doc conditions.")
	return nil
}

// conditionsTopic is `pleiades doc conditions --json`'s model.
type conditionsTopic struct {
	Forms     []docRoot                 `json:"forms"`
	Roots     []docRoot                 `json:"roots"`
	Libraries []engine.ConditionLibrary `json:"libraries"`
}

// conditionForms are the three ways a task is gated.
var conditionForms = []docRoot{
	{"when", "a list of CEL expressions, all of which must be true (Ansible's when)"},
	{"when_or", "a list of CEL expressions, any one of which must be true"},
	{"when_cel", "one CEL expression"},
}

// printConditions is `pleiades doc conditions`.
func printConditions(asJSON bool) error {
	libs, err := engine.ConditionLibraries()
	if err != nil {
		return err
	}
	topic := conditionsTopic{Forms: conditionForms, Roots: conditionRoots, Libraries: libs}
	if asJSON {
		return writeJSON(os.Stdout, topic)
	}
	fmt.Println("A task runs on a device only when its condition holds. The forms:")
	for _, f := range topic.Forms {
		fmt.Printf("  %-9s %s\n", f.Name, f.Holds)
	}
	fmt.Println("\nA condition is CEL (==, !=, <, &&, ||, !, in, has(), size(), x.exists(e, p), x.all(e, p)), never a {{ }} template. It reads:")
	for _, r := range topic.Roots {
		fmt.Printf("  %-8s %s\n", r.Name, r.Holds)
	}
	fmt.Println("\nBeyond CEL's own operators and macros, these functions:")
	for _, lib := range topic.Libraries {
		fmt.Printf("\n  %s\n", lib.Name)
		for _, fn := range lib.Functions {
			for _, o := range fn.Overloads {
				switch {
				case fn.Macro && len(o.Examples) > 0:
					fmt.Printf("    %s (macro), e.g. %s\n", fn.Name, strings.ReplaceAll(o.Examples[0], "\n", " "))
				case fn.Macro:
					fmt.Printf("    %s (macro)\n", fn.Name)
				default:
					fmt.Printf("    %s\n", o.Signature)
				}
			}
		}
	}
	fmt.Println("\nExample: when: [\"result.wan.cleartext == 0\", \"vars.site in ['br1', 'br2']\"]")
	return nil
}
