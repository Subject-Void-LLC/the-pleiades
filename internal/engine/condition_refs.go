// Package engine: which registered results a task's condition reads,
// worked out from the parsed CEL rather than by matching text.
package engine

import (
	"fmt"
	"sort"
	"sync"

	"github.com/google/cel-go/cel"
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
)

// registerVariables are the CEL variables a condition reaches registered
// results through. Both are bound to the same WorkflowContext tree
// (runNode), keyed by register name.
var registerVariables = map[string]bool{"stat": true, "nodes": true}

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
