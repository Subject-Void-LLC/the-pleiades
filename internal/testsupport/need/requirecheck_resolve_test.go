// checkRequireCalls' reading of one package: which calls are Require's,
// and what each one's need resolves to.
package need

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strings"
)

// pkgCheck is one package being read.
type pkgCheck struct {
	c     *requireChecker
	pkg   *types.Package
	info  *types.Info
	files []*ast.File
	// funcs maps each function declared in the package to its declaration.
	funcs map[*types.Func]*ast.FuncDecl
	// forwarders are the package's own functions that pass one of their
	// parameters to Require as its need, by that parameter's index.
	forwarders map[*types.Func]int
}

// checkPackage type-checks one candidate package and checks every Require
// call, every value use of Require, and every Pin literal in it.
func (c *requireChecker) checkPackage(g pkgGroup) {
	files := c.parseDir(g.dir, g.name)
	if len(files) == 0 {
		return
	}
	pc := &pkgCheck{c: c, files: files, funcs: map[*types.Func]*ast.FuncDecl{}, forwarders: map[*types.Func]int{},
		info: &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}}
	conf := types.Config{Importer: c, Error: func(error) {}, FakeImportC: true}
	path := c.importPath(g.dir)
	if strings.HasSuffix(g.name, "_test") {
		path += "_test"
	}
	pc.pkg, _ = conf.Check(path, c.fset, files, pc.info)
	for _, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if obj, ok := pc.info.Defs[fn.Name].(*types.Func); ok {
					pc.funcs[obj] = fn
				}
			}
		}
	}
	// Require's own calls first; each forwarder they find is then read at
	// its own calls, until no new one appears.
	pc.eachCall(func(call *ast.CallExpr, fn *ast.FuncDecl) {
		if pc.isRequire(call.Fun) {
			pc.checkNeed(call.Args[1], fn, "Require")
		}
	})
	for done := map[*types.Func]bool{}; ; {
		var next []*types.Func
		for f := range pc.forwarders {
			if !done[f] {
				next = append(next, f)
			}
		}
		if len(next) == 0 {
			break
		}
		for _, f := range next {
			done[f] = true
			index := pc.forwarders[f]
			pc.eachCall(func(call *ast.CallExpr, fn *ast.FuncDecl) {
				if pc.callee(call.Fun) == f && index < len(call.Args) {
					pc.checkNeed(call.Args[index], fn, f.Name())
				}
			})
		}
	}
	pc.checkValueUses()
	pc.checkPins()
}

// eachCall calls visit with every call in the package and the function
// declaration it is in, nil at package level.
func (pc *pkgCheck) eachCall(visit func(*ast.CallExpr, *ast.FuncDecl)) {
	for _, f := range pc.files {
		for _, decl := range f.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			ast.Inspect(decl, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					visit(call, fn)
				}
				return true
			})
		}
	}
}

// callee is the function fun names, when it names one.
func (pc *pkgCheck) callee(fun ast.Expr) *types.Func {
	switch f := ast.Unparen(fun).(type) {
	case *ast.Ident:
		obj, _ := pc.info.Uses[f].(*types.Func)
		return obj
	case *ast.SelectorExpr:
		obj, _ := pc.info.Uses[f.Sel].(*types.Func)
		return obj
	}
	return nil
}

// isRequire reports whether fun names need.Require or testsupport.Require.
func (pc *pkgCheck) isRequire(fun ast.Expr) bool {
	return pc.isRequireFunc(pc.callee(fun))
}

// isRequireFunc reports whether f is need.Require or testsupport.Require.
func (pc *pkgCheck) isRequireFunc(f *types.Func) bool {
	return f != nil && f.Name() == "Require" && f.Pkg() != nil && slices.Contains(pc.c.entryPaths(), f.Pkg().Path()) &&
		f.Type().(*types.Signature).Recv() == nil
}

// checkNeed resolves e, the need a call in fn passes to via, and records
// it or the problem.
func (pc *pkgCheck) checkNeed(e ast.Expr, fn *ast.FuncDecl, via string) {
	e = ast.Unparen(e)
	if name, ok := pc.constString(e); ok {
		pc.record(e.Pos(), name, via)
		return
	}
	if id, ok := e.(*ast.Ident); ok {
		if v, ok := pc.info.Uses[id].(*types.Var); ok {
			if name, ok := pc.packageVar(v); ok {
				pc.record(e.Pos(), name, via)
				return
			}
			if f, index, ok := pc.parameterOf(v, fn); ok {
				pc.forward(e.Pos(), f, index)
				return
			}
		}
	}
	if fn != nil {
		if key := pc.funcKey(fn); pc.c.allow[key] != "" {
			pc.c.allowUsed[key] = true
			return
		}
	}
	pc.c.problem(e.Pos(), "the need passed to %s cannot be resolved to a name; pass a need constant (need.Docker) or a literal, or list the function with a reason", via)
}

// record checks one resolved need.
func (pc *pkgCheck) record(pos token.Pos, name, via string) {
	pc.c.out.seen[name] = true
	if !slices.Contains(pc.c.known, name) {
		pc.c.problem(pos, "%s is given need %q, which is not in need.Names; register it, or use one that is", via, name)
	}
}

// constString is e's value when it is a string constant.
func (pc *pkgCheck) constString(e ast.Expr) (string, bool) {
	tv, ok := pc.info.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// packageVar is v's value when v is an unexported variable of this
// package, initialized from a string constant and never assigned again
// nor has its address taken.
func (pc *pkgCheck) packageVar(v *types.Var) (string, bool) {
	if pc.pkg == nil || v.Parent() != pc.pkg.Scope() || v.Exported() {
		return "", false
	}
	value, found := "", false
	for _, f := range pc.files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if pc.info.Defs[name] == v && len(vs.Values) == len(vs.Names) {
						value, found = pc.constString(ast.Unparen(vs.Values[i]))
					}
				}
			}
		}
	}
	if !found {
		return "", false
	}
	changed := false
	for _, f := range pc.files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					changed = changed || pc.names(lhs, v)
				}
			case *ast.UnaryExpr:
				changed = changed || n.Op == token.AND && pc.names(n.X, v)
			}
			return !changed
		})
	}
	return value, !changed
}

// names reports whether e is an identifier naming v.
func (pc *pkgCheck) names(e ast.Expr, v *types.Var) bool {
	id, ok := ast.Unparen(e).(*ast.Ident)
	return ok && pc.info.Uses[id] == v
}

// parameterOf reports which parameter of fn, the function declaration a
// call is in, v is.
func (pc *pkgCheck) parameterOf(v *types.Var, fn *ast.FuncDecl) (*types.Func, int, bool) {
	if fn == nil {
		return nil, 0, false
	}
	f, ok := pc.info.Defs[fn.Name].(*types.Func)
	if !ok {
		return nil, 0, false
	}
	params := f.Type().(*types.Signature).Params()
	for i := range params.Len() {
		if params.At(i) == v {
			return f, i, true
		}
	}
	return nil, 0, false
}

// forward records f as a forwarder of its parameter index: its own calls
// in this package are then checked. Require itself forwards its caller's
// need, and every call of it is checked. A forwarder another package can
// call is refused, since those callers are not read here.
func (pc *pkgCheck) forward(pos token.Pos, f *types.Func, index int) {
	if pc.isRequireFunc(f) {
		return
	}
	if f.Exported() {
		if key := pc.funcKey(pc.funcs[f]); pc.c.allow[key] != "" {
			pc.c.allowUsed[key] = true
			return
		}
		pc.c.problem(pos, "%s is exported and passes its parameter to Require as the need, so its callers in other packages are not checked; pass a need constant instead, or list it with a reason", f.Name())
		return
	}
	pc.forwarders[f] = index
}

// funcKey is fn's allow key: its package's path within the module, then
// ".Func" or ".Type.Method".
func (pc *pkgCheck) funcKey(fn *ast.FuncDecl) string {
	if fn == nil || pc.pkg == nil {
		return ""
	}
	name := fn.Name.Name
	if fn.Recv != nil && len(fn.Recv.List) == 1 {
		t := fn.Recv.List[0].Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if id, ok := t.(*ast.Ident); ok {
			name = id.Name + "." + name
		}
	}
	return strings.TrimPrefix(strings.TrimPrefix(pc.pkg.Path(), pc.c.module), "/") + "." + name
}

// checkValueUses refuses Require named anywhere but as the function of a
// call: a value of it could be called with a need nothing here reads.
func (pc *pkgCheck) checkValueUses() {
	called := map[*ast.Ident]bool{}
	pc.eachCall(func(call *ast.CallExpr, _ *ast.FuncDecl) {
		switch f := ast.Unparen(call.Fun).(type) {
		case *ast.Ident:
			called[f] = true
		case *ast.SelectorExpr:
			called[f.Sel] = true
		}
	})
	for id, obj := range pc.info.Uses {
		if f, ok := obj.(*types.Func); ok && pc.isRequireFunc(f) && !called[id] {
			pc.c.problem(id.Pos(), "Require is used as a value; call it directly, so the need it is given can be checked")
		}
	}
}

// checkPins holds every testsupport.Pin literal's What, which Download
// passes to Require, to the rule a need argument is held to.
func (pc *pkgCheck) checkPins() {
	for _, f := range pc.files {
		for _, decl := range f.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !pc.isPin(pc.info.Types[lit].Type) {
					return true
				}
				for _, elt := range lit.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "What" {
							pc.checkNeed(kv.Value, fn, "a Pin's What")
							return true
						}
					}
				}
				pc.c.problem(lit.Pos(), "a Pin with no What: key; Download hands its What to Require, so name the need it is")
				return true
			})
		}
	}
}

// isPin reports whether t is testsupport.Pin.
func (pc *pkgCheck) isPin(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Name() == "Pin" && obj.Pkg() != nil && obj.Pkg().Path() == pc.c.module+"/internal/testsupport"
}
