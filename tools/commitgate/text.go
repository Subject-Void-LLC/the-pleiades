// Package main: the rules that apply to any staged text, Go or not.
//
// AGENTS.md's writing-style rules are about prose, and prose in this
// repository lives in Markdown, in YAML comments, in CLI help strings
// and in Go doc comments alike. So these rules read added lines of every
// staged text file rather than picking a file type.
package main

import (
	"bytes"
	"fmt"
	"strings"
)

// emDash is the one character AGENTS.md forbids outright.
//
// Written as a code point rather than as the character itself, and the
// same goes for the two below. This file has to NAME what it refuses,
// and a rule that had to exempt its own source to state itself would be
// a rule with a hole in it. Spelling the code point costs one lookup and
// keeps the prohibition absolute.
const emDash = '\u2014'

// lookalikeDashes are the characters a reader cannot tell from an em
// dash at a glance. AGENTS.md does not forbid them, so they warn rather
// than fail: an en dash in a number range is ordinary American usage,
// and a horizontal bar is almost always a paste artifact. Reporting both
// at the same weight as the forbidden one would make the forbidden one
// easier to miss.
var lookalikeDashes = map[rune]string{
	'\u2013': "en dash",
	'\u2015': "horizontal bar",
}

// isBinary reports whether content looks like a binary blob.
//
// A NUL byte in the first few kilobytes is the same heuristic git itself
// uses to decide a file is binary, and it is enough here: the rules
// below are about characters a person wrote, and nothing in a binary
// file is.
func isBinary(content []byte) bool {
	head := content
	if len(head) > 8000 {
		head = head[:8000]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// isTestdata reports whether path lives under a testdata directory.
//
// Those files are excluded from the dash rules on purpose. A fixture's
// bytes are the data a test asserts against, not prose this project
// wrote, and a repository whose own subject includes text redaction has
// every reason to keep a fixture containing an awkward character. Making
// the gate refuse one would force the fixture to be weakened to satisfy
// a style rule that was never about it.
func isTestdata(path string) bool {
	return strings.HasPrefix(path, "testdata/") || strings.Contains(path, "/testdata/")
}

// checkDashes reports every forbidden or lookalike dash this commit adds
// to path.
//
// It reads only added lines. Existing text is deliberately out of scope:
// HANDOFF_ARCHIVE.md alone carries 218 em dashes of real history, and a
// rule that refused any commit touching such a file would be a rule
// people turn off rather than one they follow.
func checkDashes(path string, lines []addedLine) []Finding {
	if isTestdata(path) {
		return nil
	}

	var findings []Finding
	for _, line := range lines {
		for _, r := range line.Text {
			if r == emDash {
				findings = append(findings, Finding{
					Severity: SeverityFail,
					Rule:     "em dash is forbidden in all code, comments and documentation",
					File:     path,
					Line:     line.Number,
					Detail: fmt.Sprintf("this line adds one: %s\n  use a comma, period, semicolon, colon, or parentheses instead",
						excerpt(line.Text)),
				})
				break
			}
		}
		for r, name := range lookalikeDashes {
			if strings.ContainsRune(line.Text, r) {
				findings = append(findings, Finding{
					Severity: SeverityWarn,
					Rule:     "a dash that reads as an em dash",
					File:     path,
					Line:     line.Number,
					Detail: fmt.Sprintf("this line adds a %s: %s\n  AGENTS.md forbids only the em dash, so this is allowed; check it was meant",
						name, excerpt(line.Text)),
				})
				break
			}
		}
	}
	return findings
}

// excerpt trims a line down to something that fits on a terminal row,
// so a finding about a 200 character Markdown paragraph still prints as
// one readable line.
func excerpt(s string) string {
	s = strings.TrimSpace(s)
	const limit = 96
	// Counted in runes rather than bytes: cutting a multi-byte character
	// in half is exactly the kind of mangling that makes a reader doubt
	// the tool reporting it.
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "..."
}
