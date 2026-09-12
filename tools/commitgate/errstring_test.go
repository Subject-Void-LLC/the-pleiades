// Package main: tests for the error-message rules.
package main

import (
	"go/parser"
	"go/token"
	"testing"
)

// findingsForSource parses src as a Go file and runs the error-message
// rules over it, which is the whole path checkGoFile takes.
func findingsForSource(t *testing.T, src string) []Finding {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	return checkErrorStrings(fset, file, "x.go")
}

func TestCheckErrorStringsAcceptsTheHouseStyle(t *testing.T) {
	src := `package x

import (
	"errors"
	"fmt"
)

func f() error {
	if false {
		return errors.New("device not found")
	}
	return fmt.Errorf("failed to read the journal: %w", errors.New("x"))
}
`
	if findings := findingsForSource(t, src); len(findings) != 0 {
		t.Errorf("reported %d findings against messages written in the house style: %+v", len(findings), findings)
	}
}

func TestCheckErrorStringsWarnsOnACapital(t *testing.T) {
	src := `package x

import "errors"

func f() error { return errors.New("Device not found") }
`
	if !hasRule(findingsForSource(t, src), SeverityWarn, "lowercase") {
		t.Error("did not warn about a message opening with a capital")
	}
}

func TestCheckErrorStringsAllowsAnAcronym(t *testing.T) {
	// "JSON decode failed" opens with a capital and is not a style
	// mistake. A rule that cannot tell the difference should not fail,
	// and should not cry wolf either.
	for _, msg := range []string{"JSON decode failed", "SSH dial failed", "NATS is unreachable", "TLS handshake failed"} {
		t.Run(msg, func(t *testing.T) {
			src := "package x\n\nimport \"errors\"\n\nfunc f() error { return errors.New(\"" + msg + "\") }\n"
			if hasRule(findingsForSource(t, src), SeverityWarn, "lowercase") {
				t.Errorf("warned about %q, which opens with an acronym", msg)
			}
		})
	}
}

func TestCheckErrorStringsWarnsOnTrailingPunctuation(t *testing.T) {
	for _, msg := range []string{"device not found.", "device not found!"} {
		t.Run(msg, func(t *testing.T) {
			src := "package x\n\nimport \"errors\"\n\nfunc f() error { return errors.New(\"" + msg + "\") }\n"
			if !hasRule(findingsForSource(t, src), SeverityWarn, "trailing punctuation") {
				t.Errorf("did not warn about %q", msg)
			}
		})
	}
}

func TestCheckErrorStringsReadsARawLiteral(t *testing.T) {
	src := "package x\n\nimport \"errors\"\n\nfunc f() error { return errors.New(`Device not found`) }\n"
	if !hasRule(findingsForSource(t, src), SeverityWarn, "lowercase") {
		t.Error("did not read a raw string literal")
	}
}

func TestCheckErrorStringsIgnoresANonLiteralFormat(t *testing.T) {
	// A format string held in a variable is unreadable to this check.
	// Guessing at one would produce a finding nobody can act on.
	src := `package x

import "fmt"

var format = "Device not found."

func f() error { return fmt.Errorf(format) }
`
	if findings := findingsForSource(t, src); len(findings) != 0 {
		t.Errorf("judged a format string it could not see: %+v", findings)
	}
}

func TestCheckErrorStringsIgnoresOtherCalls(t *testing.T) {
	// fmt.Printf is not an error constructor, and a rule that judged
	// every string in the file would be judging prose.
	src := `package x

import "fmt"

func f() { fmt.Printf("Wrote the file.\n") }
`
	if findings := findingsForSource(t, src); len(findings) != 0 {
		t.Errorf("judged a call that constructs no error: %+v", findings)
	}
}

func TestFirstWordIsAcronym(t *testing.T) {
	cases := map[string]bool{
		"JSON decode failed": true,
		"SSH":                true,
		"A thing":            false,
		"Device not found":   false,
		"":                   false,
		"X":                  false,
		"HTTP/2 failed":      true,
	}
	for text, want := range cases {
		t.Run(text, func(t *testing.T) {
			if got := firstWordIsAcronym(text); got != want {
				t.Errorf("firstWordIsAcronym(%q) = %v, want %v", text, got, want)
			}
		})
	}
}
