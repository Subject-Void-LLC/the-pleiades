// Package facts: how facts.gather reaches a device. A device reached over
// WinRM (a Windows server) runs the Windows probes as PowerShell; any other
// runs its probes over SSH, after uname -s says which platform's they are.
package facts

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// gatherWinRM reaches a Windows host; a test swaps it for a model.
var gatherWinRM = winrmexec.RunWithStdin

// windowsStdinLoader is what runs in place of a script past cmd.exe's command
// line (winrmexec.MaxCmdLine, which the files script with its Win32 helper
// passes for even one path, and which the script without it passed for 64):
// it reads the script from standard input, which WS-Man carries in Send
// messages with no such limit, and runs it. The script is this method's own
// text, the task's paths inside it as base64, so nothing of the task's
// becomes code.
const windowsStdinLoader = `$ErrorActionPreference = 'Stop'; & ([scriptblock]::Create([Console]::In.ReadToEnd()))`

// windowsScriptAndStdin is what gatherWinRM sends for script: the script
// itself when its command line fits, else the loader with the script on
// standard input.
func windowsScriptAndStdin(script string, opts winrmexec.Options) (string, string) {
	if winrmexec.CheckScript(winrmexec.ShellPowerShell, script, opts) == nil {
		return script, ""
	}
	return windowsStdinLoader, script
}

// gatherRun runs one probe's command and returns what it printed.
type gatherRun func(ctx context.Context, command string) (string, error)

// gatherWay is the way gatherRunner opened to a device.
type gatherWay struct {
	// run runs one probe's command.
	run gatherRun
	// platform is whose probes to run, as uname -s names it, or Windows.
	platform string
	// close closes the connection.
	close func()
	// hostKeys, for a device reached over SSH, says what the session's
	// known_hosts check accepted (pkg/sshfacts); nil over WinRM, which has no
	// host key. It is read when the probes are done, so a key exchange the
	// session made in between is counted too.
	hostKeys func() remoteexec.HostKeys
}

// gatherRunner opens the way to device: WinRM for a device reached that
// way, as wait.connection chooses, and SSH for any other. Its errors carry
// fqcn.
func gatherRunner(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (gatherWay, error) {
	if winrm, ok := device.(capability.WinRMCapable); ok && device.HasCapability(capability.NameWinRM) {
		auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
		if err != nil {
			return gatherWay{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		var opts winrmexec.Options
		if shell, ok := device.(capability.WindowsShellCapable); ok {
			opts.PowerShellPath = shell.PowerShellPath()
		}
		opts = winrmexec.WithDeviceTLS(opts, devicetls.For(device))
		target := winrmexec.Target{Host: winrm.WinRMHost(), Port: winrm.WinRMPort()}
		run := func(ctx context.Context, script string) (string, error) {
			line, stdin := windowsScriptAndStdin(script, opts)
			res, err := gatherWinRM(ctx, target, auth, winrmexec.ShellPowerShell, line, stdin, opts)
			if err != nil {
				return "", err
			}
			// Every script only reads, guards the cmdlets a host may lack,
			// and holds no task text but the files script's base64 paths, so
			// one that fails is worth saying, not a fact quietly absent.
			if res.ExitCode != 0 {
				return "", fmt.Errorf("exited %d: %s", res.ExitCode, farside.Output(res.Stderr+"\n"+res.Stdout))
			}
			return res.Stdout, nil
		}
		return gatherWay{run: run, platform: platformWindows, close: func() {}}, nil
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return gatherWay{}, err
	}
	// Every probe runs in sh, so the login shell (csh, tcsh or fish as
	// well as a POSIX one) only passes it on; each is one line, since csh
	// refuses a newline inside quotes.
	run := func(ctx context.Context, command string) (string, error) {
		res, err := conn.Run(ctx, remoteexec.QuoteCommand([]string{"sh", "-c", command}))
		return res.Stdout, err
	}
	// Which platform's sources to read: uname -s answers on every system
	// reached over SSH, and an answer this does not know reads Linux's. Its
	// last line, since a login that prints a banner prints it first.
	system, err := run(ctx, "uname -s")
	if err != nil {
		_ = conn.Close()
		return gatherWay{}, fmt.Errorf("%s: uname -s: %w", fqcn, err)
	}
	return gatherWay{run: run, platform: lastLine(system), close: func() { _ = conn.Close() }, hostKeys: conn.VerifiedHostKeys}, nil
}

// lastLine is out's last line that is not empty, trimmed.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// name is how an error names the probe: its command, or for a Windows
// script or a command built from paths, which subset's it is.
func (p gatherProbe) name() string {
	if p.label != "" {
		return p.label
	}
	// A command built from the task's paths is named by its subset: the
	// paths are the task's own, and a list of them is no name.
	if p.commandFor != nil {
		return "the " + p.subset + " probe"
	}
	if strings.Contains(p.command, "\n") {
		return "the Windows " + p.subset + " script"
	}
	return p.command
}
