// The transport check through the real pleiades binary: an SSH method is
// refused for a Windows server at plan time, before any connection opens.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// TestTransportRuleGate_AnSSHMethodIsRefusedForAWindowsServer runs the
// transport check through the real binary, the way an operator meets it.
//
// A windows_server has CommandExecCapable through WindowsShellCapable,
// which is what exec.command requires, so the capability alone would
// admit it; exec.command reaches its device over SSH and the server only
// over WinRM. pleiades validate must refuse the runbook naming both
// transports, pleiades run must refuse it the same way before any task
// runs, and no connection may reach the device address at all, which a
// listener standing in for the server counts. The same inventory then
// validates exec.winrm.shell, the method that does reach it, so the
// refusal is about the transport and not about the device.
func TestTransportRuleGate_AnSSHMethodIsRefusedForAWindowsServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			conn.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "win1", "--type", "windows_server",
		"--set", "host=127.0.0.1", "--set", "port="+strconv.Itoa(port)); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}

	writeRunbook := func(name, body string) string {
		t.Helper()
		path := filepath.Join("runbooks", name)
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		return path
	}
	sshMethod := writeRunbook("ssh_on_windows.yaml", `id: ssh-on-windows
hosts: win1
tasks:
  - name: who am i
    exec.command:
      cmd: whoami
`)
	winrmMethod := writeRunbook("winrm_on_windows.yaml", `id: winrm-on-windows
hosts: win1
tasks:
  - name: who am i
    exec.winrm.shell:
      command: C:\Windows\System32\whoami.exe
      shell: none
`)

	for _, command := range [][]string{{"validate", sshMethod}, {"run", sshMethod}} {
		out, err := runPleiades(t, dir, command...)
		if err == nil {
			t.Fatalf("pleiades %s: accepted an SSH method for a WinRM-only server:\n%s", command[0], out)
		}
		for _, want := range []string{`"exec.command" reaches its device over ssh`, `device "win1" reaches only winrm`} {
			if !strings.Contains(out, want) {
				t.Errorf("pleiades %s: output does not say %q:\n%s", command[0], want, out)
			}
		}
	}
	if n := accepted.Load(); n != 0 {
		t.Fatalf("%d connection(s) reached the device address; the refusal must come before any", n)
	}

	if out, err := runPleiades(t, dir, "validate", winrmMethod); err != nil {
		t.Fatalf("pleiades validate refused exec.winrm.shell, which does reach the server:\n%s", out)
	}
}
