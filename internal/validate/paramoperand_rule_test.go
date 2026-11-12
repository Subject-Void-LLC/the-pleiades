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
		{"getent's -i as an account name", "identity.user.create:\n      name: -i", []string{`parameter "name"`, "identity.user.create", `begins with "-"`}},
		{"a pattern as a primary group", "identity.user.modify:\n      name: deploy\n      group: \"adm*\"", []string{`parameter "group"`, "identity.user.modify", "pattern"}},
		{"an option as a group name", "identity.group.remove:\n      name: --badname", []string{`parameter "name"`, "identity.group.remove", `begins with "-"`}},
		{"a numeric primary group and a real shell", "identity.user.create:\n      name: deploy\n      group: \"1000\"\n      shell: /bin/bash", nil},
		{"a real package", "pkg.apt.install:\n      name: openssh-server", nil},
		{"g++", "pkg.install:\n      name: g++", nil},
		{"a real unit", "svc.systemd.start:\n      name: getty@tty1.service", nil},
		{"a template is the engine's", "pkg.apt.install:\n      name: \"{{ vars.pkg }}\"", nil},
		{"the wrong type is param-type's", "pkg.apt.install:\n      name: 7", nil},
		{"not an operand", "exec.command:\n      argv: [ls, -la, \"*\"]", nil},
		// Each program's own grammar, held at validation as in the method.
		{"systemd's root mount", "svc.systemd.start:\n      name: \"-.mount\"", nil},
		{"systemd's root slice through the generic method", "svc.restart:\n      name: \"-.slice\"", nil},
		{"another dash-led unit", "svc.systemd.stop:\n      name: \"-.swap\"", []string{"svc.systemd.stop", `begins with "-"`}},
		{"the root mount as a Windows service", "svc.windows.start:\n      name: \"-.mount\"", []string{"svc.windows.start", `begins with "-"`}},
		{"apt's trailing dash", "pkg.apt.install:\n      name: diffutils-", []string{"pkg.apt.install", `ends in "-"`}},
		{"apt's trailing dash in an undo's dependency", "pkg.apt.remove:\n      name: jq\n      dependencies: [libjq1-]", []string{`item 0 is refused: it ends in "-"`}},
		{"apt's release", "pkg.apt.install:\n      name: jq/bookworm", []string{"pkg.apt.install", `holds "/"`}},
		{"the generic trailing dash", "pkg.install:\n      name: diffutils-", []string{"pkg.install", `ends in "-"`}},
		{"a dnf group", "pkg.dnf.install:\n      name: \"@core\"", []string{"pkg.dnf.install", `begins with "@"`}},
		{"a dnf file", "pkg.dnf.install:\n      name: /usr/bin/jq", []string{"pkg.dnf.install", `holds "/"`}},
		{"a version pattern", "pkg.dnf.install:\n      name: nginx\n      version: \"*\"", []string{`parameter "version"`, "pattern"}},
		{"apt's version ending in a dash", "pkg.apt.install:\n      name: diffutils\n      version: 1:3.8-4-", []string{`parameter "version"`, `ends in "-"`}},
		{"a generic version pattern", "pkg.install:\n      name: nginx\n      version: \"1.*\"", []string{`parameter "version"`, "pattern"}},
		{"a real version", "pkg.apt.install:\n      name: nginx\n      version: 1.24.0-2ubuntu7", nil},
		{"a mountpoint mount reads as -a", "fs.mount:\n      path: -a\n      src: /dev/sdb1\n      fstype: ext4", []string{`parameter "path"`, "fs.mount", `begins with "-"`}},
		{"a mount source holding a line", "fs.mount:\n      path: /data\n      src: \"/dev/sdb1 /etc ext4 defaults 0 0\\n/dev/sdc1\"\n      fstype: ext4", []string{`parameter "src"`, "line break"}},
		{"an unmount of -a", "fs.unmount:\n      path: -a", []string{`parameter "path"`, "fs.unmount", `begins with "-"`}},
		{"a real mount", "fs.mount:\n      path: /data\n      src: UUID=0b1c-2d3e\n      fstype: ext4\n      opts: rw,noatime", nil},
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
