// Tests for ParamOperandRule against the real registered catalog and through
// the whole of Validate, so the operands it reads are the ones the binary
// ships and a value is what a runbook file decodes to.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// TestParamOperandRule covers what validation refuses before the run, for
// a package, a unit, a Windows service and a generic dispatcher's name, and
// what it lets through: a real name, a template (the engine's), a value of
// the wrong type (param-type's) and a parameter that is not an operand.
func TestParamOperandRule(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		task string
		want []string // substrings of the one param-operand finding; nil means none
	}{
		{"an apt option", "pkg.apt.install:\n      name: \"-oDPkg::Pre-Invoke::=touch /tmp/pwned\"",
			[]string{`parameter "name"`, "pkg.apt.install cannot pass on as one name", `begins with "-"`}},
		{"a dnf pattern", "pkg.dnf.install:\n      name: \"*\"", []string{"pkg.dnf.install", "pattern"}},
		{"a generic package option", "pkg.remove:\n      name: --purge", []string{"pkg.remove", `begins with "-"`}},
		{"a systemd pattern", "svc.systemd.stop:\n      name: \"s*\"", []string{"svc.systemd.stop", "pattern"}},
		{"a generic service pattern", "svc.restart:\n      name: \"ssh?\"", []string{"svc.restart", "pattern"}},
		{"a Windows service pattern", "svc.windows.start:\n      name: \"*\"", []string{"svc.windows.start", "pattern"}},
		{"an apt dependency option", "pkg.apt.remove:\n      name: jq\n      dependencies: [libjq1, -f]",
			[]string{`parameter "dependencies"`, `item 1 is refused: it begins with "-"`}},
		{"a chown option as a file's owner", "file.copy:\n      dest: /srv/app.conf\n      content: x\n      owner: --reference=/etc/shadow",
			[]string{`parameter "owner"`, "file.copy cannot pass on as one name", `begins with "-"`}},
		{"a pattern as a directory's group", "file.directory:\n      path: /srv/app\n      group: \"adm*\"", []string{`parameter "group"`, "file.directory", "pattern"}},
		{"a chgrp option as a touched file's group", "file.touch:\n      path: /srv/stamp\n      group: -R", []string{`parameter "group"`, "file.touch", `begins with "-"`}},
		{"a real owner and group", "file.permissions:\n      path: /srv/app.conf\n      owner: www-data\n      group: app.svc", nil},
		{"a real package", "pkg.apt.install:\n      name: openssh-server", nil},
		{"g++", "pkg.install:\n      name: g++", nil},
		{"a real unit", "svc.systemd.start:\n      name: getty@tty1.service", nil},
		{"a template is the engine's", "pkg.apt.install:\n      name: \"{{ vars.pkg }}\"", nil},
		{"the wrong type is param-type's", "pkg.apt.install:\n      name: 7", nil},
		{"not an operand", "exec.command:\n      argv: [ls, -la, \"*\"]", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: c\nhosts: web1\ntasks:\n  - name: step\n    " + tc.task + "\n"))
			if err != nil {
				t.Fatalf("%s: %v", tc.task, err)
			}
			var got []validate.Finding
			for _, f := range validate.Validate(validate.WorldView{DAG: dag}).Findings {
				if f.RuleName == "param-operand" {
					got = append(got, f)
				}
			}
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("want no param-operand finding, got %v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one param-operand finding, got %v", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got[0].Message, w) {
					t.Errorf("finding %q lacks %q", got[0].Message, w)
				}
			}
		})
	}
}
