// Tests for the WindowsShellCapable accessors.
package windows_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/windows"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// shellDevice builds a Server with props and returns it as the
// capability the accessors satisfy, failing if it does not.
func shellDevice(t *testing.T, props map[string]inventory.PropertyValue) capability.WindowsShellCapable {
	t.Helper()
	item, err := windows.NewServer(record.Record{ID: "w1", Name: "w1", Type: "windows_server", Properties: props})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	dev, ok := item.(capability.WindowsShellCapable)
	if !ok {
		t.Fatal("windows.Server does not satisfy capability.WindowsShellCapable")
	}
	return dev
}

func TestServer_ShellPaths(t *testing.T) {
	dev := shellDevice(t, nil)
	if got := dev.CmdPath(); got != `C:\Windows\System32\cmd.exe` {
		t.Errorf("CmdPath() default = %q", got)
	}
	if got := dev.PowerShellPath(); got != `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` {
		t.Errorf("PowerShellPath() default = %q", got)
	}
	if got := dev.WorkingDirectory(); got != "" {
		t.Errorf("WorkingDirectory() default = %q, want empty so the service chooses", got)
	}

	dev = shellDevice(t, map[string]inventory.PropertyValue{
		"cmd_path":          `D:\Windows\System32\cmd.exe`,
		"powershell_path":   `C:\Program Files\PowerShell\7\pwsh.exe`,
		"working_directory": `C:\Work`,
	})
	if dev.CmdPath() != `D:\Windows\System32\cmd.exe` || dev.PowerShellPath() != `C:\Program Files\PowerShell\7\pwsh.exe` || dev.WorkingDirectory() != `C:\Work` {
		t.Errorf("overrides not honored: %q %q %q", dev.CmdPath(), dev.PowerShellPath(), dev.WorkingDirectory())
	}

	// An explicit empty string falls back to the default rather than
	// naming no program at all.
	dev = shellDevice(t, map[string]inventory.PropertyValue{"cmd_path": "", "powershell_path": ""})
	if dev.CmdPath() == "" || dev.PowerShellPath() == "" {
		t.Error("an empty path property must fall back to the default")
	}
}

// TestServer_DoesNotClaimCommandExec pins the deviation
// capability.WindowsShellCapable's own doc comment explains: a Windows
// server must not declare CommandExecCapable, because exec.command and
// exec.shell require it and speak SSH only.
func TestServer_DoesNotClaimCommandExec(t *testing.T) {
	item, err := windows.NewServer(record.Record{ID: "w1", Name: "w1", Type: "windows_server"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	for _, name := range []capability.Name{capability.NameCommandExec, capability.NameShellExec} {
		if item.HasCapability(name) {
			t.Errorf("HasCapability(%s) = true: SSH-only methods would validate against a Windows host", name)
		}
	}
}
