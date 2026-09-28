// Release gates for Phase 46's checks that had been proven only on fakes:
// every pkg.apt.*, pkg.dnf.*, generic pkg.* and fw.firewalld.* check, run
// through the real binary against a real package manager and a real
// firewalld, and held to what the real run then does.
//
// Each call goes through `pleiades adhoc ... --json`, the way a user calls
// one method once, and every state is read back from the container beside
// the product, never from the command's own report.
package main_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
)

// rockyGatePassword is root's password in the throwaway image
// testdata/rocky-systemd-sshd builds.
const rockyGatePassword = "release-gate-rocky-password"

// systemdDevice lets systemd run as PID 1 without --privileged: the host
// cgroup namespace with the cgroup tree mounted writable, which systemd
// needs to create its own scopes (under a private namespace Docker mounts
// it read-only and systemd exits at once), tmpfs for /run and /tmp, and
// the two network capabilities firewalld uses. Measured before this was
// written: with these, systemd reaches "running", sshd and firewalld are
// active, and firewall-cmd adds and reloads rules. The cost worth naming is
// that the container can write the Docker host's cgroup tree, which
// --privileged would also allow, alongside every device and capability.
func systemdDevice(hc *container.HostConfig) {
	hc.CgroupnsMode = container.CgroupnsModeHost
	hc.Binds = append(hc.Binds, "/sys/fs/cgroup:/sys/fs/cgroup:rw")
	hc.Tmpfs = map[string]string{"/run": "", "/tmp": ""}
	hc.CapAdd = append(hc.CapAdd, "NET_ADMIN", "NET_RAW")
}

// checkThenRun proves one call's check against its real run:
//
//  1. a check predicts a change and the device is as it was;
//  2. the run makes a change the device shows, and every value the check
//     predicted for after the run is what the run reports after it;
//  3. a check then predicts nothing to do, and a second run changes nothing.
//
// observe reads the state the call acts on, from inside the container.
func checkThenRun(t *testing.T, dir, home, host string, observe func() string, method string, params ...string) {
	t.Helper()
	call := append([]string{"adhoc", host, method}, params...)
	label := method + " " + strings.Join(params, " ")

	before := observe()
	check, raw, code := runPleiadesJSONWithHome(t, dir, home, append(call, "--mode", "check")...)
	if code != 0 || len(check.Tasks) != 1 || check.Tasks[0].Status != "would_change" {
		t.Fatalf("%s: the check before the run did not predict a change (exit %d):\n%s", label, code, raw)
	}
	if got := observe(); got != before {
		t.Fatalf("%s: the check changed the device: %q became %q", label, before, got)
	}

	run, raw, code := runPleiadesJSONWithHome(t, dir, home, call...)
	if code != 0 || len(run.Tasks) != 1 || run.Tasks[0].Status != "changed" {
		t.Fatalf("%s: the run did not change anything (exit %d):\n%s", label, code, raw)
	}
	if got := observe(); got == before {
		t.Fatalf("%s: the run reported a change the device does not show: still %q", label, got)
	}
	predictionMatches(t, label, check.Tasks[0].Stats, run.Tasks[0].Stats)

	again, raw, code := runPleiadesJSONWithHome(t, dir, home, append(call, "--mode", "check")...)
	if code != 0 || again.Tasks[0].Status != "ok" {
		t.Fatalf("%s: the check after the run still predicts a change, so a converged task would report changed forever (exit %d):\n%s", label, code, raw)
	}
	rerun, raw, code := runPleiadesJSONWithHome(t, dir, home, call...)
	if code != 0 || rerun.Tasks[0].Status != "ok" {
		t.Fatalf("%s: a second run changed something (exit %d):\n%s", label, code, raw)
	}
}

// predictionMatches holds a check's predicted after half to the run's: each
// value the prediction states must be the value the run reports. A
// prediction may leave a value out (dnf's unnamed version, which asking
// for would refresh the device's metadata); it may not state a wrong one.
func predictionMatches(t *testing.T, label string, checkStats, runStats map[string]any) {
	t.Helper()
	predicted := diffHalf(checkStats, "after")
	actual := diffHalf(runStats, "after")
	if len(predicted) == 0 {
		t.Fatalf("%s: the check carried no predicted after state: %v", label, checkStats)
	}
	for key, want := range predicted {
		if key == "predicted" {
			continue
		}
		if got, ok := actual[key]; !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the check predicted %s = %v, the run left %v", label, key, want, got)
		}
	}
}

// diffHalf returns stats' diff's before or after half, or nil.
func diffHalf(stats map[string]any, half string) map[string]any {
	diff, _ := stats["diff"].(map[string]any)
	out, _ := diff[half].(map[string]any)
	return out
}

// packageCalls is the sequence each package manager runs: its own install,
// remove and upgrade, then the generic methods that dispatch to them.
// upgrade of a package that is not installed installs it, as Ansible's
// state=latest does, which is the change a check can predict here.
func packageCalls(t *testing.T, dir, home, host, manager string, installed func(pkg string) string) {
	t.Helper()
	for _, step := range []struct{ method, pkg string }{
		{"pkg." + manager + ".install", "tree"},
		{"pkg." + manager + ".remove", "tree"},
		{"pkg." + manager + ".upgrade", "tree"},
		{"pkg.install", "bc"},
		{"pkg.remove", "bc"},
		{"pkg.upgrade", "bc"},
	} {
		t.Run(step.method, func(t *testing.T) {
			checkThenRun(t, dir, home, host, func() string { return installed(step.pkg) }, step.method, "name="+step.pkg)
		})
	}
}

// TestCLI_AptChecksMatchTheirRealRuns runs every apt check against a real
// Debian device's apt and dpkg.
func TestCLI_AptChecksMatchTheirRealRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the apt check gate, which builds a Debian device and installs real packages, in short mode")
	}
	device := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	dir, home := device.manage(t, "debian", "linux_server,debian_family")
	packageCalls(t, dir, home, "debian", "apt", func(pkg string) string {
		out, _ := device.exec(t, "sh", "-c", "dpkg-query -W -f='${db:Status-Abbrev}${Version}' "+pkg+" 2>/dev/null || echo absent")
		return out
	})
}

// TestCLI_DnfAndFirewalldChecksMatchTheirRealRuns runs every dnf check and
// every firewalld check against a real Rocky Linux device, with firewalld
// running under systemd as it does on a real host.
func TestCLI_DnfAndFirewalldChecksMatchTheirRealRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the dnf and firewalld check gate, which builds a Rocky Linux device under systemd, in short mode")
	}
	device := startTestdataDevice(t, "rocky-systemd-sshd", rockyGatePassword, systemdDevice)
	dir, home := device.manage(t, "rocky", "linux_server,rhel_family", "firewalld=true")

	t.Run("dnf", func(t *testing.T) {
		packageCalls(t, dir, home, "rocky", "dnf", func(pkg string) string {
			out, _ := device.exec(t, "sh", "-c", "rpm -q "+pkg+" || true")
			return out
		})
	})

	// firewalld starts after sshd, so wait for it to answer before asking
	// it anything.
	deadline := time.Now().Add(deviceSettleTimeout)
	for {
		if state, _ := device.exec(t, "firewall-cmd", "--state"); state == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("firewalld did not start within %v", deviceSettleTimeout)
		}
		time.Sleep(time.Second)
	}
	// rules reads the zone's ports and services, runtime and permanent,
	// one firewall-cmd call each (it refuses two --list options at once),
	// and fails on any error, so a broken read can never pass as a state.
	rules := func() string {
		var parts []string
		for _, args := range [][]string{
			{"firewall-cmd", "--list-ports"},
			{"firewall-cmd", "--list-services"},
			{"firewall-cmd", "--permanent", "--list-ports"},
			{"firewall-cmd", "--permanent", "--list-services"},
		} {
			out, code := device.exec(t, args...)
			if code != 0 {
				t.Fatalf("%v failed (exit %d): %s", args, code, out)
			}
			parts = append(parts, strings.Join(args[1:], " ")+": "+out)
		}
		return strings.Join(parts, " | ")
	}

	t.Run("firewalld", func(t *testing.T) {
		for _, step := range []struct {
			method string
			params []string
		}{
			{"fw.firewalld.allow", []string{"port=8443"}},
			{"fw.firewalld.deny", []string{"port=8443"}},
			{"fw.firewalld.allow", []string{"service=https"}},
			{"fw.firewalld.deny", []string{"service=https"}},
		} {
			t.Run(step.method+" "+strings.Join(step.params, " "), func(t *testing.T) {
				checkThenRun(t, dir, home, "rocky", rules, step.method, step.params...)
			})
		}

		// A reload always reports a change, so it cannot converge: its proof
		// is that the check predicts the reload and moves nothing, and the
		// run then drops a rule that was only in the runtime configuration.
		t.Run("fw.firewalld.reload", func(t *testing.T) {
			if out, code := device.exec(t, "firewall-cmd", "--add-port=9999/tcp"); code != 0 {
				t.Fatalf("adding a runtime-only rule: %s", out)
			}
			before := rules()
			check, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "rocky", "fw.firewalld.reload", "--mode", "check")
			if code != 0 || check.Tasks[0].Status != "would_change" {
				t.Fatalf("the reload check did not predict the reload (exit %d):\n%s", code, raw)
			}
			if got := rules(); got != before {
				t.Fatalf("the reload check changed the rules: %q became %q", before, got)
			}
			run, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "rocky", "fw.firewalld.reload")
			if code != 0 || run.Tasks[0].Status != "changed" {
				t.Fatalf("the reload did not run (exit %d):\n%s", code, raw)
			}
			if got := rules(); strings.Contains(got, "9999/tcp") {
				t.Fatalf("the reload left the runtime-only rule in place: %s", got)
			}
		})
	})
}
