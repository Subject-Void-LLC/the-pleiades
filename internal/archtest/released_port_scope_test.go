// Package archtest: the scope analysis behind the released-port guard,
// finding in one function body each listener, where it is closed, and
// where its address is used or returned.
package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"
)

// scanScope checks one function body. Nested function literals are their
// own scopes for listeners they create, and a Close inside one (a deferred
// func, a t.Cleanup) counts as closing at the end, not where it is written.
func scanScope(fset *token.FileSet, body *ast.BlockStmt) []string {
	listeners := map[string]bool{}
	derived := map[string]string{} // identifier -> the listener its value came from
	type closeAt struct {
		listener string
		end      token.Pos
		// until is where the close stops mattering: the end of the
		// branch it is in when that branch returns or fails, else never.
		until token.Pos
	}
	// branchEnd is the end of the innermost block holding pos that ends
	// the function (its last statement returns, fails or panics), or
	// NoPos when the close is on the path every later statement takes.
	branchEnd := func(pos token.Pos) token.Pos {
		end := token.NoPos
		ast.Inspect(body, func(n ast.Node) bool {
			b, ok := n.(*ast.BlockStmt)
			if !ok || b == body || pos < b.Pos() || pos > b.End() {
				return true
			}
			if terminates(b) {
				end = b.End()
			}
			return true
		})
		return end
	}
	var explicit []closeAt
	// atReturn names the listeners a defer closes as this function
	// returns. A close in a t.Cleanup or a goroutine runs later, so an
	// address returned past one is still held.
	atReturn := map[string]bool{}
	type use struct {
		listener string
		pos      token.Pos
	}
	var uses, returns []use

	// fromListener names the listener an expression reads the address
	// of, directly or through a derived identifier. It follows only calls
	// that keep an address an address (addressCalls): a connection dialed
	// to an address, or an error naming one, is not one.
	fromListener := func(e ast.Node) string {
		got := ""
		ast.Inspect(e, func(n ast.Node) bool {
			if got != "" {
				return false
			}
			switch x := n.(type) {
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Addr" {
					if id, ok := sel.X.(*ast.Ident); ok && listeners[id.Name] {
						got = id.Name
					}
					return false
				}
				if !addressCall(x) {
					return false
				}
			case *ast.Ident:
				if l, ok := derived[x.Name]; ok {
					got = l
				}
			}
			return true
		})
		return got
	}

	var walk func(n ast.Node, nested bool) bool
	walk = func(n ast.Node, nested bool) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			if n != nil {
				ast.Inspect(x.Body, func(m ast.Node) bool { return walk(m, true) })
			}
			return false
		case *ast.DeferStmt:
			// A deferred close runs when this function returns, so it
			// releases the port before a caller can use a returned address.
			if l, _ := closeOf(x.Call, listeners); l != "" {
				atReturn[l] = true
			}
			if lit, ok := x.Call.Fun.(*ast.FuncLit); ok {
				ast.Inspect(lit.Body, func(m ast.Node) bool {
					if call, ok := m.(*ast.CallExpr); ok {
						if l, _ := closeOf(call, listeners); l != "" {
							atReturn[l] = true
						}
					}
					return true
				})
			}
			ast.Inspect(x.Call, func(m ast.Node) bool { return walk(m, true) })
			return false
		case *ast.AssignStmt:
			if !nested && len(x.Rhs) == 1 {
				if call, ok := x.Rhs[0].(*ast.CallExpr); ok && isListen(call) && len(x.Lhs) > 0 {
					if id, ok := x.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
						listeners[id.Name] = true
						return true
					}
				}
			}
			for _, r := range x.Rhs {
				if l := fromListener(r); l != "" {
					for _, lhs := range x.Lhs {
						if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
							derived[id.Name] = l
						}
					}
				}
			}
			for _, r := range x.Rhs {
				if l, end := closeOf(r, listeners); l != "" {
					if !nested {
						explicit = append(explicit, closeAt{l, end, branchEnd(end)})
					}
				}
				ast.Inspect(r, func(m ast.Node) bool { return walk(m, nested) })
			}
			return false
		case *ast.ExprStmt:
			if l, end := closeOf(x.X, listeners); l != "" {
				if !nested {
					explicit = append(explicit, closeAt{l, end, branchEnd(end)})
				}
				return false
			}
		case *ast.ReturnStmt:
			for _, r := range x.Results {
				if l := fromListener(r); l != "" {
					returns = append(returns, use{l, r.Pos()})
				}
			}
		case *ast.CallExpr:
			if messageCall(x) {
				// An error or a log naming the address does not connect to it.
				return false
			}
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Addr" {
				if id, ok := sel.X.(*ast.Ident); ok && listeners[id.Name] {
					uses = append(uses, use{id.Name, x.Pos()})
				}
			}
		case *ast.Ident:
			if l, ok := derived[x.Name]; ok {
				uses = append(uses, use{l, x.Pos()})
			}
		}
		return true
	}
	ast.Inspect(body, func(n ast.Node) bool {
		if n == body {
			return true
		}
		return walk(n, false)
	})

	var out []string
	for _, c := range explicit {
		for _, u := range uses {
			if u.listener == c.listener && u.pos > c.end && (c.until == token.NoPos || u.pos < c.until) {
				out = append(out, fmt.Sprintf("line %d closes %s, and line %d uses its address after that; a released port is anyone's (refusing.Addr holds one)",
					fset.Position(c.end).Line, c.listener, fset.Position(u.pos).Line))
				break
			}
		}
	}
	// An explicit close followed by a return of the address is already a
	// use after the close; this rule is for a deferred close, which runs
	// after the return has handed the address out.
	for _, r := range returns {
		if atReturn[r.listener] {
			out = append(out, fmt.Sprintf("line %d returns the address of %s, which this function closes; a released port is anyone's (refusing.Addr holds one)",
				fset.Position(r.pos).Line, r.listener))
		}
	}
	sort.Strings(out)
	return out
}

// isListen reports whether call binds a port: net.Listen, ListenTCP,
// ListenUDP or ListenPacket on a network that is not a Unix socket.
func isListen(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "net" || !listenFuncs[sel.Sel.Name] {
		return false
	}
	if len(call.Args) > 0 {
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && strings.HasPrefix(strings.Trim(lit.Value, "\"`"), "unix") {
			return false
		}
	}
	return true
}

// closeOf names the listener e closes (L.Close()), and where the call ends.
func closeOf(e ast.Expr, listeners map[string]bool) (string, token.Pos) {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return "", token.NoPos
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Close" {
		return "", token.NoPos
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || !listeners[id.Name] {
		return "", token.NoPos
	}
	return id.Name, call.End()
}

// addressCalls are the calls whose result is still the address their
// argument was: formatting it, splitting or joining it, converting it.
var addressCalls = map[string]bool{
	"String": true, "Itoa": true, "FormatInt": true, "FormatUint": true, "Sprintf": true, "Sprint": true,
	"JoinHostPort": true, "SplitHostPort": true, "AddrPort": true, "Port": true, "Addr": true,
	"int": true, "uint16": true, "int64": true, "string": true, "uint32": true,
}

// addressCall reports whether call keeps an address an address.
func addressCall(call *ast.CallExpr) bool {
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		return addressCalls[f.Sel.Name]
	case *ast.Ident:
		return addressCalls[f.Name]
	}
	return false
}

// messageCalls are the calls an address can appear in without anything
// connecting to it: an error, a test's failure or log.
var messageCalls = map[string]bool{
	"Errorf": true, "Error": true, "Fatalf": true, "Fatal": true, "Logf": true, "Log": true,
	"Skipf": true, "Skip": true, "New": true, "Printf": true, "Println": true,
}

// messageCall reports whether call only reports something.
func messageCall(call *ast.CallExpr) bool {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		return messageCalls[sel.Sel.Name]
	}
	return false
}

// terminates reports whether a block's last statement leaves the
// function: a return, a panic, or a test's Fatal, FailNow or Skip.
func terminates(b *ast.BlockStmt) bool {
	if len(b.List) == 0 {
		return false
	}
	switch last := b.List[len(b.List)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		call, ok := last.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			return f.Name == "panic"
		case *ast.SelectorExpr:
			switch f.Sel.Name {
			case "Fatal", "Fatalf", "FailNow", "Skip", "Skipf", "SkipNow":
				return true
			}
		}
	}
	return false
}
