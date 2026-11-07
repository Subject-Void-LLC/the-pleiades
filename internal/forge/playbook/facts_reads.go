// Package playbook: a play that reads Ansible's facts reads the native ones.
//
// Ansible gathers facts at the start of a play unless gather_facts is off, and
// a condition or template then reads them by Ansible's names
// (ansible_os_family == "Debian"). The Pleiades stores facts under native
// names with native values (os.family == "debian"), and pkg/ansiblefacts holds
// the mapping. So a play that gathers and reads a fact the mapping knows gets a
// facts.gather task inserted at its start, registered, and each read becomes
// the native field of that register, the compared value written the native
// way. The read is the device's own entry (nodes.<register>[device.id]), so
// a condition decides per host, as Ansible's does. A fact the mapping does not know, or a
// value it cannot write natively, stays a finding for a person, and so does a
// template reading a fact: a template's paths are literal (internal/render),
// so it cannot index the facts by the device it runs on.
package playbook

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"go.yaml.in/yaml/v3"
)

// factsRegisterName is the register the inserted facts.gather task writes,
// numbered when a playbook already uses the name.
const factsRegisterName = "gathered_facts"

// ansibleFactName finds the Ansible fact names a play's text holds.
var ansibleFactName = regexp.MustCompile(`\bansible_[a-z_]+\b`)

// readsMappedFact reports whether any text in node names an Ansible fact the
// mapping knows, so the play needs its facts gathered.
func readsMappedFact(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.ScalarNode {
		for _, name := range ansibleFactName.FindAllString(node.Value, -1) {
			if _, ok := ansiblefacts.NativeKey(name); ok {
				return true
			}
		}
		return false
	}
	for _, child := range node.Content {
		if readsMappedFact(child) {
			return true
		}
	}
	return false
}

// pickFactsRegister picks the register name for a play's inserted gather: the
// plain name, or the first numbered one the playbook does not already use.
func (t *translator) pickFactsRegister() string {
	name := factsRegisterName
	for n := 2; t.registerTaken(name); n++ {
		name = factsRegisterName + "_" + strconv.Itoa(n)
	}
	return name
}

// registerTaken reports whether a playbook task already registers name.
func (t *translator) registerTaken(name string) bool {
	_, converted := t.registers[name]
	return converted || len(t.ix.defs[name]) > 0
}

// gatherTask builds the facts.gather task inserted at the start of a play,
// registered as register.
func (t *translator) gatherTask(register string, at Position, runbook string) *outTask {
	id := t.raise("play.facts_gathered", at, "", fmt.Sprintf("facts.gather is inserted at the start of the play and registered as %s, which the play's fact reads use", register))
	t.emitted++
	task := &outTask{name: "Gather facts", fqcn: "facts.gather", params: map[string]any{}, register: register, at: at, cites: []string{id}}
	task.result = &TaskResult{
		At: at, Name: task.name, Module: "gather_facts", Runbook: runbook, FQCN: task.fqcn, Params: task.params,
		Class: ClassObserve, ClassBasis: "gathering facts reads and changes nothing", Outcome: outcomeOf(t, task.cites), Findings: task.cites,
	}
	return task
}

// factTerm translates a read of the Ansible fact name, whose path must be
// empty, into the device's own native field of the facts register, or
// reports false when the play gathers no facts or the mapping does not know
// the fact.
func (c *celCtx) factTerm(e *expr) (term, bool) {
	native, ok := ansiblefacts.NativeKey(e.name)
	if c.facts == "" || !ok || len(e.path) != 0 {
		return term{}, false
	}
	return term{cel: factRead(c.facts, native), typ: gatherReturnType(native), register: c.facts, field: native, fact: e.name}, true
}

// gatherReturnType is the type facts.gather documents for the native fact,
// "dyn" when it documents none.
func gatherReturnType(native string) string {
	if d, ok := collection.Lookup("facts.gather"); ok {
		for _, r := range d.Manifest.Doc.Returns {
			if r.Name == native {
				return r.Type
			}
		}
	}
	return "dyn"
}

// nativeOperand writes fixed, the value a condition compares the Ansible fact
// fact with, the native way, or refuses the condition when the mapping has
// no one native value for it.
func nativeOperand(fact string, fixed term) (term, *celError) {
	v, ok := ansiblefacts.NativeValue(fact, fixed.value)
	if !ok {
		return term{}, &celError{"when.fact", fmt.Sprintf("Ansible's %v for %s has no one native value; write the condition on the native fact", fixed.value, fact)}
	}
	fixed.value = v
	return fixed, nil
}

// factRead is the read of the native fact native from the facts register,
// for the device the task runs on: nodes.<register>[device.id][...]. Reading
// the device root is what makes the engine decide the condition on each
// device, as Ansible decides per host; a read through the result root would
// hold only a register one device wrote.
func factRead(register, native string) string {
	return "nodes" + celKey(register) + "[device.id]" + celKey(native)
}
