// Package main: the error-message rules, and the AST walk they read.
//
// AGENTS.md states these absolutely ("Error messages are lowercase, no
// trailing punctuation"), but a static check cannot be absolute about
// them: a message opening with a proper noun looks exactly like one
// opening with a capitalized word. So the walk is exact, the judgement
// is not, and both findings warn rather than fail.
package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"unicode"
)

// errorMessage is one error string literal and where it was written.
type errorMessage struct {
	// Text is the string the literal denotes, already unquoted.
	Text string

	// Pos is the literal's own position, so a finding points at the
	// message rather than at the statement holding it.
	Pos token.Pos
}

// collectErrorMessages returns every fmt.Errorf or errors.New message
// literal in file.
//
// Only a literal first argument is collected. A format string held in a
// variable is unreadable to this check, and guessing at one would
// produce findings nobody can act on.
func collectErrorMessages(file *ast.File) []errorMessage {
	var found []errorMessage
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if !isErrorConstructor(call.Fun) {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		found = append(found, errorMessage{Text: text, Pos: lit.Pos()})
		return true
	})
	return found
}

// isErrorConstructor reports whether fn names fmt.Errorf or errors.New.
//
// It matches on the package identifier as written rather than resolving
// the import, because a file that renamed fmt or errors on import is
// vanishingly rare here and resolving would mean type-checking the whole
// package for a warning.
func isErrorConstructor(fn ast.Expr) bool {
	sel, ok := fn.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return (pkg.Name == "fmt" && sel.Sel.Name == "Errorf") ||
		(pkg.Name == "errors" && sel.Sel.Name == "New")
}

// checkErrorStrings warns about an error message that opens with a
// capital or closes with punctuation.
func checkErrorStrings(fset *token.FileSet, file *ast.File, path string) []Finding {
	var findings []Finding
	for _, msg := range collectErrorMessages(file) {
		runes := []rune(msg.Text)
		if len(runes) == 0 {
			continue
		}
		line := fset.Position(msg.Pos).Line
		if unicode.IsUpper(runes[0]) && !firstWordIsAcronym(msg.Text) {
			findings = append(findings, Finding{
				Severity: SeverityWarn,
				Rule:     "error messages are lowercase",
				File:     path,
				Line:     line,
				Detail:   fmt.Sprintf("%q opens with a capital\n  lowercase it unless that word is a proper noun", excerpt(msg.Text)),
			})
		}
		if last := runes[len(runes)-1]; last == '.' || last == '!' {
			findings = append(findings, Finding{
				Severity: SeverityWarn,
				Rule:     "error messages carry no trailing punctuation",
				File:     path,
				Line:     line,
				Detail:   fmt.Sprintf("%q ends with %q\n  drop it: the caller decides how the message is punctuated", excerpt(msg.Text), string(last)),
			})
		}
	}
	return findings
}

// firstWordIsAcronym reports whether a message opens with an
// all-uppercase word such as JSON, SSH or NATS, which is the one
// capitalized opening this repository writes on purpose.
func firstWordIsAcronym(text string) bool {
	word := text
	if i := strings.IndexFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}); i > 0 {
		word = text[:i]
	}
	runes := []rune(word)
	if len(runes) < 2 {
		return false
	}
	for _, r := range runes {
		if unicode.IsLetter(r) && !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}
