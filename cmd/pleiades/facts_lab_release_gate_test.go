// facts.gather's FreeBSD and macOS subsets through the real binary against
// the lab's real machines (Phase 41c): each machine onboarded (which grants
// FactGathererCapable), every subset gathered, and what was gathered held to
// what the machine's own tools report. The file name names no platform,
// since a _freebsd or _darwin suffix would build it only there (FAILURE
// PATTERNS 374); each test skips unless its machine is configured.
package main_test

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// PLEIADES_MAC_PROJECT and PLEIADES_MAC_DEVICE name a project that manages
// a Mac over SSH, as generic_ssh, and the Mac's inventory name there.
const (
	envMacProject = "PLEIADES_MAC_PROJECT"
	envMacDevice  = "PLEIADES_MAC_DEVICE"
)

// labMachine is a configured lab machine: its project and inventory name.
type labMachine struct{ dir, host string }

// labShell runs cmd on m through exec.shell and returns its trimmed stdout.
func labShell(t *testing.T, m labMachine, cmd string) string {
	t.Helper()
	rep, raw, code := runPleiadesJSONWithHome(t, m.dir, "", "adhoc", m.host, "exec.shell", "cmd="+cmd)
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("%q on %s (exit %d):\n%s", cmd, m.host, code, raw)
	}
	return strings.TrimSpace(stringStat(rep.Tasks[0].Stats, "stdout"))
}

// labGatherAll onboards m and gathers every subset, returning the task's
// stats (its facts).
func labGatherAll(t *testing.T, m labMachine) map[string]any {
	t.Helper()
	// Run as the user runs it, with this environment's home and known_hosts.
	onboard := exec.Command(binPath, "onboard", m.host)
	onboard.Dir = m.dir
	if out, err := onboard.CombinedOutput(); err != nil {
		t.Fatalf("onboarding %s: %v\n%s", m.host, err, out)
	}
	rep, raw, code := runPleiadesJSONWithHome(t, m.dir, "", "adhoc", m.host, "facts.gather", "gather_subset:=[all]")
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("facts.gather on %s (exit %d):\n%s", m.host, code, raw)
	}
	return rep.Tasks[0].Stats
}

// labCount is how many of stats' keys start with prefix and, when source is
// set, come from it.
func labCount(stats map[string]any, prefix, source string) int {
	n := 0
	for k, v := range stats {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if m, _ := v.(map[string]any); source == "" || m["source"] == source {
			n++
		}
	}
	return n
}

// numberStat is stats[key] as a whole number (JSON gives a float64), or -1.
func numberStat(stats map[string]any, key string) int {
	if f, ok := stats[key].(float64); ok {
		return int(f)
	}
	return -1
}

// labAtoi is s as a number, failing t when it is not one.
func labAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("%q is not a number: %v", s, err)
	}
	return n
}

func TestCLI_FactsGatherOnFreeBSDMatchesItsOwnTools(t *testing.T) {
	m := labMachine{os.Getenv(envBSDProject), os.Getenv(envBSDDevice)}
	if m.dir == "" || m.host == "" {
		t.Skipf("needs a FreeBSD host onboarded as generic_ssh: set %s and %s", envBSDProject, envBSDDevice)
	}
	if got := labShell(t, m, "uname -s"); got != "FreeBSD" {
		t.Fatalf("%s is %q, not FreeBSD", m.host, got)
	}
	stats := labGatherAll(t, m)
	if stats["ansible_system"] != "FreeBSD" || stats["ansible_distribution"] != "FreeBSD" {
		t.Errorf("system %v, distribution %v", stats["ansible_system"], stats["ansible_distribution"])
	}
	if want := labAtoi(t, labShell(t, m, "sysctl -n hw.physmem")) / (1 << 20); numberStat(stats, "ansible_memtotal_mb") != want {
		t.Errorf("ansible_memtotal_mb = %v, sysctl says %d", stats["ansible_memtotal_mb"], want)
	}
	if want := labAtoi(t, labShell(t, m, "pkg query %n | wc -l")); labCount(stats, "package.", "pkg") != want || want == 0 {
		t.Errorf("%d package facts, pkg lists %d", labCount(stats, "package.", "pkg"), want)
	}
	first := strings.Fields(labShell(t, m, `pkg query '%n %v' | head -1`))
	if v, _ := stats["package."+first[0]].(map[string]any); v["version"] != first[1] {
		t.Errorf("package.%s = %v, pkg says %s", first[0], v, first[1])
	}
	// Every rc script but rcorder's all-capitals placeholders.
	if want := labAtoi(t, labShell(t, m, `service -l | grep -c '[a-z]'`)); labCount(stats, "service.", "rc") != want {
		t.Errorf("%d service facts, rc lists %d scripts", labCount(stats, "service.", "rc"), want)
	}
	if sshd, _ := stats["service.sshd"].(map[string]any); sshd["state"] != "running" || sshd["enabled"] != true {
		t.Errorf("service.sshd = %v, want the running, enabled daemon this test logged in through", sshd)
	}
}

func TestCLI_FactsGatherOnMacOSMatchesItsOwnTools(t *testing.T) {
	m := labMachine{os.Getenv(envMacProject), os.Getenv(envMacDevice)}
	if m.dir == "" || m.host == "" {
		t.Skipf("needs a Mac reached over SSH as generic_ssh: set %s and %s", envMacProject, envMacDevice)
	}
	if got := labShell(t, m, "uname -s"); got != "Darwin" {
		t.Fatalf("%s is %q, not Darwin", m.host, got)
	}
	stats := labGatherAll(t, m)
	if stats["ansible_distribution"] != "MacOSX" || stats["ansible_distribution_version"] != labShell(t, m, "sw_vers -productVersion") {
		t.Errorf("distribution %v %v", stats["ansible_distribution"], stats["ansible_distribution_version"])
	}
	if want := labAtoi(t, labShell(t, m, "sysctl -n hw.memsize")) / (1 << 20); numberStat(stats, "ansible_memtotal_mb") != want {
		t.Errorf("ansible_memtotal_mb = %v, sysctl says %d", stats["ansible_memtotal_mb"], want)
	}
	if want := labAtoi(t, labShell(t, m, "pkgutil --pkgs | wc -l")); labCount(stats, "package.", "pkgutil") != want {
		t.Errorf("%d pkgutil facts, pkgutil lists %d receipts", labCount(stats, "package.", "pkgutil"), want)
	}
	brew := labShell(t, m, `for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do [ -x "$b" ] && { HOMEBREW_NO_AUTO_UPDATE=1 "$b" list --formula | wc -l; break; }; done`)
	if brew != "" && labCount(stats, "package.", "brew") != labAtoi(t, brew) {
		t.Errorf("%d brew facts, brew lists %s formulae", labCount(stats, "package.", "brew"), brew)
	}
	daemons := labAtoi(t, labShell(t, m, `launchctl print system | awk '/^\tservices = \{/{f=1;next} f&&/^\t\}/{exit} f' | wc -l`))
	if got := labCount(stats, "service.", "launchd"); got == 0 || got != daemons {
		t.Errorf("%d launchd facts, launchd lists %d daemons", got, daemons)
	}
}
