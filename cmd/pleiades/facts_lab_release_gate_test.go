// facts.gather's FreeBSD, macOS and Windows subsets through the real binary
// against the lab's real machines (Phase 41c): each machine onboarded where
// its type is generic (which grants FactGathererCapable), every subset
// gathered, and what was gathered held to what the machine's own tools
// report. The file name names no platform, since a _freebsd, _darwin or
// _windows suffix would build it only there (FAILURE PATTERNS 374); each
// test skips unless its machine is configured.
package main_test

import (
	"encoding/json"
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

// labGatherAll onboards m and gathers every subset, files naming files,
// returning the task's stats (its facts).
func labGatherAll(t *testing.T, m labMachine, files ...string) map[string]any {
	t.Helper()
	// Run as the user runs it, with this environment's home and known_hosts.
	onboard := exec.Command(binPath, "onboard", m.host)
	onboard.Dir = m.dir
	if out, err := onboard.CombinedOutput(); err != nil {
		t.Fatalf("onboarding %s: %v\n%s", m.host, err, out)
	}
	return labGather(t, m, files...)
}

// labGather gathers every subset from m, files naming files, returning the
// task's stats.
func labGather(t *testing.T, m labMachine, files ...string) map[string]any {
	t.Helper()
	args := []string{"adhoc", m.host, "facts.gather", "gather_subset:=[all]"}
	if len(files) > 0 {
		// JSON is YAML: each path quoted, whatever it holds.
		raw, _ := json.Marshal(files)
		args = append(args, "files:="+string(raw))
	}
	rep, raw, code := runPleiadesJSONWithHome(t, m.dir, "", args...)
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

// labItem is stats[key] as an object, failing t when it is not one.
func labItem(t *testing.T, stats map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := stats[key].(map[string]any)
	if !ok {
		t.Errorf("no fact %s", key)
	}
	return v
}

// labFileKey is path's file fact key: each slash, percent sign, space or
// backslash-free byte the key cannot hold percent-encoded, as the method does.
func labFileKey(path string) string {
	r := strings.NewReplacer("%", "%25", "/", "%2F", " ", "%20")
	return "file." + r.Replace(path)
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
	stats := labGatherAll(t, m, "/etc/hosts", "/root/.cshrc", "/nonexistent")
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
	// Phase 41c-2. Every listener sockstat lists (a UDP one only unconnected).
	if want := labAtoi(t, labShell(t, m, `sockstat -46lq | awk '$5 ~ /^tcp/ || $7 == "*:*"' | wc -l`)); labCount(stats, "port.", "") != want {
		t.Errorf("%d port facts, sockstat lists %d listeners", labCount(stats, "port.", ""), want)
	}
	if p := labItem(t, stats, "port.tcp:0.0.0.0:22"); p["process"] != "sshd" {
		t.Errorf("port.tcp:0.0.0.0:22 = %v, want sshd", p)
	}
	if want := labAtoi(t, labShell(t, m, `grep -cv '^#' /etc/passwd`)); labCount(stats, "user.", "files") != want {
		t.Errorf("%d user facts, /etc/passwd lists %d accounts", labCount(stats, "user.", "files"), want)
	}
	if u := labItem(t, stats, "user.pleiades"); numberStat(u, "uid") != labAtoi(t, labShell(t, m, "id -u")) {
		t.Errorf("user.pleiades = %v", u)
	}
	if f := labItem(t, stats, labFileKey("/etc/hosts")); f["sha256"] != labShell(t, m, "sha256 -q /etc/hosts") || f["owner"] != "root" || f["group"] != "wheel" {
		t.Errorf("/etc/hosts = %v", f)
	}
	// /root is root's and wheel's; this login is in neither.
	if f := labItem(t, stats, labFileKey("/root/.cshrc")); f["state"] != "unreadable" {
		t.Errorf("/root/.cshrc = %v, want unreadable, never absent", f)
	}
	if f := labItem(t, stats, labFileKey("/nonexistent")); f["state"] != "absent" {
		t.Errorf("/nonexistent = %v", f)
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
	stats := labGatherAll(t, m, "/etc/hosts", "/etc", "/private/var/root/.forward", "/nonexistent")
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
	// Phase 41c-2. Every TCP listener and unconnected, bound UDP socket.
	listeners := labAtoi(t, labShell(t, m, `{ netstat -anv -p tcp | awk '$6 == "LISTEN"'; netstat -anv -p udp | awk '$1 ~ /^udp/ && $5 == "*.*" && $4 !~ /[.][*]$/'; } | wc -l`))
	if got := labCount(stats, "port.", ""); got != listeners || got == 0 {
		t.Errorf("%d port facts, netstat lists %d listeners", got, listeners)
	}
	if want := labAtoi(t, labShell(t, m, "dscl . -list /Users | wc -l")); labCount(stats, "user.", "dslocal") != want {
		t.Errorf("%d user facts, the local directory lists %d", labCount(stats, "user.", "dslocal"), want)
	}
	me := labShell(t, m, "id -un")
	if u := labItem(t, stats, "user."+me); numberStat(u, "uid") != labAtoi(t, labShell(t, m, "id -u")) || u["home"] != labShell(t, m, "echo $HOME") {
		t.Errorf("user.%s = %v", me, u)
	}
	digest := strings.Fields(labShell(t, m, "shasum -a 256 /etc/hosts"))[0]
	if f := labItem(t, stats, labFileKey("/etc/hosts")); f["type"] != "file" || f["sha256"] != digest {
		t.Errorf("/etc/hosts = %v, shasum says %s", f, digest)
	}
	// /etc is a link on a Mac, and is described as itself.
	if f := labItem(t, stats, labFileKey("/etc")); f["type"] != "link" || f["link_target"] != labShell(t, m, "readlink /etc") {
		t.Errorf("/etc = %v, want the link to private/etc as itself", f)
	}
	if f := labItem(t, stats, labFileKey("/private/var/root/.forward")); f["state"] != "unreadable" {
		t.Errorf("a file in root's home = %v, want unreadable, never absent", f)
	}
	if f := labItem(t, stats, labFileKey("/nonexistent")); f["state"] != "absent" {
		t.Errorf("/nonexistent = %v", f)
	}
}

// labPowerShell runs script on m through exec.winrm.shell and returns its
// trimmed stdout.
func labPowerShell(t *testing.T, m labMachine, script string) string {
	t.Helper()
	rep, raw, code := runPleiadesJSONWithHome(t, m.dir, "", "adhoc", m.host, "exec.winrm.shell", "shell=powershell", "command="+script)
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("%q on %s (exit %d):\n%s", script, m.host, code, raw)
	}
	return strings.TrimSpace(stringStat(rep.Tasks[0].Stats, "stdout"))
}

// The machine's own Uninstall keys, both views.
const labUninstallKeys = `'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall', 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall'`

func TestCLI_FactsGatherOnWindowsMatchesItsOwnTools(t *testing.T) {
	m := labMachine{os.Getenv(envWinRMProject), os.Getenv(envWinRMDevice)}
	if m.dir == "" || m.host == "" {
		t.Skipf("needs a Windows host managed as windows_server: set %s and %s", envWinRMProject, envWinRMDevice)
	}
	if strings.HasPrefix(m.dir, "~/") {
		home, _ := os.UserHomeDir()
		m.dir = home + m.dir[1:]
	}
	// Two programs registered as an installer registers one, in each view,
	// sharing a name with a character outside ASCII, so the real script
	// reads both views and the name survives the console; removed again
	// when the test ends.
	register := `$name = 'Pleiades facts gate ' + [char]0xe9; $i = 0; foreach ($k in @(` + labUninstallKeys + `)) { $i++; ` +
		`$p = Join-Path $k 'PleiadesFactsGate'; New-Item -Path $p -Force | Out-Null; ` +
		`New-ItemProperty -Path $p -Name DisplayName -Value $name -Force | Out-Null; ` +
		`New-ItemProperty -Path $p -Name DisplayVersion -Value ('1.2.' + $i) -Force | Out-Null }; 'registered'`
	t.Cleanup(func() {
		labPowerShell(t, m, `foreach ($k in @(`+labUninstallKeys+`)) { Remove-Item -LiteralPath (Join-Path $k 'PleiadesFactsGate') -Recurse -Force -ErrorAction SilentlyContinue }; 'removed'`)
	})
	if got := labPowerShell(t, m, register); got != "registered" {
		t.Fatalf("registering the gate's programs printed %q", got)
	}

	stats := labGather(t, m, `C:\Windows\win.ini`, `C:\pagefile.sys`, `C:\nonexistent\x`)
	if stats["ansible_system"] != "Win32NT" || stats["ansible_hostname"] != labPowerShell(t, m, `$env:COMPUTERNAME`) {
		t.Errorf("system %v, host name %v", stats["ansible_system"], stats["ansible_hostname"])
	}
	if want := labPowerShell(t, m, `[Environment]::OSVersion.Version.ToString()`); stats["ansible_kernel"] != want || stats["ansible_distribution_version"] != want {
		t.Errorf("kernel %v, distribution version %v, the host says %s", stats["ansible_kernel"], stats["ansible_distribution_version"], want)
	}
	if want := labAtoi(t, labPowerShell(t, m, `[int64][math]::Floor((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory / 1MB)`)); numberStat(stats, "ansible_memtotal_mb") != want {
		t.Errorf("ansible_memtotal_mb = %v, the host says %d", stats["ansible_memtotal_mb"], want)
	}
	if want := labAtoi(t, labPowerShell(t, m, `[Environment]::ProcessorCount`)); numberStat(stats, "ansible_processor_vcpus") != want {
		t.Errorf("ansible_processor_vcpus = %v, the host says %d", stats["ansible_processor_vcpus"], want)
	}

	named := labAtoi(t, labPowerShell(t, m, `@(foreach ($k in @(`+labUninstallKeys+`)) { if (Test-Path $k) { Get-ItemProperty (Join-Path $k '*') -ErrorAction SilentlyContinue | Where-Object DisplayName } }).Count`))
	if got := labCount(stats, "package.", "registry"); got != named || named < 2 {
		t.Errorf("%d package facts, the registry names %d programs", got, named)
	}
	key := "package.Pleiades%20facts%20gate%20\u00e9"
	if native, _ := stats[key].(map[string]any); native["version"] != "1.2.1" || native["arch"] != "amd64" {
		t.Errorf("%s = %v, want 1.2.1 from the native view", key, native)
	}
	if wow, _ := stats[key+":x86"].(map[string]any); wow["version"] != "1.2.2" {
		t.Errorf("%s:x86 = %v, want 1.2.2 from the 32-bit view", key, wow)
	}

	if want := labAtoi(t, labPowerShell(t, m, `@(Get-Service).Count`)); labCount(stats, "service.", "scm") != want {
		t.Errorf("%d service facts, Get-Service lists %d", labCount(stats, "service.", "scm"), want)
	}
	if winrm, _ := stats["service.WinRM"].(map[string]any); winrm["state"] != "running" || winrm["enabled"] != true {
		t.Errorf("service.WinRM = %v, want the running, enabled service this test came in through", winrm)
	}
	// Phase 41c-2.
	if want := labAtoi(t, labPowerShell(t, m, `@(Get-NetTCPConnection -State Listen).Count + @(Get-NetUDPEndpoint).Count`)); labCount(stats, "port.", "") != want {
		t.Errorf("%d port facts, the host lists %d listeners", labCount(stats, "port.", ""), want)
	}
	if p := labItem(t, stats, "port.tcp:[::]:5985"); p["process"] != "System" {
		t.Errorf("WinRM's listener = %v", p)
	}
	if want := labAtoi(t, labPowerShell(t, m, `@(Get-LocalUser).Count`)); labCount(stats, "user.", "sam") != want {
		t.Errorf("%d user facts, the host lists %d accounts", labCount(stats, "user.", "sam"), want)
	}
	if want := labAtoi(t, labPowerShell(t, m, `@(Get-LocalGroup).Count`)); labCount(stats, "group.", "sam") != want {
		t.Errorf("%d group facts, the host lists %d groups", labCount(stats, "group.", "sam"), want)
	}
	if a := labItem(t, stats, "user.Administrator"); a["enabled"] != true {
		t.Errorf("user.Administrator = %v", a)
	}
	digest := strings.ToLower(labPowerShell(t, m, `(Get-FileHash -LiteralPath 'C:\Windows\win.ini' -Algorithm SHA256).Hash`))
	if f := labItem(t, stats, `file.C:\Windows\win.ini`); f["sha256"] != digest || f["state"] != "present" {
		t.Errorf("win.ini = %v, the host's digest is %s", f, digest)
	}
	// The page file is there, and PowerShell's Get-Item says it is not:
	// this is the answer that must not be absent.
	if f := labItem(t, stats, `file.C:\pagefile.sys`); f["state"] != "present" || f["readable"] != false {
		t.Errorf("C:\\pagefile.sys = %v, want present and not readable", f)
	}
	if f := labItem(t, stats, `file.C:\nonexistent\x`); f["state"] != "absent" {
		t.Errorf("a missing path = %v", f)
	}
}
