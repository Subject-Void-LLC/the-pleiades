// Package engine: which registered results a task's condition reads,
// worked out from the parsed CEL rather than by matching text.
package engine

import (
	"fmt"
	"sort"
	"sync"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/types"
)

// registerVariables are the CEL variables a condition reaches registered
// results through, each keyed by register name: stat and nodes are the same
// WorkflowContext tree (runNode), and result holds the registers one device
// wrote (singleWriters).
var registerVariables = map[string]bool{"stat": true, "nodes": true, "result": true}

// parseEnv is the CEL environment a condition is parsed in to read what it
// reads. It is built from the very options the compile environment is
// (conditionEnvOptions), never a list of its own, so it accepts exactly the
// syntax a condition compiles with: a parser built with fewer options once
// refused every .? and [?] condition, and callers took that refusal for "reads
// nothing" (FAILURE_PATTERNS 1351). One environment serves every call, the
// facts root's reader included.
var parseEnv = sync.OnceValues(func() (*cel.Env, error) { return cel.NewEnv(conditionEnvOptions()...) })

// ParseCondition parses one CEL source of a condition (an item of Sources)
// with exactly the syntax it compiles with, and returns its syntax tree, for
// a reader outside the package that works out what a condition reads. It
// never type-checks or evaluates.
func ParseCondition(src string) (celast.Expr, error) {
	env, err := parseEnv()
	if err != nil {
		return nil, err
	}
	ast, iss := env.Parse(src)
	if iss.Err() != nil {
		return nil, fmt.Errorf("condition %q does not parse: %w", src, iss.Err())
	}
	return ast.NativeRep().Expr(), nil
}

// ConditionReads returns the register names c's condition reads, sorted:
// every name it reaches as stat.<name> or nodes.<name>, or by indexing
// either with a literal string. dynamic reports that it also reaches stat
// or nodes some other way (an index computed while the run goes, the
// whole map handed to a macro or a function), so it may read any
// registered result and a caller must assume it does.
//
// It parses and never evaluates, so it works before a run, which is what
// validation needs to refuse a real task that acts on a checked task's
// predicted result.
func ConditionReads(c Conditional) (names []string, dynamic bool, err error) {
	seen := map[string]bool{}
	for _, src := range c.Sources() {
		expr, err := ParseCondition(src)
		if err != nil {
			return nil, false, err
		}
		d := collectReads(expr, seen)
		dynamic = dynamic || d
	}
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, dynamic, nil
}

// collectReads adds to seen every register name root reads by a name that
// can be read off the expression, and reports whether stat or nodes also
// appears anywhere else. It counts both: every appearance of the variable
// that is a named read, and every appearance at all. Any appearance
// beyond the named reads is a use no name can be read off.
func collectReads(root celast.Expr, seen map[string]bool) (dynamic bool) {
	appearances, named := 0, 0
	isRegisterVar := func(e celast.Expr) bool {
		return e.Kind() == celast.IdentKind && registerVariables[e.AsIdent()]
	}
	celast.PreOrderVisit(root, celast.NewExprVisitor(func(e celast.Expr) {
		switch e.Kind() {
		case celast.IdentKind:
			if registerVariables[e.AsIdent()] {
				appearances++
			}
		case celast.SelectKind:
			if sel := e.AsSelect(); isRegisterVar(sel.Operand()) {
				seen[sel.FieldName()] = true
				named++
			}
		case celast.CallKind:
			call := e.AsCall()
			switch call.FunctionName() {
			// stat["name"], and the optional stat[?"name"] and stat.?name,
			// which the parser writes as a call with the name a literal.
			case operators.Index, operators.OptIndex, operators.OptSelect:
				args := call.Args()
				if len(args) == 2 && isRegisterVar(args[0]) && args[1].Kind() == celast.LiteralKind {
					if key, ok := args[1].AsLiteral().(types.String); ok {
						seen[string(key)] = true
						named++
					}
				}
			case operators.In:
				// "name" in stat, the converter's spelling of Ansible's
				// is defined, names the register it tests.
				if key, ok := inLiteral(call); ok {
					seen[key] = true
					named++
				}
			}
		}
	}))
	return appearances > named
}

// ConditionUsesRoot reports whether any of c's expressions names the CEL
// variable root at all, which validation reads to refuse error outside a
// rescue: task.
func ConditionUsesRoot(c Conditional, root string) (bool, error) {
	found := false
	err := eachConditionExpr(c, func(e celast.Expr) {
		if e.Kind() == celast.IdentKind && e.AsIdent() == root {
			found = true
		}
	})
	return found, err
}

// ConditionTestsPresence returns the register names c tests for with
// has(stat.<name>), has(nodes.<name>) or has(result.<name>), with "<name>" in
// stat, nodes or result, or reads through the optional field or index
// (result.?<name>, result[?"<name>"]), which yields an empty optional rather
// than failing when the register is absent: a condition that does is written
// for the register's absence, and validation lets it read a register that may
// not exist.
func ConditionTestsPresence(c Conditional) (map[string]bool, error) {
	tested := map[string]bool{}
	err := eachConditionExpr(c, func(e celast.Expr) {
		if e.Kind() == celast.CallKind {
			call := e.AsCall()
			switch call.FunctionName() {
			case operators.In:
				if key, ok := inLiteral(call); ok {
					tested[key] = true
				}
			case operators.OptSelect, operators.OptIndex:
				if key, ok := optionalLiteral(call); ok {
					tested[key] = true
				}
			}
			return
		}
		if e.Kind() != celast.SelectKind {
			return
		}
		sel := e.AsSelect()
		if op := sel.Operand(); sel.IsTestOnly() && op.Kind() == celast.IdentKind && registerVariables[op.AsIdent()] {
			tested[sel.FieldName()] = true
		}
	})
	return tested, err
}

// inLiteral returns the register a call of the in operator tests for, when
// it is "name" in stat, nodes or result with a literal name.
func inLiteral(call celast.CallExpr) (string, bool) {
	args := call.Args()
	if len(args) != 2 || args[0].Kind() != celast.LiteralKind || args[1].Kind() != celast.IdentKind || !registerVariables[args[1].AsIdent()] {
		return "", false
	}
	key, ok := args[0].AsLiteral().(types.String)
	return string(key), ok
}

// optionalLiteral returns the register an optional field or index reads,
// when it is stat, nodes or result with a literal name: result.?name or
// result[?"name"].
func optionalLiteral(call celast.CallExpr) (string, bool) {
	args := call.Args()
	if len(args) != 2 || args[0].Kind() != celast.IdentKind || !registerVariables[args[0].AsIdent()] || args[1].Kind() != celast.LiteralKind {
		return "", false
	}
	key, ok := args[1].AsLiteral().(types.String)
	return string(key), ok
}

// eachConditionExpr parses each of c's expressions and visits every node of
// each.
func eachConditionExpr(c Conditional, visit func(celast.Expr)) error {
	for _, src := range c.Sources() {
		expr, err := ParseCondition(src)
		if err != nil {
			return err
		}
		celast.PreOrderVisit(expr, celast.NewExprVisitor(visit))
	}
	return nil
}
