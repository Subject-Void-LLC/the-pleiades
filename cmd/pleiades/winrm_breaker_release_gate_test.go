// Package main_test: the Release Gate for the WinRM circuit breaker's waits
// (Phase 112), the real built binary against a real Windows host restarted
// for real, and a real local account with a real lockout threshold. It is
// env-gated for the reason every WinRM gate here is
// (winrm_service_feature_release_gate_test.go says it in full): there is no
// Windows container, and faking WinRM would fake the thing under test.
//
// It points at a project that already manages the host, through
// PLEIADES_WINRM_PROJECT and PLEIADES_WINRM_DEVICE, and leaves the host as
// it found it: an account it makes is removed, and the lockout policy it
// sets is put back to what it read, whether the test passes or not.
package main_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// envWinRMMeasureReboots asks TestWinRMGate_MeasureTheBootStatusSequence
// for that many restarts of the host. It is its own opt-in because each
// restart takes the host away for a minute or more.
const envWinRMMeasureReboots = "PLEIADES_WINRM_MEASURE_REBOOTS"

// envWinRMVBoxHost names the vbox_host device the Windows host runs on as
// a VirtualBox VM of the same name. When it is set, a restart the guest
// never comes back from (the lab's hypervisor sometimes hangs a boot) is
// power-cycled through virt.vbox.vm.stop and start and recorded as hung,
// rather than ending the measurement.
const envWinRMVBoxHost = "PLEIADES_WINRM_VBOX_HOST"

// winrmBreakerTarget is the project and device the gates run against, or a
// skip naming what to set.
func winrmBreakerTarget(t *testing.T) (dir, device string) {
	t.Helper()
	dir, device = os.Getenv(envWinRMProject), os.Getenv(envWinRMDevice)
	if dir == "" || device == "" {
		t.Skipf("this Release Gate needs a real Windows host it may restart: set %s and %s to a project that "+
			"manages it (the VirtualBox lab's win-check)", envWinRMProject, envWinRMDevice)
	}
	return dir, device
}

// inventoryHost reads device's address from the project's inventory, so a
// throwaway project can reach the same host under another account.
func inventoryHost(t *testing.T, dir, device string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "inventory.yaml"))
	if err != nil {
		t.Fatalf("reading the inventory: %v", err)
	}
	var inv struct {
		Hosts []struct {
			Name       string         `yaml:"name"`
			Properties map[string]any `yaml:"properties"`
		} `yaml:"hosts"`
	}
	if err := yaml.Unmarshal(raw, &inv); err != nil {
		t.Fatalf("parsing the inventory: %v", err)
	}
	for _, h := range inv.Hosts {
		if h.Name == device {
			if host, ok := h.Properties["host"].(string); ok && host != "" {
				if port, ok := h.Properties["port"]; ok && fmt.Sprint(port) != strconv.Itoa(winrmexec.DefaultPort) {
					t.Skipf("%s listens on port %v; these gates use a password account over the default HTTP listener", device, port)
				}
				return host
			}
		}
	}
	t.Fatalf("%s has no host property in %s", device, dir)
	return ""
}

// throwawayAccount makes a local account on the host, a member of Remote
// Management Users and nothing more, and removes it when the test ends. The
// lab host's WinRM service gives a shell only to Administrators (its
// RootSDDL), so the account's logons are judged and then refused a shell:
// a right password reads "Access is denied." after logging in, while a
// wrong one, or a locked account, is refused the logon with 401. That
// difference is all these gates need, and nothing about the host's security
// settings changes for it. Measured on win-check: an account outside
// Remote Management Users is refused with 401 even with its right
// password, so the membership is what makes the two tell apart. Its password reaches the host as a file copied by
// win.file.copy, which journals only the file's path and digest, and the
// script that makes the account deletes the file before anything else.
func throwawayAccount(t *testing.T, dir, device string) (user, password string) {
	t.Helper()
	user = "pltest" + randomHex(t, 3)
	password = "Pl-" + randomHex(t, 12) + "aZ9!"
	name := "pltest-" + randomHex(t, 4) + ".txt"
	local := filepath.Join(dir, "files", name)
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(password), 0o600); err != nil {
		t.Fatal(err)
	}
	// Gone from the project the moment it has been copied, and again here
	// in case the copy failed.
	defer func() { _ = os.Remove(local) }()
	remote := `C:\Windows\Temp\` + name
	winrmAdhoc(t, dir, device, "win.file.copy", "src=files/"+name, "path="+remote)
	_ = os.Remove(local)

	t.Cleanup(func() {
		rep, raw, code := runPleiadesJSONWithHome(t, dir, "", "adhoc", device, "exec.winrm.shell", "shell=powershell",
			"command=Remove-LocalUser -Name '"+user+"' -ErrorAction SilentlyContinue; if (Get-LocalUser -Name '"+user+"' -ErrorAction SilentlyContinue) { exit 1 }")
		if code != 0 || len(rep.Tasks) != 1 {
			t.Errorf("removing the throwaway account %s (exit %d); remove it by hand:\n%s", user, code, raw)
		}
	})
	winrmAdhoc(t, dir, device, "exec.winrm.shell", "shell=powershell", "command=$ErrorActionPreference = 'Stop'; "+
		"$f = '"+remote+"'; try { $p = (Get-Content -Raw -LiteralPath $f).Trim() } finally { Remove-Item -LiteralPath $f -Force -ErrorAction SilentlyContinue }; "+
		"$s = ConvertTo-SecureString $p -AsPlainText -Force; "+
		"New-LocalUser -Name '"+user+"' -Password $s -PasswordNeverExpires -AccountNeverExpires | Out-Null; "+
		"Add-LocalGroupMember -Group 'Remote Management Users' -Member '"+user+"'")
	return user, password
}

// lockoutPolicy is the host's account lockout settings, from net accounts.
type lockoutPolicy struct{ threshold, duration, window int }

// readLockoutPolicy reads the host's lockout settings.
func readLockoutPolicy(t *testing.T, dir, device string) lockoutPolicy {
	t.Helper()
	out := winrmRead(t, dir, device, "net accounts")
	var p lockoutPolicy
	found := 0
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		n, err := strconv.Atoi(value)
		if strings.EqualFold(value, "Never") {
			n, err = 0, nil
		}
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Lockout threshold":
			p.threshold, found = n, found+1
		case "Lockout duration (minutes)":
			p.duration, found = n, found+1
		case "Lockout observation window (minutes)":
			p.window, found = n, found+1
		}
	}
	if found != 3 {
		t.Fatalf("net accounts did not name all three lockout settings:\n%s", out)
	}
	return p
}

// setLockoutPolicy writes the host's lockout settings.
func setLockoutPolicy(t *testing.T, dir, device string, p lockoutPolicy) {
	t.Helper()
	winrmAdhoc(t, dir, device, "exec.winrm.shell", "shell=none", fmt.Sprintf(
		`command=C:\Windows\System32\net.exe accounts /lockoutthreshold:%d /lockoutwindow:%d /lockoutduration:%d`,
		p.threshold, p.window, p.duration))
}

// badPasswordAttempts reads the host's own count of failed logons for user
// since its last good one.
func badPasswordAttempts(t *testing.T, dir, device, user string) int {
	t.Helper()
	out := winrmRead(t, dir, device, "([ADSI]'WinNT://./"+user+",user').BadPasswordAttempts.Value")
	n, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("the bad password count for %s is %q, not a number", user, out)
	}
	return n
}

// TestWinRMGate_AWrongPasswordWaitSendsItOnce proves wait.connection with a
// wrong password sends it to the host once, so waiting cannot lock an
// account. The host's lockout threshold is set to 3 for the test, and the
// host itself counts: after two waits with the wrong password its count of
// failed logons for the account is exactly 2, and the right password still
// logs in. A wait that sent the password at every try would have locked
// the account in its first ten seconds. The control at the end proves the
// threshold is real: three wrong logons through ordinary calls do lock it.
func TestWinRMGate_AWrongPasswordWaitSendsItOnce(t *testing.T) {
	dir, device := winrmBreakerTarget(t)
	host := inventoryHost(t, dir, device)

	before := readLockoutPolicy(t, dir, device)
	t.Cleanup(func() { setLockoutPolicy(t, dir, device, before) })
	setLockoutPolicy(t, dir, device, lockoutPolicy{threshold: 3, duration: 30, window: 30})
	user, password := throwawayAccount(t, dir, device)

	wrong := winrmGateProject(t, winrmGateConfig{host: host, user: user, password: password + "-wrong"})
	right := winrmGateProject(t, winrmGateConfig{host: host, user: user, password: password})
	if got := badPasswordAttempts(t, dir, device, user); got != 0 {
		t.Fatalf("a new account starts with %d failed logons, want 0", got)
	}

	for i := 1; i <= 2; i++ {
		start := time.Now()
		rep, raw, code := runPleiadesJSONWithHome(t, wrong, "", "adhoc", "win-gate", "wait.connection", "timeout=120", "sleep=2")
		if code == 0 || len(rep.Tasks) != 1 {
			t.Fatalf("wait %d with the wrong password succeeded (exit %d):\n%s", i, code, raw)
		}
		if msg := rep.Tasks[0].Error; !strings.Contains(msg, "refused the credential") || !strings.Contains(msg, "stopped at try 1") {
			t.Errorf("wait %d ended with %q, want the refusal at its first try", i, msg)
		}
		if elapsed := time.Since(start); elapsed > 60*time.Second {
			t.Errorf("wait %d took %s; a refusal ends it at once, not at its timeout", i, elapsed)
		}
		if got := badPasswordAttempts(t, dir, device, user); got != i {
			t.Fatalf("after %d wait(s) the host counts %d failed logons for %s, want %d: each wait sends the password once", i, got, user, i)
		}
	}
	if got := rightPasswordAnswer(t, right); got != "authenticated" {
		t.Fatalf("the right password after two wrong waits was %s, want it to log in: the account was locked", got)
	}

	// The control: three wrong logons, by ordinary calls, lock it, so the
	// right password is refused too. Without it a host whose threshold was
	// not in force would pass the check above whatever the wait sent.
	for i := 0; i < 3; i++ {
		_, _, _ = runPleiadesJSONWithHome(t, wrong, "", "adhoc", "win-gate", "exec.winrm.shell", "shell=powershell", "command='x'")
	}
	if got := rightPasswordAnswer(t, right); got != "refused" {
		t.Fatalf("after three wrong logons the right password was %s, want it refused: the threshold this gate relies on is not in force", got)
	}
}

// rightPasswordAnswer runs one command with the throwaway account's right
// password and says what the host made of the logon: "authenticated" when
// it logged in and was refused a shell (the lab host admits only
// Administrators), "refused" when the logon itself was refused, as for a
// locked account, or the error when it was anything else.
func rightPasswordAnswer(t *testing.T, project string) string {
	t.Helper()
	rep, raw, code := runPleiadesJSONWithHome(t, project, "", "adhoc", "win-gate", "exec.winrm.shell", "shell=powershell", "command='x'")
	if len(rep.Tasks) != 1 {
		t.Fatalf("the right-password call did not run (exit %d):\n%s", code, raw)
	}
	msg := rep.Tasks[0].Error
	switch {
	case code == 0, strings.Contains(msg, "Access is denied"):
		return "authenticated"
	case strings.Contains(msg, "refused the login"):
		return "refused"
	}
	return msg
}

// listenerStatus is what the host's WinRM listener answers a request with
// no credential right now, in a word: the HTTP status, or how the
// connection failed.
func listenerStatus(host string) string {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Post("http://"+net.JoinHostPort(host, strconv.Itoa(winrmexec.DefaultPort))+"/wsman", "application/soap+xml;charset=UTF-8", nil)
	switch {
	case err == nil:
		_ = resp.Body.Close()
		return strconv.Itoa(resp.StatusCode)
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "reset"
	case errors.Is(err, syscall.EHOSTUNREACH):
		return "unreachable"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "error"
}

// authStatus is what one authenticated command answers right now, in a
// word: ok (the logon was accepted, whether or not a shell followed), 401
// (the logon was refused), or the failure's start. Each is a
// fresh circuit, so the measurement is never the breaker's own refusal.
func authStatus(target winrmexec.Target, auth winrmexec.Auth) string {
	defer winrmexec.ResetCircuitsForTest()()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := winrmexec.Run(ctx, target, auth, winrmexec.ShellPowerShell, "'up'", winrmexec.Options{Timeout: 10 * time.Second})
	switch {
	case err == nil && strings.TrimSpace(res.Stdout) == "up":
		return "ok"
	case err == nil:
		return "exit" + strconv.Itoa(res.ExitCode)
	case strings.Contains(err.Error(), "Access is denied"):
		// Logged in, and refused a shell: the lab host admits only
		// Administrators. The credential was accepted.
		return "ok"
	case strings.Contains(err.Error(), "refused the login"), strings.Contains(err.Error(), "401"):
		return "401"
	}
	msg := err.Error()
	if len(msg) > 60 {
		msg = msg[:60]
	}
	return "err(" + msg + ")"
}

// statusRun is one status seen for a while: how many looks in a row, from
// when.
type statusRun struct {
	status string
	from   time.Duration
	looks  int
}

// String is "status@from x looks".
func (r statusRun) String() string {
	return fmt.Sprintf("%s@%.1fs x%d", r.status, r.from.Seconds(), r.looks)
}

// TestWinRMGate_MeasureTheBootStatusSequence restarts the host as many times
// as PLEIADES_WINRM_MEASURE_REBOOTS says and records, for each restart, what
// the listener answers a request with no credential every half second
// until it is back, then what valid credentials get: the first try the
// moment the listener answers, and nine more a second apart. It answers
// whether Windows refuses valid credentials with a 401 while it boots, the
// question the wait's one-send rule turns on. The credentials are a
// throwaway account's, made and removed by the test.
func TestWinRMGate_MeasureTheBootStatusSequence(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv(envWinRMMeasureReboots))
	if n <= 0 {
		t.Skipf("set %s to the number of restarts to measure; each takes the host away for a minute or more", envWinRMMeasureReboots)
	}
	dir, device := winrmBreakerTarget(t)
	host := inventoryHost(t, dir, device)
	user, password := throwawayAccount(t, dir, device)
	target := winrmexec.Target{Host: host}
	auth := winrmexec.Auth{Username: user, Password: password}
	if got := authStatus(target, auth); got != "ok" {
		t.Fatalf("the throwaway account cannot log in before any restart: %s", got)
	}

	transient, hung := 0, 0
	vboxHost := os.Getenv(envWinRMVBoxHost)
	for i := 1; i <= n; i++ {
		_, _, _ = runPleiadesJSONWithHome(t, dir, "", "adhoc", device, "exec.winrm.shell", "shell=none",
			`command=C:\Windows\System32\shutdown.exe /r /t 0 /f /d p:4:1`)
		start := time.Now()
		var runs []statusRun
		down, cycled := false, false
		for time.Since(start) < 10*time.Minute {
			if !cycled && vboxHost != "" && time.Since(start) > 150*time.Second {
				// Not back in two and a half minutes, when a boot here takes
				// under one: a hung boot, which is the hypervisor's, not
				// Windows'.
				cycled = true
				hung++
				winrmAdhoc(t, dir, vboxHost, "virt.vbox.vm.stop", "name="+device, "mode=poweroff")
				winrmAdhoc(t, dir, vboxHost, "virt.vbox.vm.start", "name="+device)
				runs = append(runs, statusRun{status: "power-cycled", from: time.Since(start), looks: 1})
			}
			status := listenerStatus(host)
			if len(runs) > 0 && runs[len(runs)-1].status == status {
				runs[len(runs)-1].looks++
			} else {
				runs = append(runs, statusRun{status: status, from: time.Since(start), looks: 1})
			}
			if status != "401" {
				down = true
			}
			if down && status == "401" {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		back := time.Since(start)
		var auths []string
		for k := 0; k < 10; k++ {
			if k > 0 {
				time.Sleep(time.Second)
			}
			auths = append(auths, authStatus(target, auth))
		}
		if auths[0] != "ok" {
			transient++
		}
		var listened []string
		for _, r := range runs {
			listened = append(listened, r.String())
		}
		t.Logf("restart %d: listener %s; back at %.1fs; valid credentials then: %s", i, strings.Join(listened, " "), back.Seconds(), strings.Join(auths, " "))
		if cycled {
			// A cold boot after a power cut is not the warm restart this
			// measures, so its sequence is logged and not counted.
			if auths[0] != "ok" {
				transient--
			}
			continue
		}
		if !down || runs[len(runs)-1].status != "401" {
			t.Fatalf("restart %d: the host never went down and came back within ten minutes", i)
		}
		if auths[len(auths)-1] != "ok" {
			t.Fatalf("restart %d: valid credentials are still refused ten seconds after the listener answered", i)
		}
	}
	t.Logf("of %d restarts, %d hung and were power-cycled; of the other %d, %d refused or failed valid credentials at the moment the listener first answered",
		n, hung, n-hung, transient)
}

// outageRetries and outageDelay size the task during the outage: how many
// more tries it makes after its first, and the seconds between them. They
// come from the measured restart (TestWinRMGate_MeasureTheBootStatusSequence
// on win-check: silent from about two seconds after shutdown.exe until the
// listener answers at about fifteen, and still answering for up to three
// and a half seconds after it in one restart of twenty). Each try is
// bounded by one second: the tries while the host is still up take a
// fraction of one, five failures then open the circuit within about five
// seconds, and the refusals after it cost nothing, so the task ends well
// before the host is back however long it took to go down, and the wait
// ends inside the circuit's thirty-second cooldown. Too few tries were
// spent before the host went down at all in one run.
const (
	outageRetries = 25
	outageDelay   = 0
)

// rebootGateRunbook restarts the host, keeps calling it through the outage
// until the circuit refuses, waits for it, and runs one task after. The
// task during the outage never succeeds by design (its until never holds,
// and the host is down), and outageRetries and outageDelay are sized from
// the measured outage so it ends while the host is still away.
const rebootGateRunbook = `# Restarts the host, calls it through the outage, waits for it, and runs a
# task after the wait. Blast radius: the one host, restarted once. Changes:
# none that outlast the restart. Undo: none needed.
id: winrm-breaker-reboot
hosts: %s
tasks:
  - name: Restart the host now
    exec.winrm.shell:
      shell: none
      command: C:\Windows\System32\shutdown.exe /r /t 0 /f /d p:4:1
    ignore_errors: true
  - name: Call the host through the outage
    exec.winrm.shell:
      shell: powershell
      command: "'during'"
      timeout: 1
    register: during
    until:
      - "result.during.stdout == 'never'"
    retries: %d
    delay: %d
    ignore_errors: true
  - name: Wait for the host
    wait.connection:
      timeout: 600
      sleep: 2
  - name: Run at once after the wait
    exec.winrm.shell:
      shell: powershell
      command: "'after'"
`

// TestWinRMGate_RebootWaitLeavesTheNextTaskRunnable restarts the host for
// real and proves the two halves of the wait's design through one run:
//
//   - a second task during the outage fails fast: once five of its tries
//     have failed to reach the host, the rest are refused by the circuit,
//     saying nothing was sent, in well under a second each;
//   - the task after the wait runs at once: the wait, neither counted nor
//     refused, ends the cooldown when the listener answers and closes the
//     circuit when its command runs, so the next task is sent rather than
//     refused for the rest of a cooldown the task during the outage
//     started. Either half alone lets one task through (the first makes it
//     the probe); with both removed this gate fails, the task after the
//     wait refused as "circuit open" (measured), and the unit tests in
//     pkg/winrmexec hold each half on its own.
//
// The second half only bites when the wait ends inside that cooldown,
// within thirty seconds of the circuit last opening; the run says from its
// own timings whether it did, and fails when it did not, since a run that
// could not tell the two apart proves nothing.
func TestWinRMGate_RebootWaitLeavesTheNextTaskRunnable(t *testing.T) {
	dir, device := winrmBreakerTarget(t)
	rb := filepath.Join(t.TempDir(), "reboot.yaml")
	if err := os.WriteFile(rb, []byte(fmt.Sprintf(rebootGateRunbook, device, outageRetries, outageDelay)), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, raw, code := runPleiadesJSONWithHome(t, dir, "", "run", rb)
	if code != 0 {
		t.Fatalf("the run failed (exit %d):\n%s", code, raw)
	}

	var during []int
	wait, after := -1, -1
	for i, task := range rep.Tasks {
		switch task.Name {
		case "Call the host through the outage":
			during = append(during, i)
		case "Wait for the host":
			wait = i
		case "Run at once after the wait":
			after = i
		}
	}
	if len(during) == 0 || wait < 0 || after < 0 {
		t.Fatalf("the report does not hold every task:\n%s", raw)
	}

	refusals, lastOpen, failed := 0, time.Time{}, false
	for _, i := range during {
		task := rep.Tasks[i]
		if task.Error == "" && !failed {
			// The host had not gone down yet: shutdown.exe returns before
			// the WinRM service stops.
			continue
		}
		failed = true
		if task.Error == "" {
			t.Fatalf("a call during the outage succeeded at %s: the host came back before the task ended, so this run "+
				"cannot show the wait closing the circuit; shorten outageRetries:\n%s", task.FinishedAt, raw)
		}
		took := task.FinishedAt.Sub(*task.StartedAt)
		if strings.Contains(task.Error, "circuit open") {
			refusals++
			if !strings.Contains(task.Error, "nothing was sent") || took > time.Second {
				t.Errorf("a refusal said %q and took %s; it sends nothing and is at once", task.Error, took)
			}
			continue
		}
		lastOpen = *task.FinishedAt
	}
	if refusals == 0 {
		t.Fatalf("no call during the outage was refused by the circuit:\n%s", raw)
	}

	w, a := rep.Tasks[wait], rep.Tasks[after]
	if w.Status == "failed" || a.Status == "failed" || a.Error != "" {
		t.Fatalf("the wait (%s) or the task after it (%s: %s) failed:\n%s", w.Status, a.Status, a.Error, raw)
	}
	if gap := a.StartedAt.Sub(*w.FinishedAt); gap > 5*time.Second {
		t.Errorf("the task after the wait started %s after it", gap)
	}
	inCooldown := a.StartedAt.Sub(lastOpen)
	t.Logf("%d of %d calls during the outage refused by the circuit; the circuit last opened %s before the task after the wait",
		refusals, len(during), inCooldown.Round(100*time.Millisecond))
	if inCooldown >= 30*time.Second {
		t.Fatalf("the circuit last opened %s before the task after the wait, past its cooldown, so this run would have "+
			"passed without the wait closing it; lengthen outageRetries", inCooldown)
	}
}
