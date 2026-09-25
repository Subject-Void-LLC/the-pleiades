// The WinRM execution modes, through the real pleiades binary.
//
// pkg/winrmexec's own modes gate proves the three modes and the wire
// messages against a real host. This one proves the same host is reached
// the way an operator reaches it: init, add-host, add-credential, and a
// runbook of winrm_exec tasks, each naming its shell, run by the built
// binary. It reuses the certificate gate's setup, so it runs whenever that
// gate runs and skips with the same message otherwise.
package main_test

import (
	"regexp"
	"strings"
	"testing"
)

// injectedLine matches a line that is only the word INJECTED, which is
// what a value that ran as a command would print. A value printed as text
// shows the word inside its brackets instead.
var injectedLine = regexp.MustCompile(`(?m)^\s*INJECTED\s*$`)

func TestWinRMModesGate_ThreeModesThroughTheBinary(t *testing.T) {
	host, cred := winrmCertificateGate(t)
	dir := certificateGateProject(t, host, cred)

	rb := writeRunbook(t, dir, "modes", `id: modes
hosts: win-cert-gate
tasks:
  - name: none runs a program with no shell
    fqcn: winrm_exec
    params:
      shell: none
      command: C:\Windows\System32\whoami.exe
      changed: false
  - name: cmd runs a builtin and reads a value as text
    fqcn: winrm_exec
    params:
      shell: cmd
      command: echo [!PLEIADES_X!]
      env:
        X: "a & echo INJECTED"
  - name: powershell reads a value as text
    fqcn: winrm_exec
    params:
      shell: powershell
      command: Write-Output "[$env:PLEIADES_Y]"
      env:
        Y: "$(Write-Output INJECTED)"
`)
	out, err := runPleiades(t, dir, "run", rb, "--verbose")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{`pleiades-gate`, `[a & echo INJECTED]`, `[$(Write-Output INJECTED)]`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if injectedLine.MatchString(out) {
		t.Errorf("a value ran as a command:\n%s", out)
	}

	exitRB := writeRunbook(t, dir, "exit", `id: exit
hosts: win-cert-gate
tasks:
  - name: powershell keeps a real exit code
    fqcn: winrm_exec
    params:
      shell: powershell
      command: exit 42
`)
	out, err = runPleiades(t, dir, "run", exitRB, "--verbose")
	if err == nil || !strings.Contains(out, "exited 42") {
		t.Errorf("exit 42: err = %v, want the run to fail naming exit code 42:\n%s", err, out)
	}
}
