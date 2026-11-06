// facts.gather's services on the lab's real Mac and Windows machines, through
// the real binary, held to the rule that a stable fact does not change on a
// machine nobody changed (sweep 2026-11-04, items 5 and 6). Each gather is a
// login of its own, and both platforms make a service for a login: launchd a
// job per SSH connection (com.openssh.sshd.<UUID>), Windows an instance of
// each per-user service per signed-in user (Template_<LUID>). The file name
// names no platform, since a suffix would build it only there (FAILURE
// PATTERNS 374); each test skips unless its machine is configured.
package main_test

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// PLEIADES_WINRM_SIGN_IN, set to 1, lets the Windows test sign a throwaway
// user in at the console, which takes a restart; nothing else here restarts
// a lab machine.
const envWinRMSignIn = "PLEIADES_WINRM_SIGN_IN"

// labServices gathers only the services subset from m and returns its
// service. facts, their states left out (those move on their own and are
// kept apart for it).
func labServices(t *testing.T, m labMachine) map[string]any {
	t.Helper()
	rep, raw, code := runPleiadesJSONWithHome(t, m.dir, "", "adhoc", m.host, "facts.gather", "gather_subset:=[services]")
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("facts.gather on %s (exit %d):\n%s", m.host, code, raw)
	}
	out := map[string]any{}
	for k, v := range rep.Tasks[0].Stats {
		if strings.HasPrefix(k, "service.") {
			out[k] = v
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s reported no service", m.host)
	}
	return out
}

// labServicesDiffer names the service. keys one of two gathers has and the
// other lacks, and those whose value changed.
func labServicesDiffer(a, b map[string]any) []string {
	var diff []string
	for k, v := range a {
		if w, ok := b[k]; !ok {
			diff = append(diff, "-"+k)
		} else if !reflect.DeepEqual(v, w) {
			diff = append(diff, "~"+k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			diff = append(diff, "+"+k)
		}
	}
	return diff
}

func TestCLI_FactsGatherOnMacOSKeepsItsServicesAcrossLogins(t *testing.T) {
	m := labMachine{os.Getenv(envMacProject), os.Getenv(envMacDevice)}
	if m.dir == "" || m.host == "" {
		t.Skipf("needs a Mac reached over SSH as generic_ssh: set %s and %s", envMacProject, envMacDevice)
	}
	first, second := labServices(t, m), labServices(t, m)
	if diff := labServicesDiffer(first, second); len(diff) > 0 {
		t.Errorf("two logins in a row to an unchanged Mac gathered different services: %v", diff)
	}
	if _, ok := first["service.com.openssh.sshd"]; !ok {
		t.Error("no service.com.openssh.sshd, the daemon this test logs in through")
	}
}

func TestCLI_FactsGatherOnWindowsReportsPerUserServicesByTemplate(t *testing.T) {
	m := labMachine{os.Getenv(envWinRMProject), os.Getenv(envWinRMDevice)}
	if m.dir == "" || m.host == "" || os.Getenv(envWinRMSignIn) != "1" {
		t.Skipf("needs a Windows host managed as windows_server that may restart: set %s, %s and %s=1", envWinRMProject, envWinRMDevice, envWinRMSignIn)
	}
	if strings.HasPrefix(m.dir, "~/") {
		home, _ := os.UserHomeDir()
		m.dir = home + m.dir[1:]
	}
	// A throwaway user, its password made inside the guest and never sent,
	// signed in once at the console by Windows' autologon, which clears its
	// own settings after that one sign-in (AutoLogonCount).
	const user = "pleiades-usersvc"
	winlogon := `'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'`
	t.Cleanup(func() {
		labPowerShell(t, m, `$w = `+winlogon+`; foreach ($v in 'DefaultPassword', 'AutoLogonCount', 'DefaultUserName', 'DefaultDomainName') { Remove-ItemProperty $w $v -ErrorAction SilentlyContinue }; `+
			`Set-ItemProperty $w AutoAdminLogon '0'; foreach ($line in @(quser 2>$null)) { if ($line -match '`+user+`\s+\S*\s+(\d+)') { logoff $Matches[1] } }; Start-Sleep 10; `+
			`Remove-LocalUser -Name `+user+` -ErrorAction SilentlyContinue; `+
			`Get-CimInstance Win32_UserProfile | Where-Object { $_.LocalPath -like '*\`+user+`*' } | Remove-CimInstance -ErrorAction SilentlyContinue; 'removed'`)
	})
	setup := `$ErrorActionPreference = 'Stop'; $chars = [char[]]'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'; $bytes = New-Object byte[] 24; ` +
		`[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes); $plain = 'Aa1!' + (-join ($bytes | ForEach-Object { $chars[$_ % $chars.Length] })); ` +
		`if (Get-LocalUser -Name ` + user + ` -ErrorAction SilentlyContinue) { Remove-LocalUser -Name ` + user + ` }; ` +
		`New-LocalUser -Name ` + user + ` -Password (ConvertTo-SecureString $plain -AsPlainText -Force) -PasswordNeverExpires -AccountNeverExpires | Out-Null; ` +
		`Add-LocalGroupMember -Group Users -Member ` + user + `; $w = ` + winlogon + `; Set-ItemProperty $w AutoAdminLogon '1'; ` +
		`Set-ItemProperty $w DefaultUserName '` + user + `'; Set-ItemProperty $w DefaultDomainName $env:COMPUTERNAME; Set-ItemProperty $w DefaultPassword $plain; ` +
		`Set-ItemProperty $w AutoLogonCount 1 -Type DWord; 'ready'`
	if got := labPowerShell(t, m, setup); got != "ready" {
		t.Fatalf("setting up the sign-in printed %q", got)
	}
	if rep, raw, code := runPleiadesJSONWithHome(t, m.dir, "", "adhoc", m.host, "win.reboot", "post_reboot_delay=60"); code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("restarting %s (exit %d):\n%s", m.host, code, raw)
	}
	// The sign-in made one instance of each per-user service: their names,
	// from the registry, where an instance's Type holds 0x80.
	instances := strings.Fields(labPowerShell(t, m, labServiceTypes+`($t.Keys | Where-Object { $t[$_] -band 0x80 } | Sort-Object) -join ' '`))
	templates := strings.Fields(labPowerShell(t, m, labServiceTypes+`($t.Keys | Where-Object { ($t[$_] -band 0x40) -and -not ($t[$_] -band 0x80) } | Sort-Object) -join ' '`))
	if len(instances) == 0 || len(templates) == 0 {
		t.Fatalf("the sign-in made no per-user service instance (templates %v), so this proves nothing", templates)
	}
	services := labServices(t, m)
	for _, name := range instances {
		if _, ok := services["service."+name]; ok {
			t.Errorf("service.%s is a signed-in user's instance, whose name changes with every sign-in", name)
		}
	}
	for _, name := range templates {
		if v, _ := services["service."+name].(map[string]any); v["per_user"] != true {
			t.Errorf("service.%s = %v, want the per-user template, marked per_user", name, services["service."+name])
		}
	}
}

func TestCLI_FactsGatherOnWindowsKeepsItsServicesAcrossGathers(t *testing.T) {
	m := labMachine{os.Getenv(envWinRMProject), os.Getenv(envWinRMDevice)}
	if m.dir == "" || m.host == "" {
		t.Skipf("needs a Windows host managed as windows_server: set %s and %s", envWinRMProject, envWinRMDevice)
	}
	if strings.HasPrefix(m.dir, "~/") {
		home, _ := os.UserHomeDir()
		m.dir = home + m.dir[1:]
	}
	// Half a minute apart, as on win-check on 2026-11-06, when KeyIso,
	// TrustedInstaller, WaaSMedicSvc and wlidsvc each started on their own
	// between two gathers: their states move, and only their states.
	first := labServices(t, m)
	time.Sleep(30 * time.Second)
	if diff := labServicesDiffer(first, labServices(t, m)); len(diff) > 0 {
		t.Errorf("two gathers of an unchanged Windows host gathered different services: %v", diff)
	}
}
