// Package playbook: the playbook's variables, and when one of them has a
// value this converter may write into a runbook.
//
// A variable resolves only when the whole playbook defines it exactly
// once, as a literal, and nothing sets it at run time: not a register, a
// set_fact with a condition, an include_vars, a vars_prompt or a loop.
// Scope is deliberately ignored, so a name defined in two plays resolves
// in neither: that costs a person a sentence, where guessing the wrong
// scope would write the wrong value. Extra variables given with -e at run
// time override everything and cannot be seen here, which the report says.
package playbook

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// varDef is one definition of a variable.
type varDef struct {
	at Position
	// value is the literal it was given, or nil when it is set only at
	// run time.
	value *yaml.Node
	// register names the module whose result a register definition holds.
	register string
}

// varIndex is every variable definition in one playbook.
type varIndex struct {
	defs map[string][]varDef
	// unknownSource is set when a source of variables could not be read
	// (a vars file outside the playbook's directory, an include_vars), so
	// no variable can be said to have only one definition.
	unknownSource string
}

// newVarIndex returns an empty index.
func newVarIndex() *varIndex { return &varIndex{defs: map[string][]varDef{}} }

// addLiterals records every key of m, a vars: map, as a literal
// definition.
func (ix *varIndex) addLiterals(m *yaml.Node, file string) {
	entries, _ := mapEntries(m)
	for _, e := range entries {
		ix.defs[e.key] = append(ix.defs[e.key], varDef{at: nodePos(e.keyAt, file), value: e.value})
	}
}

// addRuntime records name as set only at run time.
func (ix *varIndex) addRuntime(name string, at Position) {
	ix.defs[name] = append(ix.defs[name], varDef{at: at})
}

// addRegister records name as a register holding module's result.
func (ix *varIndex) addRegister(name, module string, at Position) {
	ix.defs[name] = append(ix.defs[name], varDef{at: at, register: module})
}

// isMagic reports whether name is one of Ansible's own variables: facts,
// connection variables and the per-host magic names. None has a value
// before the run.
func isMagic(name string) bool {
	switch name {
	case "inventory_hostname", "inventory_hostname_short", "inventory_dir", "inventory_file",
		"hostvars", "groups", "group_names", "play_hosts", "omit", "playbook_dir", "role_path",
		"role_name", "environment", "vars", "lookup", "query", "q":
		return true
	}
	return strings.HasPrefix(name, "ansible_")
}

// resolveError says why a variable has no value here, and which finding
// code that is.
type resolveError struct {
	code Code
	why  string
}

// Error implements error.
func (e *resolveError) Error() string { return e.why }

// literal returns name's one literal definition, or why there is none.
func (ix *varIndex) literal(name string) (varDef, *resolveError) {
	switch defs := ix.defs[name]; {
	case isMagic(name):
		return varDef{}, &resolveError{"template.fact", fmt.Sprintf("%s is a fact or a magic variable, known only on a host at run time", name)}
	case redact.SecretName(name):
		return varDef{}, &resolveError{"template.secret", fmt.Sprintf("%s is named like a secret", name)}
	case ix.unknownSource != "" && len(defs) <= 1:
		return varDef{}, &resolveError{"template.unresolved", fmt.Sprintf("%s may also be set by %s, which this converter cannot read", name, ix.unknownSource)}
	case len(defs) == 0:
		return varDef{}, &resolveError{"template.unresolved", fmt.Sprintf("%s is not defined in this playbook", name)}
	case len(defs) > 1:
		return varDef{}, &resolveError{"template.unresolved", fmt.Sprintf("%s is defined %d times, so which value applies depends on where it is read", name, len(defs))}
	case defs[0].value == nil:
		return varDef{}, &resolveError{"template.unresolved", fmt.Sprintf("%s is set only at run time", name)}
	case isVault(defs[0].value):
		return varDef{}, &resolveError{"vault.value", fmt.Sprintf("%s is vault-encrypted", name)}
	default:
		return defs[0], nil
	}
}

// nodePos is n's position in file.
func nodePos(n *yaml.Node, file string) Position {
	if n == nil {
		return Position{File: file}
	}
	return Position{File: file, Line: n.Line, Column: n.Column}
}
