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

// ansibleFactName finds the Ansible fact names a condition holds, written
// ansible_<name> or through ansible_facts (ansible_facts.os_family,
// ansible_facts['os_family']); the second group is the name after
// ansible_facts.
var ansibleFactName = regexp.MustCompile(`\bansible_facts(?:\.|\[\s*['"])([a-z_]+)|\bansible_[a-z_]+\b`)

// conditionKeys are the task keys whose text is a condition, the only reads
// this converter turns into native ones; a task's name, its params and its
// templates do not decide whether a play gathers.
var conditionKeys = map[string]bool{"when": true, "changed_when": true, "failed_when": true, "until": true}

// readsMappedFact reports whether a condition anywhere in node (a play, its
// blocks and tasks) reads an Ansible fact the mapping knows, so the play
// needs its facts gathered.
func readsMappedFact(node *yaml.Node) bool {
	node = deref(node)
	if node == nil {
		return false
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			value := node.Content[i+1]
			if conditionKeys[node.Content[i].Value] {
				if conditionReadsMappedFact(value) {
					return true
				}
				continue
			}
			if readsMappedFact(value) {
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

// conditionReadsMappedFact reports whether a condition's text (one string or
// a list of them) names a fact the mapping knows.
func conditionReadsMappedFact(node *yaml.Node) bool {
	node = deref(node)
	if node == nil {
		return false
	}
	if node.Kind != yaml.ScalarNode {
		for _, child := range node.Content {
			if conditionReadsMappedFact(child) {
				return true
			}
		}
		return false
	}
	for _, m := range ansibleFactName.FindAllStringSubmatch(node.Value, -1) {
		name := m[0]
		if m[1] != "" {
			name = "ansible_" + m[1]
		}
		if _, ok := ansiblefacts.NativeKey(name); ok {
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
	// Claimed, so a later play merged into the same runbook picks the next
	// free name rather than registering this one twice.
	t.registers[register] = produced{fqcn: "facts.gather"}
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
	name, path := e.name, e.path
	// ansible_facts.os_family and ansible_facts['os_family'] are Ansible's
	// newer spelling of ansible_os_family.
	if key, ok := firstKey(path); name == "ansible_facts" && ok {
		name, path = "ansible_"+key, path[1:]
	}
	native, ok := ansiblefacts.NativeKey(name)
	if c.facts == "" || !ok || len(path) != 0 {
		return term{}, false
	}
	return term{cel: factRead(c.facts, native), typ: gatherReturnType(native), register: c.facts, field: native, fact: name}, true
}

// firstKey is path's first step when it is a name.
func firstKey(path []any) (string, bool) {
	if len(path) == 0 {
		return "", false
	}
	key, ok := path[0].(string)
	return key, ok
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

// majorVersionSemantics refuses a comparison of the major version whose
// meaning the native integer would change. Ansible's major version is a
// string, so without | int an ordering compares text ("10" < "8") and a
// number never equals it ("12" == 12 is false); only equality and membership
// against strings, or a read through | int, mean the same over the integer.
func majorVersionSemantics(open, fixed term, op string) *celError {
	if open.fact != "ansible_distribution_major_version" || open.intFiltered {
		return nil
	}
	ordering := op != "==" && op != "!=" && op != "in" && op != "not in"
	if ordering || !allStrings(fixed.value) {
		return &celError{"when.fact", "Ansible compares ansible_distribution_major_version as a string here, which the native integer would not; add | int, or write the condition on os.version_major"}
	}
	return nil
}

// allStrings reports whether v is a string or a list of strings only.
func allStrings(v any) bool {
	switch x := v.(type) {
	case string:
		return true
	case []any:
		for _, item := range x {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	}
	return false
}

// guardFact makes cel, a comparison over the fact read open, hold where the
// device has no such fact the way Ansible's comparison with its placeholder
// (NA, "") does: an absent fact is not equal to and not in anything, so ==,
// in and the orderings are false and != and not in are true. Without the
// guard the read of a missing key would fail the task.
func guardFact(open term, cel, op string) string {
	present := strconvQuote(open.field) + " in nodes" + celKey(open.register) + "[device.id]"
	if op == "!=" || op == "not in" {
		return "(!(" + present + ") || (" + cel + "))"
	}
	return "(" + present + " && (" + cel + "))"
}

// strconvQuote quotes a key for CEL.
func strconvQuote(s string) string { return strconv.Quote(s) }
