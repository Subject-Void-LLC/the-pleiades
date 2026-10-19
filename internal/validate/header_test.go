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

// TestTaskFormWarnings: each task written as fqcn: and params: is named, in
// every task list and in block, rescue and always, by its node ID and with
// its method; a task keyed by its method, and a file that is not a runbook
// mapping, raise none.
func TestTaskFormWarnings(t *testing.T) {
	src := `# A runbook.
id: x
pretasks:
  - name: a
    fqcn: noop
tasks:
  - name: b
    pkg.apt.install:
      name: curl
  - name: c
    block:
      - name: d
        fqcn: exec.command
        params: {argv: [true]}
    rescue:
      - name: e
        noop: {}
    always:
      - fqcn: net.ssh.ping
posttasks: []
`
	var got []string
	for _, w := range TaskFormWarnings([]byte(src)) {
		if w.RuleName != TaskFormRule || !strings.Contains(w.Message, "write its method as the task's key") {
			t.Errorf("%+v", w)
		}
		got = append(got, w.Node)
	}
	if want := []string{"pretasks[0]", "tasks[1].block[0]", "tasks[1].always[0]"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("warned about %v, want %v", got, want)
	}
	if w := TaskFormWarnings([]byte(src)); !strings.Contains(w[1].Message, "exec.command:") {
		t.Errorf("the method is not named: %s", w[1].Message)
	}
	for _, none := range []string{"id: x\ntasks:\n  - pkg.apt.install: {name: curl}\n", "- not: a runbook\n", "a: [unclosed", "", "id: x\ntasks: 5\n", "id: x\ntasks:\n  - just a string\n"} {
		if w := TaskFormWarnings([]byte(none)); len(w) != 0 {
			t.Errorf("%q: %v", none, w)
		}
	}
}
