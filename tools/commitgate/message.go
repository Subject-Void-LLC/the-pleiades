// Package main: the rules about the commit message itself.
//
// These run from the commit-msg hook, which git gives the path of the
// file holding the message a person just wrote. The message is text this
// project produces and keeps forever, so the writing-style rules apply
// to it exactly as they apply to a doc comment.
package main

import (
	"fmt"
	"strings"
)

// conventionalTypes is the exact set AGENTS.md's Git Workflow section
// lists. It is a closed set on purpose: a type outside it has no agreed
// meaning here, and letting one through quietly is how a convention
// stops being one.
var conventionalTypes = []string{"feat", "fix", "docs", "refactor", "test", "chore"}

// exemptSubjectPrefixes are the subjects git itself writes or that name
// a mechanical operation, which no convention applies to.
var exemptSubjectPrefixes = []string{"Merge ", "Revert ", "fixup!", "squash!", "amend!"}

// aiAttributionMarkers are the trailers that name a model as an author.
//
// AGENTS.md's Licensing section is the reason they are refused:
// corporate legal teams prefer not to list AI as a co-author because it
// creates ambiguity about intellectual property, commercial licensing
// and patent filings. This repository's own history agrees, carrying one
// such trailer in its last two hundred commits.
var aiAttributionMarkers = []string{
	"claude", "anthropic", "copilot", "chatgpt", "gpt-", "gemini",
	"cursor", "codex", "devin", "aider",
}

// subjectSoftLimit is the width a subject stays inside so `git log
// --oneline` and a pull request title both render it whole.
const subjectSoftLimit = 72

// parseMessage strips a raw commit message file down to what will
// actually be recorded.
//
// git keeps the comment lines a template wrote and everything below a
// scissors line, and neither is part of the commit. Judging them would
// fail a commit for text git is about to throw away.
func parseMessage(raw string) string {
	const scissors = "# ------------------------ >8 ------------------------"
	if i := strings.Index(raw, scissors); i >= 0 {
		raw = raw[:i]
	}
	var kept []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Trim(strings.Join(kept, "\n"), "\n")
}

// subjectOf returns a message's first line.
func subjectOf(message string) string {
	if i := strings.Index(message, "\n"); i >= 0 {
		return message[:i]
	}
	return message
}

// isExemptSubject reports whether a subject is one git or a rebase
// wrote, which the conventional-commit rule does not apply to.
func isExemptSubject(subject string) bool {
	for _, prefix := range exemptSubjectPrefixes {
		if strings.HasPrefix(subject, prefix) {
			return true
		}
	}
	return false
}

// hasConventionalPrefix reports whether subject opens with one of the
// allowed types, an optional parenthesized scope, an optional breaking
// change marker, then ": " and something after it.
func hasConventionalPrefix(subject string) bool {
	colon := strings.Index(subject, ": ")
	if colon < 0 || colon == len(subject)-2 {
		return false
	}
	head := subject[:colon]
	head = strings.TrimSuffix(head, "!")
	if open := strings.Index(head, "("); open >= 0 {
		if !strings.HasSuffix(head, ")") || open == 0 {
			return false
		}
		head = head[:open]
	}
	for _, t := range conventionalTypes {
		if head == t {
			return true
		}
	}
	return false
}

// checkMessage reports every rule the commit message breaks.
func checkMessage(raw string) []Finding {
	message := parseMessage(raw)
	if strings.TrimSpace(message) == "" {
		// An empty message aborts the commit in git itself, so there is
		// nothing here to add.
		return nil
	}

	var findings []Finding
	subject := subjectOf(message)

	if !isExemptSubject(subject) && !hasConventionalPrefix(subject) {
		findings = append(findings, Finding{
			Severity: SeverityFail,
			Rule:     "commit messages follow conventional commits",
			Detail: fmt.Sprintf("subject is %q\n  start it with one of %s, an optional (scope), then \": \"",
				excerpt(subject), strings.Join(conventionalTypes, ", ")),
		})
	}

	for i, line := range strings.Split(message, "\n") {
		if strings.ContainsRune(line, emDash) {
			findings = append(findings, Finding{
				Severity: SeverityFail,
				Rule:     "em dash is forbidden in all code, comments and documentation",
				Line:     i + 1,
				Detail: fmt.Sprintf("the message has one on line %d: %s\n  use a comma, period, semicolon, colon, or parentheses instead",
					i+1, excerpt(line)),
			})
			break
		}
	}

	if marker := aiAttribution(message); marker != "" {
		findings = append(findings, Finding{
			Severity: SeverityFail,
			Rule:     "do not list AI as a co-author",
			Detail: fmt.Sprintf("the message carries an attribution trailer naming %q\n"+
				"  AGENTS.md's Licensing section: listing AI as a co-author creates ambiguity about\n"+
				"  intellectual property, commercial licensing and patent filings", marker),
		})
	}

	if n := len([]rune(subject)); n > subjectSoftLimit {
		findings = append(findings, Finding{
			Severity: SeverityWarn,
			Rule:     "keep the subject readable in one line",
			Detail:   fmt.Sprintf("the subject is %d characters, over the usual %d", n, subjectSoftLimit),
		})
	}

	if strings.HasSuffix(subject, ".") {
		findings = append(findings, Finding{
			Severity: SeverityWarn,
			Rule:     "a subject is a title, not a sentence",
			Detail:   "the subject ends with a period",
		})
	}

	return findings
}

// aiAttribution returns the marker that made a message look like it
// credits a model, or an empty string when none did.
//
// It looks only at attribution trailers. A commit that merely discusses
// Claude or Copilot in its body is describing the work, which is exactly
// what a body is for.
func aiAttribution(message string) string {
	for _, line := range strings.Split(message, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		isTrailer := strings.HasPrefix(lower, "co-authored-by:") ||
			strings.HasPrefix(lower, "signed-off-by:") ||
			strings.HasPrefix(lower, "generated with") ||
			strings.HasPrefix(lower, "🤖 generated with")
		if !isTrailer {
			continue
		}
		for _, marker := range aiAttributionMarkers {
			if strings.Contains(lower, marker) {
				return marker
			}
		}
	}
	return ""
}
