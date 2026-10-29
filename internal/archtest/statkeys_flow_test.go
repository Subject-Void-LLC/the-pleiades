// The flow half of the stat-key scan: following a key from the literal
// that names it to the SetStat or EmitFact call that records it, within one
// function and through a package's own recordStats-style helpers.
package archtest

import (
	"go/ast"
	"path/filepath"
)

// recordCall is call's key argument when call is rc.SetStat or rc.EmitFact.
func recordCall(n ast.Node) (*ast.CallExpr, ast.Expr, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) < 1 {
		return nil, nil, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !recordCalls[sel.Sel.Name] {
		return nil, nil, false
	}
	return call, call.Args[0], true
}

// rangeKeyName is the loop variable that carries a key in rs: the key of a
// map range, or the value of a `for _, k := range` over a slice of names.
func rangeKeyName(rs *ast.RangeStmt) []string {
	var names []string
	for _, e := range []ast.Expr{rs.Key, rs.Value} {
		if id, ok := e.(*ast.Ident); ok && id.Name != "_" {
			names = append(names, id.Name)
		}
	}
	return names
}

// recordsRangeVar reports the record calls in body whose key is one of
// names, the loop variables of a range.
func recordsRangeVar(body ast.Node, names []string) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		call, key, ok := recordCall(n)
		if !ok {
			return true
		}
		// The key is a loop variable, or a field of one (a slice of
		// {key, value} structs).
		if sel, ok := key.(*ast.SelectorExpr); ok {
			key = sel.X
		}
		if id, ok := key.(*ast.Ident); ok {
			for _, name := range names {
				if id.Name == name {
					calls = append(calls, call)
				}
			}
		}
		return true
	})
	return calls
}

// recorders finds a package's helpers that range one of their parameters
// into SetStat or EmitFact, by function name and parameter position.
func recorders(files []*ast.File) map[string]int {
	out := map[string]int{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil {
				continue
			}
			params := map[string]int{}
			var i int
			for _, field := range fn.Type.Params.List {
				for _, name := range field.Names {
					params[name.Name] = i
					i++
				}
				if len(field.Names) == 0 {
					i++
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				rs, ok := n.(*ast.RangeStmt)
				if !ok {
					return true
				}
				x, ok := rs.X.(*ast.Ident)
				if !ok {
					return true
				}
				if idx, isParam := params[x.Name]; isParam && len(recordsRangeVar(rs.Body, rangeKeyName(rs))) > 0 {
					out[fn.Name.Name] = idx
				}
				return true
			})
		}
	}
	return out
}

// pkgScan is the state of one package's scan: its resolver, its helpers,
// and the result so far.
type pkgScan struct {
	s       *constScope
	local   map[string]string
	helpers map[string]int
	funcs   map[string]*ast.FuncDecl
	files   map[*ast.FuncDecl]*ast.File
	// producers are package functions whose returned map reaches a record
	// call, found while scanning their callers.
	producers map[string]bool
	out       statScan
}

// scanPackage is every key the non-test code in dir records.
func (s *constScope) scanPackage(dir string) statScan {
	files := s.parse(dir)
	p := &pkgScan{s: s, local: s.consts(dir), helpers: recorders(files), funcs: map[string]*ast.FuncDecl{}, files: map[*ast.FuncDecl]*ast.File{}, producers: map[string]bool{}, out: statScan{keys: map[string]map[string]bool{}}}
	for _, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				// A method is filed under "." and its name, so a producer
				// named through a value (after.view()) is found too; two
				// types' methods of one name are rare enough here to share.
				if fn.Recv == nil {
					p.funcs[fn.Name.Name] = fn
				} else {
					p.funcs["."+fn.Name.Name] = fn
				}
				p.files[fn] = f
			}
		}
	}
	for fn := range p.files {
		p.scanFunc(fn, map[string]bool{})
	}
	// A producer's returned map is scanned as if ranged into SetStat, and
	// producing it may name further producers, so this runs to a fixpoint.
	done := map[string]bool{}
	for {
		var next []string
		for name := range p.producers {
			if !done[name] {
				next = append(next, name)
			}
		}
		if len(next) == 0 {
			break
		}
		for _, name := range next {
			done[name] = true
			fn, ok := p.funcs[name]
			if !ok {
				continue
			}
			returned := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if _, isLit := n.(*ast.FuncLit); isLit {
					return false
				}
				ret, ok := n.(*ast.ReturnStmt)
				if !ok {
					return true
				}
				for _, r := range ret.Results {
					switch v := r.(type) {
					case *ast.CompositeLit:
						p.addLiteral(fn, v)
					case *ast.Ident:
						returned[v.Name] = true
					case *ast.CallExpr:
						p.markProducer(v)
					}
				}
				return true
			})
			p.collectFlowing(fn, returned)
		}
	}
	return p.out
}

// add records expr's value as a key fn's file records, when it resolves.
func (p *pkgScan) add(fn *ast.FuncDecl, expr ast.Expr) bool {
	f := p.files[fn]
	v, ok := p.s.resolve(expr, p.local, p.s.fileImports(f))
	if ok {
		if p.out.keys[v] == nil {
			p.out.keys[v] = map[string]bool{}
		}
		p.out.keys[v][filepath.Base(p.s.fileset.Position(f.Pos()).Filename)] = true
	}
	return ok
}

// addLiteral records a map literal's keys, a slice literal's elements, or
// the key field of each element of a slice of {key, value} structs.
func (p *pkgScan) addLiteral(fn *ast.FuncDecl, lit *ast.CompositeLit) {
	for _, elt := range lit.Elts {
		switch e := elt.(type) {
		case *ast.KeyValueExpr:
			p.add(fn, e.Key)
		case *ast.CompositeLit:
			p.addStructKey(fn, e)
		default:
			p.add(fn, elt)
		}
	}
}

// addStructKey records the key of one {key, value} struct literal: its
// field named key, or its first field when the literal is positional.
func (p *pkgScan) addStructKey(fn *ast.FuncDecl, lit *ast.CompositeLit) {
	for i, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "key" {
				p.add(fn, kv.Value)
			}
			continue
		}
		if i == 0 {
			p.add(fn, elt)
		}
	}
}

// markProducer notes a call to a package function whose result reaches a
// record call, and reports whether it was one.
func (p *pkgScan) markProducer(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	var name string
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		name = fun.Name
	case *ast.SelectorExpr:
		name = "." + fun.Sel.Name
	default:
		return false
	}
	if _, isFunc := p.funcs[name]; !isFunc {
		return false
	}
	p.producers[name] = true
	return true
}

// scanFunc finds the keys fn records directly, through its loops and
// through the package's recordStats-style helpers.
func (p *pkgScan) scanFunc(fn *ast.FuncDecl, flowing map[string]bool) {
	explained := map[*ast.CallExpr]bool{}
	// Loops first, so a record call keyed by a loop variable is credited
	// to the loop's source rather than counted as dynamic.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.RangeStmt:
			calls := recordsRangeVar(node.Body, rangeKeyName(node))
			if len(calls) == 0 {
				return true
			}
			for _, c := range calls {
				explained[c] = true
			}
			switch x := node.X.(type) {
			case *ast.CompositeLit:
				p.addLiteral(fn, x)
			case *ast.Ident:
				if _, isHelper := p.helpers[fn.Name.Name]; !isHelper {
					flowing[x.Name] = true
				}
			default:
				if !p.markProducer(x) {
					p.out.dynamicAt = append(p.out.dynamicAt, p.s.fileset.Position(node.Pos()).String())
				}
			}
		case *ast.CallExpr:
			id, ok := node.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			idx, isHelper := p.helpers[id.Name]
			if !isHelper || idx >= len(node.Args) {
				return true
			}
			switch arg := node.Args[idx].(type) {
			case *ast.CompositeLit:
				p.addLiteral(fn, arg)
			case *ast.Ident:
				flowing[arg.Name] = true
			default:
				p.markProducer(arg)
			}
		}
		return true
	})
	// Then every record call nothing above explained.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, key, ok := recordCall(n)
		if ok && !explained[call] && !p.add(fn, key) {
			p.out.dynamicAt = append(p.out.dynamicAt, p.s.fileset.Position(call.Pos()).String())
		}
		return true
	})
	p.collectFlowing(fn, flowing)
}

// collectFlowing records the literals, constant indexes and producer calls
// that build a map named in flowing, which reaches a record call.
func (p *pkgScan) collectFlowing(fn *ast.FuncDecl, flowing map[string]bool) {
	if len(flowing) == 0 {
		return
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				switch l := lhs.(type) {
				case *ast.Ident:
					if !flowing[l.Name] || i >= len(node.Rhs) {
						continue
					}
					if lit, ok := rhsLiteral(node.Rhs, i); ok {
						p.addLiteral(fn, lit)
					} else if !p.addAppended(fn, node.Rhs[i]) {
						p.markProducer(node.Rhs[i])
					}
				case *ast.IndexExpr:
					if id, ok := l.X.(*ast.Ident); ok && flowing[id.Name] {
						p.add(fn, l.Index)
					}
				}
			}
		case *ast.ValueSpec:
			for i, name := range node.Names {
				if lit, ok := rhsLiteral(node.Values, i); ok && flowing[name.Name] {
					p.addLiteral(fn, lit)
				}
			}
		}
		return true
	})
}

// addAppended records the elements an append(x, ...) call adds, when they
// are literals, and reports whether expr was such a call.
func (p *pkgScan) addAppended(fn *ast.FuncDecl, expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "append" {
		return false
	}
	for _, arg := range call.Args[1:] {
		if lit, ok := arg.(*ast.CompositeLit); ok {
			p.addStructKey(fn, lit)
		} else {
			p.add(fn, arg)
		}
	}
	return true
}

// rhsLiteral is the i'th right-hand value when it is a composite literal.
func rhsLiteral(values []ast.Expr, i int) (*ast.CompositeLit, bool) {
	if i >= len(values) {
		return nil, false
	}
	lit, ok := values[i].(*ast.CompositeLit)
	return lit, ok
}
