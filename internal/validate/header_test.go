// Tests for the header comment's warning, and how a report shows it.
package validate

import (
	"strings"
	"testing"
)

// TestHeaderWarnings: a comment with text before the first line of YAML is
// a header, after a blank line, a document marker, a directive or a byte
// order mark; nothing but YAML, an empty comment, a comment after the
// first key, or only a modeline, schema hint, licence or copyright line is
// not one.
func TestHeaderWarnings(t *testing.T) {
	for name, tc := range map[string]struct {
		source string
		header bool
	}{
		"a header":                         {"# Restarts nginx on the web tier.\nid: x\n", true},
		"after a blank line":               {"\n\n# Restarts nginx.\nid: x\n", true},
		"after a document marker":          {"---\n# Restarts nginx.\nid: x\n", true},
		"after a directive":                {"%YAML 1.2\n---\n# Restarts nginx.\nid: x\n", true},
		"after a byte order mark":          {"\xef\xbb\xbf# Restarts nginx.\nid: x\n", true},
		"indented, with CRLF":              {"  ## Restarts nginx.\r\nid: x\r\n", true},
		"under a modeline":                 {"# vim: set ts=2:\n# Restarts nginx.\nid: x\n", true},
		"none":                             {"id: x\nhosts: web\n", false},
		"empty":                            {"", false},
		"an empty comment":                 {"#\n##\nid: x\n", false},
		"a comment after the first key":    {"id: x\n# Restarts nginx.\n", false},
		"a schema hint alone":              {"# yaml-language-server: $schema=runbook.json\nid: x\n", false},
		"a modeline alone":                 {"# -*- mode: yaml -*-\n# vi: ts=2\nid: x\n", false},
		"a licence and a copyright alone":  {"# SPDX-License-Identifier: MIT\n# Copyright 2026 Someone\nid: x\n", false},
		"a document marker then no header": {"---\nid: x\n", false},
	} {
		got := HeaderWarnings([]byte(tc.source))
		if tc.header != (len(got) == 0) {
			t.Errorf("%s: warnings %v, want a header: %v", name, got, tc.header)
		}
		for _, w := range got {
			if w.RuleName != HeaderRule || !strings.Contains(w.Message, "start the file with # lines") {
				t.Errorf("%s: %+v", name, w)
			}
		}
	}
}

// TestReport_AWarningIsNotAnError: a report with only a warning has no
// errors, says it found no issues, and prints the warning after it, below
// any finding and above the routes.
func TestReport_AWarningIsNotAnError(t *testing.T) {
	r := Report{Warnings: HeaderWarnings([]byte("id: x\n"))}
	if r.HasErrors() {
		t.Error("a warning failed the report")
	}
	if got := r.String(); !strings.HasPrefix(got, "validate: no issues found\n[warning] header: the runbook does not open") {
		t.Errorf("String() = %q", got)
	}
	r.Findings = []Finding{{RuleName: "collection", Node: "a", Message: "no such method"}}
	r.Routes = []Route{{Node: "a", Device: "d", Transport: "ssh", Detail: "direct"}}
	got := r.String()
	finding, warning, route := strings.Index(got, "[collection]"), strings.Index(got, "[warning]"), strings.Index(got, "[route]")
	if !r.HasErrors() || finding < 0 || warning < finding || route < warning {
		t.Errorf("String() = %q", got)
	}
}
