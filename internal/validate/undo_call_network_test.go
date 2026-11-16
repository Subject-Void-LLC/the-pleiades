// Package validate_test: net.gnmi.config's and net.restconf.config's undo
// held to each call, through the real registrations: a runbook marked
// reversible, or a block undoing its tasks on failure, is refused over a call
// whose parameters alone say it records no undo, and passed over one that
// may create, which only the run can tell.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/gnmi"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/restconf"
)

// TestUndoCall_NetworkWritesThatRecordNoUndoAreRefused runs each call under
// reversible: true and under a rollback_on_failure block.
func TestUndoCall_NetworkWritesThatRecordNoUndoAreRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		task string
		want string
	}{
		"gnmi update":       {"net.gnmi.config:\n      update:\n        - path: /interface[name=lo1]\n          val: {name: lo1}\n", ""},
		"gnmi replace":      {"net.gnmi.config:\n      replace:\n        - path: /interface[name=lo1]\n          val: {name: lo1}\n", ""},
		"gnmi delete":       {"net.gnmi.config:\n      delete:\n        - /interface[name=lo1]\n", "a delete records no undo"},
		"gnmi mixed":        {"net.gnmi.config:\n      delete:\n        - /interface[name=lo2]\n      update:\n        - path: /interface[name=lo1]\n          val: {name: lo1}\n", "a delete records no undo"},
		"gnmi template":     {"net.gnmi.config:\n      delete: \"{{ vars.gone }}\"\n", "delete is a template"},
		"restconf post":     {"net.restconf.config:\n      path: ietf-netconf-acm:nacm/groups\n      content: '{}'\n", ""},
		"restconf put":      {"net.restconf.config:\n      method: PUT\n      path: ietf-netconf-acm:nacm/groups/group=ops\n      content: '{}'\n", ""},
		"restconf delete":   {"net.restconf.config:\n      method: DELETE\n      path: ietf-netconf-acm:nacm/groups/group=ops\n", "a delete records no undo"},
		"restconf patch":    {"net.restconf.config:\n      method: patch\n      path: ietf-netconf-acm:nacm/groups/group=ops\n      content: '{}'\n", "a patch merges"},
		"restconf template": {"net.restconf.config:\n      method: \"{{ vars.how }}\"\n      path: ietf-netconf-acm:nacm/groups/group=ops\n", "method is a template"},
	} {
		t.Run(name, func(t *testing.T) {
			reversible := "id: r\nhosts: r1\nreversible: true\ntasks:\n  - name: step\n    " + tc.task
			guarded := "id: r\nhosts: r1\ntasks:\n  - name: b\n    rollback_on_failure: true\n    block:\n      - name: step\n        " + strings.ReplaceAll(tc.task, "\n", "\n    ")
			for rule, findings := range map[string][]validate.Finding{
				"reversible":          validate.ReversibleRule(validate.WorldView{DAG: yamlDAG(t, reversible)}),
				"rollback_on_failure": validate.RollbackOnFailureRule(validate.WorldView{DAG: yamlDAG(t, guarded)}),
			} {
				switch {
				case tc.want == "" && len(findings) != 0:
					t.Errorf("%s refused a call that may record its undo: %+v", rule, findings)
				case tc.want != "" && (len(findings) != 1 || !strings.Contains(findings[0].Message, "records no undo here: "+tc.want)):
					t.Errorf("%s: findings %+v, want one saying %q", rule, findings, tc.want)
				}
			}
		})
	}
}
