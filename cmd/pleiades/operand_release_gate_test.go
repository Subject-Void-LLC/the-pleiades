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
//
// The second pass of the finding adds a version joined onto the name
// (version 1:3.8-4- is apt-get's "remove diffutils"), a release in the name,
// a name apt-get would read as another package ("jq+" installed jq), a
// mountpoint mount would read as -a, and an archive's dash-led source,
// which rm refused as an option rather than removing. TestCLI_SystemdTakesA
// GatheredRootUnit is the other side of the rule: systemd's root mount,
// which begins with "-", read from the device and rendered into
// svc.systemd.start, reaches systemctl as the unit it names.
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

// operandMoreRunbook renders a version and a mountpoint from run
// variables, the way a register or a loop item would hand them over.
const operandMoreRunbook = `# Pins a package's version and mounts a scratch volume, both from run variables.
# Blast radius: the release gate's own Debian container.
# Changes: one package's version and one tmpfs mount, when the values are real.
# Undo: pkg.apt.install of the earlier version and fs.unmount; the container is discarded.
id: operand-more-gate
hosts: debian
tasks:
  - name: pin what the variable names
    pkg.apt.install:
      name: diffutils
      version: "{{ vars.version }}"
    when: "vars.version != ''"
  - name: mount where the variable names
    fs.mount:
      path: "{{ vars.path }}"
      src: tmpfs
      fstype: tmpfs
      persist: false
    when: "vars.path != ''"
`

// operandArchiveRunbook archives a file whose name begins with a dash, in
// the login's home, and removes it once archived.
const operandArchiveRunbook = `# Archives one file whose name begins with a dash, then removes it.
# Blast radius: the release gate's own Debian container.
# Changes: one archive under /tmp; the file it archives is removed.
# Undo: none needed; the container is discarded.
id: operand-archive-gate
hosts: debian
tasks:
  - name: archive and remove a dash-led file
    archive.create:
      path: /tmp/operand-keep.tar.gz
      src:
        - "-keep"
      remove: true
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

	// APT's own grammar, a version joined onto the name, and a mountpoint:
	// each is refused by validation, so nothing runs and nothing logs in.
	// dpkg-query reports "diffutils-" absent, so before the fix apt-get
	// install -y diffutils- would have been sent, removing diffutils, and
	// version 1:3.8-4- did the same through name=version-.
	for _, tc := range []struct {
		label string
		args  []string
		want  string
	}{
		{"trailing dash", []string{"pkg.apt.install", "name=diffutils-"}, `ends in "-"`},
		{"generic trailing dash", []string{"pkg.install", "name=diffutils-"}, `ends in "-"`},
		{"release", []string{"pkg.apt.install", "name=jq/bookworm"}, `holds "/"`},
		{"version ending in a dash", []string{"pkg.apt.install", "name=diffutils", "version=1:3.8-4-"}, `ends in "-"`},
		{"version pattern", []string{"pkg.apt.install", "name=diffutils", "version=*"}, "pattern"},
		{"generic version pattern", []string{"pkg.install", "name=diffutils", "version=*"}, "pattern"},
		{"mountpoint -a", []string{"fs.mount", "path=-a", "src=tmpfs", "fstype=tmpfs", "persist=false"}, `begins with "-"`},
		{"unmount -a", []string{"fs.unmount", "path=-a", "persist=false"}, `begins with "-"`},
	} {
		t.Run("literal "+tc.label, func(t *testing.T) {
			before := device.acceptedLogins(t)
			rep, raw, code := runPleiadesJSONWithHome(t, dir, home, append([]string{"adhoc", "debian"}, tc.args...)...)
			if code == 0 || rep.Outcome.Status != "invalid" || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "not_run" {
				t.Fatalf("exit %d, want validation to refuse the call before anything ran:\n%s", code, raw)
			}
			var refused bool
			for _, v := range rep.Validation {
				refused = refused || (v.Rule == "param-operand" && strings.Contains(v.Message, tc.want))
			}
			if !refused {
				t.Fatalf("no param-operand finding saying %q:\n%s", tc.want, raw)
			}
			if after := device.acceptedLogins(t); after != before {
				t.Errorf("sshd accepted %d login(s) during a refused call", after-before)
			}
			untouched(t)
		})
	}

	// The same values rendered from a variable are refused by the engine.
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "operand-more.yaml"), []byte(operandMoreRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		label string
		vars  []string
		want  string
	}{
		{"version ending in a dash", []string{"version=1:3.8-4-", "path="}, `ends in "-"`},
		{"version pattern", []string{"version=*", "path="}, "pattern"},
		{"mountpoint -a", []string{"version=", "path=-a"}, `begins with "-"`},
	} {
		t.Run("rendered "+tc.label, func(t *testing.T) {
			before := device.acceptedLogins(t)
			args := []string{"run", "runbooks/operand-more.yaml"}
			for _, v := range tc.vars {
				args = append(args, "-e", v)
			}
			rep, raw, code := runPleiadesJSONWithHome(t, dir, home, args...)
			var failed bool
			for _, task := range rep.Tasks {
				failed = failed || (task.Status == "failed" && strings.Contains(task.Error, "cannot pass on as one name") && strings.Contains(task.Error, tc.want))
			}
			if code == 0 || !failed {
				t.Fatalf("exit %d, want a task failed by the operand rule saying %q:\n%s", code, tc.want, raw)
			}
			if after := device.acceptedLogins(t); after != before {
				t.Errorf("sshd accepted %d login(s) during a refused call", after-before)
			}
			untouched(t)
		})
	}

	// A name APT has no package for exactly is refused after the method reads
	// the device and before apt-get is asked for anything. apt-get install
	// -y jq+ reads the trailing "+" as "install jq" when no package is named
	// jq+, and dpkg-query reports jq+ absent before and after, so before the
	// fix jq was installed and the undo recorded, pkg.apt.remove of jq+,
	// removed nothing.
	t.Run("a name with no package of its own", func(t *testing.T) {
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "debian", "pkg.apt.install", "name=jq+")
		if code == 0 || len(rep.Tasks) != 1 || !strings.Contains(rep.Tasks[0].Error, "no package named exactly jq+") {
			t.Fatalf("exit %d, want the task refused for naming no package of its own:\n%s", code, raw)
		}
		if installed("jq") {
			t.Fatal("jq is installed: apt-get read jq+ as jq")
		}
		untouched(t)
	})

	// rm gets "--" before an archive's sources, so a source whose name begins
	// with a dash is removed as asked. Before, rm -rf -keep failed as an
	// invalid option and left the file the task said to remove.
	t.Run("archive remove of a dash-led source", func(t *testing.T) {
		if out, code := device.exec(t, "sh", "-c", "printf data > /root/-keep"); code != 0 {
			t.Fatalf("writing /root/-keep: %s", out)
		}
		if err := os.WriteFile(filepath.Join(dir, "runbooks", "operand-archive.yaml"), []byte(operandArchiveRunbook), 0o600); err != nil {
			t.Fatal(err)
		}
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/operand-archive.yaml")
		if code != 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "changed" {
			t.Fatalf("exit %d, want the archive made and its source removed:\n%s", code, raw)
		}
		if out, _ := device.exec(t, "sh", "-c", "test -e /root/-keep && echo present || echo absent"); out != "absent" {
			t.Fatal("/root/-keep is still there: rm read the source as an option")
		}
		if out, code := device.exec(t, "sh", "-c", "tar -tzf /tmp/operand-keep.tar.gz"); code != 0 || !strings.Contains(out, "-keep") {
			t.Fatalf("the archive does not hold the removed file (exit %d): %s", code, out)
		}
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

// rootUnitRunbook reads systemd's root mount from the device and renders
// it into svc.systemd.start, the way a loop over a host's units meets it.
const rootUnitRunbook = `# Reads the root mount unit from the device and makes sure it is started.
# Blast radius: the release gate's own Rocky Linux container under systemd.
# Changes: nothing; the root mount is always active.
# Undo: none needed; the container is discarded.
id: root-unit-gate
hosts: rocky
tasks:
  - name: read the root mount unit
    exec.shell:
      cmd: "systemctl list-units --type=mount --all --plain --no-legend --no-pager | awk '$1 == \"-.mount\" { print $1 }'"
    register: root
  - name: make sure it is started
    svc.systemd.start:
      name: "{{ result.root.stdout | trim }}"
`

// TestCLI_SystemdTakesAGatheredRootUnit: -.mount, the root mount every
// systemd host loads, begins with "-" and is real. Read from a real Rocky
// Linux device under systemd and rendered into svc.systemd.start, it passes
// the unit rule and reaches systemctl after its "--" as the unit it names,
// which is active, so the task changes nothing. Before the fix the engine
// refused it as an option, failing every loop over a host's units that met
// it. Any other dash-led unit stays refused, by validation.
func TestCLI_SystemdTakesAGatheredRootUnit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the systemd root unit gate, which builds a Rocky Linux device under systemd, in short mode")
	}
	device := startTestdataDevice(t, "rocky-systemd-sshd", rockyGatePassword, systemdDevice)
	dir, home := device.manage(t, "rocky", "linux_server,rhel_family")
	if out, code := device.exec(t, "systemctl", "show", "-p", "ActiveState", "--", "-.mount"); code != 0 || strings.TrimSpace(out) != "ActiveState=active" {
		t.Fatalf("the device has no active -.mount, so this gate proves nothing (exit %d): %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "root-unit.yaml"), []byte(rootUnitRunbook), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("gathered -.mount", func(t *testing.T) {
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/root-unit.yaml")
		if code != 0 || len(rep.Tasks) != 2 || rep.Tasks[1].Status != "ok" {
			t.Fatalf("exit %d, want the root mount read and found started:\n%s", code, raw)
		}
		if got := rep.Tasks[1].Stats["name"]; got != "-.mount" {
			t.Errorf("the task acted on %v, want -.mount", got)
		}
	})

	for _, name := range []string{"-f", "-.swap"} {
		t.Run("literal "+name, func(t *testing.T) {
			rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "rocky", "svc.systemd.stop", "name="+name)
			var refused bool
			for _, v := range rep.Validation {
				refused = refused || (v.Rule == "param-operand" && strings.Contains(v.Message, `begins with "-"`))
			}
			if code == 0 || rep.Outcome.Status != "invalid" || !refused {
				t.Fatalf("exit %d, want validation to refuse %s:\n%s", code, name, raw)
			}
		})
	}
	if out, code := device.exec(t, "systemctl", "show", "-p", "ActiveState", "--", "-.mount"); code != 0 || strings.TrimSpace(out) != "ActiveState=active" {
		t.Errorf("-.mount is no longer active (exit %d): %s", code, out)
	}
}
