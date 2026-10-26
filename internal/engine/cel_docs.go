// Package engine: what the condition language offers beyond CEL's own
// operators, read from the real environment rather than written down, so
// tools/gendocs's reference page and `pleiades doc conditions` cannot claim a
// function the engine does not have.
package engine

import (
	"fmt"
	"sort"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common"
)

// ConditionLibrary is what one entry of CELLibraryOptions adds to a
// condition's environment: its functions, then its macros.
type ConditionLibrary struct {
	// Name is the library, as cel-go or this engine names it.
	Name      string
	Functions []ConditionFunction
}

// ConditionFunction is one function or macro a library adds, with only the
// overloads it adds (ext.Network adds overloads to the standard library's
// own string function, and only those are its).
type ConditionFunction struct {
	Name        string
	Description string
	// Macro is set for an AST-rewriting construct such as optMap.
	Macro bool
	// Overloads are a function's signatures, each with its examples; a
	// macro has one, with no signature.
	Overloads []ConditionOverload
}

// ConditionOverload is one signature and its examples.
type ConditionOverload struct {
	Signature string
	Examples  []string
}

// conditionLibraryNames names CELLibraryOptions' entries, in its order.
var conditionLibraryNames = []string{"ext.Network", "ext.Encoders", "cel.OptionalTypes", "filters"}

// ConditionLibraries builds the environment NewCELEvaluator builds, one
// CELLibraryOptions entry at a time from a baseline with none, and returns
// what each entry adds, diffed by overload id (not by name) so an overload
// added to an existing function is found. The order is stable.
func ConditionLibraries() ([]ConditionLibrary, error) {
	opts := CELLibraryOptions()
	if len(opts) != len(conditionLibraryNames) {
		return nil, fmt.Errorf("engine: %d condition libraries and %d names for them", len(opts), len(conditionLibraryNames))
	}
	cur, err := cel.NewEnv(CELVariableOptions()...)
	if err != nil {
		return nil, fmt.Errorf("engine: building the baseline condition environment: %w", err)
	}
	out := make([]ConditionLibrary, 0, len(opts))
	for i, opt := range opts {
		next, err := cur.Extend(opt)
		if err != nil {
			return nil, fmt.Errorf("engine: adding %s to the condition environment: %w", conditionLibraryNames[i], err)
		}
		lib := ConditionLibrary{Name: conditionLibraryNames[i], Functions: addedFunctions(cur, next)}
		lib.Functions = append(lib.Functions, addedMacros(cur, next)...)
		out = append(out, lib)
		cur = next
	}
	return out, nil
}

// addedFunctions returns every function overload in next and not in prev,
// grouped by function name, in name order.
func addedFunctions(prev, next *cel.Env) []ConditionFunction {
	prevFns := prev.Functions()
	nextFns := next.Functions()
	names := make([]string, 0, len(nextFns))
	for name := range nextFns {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []ConditionFunction
	for _, name := range names {
		fn := nextFns[name]
		known := map[string]bool{}
		if p := prevFns[name]; p != nil {
			for _, o := range p.OverloadDecls() {
				known[o.ID()] = true
			}
		}
		doc := fn.Documentation()
		var overloads []ConditionOverload
		for i, o := range fn.OverloadDecls() {
			if known[o.ID()] || i >= len(doc.Children) {
				continue
			}
			child := doc.Children[i]
			var examples []string
			for _, ex := range child.Children {
				examples = append(examples, ex.Description)
			}
			overloads = append(overloads, ConditionOverload{Signature: child.Signature, Examples: examples})
		}
		if len(overloads) == 0 {
			continue
		}
		// ParseDescription, not the raw text: trailing space trimmed on each
		// line and blank lines dropped, as Documentation() itself does.
		out = append(out, ConditionFunction{Name: name, Description: common.ParseDescription(fn.Description()), Overloads: overloads})
	}
	return out
}

// addedMacros returns every macro in next and not in prev, by macro key
// (name, arity and receiver style), in key order. A macro without
// documentation is left out rather than failing the whole list.
func addedMacros(prev, next *cel.Env) []ConditionFunction {
	known := map[string]bool{}
	for _, m := range prev.Macros() {
		known[m.MacroKey()] = true
	}
	var fresh []cel.Macro
	for _, m := range next.Macros() {
		if !known[m.MacroKey()] {
			fresh = append(fresh, m)
		}
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].MacroKey() < fresh[j].MacroKey() })
	var out []ConditionFunction
	for _, m := range fresh {
		documented, ok := m.(common.Documentor)
		if !ok {
			continue
		}
		doc := documented.Documentation()
		var examples []string
		for _, ex := range doc.Children {
			examples = append(examples, ex.Description)
		}
		out = append(out, ConditionFunction{Name: doc.Name, Description: doc.Description, Macro: true, Overloads: []ConditionOverload{{Examples: examples}}})
	}
	return out
}
