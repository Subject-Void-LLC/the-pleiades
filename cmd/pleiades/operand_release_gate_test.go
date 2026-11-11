// Release gate for FAILURE_PATTERNS 1174: a package name apt-get would read
// as an option, a pattern or "remove the package before the dash" never
// reaches a real Debian device, whether it is written in the task, rendered
// from a variable, or caught by the method itself.
//
// Before the fix, pkg.apt.install with name --autoremove asked dpkg-query
// first, read its exit 2 ("unknown option") as "not installed", and sent
// apt-get install -y --autoremove, which removes every package marked as
// installed automatically that nothing needs. The gate marks sudo that way,
// so the attack has something real to remove, and proves sudo survives.
//
// "Nothing reached the device" is read from the device's own sshd log: each
// login it accepted is a line there, counted before and after every refused
// call, beside a control call that does log in.
package main_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// acceptedLogins counts the password logins the device's sshd has accepted,
// read from the container's log, where sshd -D -e writes each one.
func (d *testdataDevice) acceptedLogins(t *testing.T) int {
	t.Helper()
	logs, err := d.container.Logs(context.Background())
	if err != nil {
		t.Fatalf("reading the device's sshd log: %v", err)
	}
	defer func() { _ = logs.Close() }()
	data, err := io.ReadAll(logs)
	if err != nil {
		t.Fatalf("reading the device's sshd log: %v", err)
	}
	return strings.Count(string(data), "Accepted password for root")
}

// operandGateRunbook renders the package name from a run variable, the way
// a register or a loop item would hand one over.
const operandGateRunbook = `# Installs the package a run variable names.
# Blast radius: the release gate's own Debian container.
# Changes: one package, when the name is one.
# Undo: pkg.apt.remove of the package; the container is discarded.
id: operand-gate
hosts: debian
tasks:
  - name: install what the variable names
    pkg.apt.install:
      name: "{{ vars.pkg }}"
`

// TestCLI_AptRefusesANameItWouldReadAsAnOption is the gate.
func TestCLI_AptRefusesANameItWouldReadAsAnOption(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the apt operand gate, which builds a Debian device, in short mode")
	}
	device := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	dir, home := device.manage(t, "debian", "linux_server,debian_family")

	// The thing --autoremove would take: sudo, marked as installed
	// automatically, with nothing depending on it. The control proves apt
	// really would remove it, so its surviving below means something.
	if out, code := device.exec(t, "apt-mark", "auto", "sudo"); code != 0 {
		t.Fatalf("apt-mark auto sudo: %s", out)
	}
	if out, _ := device.exec(t, "sh", "-c", "apt-get -s install --autoremove 2>&1"); !strings.Contains(out, "Remv sudo") {
		t.Fatalf("control: apt-get install --autoremove would not remove sudo, so its survival proves nothing:\n%s", out)
	}
	installed := func(pkg string) bool {
		out, _ := device.exec(t, "sh", "-c", "dpkg-query -W -f='${db:Status-Abbrev}' -- "+pkg+" 2>/dev/null || true")
		return strings.HasPrefix(out, "ii")
	}
	untouched := func(t *testing.T) {
		t.Helper()
		for _, pkg := range []string{"sudo", "diffutils"} {
			if !installed(pkg) {
				t.Fatalf("%s is no longer installed: a refused name reached apt-get", pkg)
			}
		}
		if out, _ := device.exec(t, "sh", "-c", "test -e /tmp/pwned && echo present || echo absent"); out != "absent" {
			t.Fatal("/tmp/pwned exists: an apt option reached the device")
		}
	}

	// A literal is refused by validation, so nothing runs and nothing logs in.
	for _, method := range []string{"pkg.apt.install", "pkg.apt.upgrade", "pkg.apt.remove", "pkg.install", "pkg.upgrade"} {
		for _, name := range []string{"--autoremove", "-oDPkg::Pre-Invoke::=touch /tmp/pwned"} {
			t.Run("literal "+method+" "+name, func(t *testing.T) {
				before := device.acceptedLogins(t)
				rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "debian", method, "name="+name)
				if code == 0 || rep.Outcome.Status != "invalid" || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "not_run" {
					t.Fatalf("exit %d, want validation to refuse the call before anything ran:\n%s", code, raw)
				}
				var refused bool
				for _, v := range rep.Validation {
					refused = refused || (v.Rule == "param-operand" && strings.Contains(v.Message, `begins with "-"`))
				}
				if !refused {
					t.Fatalf("no param-operand finding:\n%s", raw)
				}
				if after := device.acceptedLogins(t); after != before {
					t.Errorf("sshd accepted %d login(s) during a refused call", after-before)
				}
				untouched(t)
			})
		}
	}

	// A rendered value is refused by the engine before the method runs.
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "operand.yaml"), []byte(operandGateRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"--autoremove", "-oDPkg::Pre-Invoke::=touch /tmp/pwned", "lib*"} {
		t.Run("rendered "+name, func(t *testing.T) {
			before := device.acceptedLogins(t)
			rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/operand.yaml", "-e", "pkg="+name)
			if code == 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "failed" ||
				!strings.Contains(rep.Tasks[0].Error, "cannot pass on as one name") {
				t.Fatalf("exit %d, want the task failed by the operand rule:\n%s", code, raw)
			}
			if strings.Contains(rep.Tasks[0].Error, name) {
				t.Errorf("the refusal carries the value: %s", rep.Tasks[0].Error)
			}
			if after := device.acceptedLogins(t); after != before {
				t.Errorf("sshd accepted %d login(s) during a refused call", after-before)
			}
			untouched(t)
		})
	}

	// APT's own grammar: a trailing "-" is refused by the method, before it
	// connects. dpkg-query reports "diffutils-" absent, so before the fix
	// apt-get install -y diffutils- would have been sent, removing diffutils.
	t.Run("trailing dash", func(t *testing.T) {
		before := device.acceptedLogins(t)
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "debian", "pkg.apt.install", "name=diffutils-")
		if code == 0 || len(rep.Tasks) != 1 || !strings.Contains(rep.Tasks[0].Error, `ends in "-"`) {
			t.Fatalf("exit %d, want the task refused for its trailing dash:\n%s", code, raw)
		}
		if after := device.acceptedLogins(t); after != before {
			t.Errorf("sshd accepted %d login(s) during a refused call", after-before)
		}
		untouched(t)
	})

	// The control: a real name through the same runbook and variable does
	// log in and install, so the unchanged counts above are refusals, not a
	// count that never moves.
	t.Run("control", func(t *testing.T) {
		before := device.acceptedLogins(t)
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/operand.yaml", "-e", "pkg=jq")
		if code != 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "changed" {
			t.Fatalf("exit %d, want jq installed:\n%s", code, raw)
		}
		if !installed("jq") {
			t.Fatal("the control run reported a change and jq is not installed")
		}
		if after := device.acceptedLogins(t); after <= before {
			t.Errorf("the control run installed jq with no login sshd accepted, so the counts above prove nothing")
		}
		untouched(t)
	})
}
