// Package main: the rules that apply only to staged Go files.
//
// Each of these answers a rule AGENTS.md states about Go specifically:
// gofmt with no exceptions, a docstring at the top of every file, and a
// soft cap on how much logic one file carries. The error-message rules
// live beside them in errstring.go.
package main

import (
	"fmt"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"strings"
)

// logicLineSoftCap is AGENTS.md's "~300-line soft cap on logic files".
const logicLineSoftCap = 300

// checkGoFile runs every Go rule against one staged file's recorded
// content.
//
// A file that does not parse returns a single failure and nothing else.
// Continuing would mean reporting rules derived from a broken syntax
// tree, and a wrong finding costs more than a missing one: it sends the
// reader to a line that is not the problem.
//
// A file that merely needs gofmt is NOT short-circuited that way. Its
// syntax tree is perfectly good, so every other rule still has something
// true to say, and stopping here would hand the author one problem now
// and the rest after they fixed it and committed again.
func checkGoFile(file stagedFile, content []byte) []Finding {
	findings := checkGofmt(file.Path, content)

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file.Path, content, parser.ParseComments)
	if err != nil {
		return []Finding{{
			Severity: SeverityFail,
			Rule:     "staged Go must parse",
			File:     file.Path,
			Detail:   "the content this commit records does not parse: " + err.Error(),
		}}
	}

	if file.Kind == changeAdded && parsed.Doc == nil {
		findings = append(findings, Finding{
			Severity: SeverityFail,
			Rule:     "every file starts with a docstring",
			File:     file.Path,
			Line:     fset.Position(parsed.Package).Line,
			Detail: "this commit adds the file with no doc comment above its package clause\n" +
				"  write a Google-style comment saying what the file is for, starting with \"Package " + parsed.Name.Name + "\"",
		})
	}
	if file.Kind == changeAdded && !strings.HasSuffix(file.Path, "_test.go") {
		findings = append(findings, checkLogicSize(file.Path, content)...)
	}
	findings = append(findings, checkErrorStrings(fset, parsed, file.Path)...)
	return findings
}

// checkGofmt reports a staged Go file gofmt would rewrite.
//
// It formats the recorded bytes rather than shelling out, so the answer
// is about what this commit stores and not about whatever the working
// tree happens to hold. go/format is the same formatter gofmt itself
// runs with no flags, which is what `make fmt` checks.
func checkGofmt(path string, content []byte) []Finding {
	formatted, err := format.Source(content)
	if err != nil {
		// Reported by checkGoFile's own parse step, with the better
		// message. Staying quiet here keeps one broken file from
		// producing two findings that say the same thing.
		return nil
	}
	if string(formatted) == string(content) {
		return nil
	}
	return []Finding{{
		Severity: SeverityFail,
		Rule:     "gofmt, no exceptions",
		File:     path,
		Detail:   "gofmt would rewrite the content this commit records\n  run `make fmt-fix`, then stage the file again",
	}}
}

// checkLogicSize warns when a newly added file carries more logic than
// AGENTS.md's soft cap.
//
// It counts lines holding at least one real token, so a file that is
// long because it explains itself is not penalized for following the
// repository's own comment rules. The count comes from go/scanner rather
// than from a text heuristic, so a "//" inside a string literal is a
// string literal and not a comment.
//
// It fires only on a file this commit ADDS. The cap is soft, and on a
// file that already exists the warning is both unactionable and endless:
// it would print on every future commit that touched one of the many
// files already over it.
func checkLogicSize(path string, content []byte) []Finding {
	var s scanner.Scanner
	fset := token.NewFileSet()
	f := fset.AddFile(path, fset.Base(), len(content))
	// A nil error handler: a file that scanned badly already failed the
	// parse check above, so there is nothing to report a second time.
	s.Init(f, content, nil, scanner.ScanComments)

	lines := make(map[int]bool)
	for {
		pos, tok, _ := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			continue
		}
		lines[fset.Position(pos).Line] = true
	}
	if len(lines) <= logicLineSoftCap {
		return nil
	}
	return []Finding{{
		Severity: SeverityWarn,
		Rule:     "~300-line soft cap on logic files",
		File:     path,
		Detail: fmt.Sprintf("this commit adds a file with %d lines of code, not counting comments or blanks\n"+
			"  the cap is soft and doc comments do not count against it, so this is a prompt to split, not a refusal",
			len(lines)),
	}}
}

// isGoSource reports whether a path is Go source.
//
// Whether the file is GENERATED is a separate question answered from its
// content, not its name (isGeneratedSource, staged.go), because a path
// cannot tell ent's generated client from the four hand-written files
// sitting beside it.
func isGoSource(path string) bool {
	return strings.HasSuffix(path, ".go")
}
