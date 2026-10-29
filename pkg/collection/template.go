// Package collection: the one test of whether a parameter's text is a
// template the engine renders, shared by the engine and by the methods
// that must tell a value known before the run from one that is not.
package collection

import "strings"

// IsTemplate reports whether s is a template: it holds an expression
// "{{", a statement "{%" or a comment "{#". The engine renders every such
// string (internal/engine's IsTemplate is this function), so a method that
// answers a question about a call before the run (ReadOnlyCall, UndoCall,
// CheckCall) holds a value IsTemplate reports as not known yet, whichever
// of the three tags it holds. A Collection may not import internal/engine,
// which is why the rule lives here.
func IsTemplate(s string) bool {
	return strings.Contains(s, "{{") || strings.Contains(s, "{%") || strings.Contains(s, "{#")
}
