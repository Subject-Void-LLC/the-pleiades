// The release gate for rendered task parameters: one runbook, run through the
// real binary against a real Debian device, whose parameters read the
// runbook's vars, a registered result, a set_fact and the run's extra
// variables at once, and whose condition reads the set_fact.
package main_test

import (
	"strings"
	"testing"
)

// renderGateRunbook writes a file from all four sources, and a second one only
// when a condition on the set_fact holds.
const renderGateRunbook = `# Writes two files under /tmp from the runbook's variables, a read and a fact.
# Changes: /tmp/render-gate and /tmp/render-gate-when.
# Undo: pleiades rollback removes both.
id: render-gate
hosts: debian
vars:
  greeting: from-vars
  shadowed: from-vars
tasks:
  - name: read the device's name
    exec.command:
      cmd: cat /etc/hostname
    register: host
  - name: remember a fact built from a variable
    set_fact:
      marker: "set-{{ vars.greeting }}"
  - name: write every source into one file
    file.copy:
      dest: /tmp/render-gate
      content: "{{ vars.greeting }} {{ vars.shadowed }} {{ result.host.stdout | trim }} {{ vars.marker }}\n"
  - name: only when the fact says so
    file.copy:
      dest: /tmp/render-gate-when
      content: "fact seen\n"
    when_cel: vars.marker == "set-from-vars"
`

// TestCLI_ParamsRenderEverySourceInItsOrder: the file holds the runbook's
// variable, the extra variable in place of the runbook's same-named one, the
// device's own hostname read by an earlier task, and the set_fact; the
// conditional task ran because the condition read the set_fact; and a run
// without the extra variable reads the runbook's value instead.
func TestCLI_ParamsRenderEverySourceInItsOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the rendered-params release gate, which builds and runs a real Debian sshd, in short mode")
	}
	device := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	dir, home := device.manage(t, "debian", "linux_server,debian_family")
	writeFile(t, dir, "runbooks/render.yaml", renderGateRunbook)
	hostname, code := device.exec(t, "cat", "/etc/hostname")
	if code != 0 || hostname == "" {
		t.Fatalf("reading the device's hostname: %q, exit %d", hostname, code)
	}

	for _, tc := range []struct {
		name  string
		args  []string
		wants string
	}{
		{"the extra variable wins", []string{"run", "runbooks/render.yaml", "--extra-vars", "shadowed=from-extra"}, "from-vars from-extra " + hostname + " set-from-vars"},
		{"without it the runbook's value is read", []string{"run", "runbooks/render.yaml"}, "from-vars from-vars " + hostname + " set-from-vars"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runPleiadesWithHome(t, dir, home, tc.args...)
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			got, code := device.exec(t, "cat", "/tmp/render-gate")
			if code != 0 || got != tc.wants {
				t.Errorf("the file holds %q (exit %d), want %q:\n%s", got, code, tc.wants, out)
			}
			if when, code := device.exec(t, "cat", "/tmp/render-gate-when"); code != 0 || when != "fact seen" {
				t.Errorf("the task conditioned on the set_fact did not run: %q, exit %d:\n%s", when, code, out)
			}
			if strings.Contains(out, "{{") {
				t.Errorf("an unrendered template reached the output:\n%s", out)
			}
		})
	}
}
