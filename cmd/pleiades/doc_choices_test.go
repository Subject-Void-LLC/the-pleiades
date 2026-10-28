// Tests that pleiades doc prints every closed set a parameter declares
// (Phase 131, R2), from the real registry, under the parameter it belongs
// to.
package main

import (
	"strings"
	"testing"
)

// TestRunDoc_PrintsAParametersChoices: a method's page lists each declared
// set on the line after its parameter's description, a fragment's set
// included, marks a set its method matches ignoring case, and prints no
// choices line for a method that declares none.
func TestRunDoc_PrintsAParametersChoices(t *testing.T) {
	for _, tc := range []struct {
		fqcn    string
		choices map[string]string // parameter to the choices line under it
	}{
		{"net.ovs.bridge", map[string]string{"state": "present, absent", "datapath_type": "system, netdev"}},
		{"scan.vuln", map[string]string{"fail_on": "critical, high, medium, low, any"}},
		{"net.netconf.config", map[string]string{"lock": "never, always, if_supported (case ignored)"}},
		{"container.run", map[string]string{"pull": "missing, never", "restart_policy": "no, on-failure, always, unless-stopped"}},
		{"net.catalyst.device_facts", nil},
	} {
		t.Run(tc.fqcn, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := runDoc([]string{tc.fqcn}); err != nil {
					t.Fatalf("runDoc(%s): %v", tc.fqcn, err)
				}
			})
			if tc.choices == nil {
				if strings.Contains(out, "choices:") {
					t.Errorf("%s declares no set but its page prints one:\n%s", tc.fqcn, out)
				}
				return
			}
			lines := strings.Split(out, "\n")
			for param, want := range tc.choices {
				// A parameter prints as its name line, its description line,
				// then its choices line.
				at := -1
				for i, l := range lines {
					if strings.HasPrefix(l, "  "+param+" ") {
						at = i
						break
					}
				}
				if at < 0 || at+2 >= len(lines) {
					t.Fatalf("%s's page has no parameter %s:\n%s", tc.fqcn, param, out)
				}
				if got := lines[at+2]; got != "    choices: "+want {
					t.Errorf("%s: the line under %s's description is %q, want %q", tc.fqcn, param, got, "    choices: "+want)
				}
			}
		})
	}
}
