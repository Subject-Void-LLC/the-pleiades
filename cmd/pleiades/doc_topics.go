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

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
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
	{Name: "device", Holds: "the device the task runs on: device.name, device.id, device.tags, device.capabilities, device.state and device.properties.<key> (only the keys its type reads, such as host, and never a secret); a task reading it renders once per device, and a target may not read it"},
	{Name: "vars", Holds: "the run's variables (pleiades run -e key=value, a launch's extra variables); never a secret"},
	{Name: "result", Holds: "result.<register>: the result an earlier task registered, when one device (or no device) wrote it"},
	{Name: "nodes", Holds: "nodes.<register>[\"<device id>\"]: an earlier task's registered result on each device, by the device's inventory id"},
	{Name: "error", Holds: "in a rescue task only: the failure it answers (error.message, error.task, error.name, error.method, error.device, error.unreached)"},
	{Name: "secret", Holds: "secret.<name>.<field>: a named secret (pleiades add-secret), only in a parameter whose method resolves one; never rendered into the runbook or its output"},
}

// conditionRoots are the names a condition may read.
var conditionRoots = []docRoot{
	{Name: "device", Holds: "the device the task decides on, as in templates: 'hq' in device.tags runs the task only on the devices tagged hq"},
	{Name: "result", Holds: "result.<register>: an earlier task's registered result, as in templates"},
	{Name: "nodes", Holds: "nodes.<register>[\"<device id>\"]: each device's registered result"},
	{Name: "vars", Holds: "the run's variables"},
	{Name: "stat", Holds: "the same registered results as nodes, kept for older runbooks"},
	{Name: "error", Holds: "in a rescue task only: the failure it answers (error.message, error.task, error.name, error.method, error.device, error.unreached)"},
}

// docRoot is one name a template or condition reads, and what it holds.
type docRoot = clireport.DocRoot

// filtersTopic is `pleiades doc filters --json`'s model, in
// internal/clireport so tools/gendocs publishes its schema.
type filtersTopic = clireport.FiltersTopic

// printTemplateFilters is `pleiades doc filters`.
func printTemplateFilters(asJSON bool) error {
	topic := filtersTopic{
		SchemaVersion: clireport.FiltersVersion, Roots: templateRoots, Filters: render.Filters(), Excluded: render.ExcludedFilters(),
		Statements: render.Statements(), Operators: render.Operators(), Tests: render.Tests(), NotSupported: render.NotSupported(),
	}
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
	printSyntax("Statements", topic.Statements)
	printSyntax("Operators, loosest first", topic.Operators)
	printSyntax("Tests, written x is name or x is not name", topic.Tests)
	fmt.Println("\nNot supported:")
	for _, e := range topic.NotSupported {
		fmt.Printf("  %-14s %s\n", e.Name, e.Why)
	}
	fmt.Println("\nA task's condition (when:) is CEL, not a template: pleiades doc conditions.")
	return nil
}

// printSyntax prints one list of statements, operators or tests under its
// heading.
func printSyntax(heading string, docs []render.SyntaxDoc) {
	fmt.Printf("\n%s:\n", heading)
	for _, d := range docs {
		fmt.Printf("  %-36s %s\n", d.Usage, d.Description)
	}
}

// conditionsTopic is `pleiades doc conditions --json`'s model, in
// internal/clireport beside filtersTopic.
type conditionsTopic = clireport.ConditionsTopic

// conditionForms are the three ways a task is gated.
var conditionForms = []docRoot{
	{Name: "when", Holds: "a list of CEL expressions, all of which must be true (Ansible's when)"},
	{Name: "when_or", Holds: "a list of CEL expressions, any one of which must be true"},
	{Name: "when_cel", Holds: "one CEL expression"},
}

// printConditions is `pleiades doc conditions`.
func printConditions(asJSON bool) error {
	libs, err := engine.ConditionLibraries()
	if err != nil {
		return err
	}
	topic := conditionsTopic{SchemaVersion: clireport.ConditionsVersion, Forms: conditionForms, Roots: conditionRoots, Libraries: libs}
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
