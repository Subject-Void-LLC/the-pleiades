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
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
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

// labFileKey is path's file fact key, its slashes, percent signs and spaces
// percent-encoded as the method encodes them (the paths here hold nothing
// else a key cannot).
func labFileKey(path string) string {
	r := strings.NewReplacer("%", "%25", "/", "%2F", " ", "%20")
	return "file." + r.Replace(path)
}

// labPrivateFile writes a file only its owner may read under dir on m, as the
// gate's login, removed when the test ends, and returns its path.
func labPrivateFile(t *testing.T, m labMachine, dir string) string {
	t.Helper()
	path := dir + "/pleiades-gate-private"
	if got := labShell(t, m, "umask 077 && printf 'pin1234\\n' > "+path+" && echo written"); got != "written" {
		t.Fatalf("writing %s printed %q", path, got)
	}
	t.Cleanup(func() { labShell(t, m, "rm -f "+path+" && echo removed") })
	return path
}

// labHoldsKeyedDigest holds the file fact key to a private file's shape: the
// project's keyed digest alone, with no plain or raw digest and no size.
func labHoldsKeyedDigest(t *testing.T, stats map[string]any, key string) {
	t.Helper()
	f := labItem(t, stats, key)
	digest, _ := f["hmac_sha256"].(string)
	id, _ := f["hmac_key"].(string)
	if len(digest) != 64 || !strings.HasPrefix(id, "project:") || f["world_readable"] != false ||
		f["sha256"] != nil || f["sha256_raw"] != nil || f["size"] != nil {
		t.Errorf("%s = %v, want the project's keyed digest alone", key, f)
	}
}

// labChangeShows changes the private file at path with change and gathers it
// again: its keyed digest must change, so the digest is of the file's
// content and a keyed digest shows drift as a plain one does.
func labChangeShows(t *testing.T, m labMachine, stats map[string]any, path string, change func()) {
	t.Helper()
	labChangeShowsAt(t, m, stats, labFileKey(path), path, change)
}

// labChangeShowsAt is labChangeShows for a fact key the caller encodes.
func labChangeShowsAt(t *testing.T, m labMachine, stats map[string]any, key, path string, change func()) {
	t.Helper()
	before, _ := stats[key].(map[string]any)
	change()
	after, _ := labGather(t, m, path)[key].(map[string]any)
	if after == nil || after["hmac_sha256"] == nil || after["hmac_sha256"] == before["hmac_sha256"] {
		t.Errorf("%s after a change = %v, before %v: want a new keyed digest", key, after, before)
	}
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
	mine := labPrivateFile(t, m, "/home/"+labShell(t, m, "id -un"))
	stats := labGatherAll(t, m, "/etc/hosts", "/root/.cshrc", "/nonexistent", "/etc/master.passwd", mine)
	if stats["os.kernel.name"] != "freebsd" || stats["os.distribution"] != "freebsd" || stats["os.family"] != "freebsd" {
		t.Errorf("system %v, distribution %v, family %v, want the native freebsd", stats["os.kernel.name"], stats["os.distribution"], stats["os.family"])
	}
	// The mapping gives what Ansible reports for the same machine.
	if a := ansiblefacts.ToAnsible(stats); a["ansible_system"] != "FreeBSD" || a["ansible_distribution"] != "FreeBSD" || a["ansible_os_family"] != "FreeBSD" {
		t.Errorf("mapped to Ansible: system %v, distribution %v, family %v, want FreeBSD", a["ansible_system"], a["ansible_distribution"], a["ansible_os_family"])
	}
	if want := labAtoi(t, labShell(t, m, "sysctl -n hw.physmem")) / (1 << 20); numberStat(stats, "hardware.memory_mb") != want {
		t.Errorf("hardware.memory_mb = %v, sysctl says %d", stats["hardware.memory_mb"], want)
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
	if sshd, _ := stats["service.sshd"].(map[string]any); stats["service_state.sshd"] != "running" || sshd["enabled"] != true {
		t.Errorf("service.sshd = %v, state %v, want the running, enabled daemon this test logged in through", sshd, stats["service_state.sshd"])
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
	// Root's alone, which this login cannot read: present, described, no
	// digest of any kind.
	if f := labItem(t, stats, labFileKey("/etc/master.passwd")); f["state"] != "present" || f["world_readable"] != false || f["readable"] != false ||
		f["sha256"] != nil || f["hmac_sha256"] != nil || f["size"] != nil {
		t.Errorf("/etc/master.passwd = %v, want present with no digest", f)
	}
	labHoldsKeyedDigest(t, stats, labFileKey(mine))
	labChangeShows(t, m, stats, mine, func() { labShell(t, m, "printf 'pin5678\\n' > "+mine+" && echo written") })
}

func TestCLI_FactsGatherOnMacOSMatchesItsOwnTools(t *testing.T) {
	m := labMachine{os.Getenv(envMacProject), os.Getenv(envMacDevice)}
	if m.dir == "" || m.host == "" {
		t.Skipf("needs a Mac reached over SSH as generic_ssh: set %s and %s", envMacProject, envMacDevice)
	}
	if got := labShell(t, m, "uname -s"); got != "Darwin" {
		t.Fatalf("%s is %q, not Darwin", m.host, got)
	}
	mine := labPrivateFile(t, m, labShell(t, m, "echo $HOME"))
	stats := labGatherAll(t, m, "/etc/hosts", "/etc", "/private/var/root/.forward", "/nonexistent", mine)
	labHoldsKeyedDigest(t, stats, labFileKey(mine))
	labChangeShows(t, m, stats, mine, func() { labShell(t, m, "printf 'pin5678\\n' > "+mine+" && echo written") })
	if stats["os.distribution"] != "macos" || stats["os.family"] != "macos" || stats["os.kernel.name"] != "darwin" || stats["os.version"] != labShell(t, m, "sw_vers -productVersion") {
		t.Errorf("distribution %v %v, family %v, kernel %v", stats["os.distribution"], stats["os.version"], stats["os.family"], stats["os.kernel.name"])
	}
	if a := ansiblefacts.ToAnsible(stats); a["ansible_distribution"] != "MacOSX" || a["ansible_os_family"] != "Darwin" || a["ansible_system"] != "Darwin" {
		t.Errorf("mapped to Ansible: distribution %v, family %v, system %v, want MacOSX, Darwin, Darwin", a["ansible_distribution"], a["ansible_os_family"], a["ansible_system"])
	}
	// Ansible reports no swap on a Mac, whose swap files come and go.
	if v, ok := stats["hardware.swap_mb"]; ok {
		t.Errorf("hardware.swap_mb = %v on a Mac", v)
	}
	if want := labAtoi(t, labShell(t, m, "sysctl -n hw.memsize")) / (1 << 20); numberStat(stats, "hardware.memory_mb") != want {
		t.Errorf("hardware.memory_mb = %v, sysctl says %d", stats["hardware.memory_mb"], want)
	}
	if want := labAtoi(t, labShell(t, m, "pkgutil --pkgs | wc -l")); labCount(stats, "package.", "pkgutil") != want {
		t.Errorf("%d pkgutil facts, pkgutil lists %d receipts", labCount(stats, "package.", "pkgutil"), want)
	}
	brew := labShell(t, m, `for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do [ -x "$b" ] && { HOMEBREW_NO_AUTO_UPDATE=1 "$b" list --formula | wc -l; break; }; done`)
	if brew != "" && labCount(stats, "package.", "brew") != labAtoi(t, brew) {
		t.Errorf("%d brew facts, brew lists %s formulae", labCount(stats, "package.", "brew"), brew)
	}
	// Every job launchd lists but those it makes per connection or session,
	// whose label ends in a UUID; and beside them only daemons it never
	// loaded because they are disabled (FAILURE_PATTERNS 935).
	listed := map[string]bool{}
	for _, label := range strings.Fields(labShell(t, m, `launchctl print system | awk '/^\tservices = \{/{f=1;next} f&&/^\t\}/{exit} f{print $NF}' | grep -vE '\.[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$'`)) {
		listed[label] = true
		if _, ok := stats["service."+label]; !ok {
			t.Errorf("launchd lists %s and no service fact names it", label)
		}
	}
	for key, v := range stats {
		label, ok := strings.CutPrefix(key, "service.")
		if ok && !listed[label] {
			if f, _ := v.(map[string]any); f["enabled"] != false {
				t.Errorf("%s = %v, which launchd does not list and is not a disabled daemon", key, v)
			}
		}
	}
	if len(listed) == 0 {
		t.Error("launchd listed no daemon")
	}
	// Phase 41c-2. TCP listeners, counted against netstat: a UDP endpoint
	// may open or close between the gather and the count, a listener rarely.
	listeners := labAtoi(t, labShell(t, m, `netstat -anvl -p tcp | awk '$6 == "LISTEN"' | wc -l`))
	if got := labCount(stats, "port.tcp:", ""); got != listeners || got == 0 {
		t.Errorf("%d TCP port facts, netstat lists %d listeners", got, listeners)
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

// labServiceTypes reads every service's registry Type into $t, by name.
const labServiceTypes = `$t = @{}; foreach ($k in @(Get-ChildItem -LiteralPath 'HKLM:\SYSTEM\CurrentControlSet\Services')) { $v = $k.GetValue('Type'); if ($null -ne $v) { $t[[string]$k.PSChildName] = [int]$v } }; `

// labServiceCount counts what the services subset reports on Windows: each
// service Get-Service lists but an instance of a per-user service (Type
// 0x80), and each per-user template (Type 0x40 without 0x80).
const labServiceCount = labServiceTypes + `@(Get-Service | Where-Object { -not ($t[$_.Name] -band 0x80) }).Count + @($t.Keys | Where-Object { ($t[$_] -band 0x40) -and -not ($t[$_] -band 0x80) }).Count`

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

	// A file in the login's own profile, which BUILTIN\Users may not read.
	profile := labPowerShell(t, m, `$env:USERPROFILE`)
	winMine := profile + `\pleiades-gate-private.txt`
	if got := labPowerShell(t, m, `Set-Content -LiteralPath '`+winMine+`' -Value 'pin1234'; 'written'`); got != "written" {
		t.Fatalf("writing %s printed %q", winMine, got)
	}
	t.Cleanup(func() { labPowerShell(t, m, `Remove-Item -LiteralPath '`+winMine+`' -Force; 'removed'`) })
	stats := labGather(t, m, `C:\Windows\win.ini`, `C:\pagefile.sys`, `C:\nonexistent\x`, winMine)
	labHoldsKeyedDigest(t, stats, "file."+strings.ReplaceAll(winMine, " ", "%20"))
	labChangeShowsAt(t, m, stats, "file."+strings.ReplaceAll(winMine, " ", "%20"), winMine,
		func() { labPowerShell(t, m, `Set-Content -LiteralPath '`+winMine+`' -Value 'pin5678'; 'written'`) })
	if stats["os.kernel.name"] != "windows" || stats["os.distribution"] != "windows" || stats["host.name"] != labPowerShell(t, m, `$env:COMPUTERNAME`) {
		t.Errorf("system %v, distribution %v, host name %v", stats["os.kernel.name"], stats["os.distribution"], stats["host.name"])
	}
	// The caption is the edition, natively; Ansible reports it as the distribution.
	if caption := labPowerShell(t, m, `(Get-CimInstance Win32_OperatingSystem).Caption`); stats["os.edition"] != caption {
		t.Errorf("os.edition = %v, the host says %q", stats["os.edition"], caption)
	}
	if a := ansiblefacts.ToAnsible(stats); a["ansible_system"] != "Win32NT" || a["ansible_os_family"] != "Windows" || a["ansible_distribution"] != stats["os.edition"] || fmt.Sprint(a["ansible_swaptotal_mb"]) != "0" {
		t.Errorf("mapped to Ansible: system %v, family %v, distribution %v, swap %v", a["ansible_system"], a["ansible_os_family"], a["ansible_distribution"], a["ansible_swaptotal_mb"])
	}
	if want := labPowerShell(t, m, `[Environment]::OSVersion.Version.ToString()`); stats["os.kernel.release"] != want || stats["os.version"] != want {
		t.Errorf("kernel %v, version %v, the host says %s", stats["os.kernel.release"], stats["os.version"], want)
	}
	// ansible.windows' setup: the family Windows, and swap 0, since a page
	// file is not swap.
	if _, swap := stats["hardware.swap_mb"]; stats["os.family"] != "windows" || swap {
		t.Errorf("family %v, swap %v, want windows and no swap fact (natively absent)", stats["os.family"], stats["hardware.swap_mb"])
	}
	// Rounded up, as ansible.windows' setup rounds it.
	if want := labAtoi(t, labPowerShell(t, m, `[int64][math]::Floor((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory / 1MB)`)); numberStat(stats, "hardware.memory_mb") != want {
		t.Errorf("hardware.memory_mb = %v, the host says %d", stats["hardware.memory_mb"], want)
	}
	if want := labAtoi(t, labPowerShell(t, m, `[Environment]::ProcessorCount`)); numberStat(stats, "hardware.cpu.threads") != want {
		t.Errorf("hardware.cpu.threads = %v, the host says %d", stats["hardware.cpu.threads"], want)
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

	// Every service Get-Service lists but a signed-in user's instance of a
	// per-user service, and each per-user template, which it never lists.
	if want := labAtoi(t, labPowerShell(t, m, labServiceCount)); labCount(stats, "service.", "scm") != want {
		t.Errorf("%d service facts, the host lists %d services and per-user templates", labCount(stats, "service.", "scm"), want)
	}
	if winrm, _ := stats["service.WinRM"].(map[string]any); stats["service_state.WinRM"] != "running" || winrm["enabled"] != true {
		t.Errorf("service.WinRM = %v, state %v, want the running, enabled service this test came in through", winrm, stats["service_state.WinRM"])
	}
	// Phase 41c-2.
	// TCP listeners only: a UDP endpoint may come and go between the two reads.
	if want := labAtoi(t, labPowerShell(t, m, `@(Get-NetTCPConnection -State Listen).Count`)); labCount(stats, "port.tcp:", "") != want {
		t.Errorf("%d TCP port facts, the host lists %d listeners", labCount(stats, "port.tcp:", ""), want)
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
	if f := labItem(t, stats, `file.C:\pagefile.sys`); f["state"] != "present" || f["sha256"] != nil {
		t.Errorf("C:\\pagefile.sys = %v, want present, with no digest", f)
	}
	if f := labItem(t, stats, `file.C:\nonexistent\x`); f["state"] != "absent" {
		t.Errorf("a missing path = %v", f)
	}
}
