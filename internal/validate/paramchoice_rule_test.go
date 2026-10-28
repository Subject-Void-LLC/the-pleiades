// Tests for ParamChoiceRule against the real registered catalog and through
// the whole of Validate, so the sets it reads are the ones the binary ships
// and a value is what a runbook file decodes to.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// TestParamChoiceRule covers what validation refuses, naming the set, and
// what it lets through: a choice, a choice in another case where the method
// folds case, a fragment's set, a template, and a value of the wrong type,
// which is param-type's finding alone.
func TestParamChoiceRule(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		task string
		want []string // substrings of the one param-choice finding; nil means none
	}{
		{"a state outside the set", "net.ovs.bridge:\n      name: br0\n      state: presnt",
			[]string{`parameter "state"`, "net.ovs.bridge does not take", "not one of present, absent"}},
		{"a state in the set", "net.ovs.bridge:\n      name: br0\n      state: absent", nil},
		{"a severity outside the set", "scan.vuln:\n      dir: /srv/app\n      fail_on: severe",
			[]string{`parameter "fail_on"`, "critical, high, medium, low, any"}},
		{"a lock policy, folded, outside the set", "net.netconf.config:\n      content: <a/>\n      lock: maybe",
			[]string{`parameter "lock"`, "never, always, if_supported (case ignored)"}},
		{"a lock policy in another case", "net.netconf.config:\n      content: <a/>\n      lock: ALWAYS", nil},
		{"an HTTP method in lower case", "http.check:\n      url: https://x.example/\n      method: head", nil},
		{"a shell in another case", "exec.winrm.shell:\n      shell: PowerShell\n      command: hostname", nil},
		{"a fragment's set", "container.run:\n      name: web\n      image: nginx\n      pull: always",
			[]string{`parameter "pull"`, "not one of missing, never"}},
		{"a template is the engine's", "net.ovs.bridge:\n      name: br0\n      state: \"{{ vars.state }}\"", nil},
		{"the wrong type is param-type's", "scan.vuln:\n      dir: /srv/app\n      fail_on: 3", nil},
		{"a protocol firewalld takes", "fw.firewalld.allow:\n      port: 9\n      protocol: sctp", nil},
		{"a protocol firewalld does not", "fw.firewalld.allow:\n      port: 9\n      protocol: icmp",
			[]string{`parameter "protocol"`, "tcp, udp, sctp, dccp"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: c\nhosts: web1\ntasks:\n  - name: step\n    " + tc.task + "\n"))
			if err != nil {
				t.Fatalf("%s: %v", tc.task, err)
			}
			var got []validate.Finding
			for _, f := range validate.Validate(validate.WorldView{DAG: dag}).Findings {
				if f.RuleName == "param-choice" {
					got = append(got, f)
				}
			}
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("want no param-choice finding, got %v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one param-choice finding, got %v", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got[0].Message, w) {
					t.Errorf("finding %q lacks %q", got[0].Message, w)
				}
			}
		})
	}
}
