// Windows capabilities (features on demand, Get-WindowsCapability's
// noun): read over WinRM, added and removed by dism.exe run as SYSTEM.
//
// # Why a change runs as SYSTEM in a scheduled task
//
// A capability's payload comes from Windows Update unless the caller names
// a source, and the Windows Update agent refuses a network logon, which is
// what every WinRM command runs under: Add-WindowsCapability over WinRM
// answers "Access is denied" (measured on Windows Server 2025, 2026-10-18).
// Ansible's win_updates meets the same refusal and answers it the same way,
// with a scheduled task. So AddCapability and RemoveCapability register a
// one-shot task that runs dism.exe as SYSTEM, start it, wait for it, read
// its exit code and unregister it; the dism.exe arguments are built and
// checked here, and the script that does the rest is a constant reading
// them from standard input. Reading a capability's state needs no download,
// so CapabilityStatus calls dism.exe directly, as Status does for a feature.
package winrmdism

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// capabilityIdentity is what a capability's identity may be: its name and
// four more fields after tildes, the last its version, as DISM lists them
// (ServerCore.AppCompatibility~~~~0.0.1.0, Language.Basic~~~en-US~0.0.1.0).
// Nothing else is sent, since the identity travels on a command line.
var capabilityIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}(~[A-Za-z0-9._-]{0,63}){3}~[0-9]+(\.[0-9]+){0,3}$`)

// CheckCapabilityName refuses a name that is not a whole capability
// identity, naming the form it takes.
func CheckCapabilityName(name string) error {
	if !capabilityIdentity.MatchString(name) {
		return fmt.Errorf("%q is not a capability identity: give the whole name DISM lists, with its four tildes and version, such as ServerCore.AppCompatibility~~~~0.0.1.0 (Get-WindowsCapability -Online lists them)", name)
	}
	return nil
}

// checkArgumentPath refuses a path that cannot travel inside a scheduled
// task's arguments unchanged: Task Scheduler expands %NAME% there, a quote
// would end the argument, and a line break cannot be carried. what names
// the path in the error.
func checkArgumentPath(what, path string) error {
	if err := winrmexec.CheckText(what, path); err != nil {
		return err
	}
	if strings.ContainsAny(path, "%\"\r\n") {
		return fmt.Errorf("%s %q holds a %%, a quote or a line break, which a scheduled task's arguments cannot carry unchanged", what, path)
	}
	return nil
}

// CapabilityState is what DISM reports about one capability.
type CapabilityState struct {
	// Name is the capability identity this state describes.
	Name string

	// Exists reports whether DISM recognizes the identity at all; false
	// means /get-capabilityinfo answered errInvalidParameter.
	Exists bool

	// State is DISM's own State line, verbatim: "Installed", "Not
	// Present", "Install Pending", "Uninstall Pending", "Staged",
	// "Superseded", "Partially Installed", or empty when Exists is false.
	State string
}

// Installed reports whether the capability is installed, or is waiting
// only for a restart to finish installing.
func (c CapabilityState) Installed() bool {
	return c.State == "Installed" || c.InstallPending()
}

// InstallPending reports whether the capability is installed pending a
// restart.
func (c CapabilityState) InstallPending() bool { return c.State == "Install Pending" }

// Removed reports whether the capability is not installed (its payload may
// still be staged), or is waiting only for a restart to finish removing.
func (c CapabilityState) Removed() bool {
	switch c.State {
	case "Not Present", "Staged", "Removed":
		return true
	}
	return c.RemovePending()
}

// RemovePending reports whether the capability is removed pending a
// restart.
func (c CapabilityState) RemovePending() bool { return c.State == "Uninstall Pending" }

// Map renders the state as the map a diff stat records.
func (c CapabilityState) Map() map[string]any {
	return map[string]any{"name": c.Name, "exists": c.Exists, "state": c.State}
}

// CapabilityStatus reads what DISM reports about the capability name.
// /english keeps the State line in English whatever the host's language.
func CapabilityStatus(ctx context.Context, session Session, logPath, name string) (CapabilityState, error) {
	if err := CheckCapabilityName(name); err != nil {
		return CapabilityState{}, err
	}
	script := dismCommand(logPath, "/english", "/get-capabilityinfo", "/capabilityname:"+winrmexec.QuotePS(name))
	result, err := winrmexec.Run(ctx, session.Target, session.Auth, winrmexec.ShellPowerShell, script, session.Options)
	if err != nil {
		return CapabilityState{}, fmt.Errorf("reading the state of capability %s: %w", name, err)
	}
	if result.ExitCode == errInvalidParameter {
		return CapabilityState{Name: name}, nil
	}
	if result.ExitCode != 0 {
		return CapabilityState{}, fmt.Errorf("reading the state of capability %s: dism.exe /get-capabilityinfo exited %d: %s",
			name, result.ExitCode, dismOutput(result))
	}
	state, ok := parseStateLine(result.Stdout)
	if !ok {
		return CapabilityState{}, fmt.Errorf("reading the state of capability %s: dism.exe /get-capabilityinfo succeeded but printed no \"State :\" line: %s",
			name, result.Stdout)
	}
	return CapabilityState{Name: name, Exists: true, State: state}, nil
}

// AddCapability installs the capability name as SYSTEM (see the file
// comment), from source when it is not empty (a folder or mounted image
// holding the host's features on demand, with Windows Update left alone),
// and reports whether the change needs a restart. timeout bounds dism.exe's
// run.
func AddCapability(ctx context.Context, session Session, logPath, name, source string, timeout time.Duration) (ChangeResult, error) {
	extra := []string{}
	if source != "" {
		if err := checkArgumentPath("source", source); err != nil {
			return ChangeResult{}, err
		}
		extra = append(extra, "/source:"+winrmexec.QuoteArg(source), "/limitaccess")
	}
	return changeCapability(ctx, session, logPath, "/add-capability", name, timeout, extra...)
}

// RemoveCapability removes the capability name as SYSTEM, and reports
// whether the change needs a restart.
func RemoveCapability(ctx context.Context, session Session, logPath, name string, timeout time.Duration) (ChangeResult, error) {
	return changeCapability(ctx, session, logPath, "/remove-capability", name, timeout)
}

// changeCapability runs dism.exe verb on name as SYSTEM.
func changeCapability(ctx context.Context, session Session, logPath, verb, name string, timeout time.Duration, extra ...string) (ChangeResult, error) {
	if err := CheckCapabilityName(name); err != nil {
		return ChangeResult{}, err
	}
	if err := checkArgumentPath("the DISM log path", logPath); err != nil {
		return ChangeResult{}, err
	}
	args := capabilityArguments(verb, name, logPath, extra...)
	code, err := runAsSystem(ctx, session, args, timeout)
	if err != nil {
		return ChangeResult{}, fmt.Errorf("dism.exe %s %s: %w", verb, name, err)
	}
	switch code {
	case 0:
		return ChangeResult{}, nil
	case rebootRequiredCode:
		return ChangeResult{RebootRequired: true}, nil
	}
	return ChangeResult{}, fmt.Errorf("dism.exe %s %s exited 0x%08X%s; its log is %s on the device", verb, name, code, dismHint(code), logPath)
}

// capabilityArguments is the dism.exe argument string for verb on name.
// Every part has been checked (CheckCapabilityName, checkArgumentPath), and
// paths are quoted as the standard Windows parser reads them.
func capabilityArguments(verb, name, logPath string, extra ...string) string {
	parts := append([]string{"/online", "/english", verb, "/capabilityname:" + name, "/norestart", "/quiet",
		"/logpath:" + winrmexec.QuoteArg(logPath)}, extra...)
	return strings.Join(parts, " ")
}

// dismHint explains the DISM failures a capability change meets most.
func dismHint(code uint32) string {
	switch code {
	case 0x800F0954:
		return ": the payload could not be fetched from Windows Update, or the host is told to use a WSUS server that lacks it; name a source holding the host's features on demand"
	case 0x800F081F:
		return ": the payload was not found in the source given or in Windows Update"
	case 0x800F0950, 0x800F0906, 0x800F0907:
		return ": the payload could not be downloaded or is blocked by policy; name a source"
	}
	return ""
}

// systemTaskPath is the Task Scheduler folder the SYSTEM tasks live in.
const systemTaskPath = `\Pleiades\`

// asSystemScript registers {task, arguments, seconds} from standard input
// as a one-shot scheduled task running dism.exe as SYSTEM, runs it, waits
// for it, prints its state and exit code, and unregisters it. A task an
// earlier run left behind (its WinRM call ended first) and that is no
// longer running is removed first. Task Scheduler stops dism.exe at
// seconds; the loop waits a minute past that for it to say so.
const asSystemScript = `$ErrorActionPreference = 'Stop'
$in = [Console]::In.ReadToEnd() | ConvertFrom-Json
$p = '` + systemTaskPath + `'
Get-ScheduledTask -TaskPath $p -ErrorAction SilentlyContinue | Where-Object { $_.TaskName -like 'dism-*' -and $_.State -ne 'Running' } | Unregister-ScheduledTask -Confirm:$false
$q = @{ TaskPath = $p; TaskName = $in.task }
$action = New-ScheduledTaskAction -Execute (Join-Path $env:SystemRoot 'System32\dism.exe') -Argument $in.arguments
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Seconds $in.seconds) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
Register-ScheduledTask @q -Action $action -Principal $principal -Settings $settings -Force | Out-Null
try {
Start-ScheduledTask @q
$deadline = (Get-Date).AddSeconds($in.seconds + 60)
do {
Start-Sleep -Seconds 5
$t = Get-ScheduledTask @q
$i = Get-ScheduledTaskInfo @q
} while (($t.State -eq 'Running' -or $t.State -eq 'Queued' -or $i.LastTaskResult -eq 267009 -or $i.LastTaskResult -eq 267011) -and (Get-Date) -lt $deadline)
"state=$($t.State)"
"result=$([uint32]$i.LastTaskResult)"
} finally {
Unregister-ScheduledTask @q -Confirm:$false -ErrorAction SilentlyContinue
}
`

// systemTaskStillRunning and systemTaskNotRun are Task Scheduler's own
// results for a task that is running and for one that has not run yet.
const (
	systemTaskStillRunning = 267009
	systemTaskNotRun       = 267011
)

// runSystem runs asSystemScript; a test replaces it.
var runSystem = func(ctx context.Context, session Session, stdin string, opts winrmexec.Options) (winrmexec.Result, error) {
	return winrmexec.RunWithStdin(ctx, session.Target, session.Auth, winrmexec.ShellPowerShell, asSystemScript, stdin, opts)
}

// runAsSystem runs dism.exe with args as SYSTEM through asSystemScript and
// returns its exit code. The WinRM call is given timeout and two minutes
// more, for the task's registration, the polling and its removal.
func runAsSystem(ctx context.Context, session Session, args string, timeout time.Duration) (uint32, error) {
	if timeout <= 0 {
		return 0, errors.New("a capability change needs a timeout above zero")
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return 0, err
	}
	task := "dism-" + hex.EncodeToString(id)
	stdin, err := json.Marshal(map[string]any{"task": task, "arguments": args, "seconds": int(timeout / time.Second)})
	if err != nil {
		return 0, err
	}
	opts := session.Options
	opts.Timeout = timeout + 2*time.Minute
	res, err := runSystem(ctx, session, string(stdin), opts)
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("running dism.exe as SYSTEM through the scheduled task %s%s failed (exit %d): %s",
			systemTaskPath, task, res.ExitCode, dismOutput(res))
	}
	state, result := "", ""
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "state="); ok {
			state = v
		}
		if v, ok := strings.CutPrefix(line, "result="); ok {
			result = v
		}
	}
	code, err := strconv.ParseUint(result, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("the scheduled task %s%s gave no exit code: %s", systemTaskPath, task, dismOutput(res))
	}
	if state == "Running" || code == systemTaskStillRunning {
		return 0, fmt.Errorf("dism.exe was still running as SYSTEM after %s; Task Scheduler stops it at that limit, so give a longer timeout", timeout)
	}
	if code == systemTaskNotRun {
		return 0, fmt.Errorf("the scheduled task %s%s never started", systemTaskPath, task)
	}
	return uint32(code), nil
}
