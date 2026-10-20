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

// parseEnv is the CEL environment conditions are parsed in. Parsing needs
// no declarations, only the standard macros, so one environment serves
// every call.
var parseEnv = sync.OnceValues(func() (*cel.Env, error) { return cel.NewEnv() })

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
	env, err := parseEnv()
	if err != nil {
		return nil, false, err
	}
	exprs := append(append(append([]string(nil), c.When...), c.WhenOr...), c.WhenCEL)
	seen := map[string]bool{}
	for _, src := range exprs {
		if src == "" {
			continue
		}
		ast, iss := env.Parse(src)
		if iss.Err() != nil {
			return nil, false, fmt.Errorf("condition %q does not parse: %w", src, iss.Err())
		}
		d := collectReads(ast.NativeRep().Expr(), seen)
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
			case operators.Index, operators.OptIndex, operators.OptSelect:
				args := call.Args()
				if len(args) == 2 && isRegisterVar(args[0]) && args[1].Kind() == celast.LiteralKind {
					if key, ok := args[1].AsLiteral().(types.String); ok {
						seen[string(key)] = true
						named++
					}
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
// has(stat.<name>), has(nodes.<name>) or has(result.<name>): a condition that
// does is written for the register's absence, and validation lets it read a
// register that may not exist.
func ConditionTestsPresence(c Conditional) (map[string]bool, error) {
	tested := map[string]bool{}
	err := eachConditionExpr(c, func(e celast.Expr) {
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

// eachConditionExpr parses each of c's expressions and visits every node of
// each.
func eachConditionExpr(c Conditional, visit func(celast.Expr)) error {
	env, err := parseEnv()
	if err != nil {
		return err
	}
	for _, src := range append(append(append([]string(nil), c.When...), c.WhenOr...), c.WhenCEL) {
		if src == "" {
			continue
		}
		ast, iss := env.Parse(src)
		if iss.Err() != nil {
			return fmt.Errorf("condition %q does not parse: %w", src, iss.Err())
		}
		celast.PreOrderVisit(ast.NativeRep().Expr(), celast.NewExprVisitor(visit))
	}
	return nil
}
