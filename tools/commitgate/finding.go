// Package main: the finding vocabulary commitgate reports in.
//
// A check never prints anything itself. It returns findings, and one
// place renders them, so every message reads the same way and the exit
// status is decided once rather than by whichever check ran last.
package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Severity says what a finding does to the commit.
type Severity int

const (
	// SeverityWarn reports something worth seeing that does not block the
	// commit. It is for the rules AGENTS.md itself states softly (the
	// ~300-line cap is a "soft cap") and for the ones a static check can
	// only guess at, where a hard failure would teach people to reach for
	// --no-verify instead of reading the message.
	SeverityWarn Severity = iota

	// SeverityFail blocks the commit. It is for the rules AGENTS.md states
	// absolutely and a check can prove: a forbidden character that is
	// really there, a Go file gofmt would rewrite, a schema edit with no
	// regenerated code beside it.
	SeverityFail
)

// String renders a severity as the label that starts a printed line.
func (s Severity) String() string {
	if s == SeverityFail {
		return "FAIL"
	}
	return "warn"
}

// Finding is one rule's complaint about one place.
type Finding struct {
	// Severity decides whether this blocks the commit.
	Severity Severity

	// Rule names the rule in AGENTS.md's own words, short enough to scan
	// a list of them. It is not a rule id: an id would need a registry to
	// look up, and the point of the line is that a reader needs nothing
	// else to act on it.
	Rule string

	// File is the repo-relative path the finding is about, empty for a
	// finding about the commit as a whole rather than about a file.
	File string

	// Line is the 1-indexed line in File's staged content, zero when the
	// finding is about the file as a whole.
	Line int

	// Detail says what is wrong and, where there is one, what to do about
	// it. It carries the fix because a gate that only names the problem
	// makes every reader rediscover the same answer.
	Detail string
}

// location renders a finding's file and line as a clickable prefix, or
// an empty string for a finding that is about no particular file.
func (f Finding) location() string {
	switch {
	case f.File == "":
		return ""
	case f.Line > 0:
		return fmt.Sprintf("%s:%d: ", f.File, f.Line)
	default:
		return f.File + ": "
	}
}

// report writes every finding to w, failures first, and reports whether
// any of them blocks the commit.
//
// Failures print first on purpose. A commit that is going to be refused
// should say so in the first line a person reads, not after a screen of
// warnings they have to scroll past to find out.
func report(w io.Writer, findings []Finding) bool {
	if len(findings) == 0 {
		return false
	}

	// Stable order so two runs over the same staged content print the
	// same thing. sort.SliceStable keeps the order a check emitted its
	// own findings in, which is already file order within that check.
	sorted := make([]Finding, len(findings))
	copy(sorted, findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Severity > sorted[j].Severity
	})

	blocked := false
	var b strings.Builder
	for _, f := range sorted {
		if f.Severity == SeverityFail {
			blocked = true
		}
		fmt.Fprintf(&b, "commitgate %s: %s%s\n  %s\n", f.Severity, f.location(), f.Rule, f.Detail)
	}
	fmt.Fprint(w, b.String())
	return blocked
}
