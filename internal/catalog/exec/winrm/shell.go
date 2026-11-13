// Package winrm implements the "exec.winrm.shell" Collection method:
// running a command on a Windows target through PowerShell, cmd.exe or
// no shell of its own choosing, over WinRM.
//
// Scaffolded by pleiades forge new-collection from
// internal/forge/catalogdata, then hand-completed. Edit the data there,
// never this file's manifest, or internal/archtest's own drift guard
// will fail.
//
// # Why this is its own FQCN and not a mode of exec.shell
//
// The same reason svc.systemd.start is separate from svc.start: the
// concrete method names the platform it actually speaks to, and the
// generic one resolves a device's capability and dispatches to it. A
// Windows target and a POSIX one do not agree on what a shell is, what
// its metacharacters mean, or which transport reaches it, and a single
// method pretending otherwise would have to hide all three behind a
// parameter.
//
// exec.shell does not dispatch here yet. When it does, it gains the same
// capability-resolving shape internal/catalog/svc already uses, and this
// method stays exactly as it is: the concrete half of the pair.
package winrm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// Parameter and stat names this method reads and emits.
const (
	paramCommand          = "command"
	paramShell            = "shell"
	paramEnv              = "env"
	paramTimeout          = "timeout"
	paramExpectDisconnect = "expect_disconnect"
	paramReconnectTimeout = "reconnect_timeout"
	paramCreates          = "creates"
	paramRemoves          = "removes"

	statStdout      = "stdout"
	statStderr      = "stderr"
	statExitCode    = "exit_code"
	statResultKnown = "result_known"
	statSkipped     = "skipped"
)

// defaultReconnectTimeout is how long a task that expects to lose its
// connection waits for the device to come back.
//
// Five minutes covers the operations that cause the disconnect in the
// first place: an address change settles in seconds, and a reboot of a
// Windows host is the slow case at a minute or two. Longer would mostly
// mean waiting out hosts that are not coming back.
const defaultReconnectTimeout = 5 * time.Minute

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "exec.winrm.shell",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"winrm"},
			RequiredCapabilities: []capability.Name{capability.NameWinRM},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: false, Site: collection.SiteTarget, Device: collection.DeviceRequired},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "This platform cannot see what a script did, so it cannot say what would undo it. The same " +
					"reasoning exec.command and exec.shell record: an arbitrary script's effect is opaque, and a " +
					"guessed inverse is worse than none because a rollback would run it believing it was true. A " +
					"task whose effect must be reversible should use a method that models the change it is making.",
			},
			// Only a call guarded by creates or removes can be checked
			// (CheckShell), as for exec.command and Ansible's win_shell.
			SupportsCheck: true,
			Doc:           shellDoc(),
		},
		Invoke:    Shell,
		Check:     CheckShell,
		CheckCall: unguardedCheck,
	})
}

// shellDoc is this method's reference documentation. It must stay
// identical to the entry in internal/forge/catalogdata, which
// internal/archtest's TestCatalogDataDocsMatchTheRegistry enforces.
func shellDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Runs a command on a Windows target through PowerShell, cmd.exe or no shell, over WinRM.",
		Description: "Runs a command on a Windows host over WinRM, naming how it runs. This is exec.shell's Windows counterpart, and it is a separate FQCN for the same reason svc.systemd.start is separate from svc.start: the concrete method names the platform it actually speaks to. The task picks one of three modes. powershell runs a script through powershell.exe with -NoProfile and -NonInteractive, sent UTF-16LE and base64 encoded as -EncodedCommand requires, and keeps a failing native program's own exit code rather than flattening it to 1. cmd runs a one-line script through cmd.exe, which is the only way to reach a cmd builtin such as dir, set or %ERRORLEVEL%. none runs a Windows command line, reaching the program it names exactly as written, so that program parses its own arguments and nothing else acts on them. The WinRM service starts every command through cmd.exe and cannot be told not to, so every command line is escaped until that cmd.exe passes it through unchanged, and the parser that acts on the text is always the one the task named. Every mode shares cmd.exe's limit of 8191 characters, counted after escaping. Values the command needs go in env, never into its text. A command cannot be inspected, so this reports changed every time it runs; creates and removes, as in Ansible's win_shell, are how a task says what its work having already happened looks like, and a run they short-circuit reports no change. Only a call with creates or removes can be checked: a check reads the guard's path and reports whether the command would run, running nothing. Any other call is named as unchecked, and check_mode on one is refused when the runbook is validated.",
		Params: []collection.Param{
			{Name: paramCommand, Type: "string", Required: true, Description: "The script to run, or with shell none the command line: a program and its arguments, quoted the way that program expects. It is passed on verbatim, so every metacharacter the named shell understands is syntax and any runbook value interpolated into it is code. Pass values in env instead.", Format: collection.ParamFormatCommand},
			{Name: paramShell, Type: "string", Required: true, Choices: []string{"powershell", "cmd", "none"}, ChoicesIgnoreCase: true, Description: "How the command runs: powershell, cmd or none. powershell and cmd name the interpreter that reads a script, and none runs a command line directly with no interpreter. Required rather than defaulted silently, because the three read the same text three different ways and a command written for one is not safe in another. Which programs run is the device's own setting (cmd_path, powershell_path)."},
			{Name: paramEnv, Type: "dict", Description: "Values the command reads, set as environment variables before it starts. Each name becomes PLEIADES_ followed by the name, read as $env:PLEIADES_NAME in PowerShell or !PLEIADES_NAME! in cmd, so a value never becomes part of the command text and needs no escaping for either shell. In cmd the %PLEIADES_NAME% form is refused, because cmd.exe expands it before it parses the line. Values must be strings, numbers or booleans. The environment is visible to other processes on the device, so a secret does not belong here."},
			{Name: paramTimeout, Type: "int", Default: "60", Description: "How many seconds to wait for the script to finish before giving up. This bounds the whole operation, including a device that accepts the connection and then never answers, which is what a host looks like after a script has reconfigured its own network. Raise it for an installer or an update run; the default is short because most work here is not."},
			{Name: paramExpectDisconnect, Type: "bool", Default: "false", Description: "Declare that this script is expected to destroy the connection carrying it, as an address change or a reboot does. The task then waits for the device to answer WinRM again instead of failing, and reports result_known false, because the script's exit status and output went down with the connection and are not recoverable. A device that never comes back is still a failure."},
			{Name: paramReconnectTimeout, Type: "int", Default: "300", Description: "How many seconds to wait for the device to answer again after an expected disconnect. Only meaningful with expect_disconnect, and setting it without that is refused rather than silently ignored."},
			{Name: "creates", Type: "string", Description: "An absolute Windows path (C:\\... or a UNC share; the \\\\?\\ and \\\\.\\ device forms are refused). When something exists there, the command does not run and the task reports no change. Read as a literal path, never a wildcard pattern, and passed to the device as data, never as script text. A path that cannot be read (access denied, a share that does not answer) fails the task rather than counting as absent, as Ansible decides it. Empty is the same as unset."},
			{Name: "removes", Type: "string", Description: "An absolute Windows path (C:\\... or a UNC share; the \\\\?\\ and \\\\.\\ device forms are refused). When nothing exists there, the command does not run and the task reports no change. Read as a literal path, never a wildcard pattern, and passed to the device as data, never as script text. A path that cannot be read (access denied, a share that does not answer) fails the task rather than counting as absent, as Ansible decides it. Empty is the same as unset."},
		},
		Returns: []collection.ReturnField{
			{Name: statStdout, Type: "string", Returned: "always", Description: "Everything the script wrote to standard output."},
			{Name: statStderr, Type: "string", Returned: "always", Description: "Everything the script wrote to standard error. PowerShell progress output is suppressed before the script runs, so this carries real errors rather than progress records."},
			{Name: statExitCode, Type: "int", Returned: "always", Description: "The command's exit status. Under powershell a failing native program's own code survives, and a failed cmdlet reports 1. A non-zero status fails the task."},
			{Name: statResultKnown, Type: "bool", Returned: "always", Description: "Whether this task actually saw the script finish. False only after an expected disconnect, where stdout, stderr and the exit status are all unavailable. Check this before trusting the other three: an unreceived result and a silent success are otherwise indistinguishable."},
			{Name: "skipped", Type: "bool", Returned: "always", Description: "True when creates or removes short-circuited this task, so no command ran."},
		},
		Examples: []collection.Example{
			{Name: "Read a fact from a Windows host", RunbookYAML: "- name: Report the OS caption\n  exec.winrm.shell:\n    shell: powershell\n    command: (Get-CimInstance Win32_OperatingSystem).Caption\n  register: os_caption\n"},
			{Name: "Use a cmd builtin", RunbookYAML: "- name: Show the environment cmd sees\n  exec.winrm.shell:\n    shell: cmd\n    command: set\n"},
			{Name: "Pass a value as data, not script text", RunbookYAML: "- name: Create the deployment folder\n  exec.winrm.shell:\n    shell: powershell\n    command: New-Item -ItemType Directory -Force -Path $env:PLEIADES_DIR\n    env:\n      DIR: C:\\Deploy\n"},
			{Name: "Run a program with no shell", RunbookYAML: "- name: Query the time service\n  exec.winrm.shell:\n    shell: none\n    command: w32tm /query /status\n"},
			{Name: "Run once, and check it", RunbookYAML: "- name: Unpack the agent unless it is there\n  exec.winrm.shell:\n    shell: powershell\n    command: Expand-Archive -Path C:\\Temp\\agent.zip -DestinationPath C:\\Agent\n    creates: C:\\Agent\\agent.exe\n"},
		},
		SeeAlso: []string{"exec.shell", "exec.command"},
	}
}

// Shell implements "exec.winrm.shell".
//
// Changed is always true, matching exec.command and exec.shell and
// Ansible's own command/shell modules. This platform cannot inspect what
// an arbitrary script did, and a method that guessed would make a run
// that changed something look converged.
//
// A non-zero exit status fails the task, which is the Collection
// contract rather than the transport one: pkg/winrmexec reports the code
// without judging it, because at that layer a non-zero status is the
// script's own answer, and it is here that the caller's intent ("run
// this and succeed") makes it a failure.
func Shell(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return shell(ctx, rc, device, params, collection.ModeExecute)
}

// CheckShell is exec.winrm.shell's check: a call guarded by creates or removes
// reads the guard exactly as Shell does and reports the skip the real run
// would make, or predicts that the script would run, without running it; an
// unguarded call answers that it cannot be checked, since what a script
// changes is not knowable without running it. This is Ansible's own check
// behavior for win_shell and win_command.
func CheckShell(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return shell(ctx, rc, device, params, collection.ModeCheck)
}

// shell is Shell's and CheckShell's one body; mode says which.
func shell(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "exec.winrm.shell"

	// Both reads happen before anything is dialed, so a runbook mistake
	// costs no round trip and the error names the runbook, not the device.
	script, err := sdk.RequiredStringParam(params, paramCommand)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	shellName, err := sdk.RequiredStringParam(params, paramShell)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	interpreter, err := winrmexec.ParseShell(shellName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	creates, removes, err := guardParams(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if mode == collection.ModeCheck {
		if err := unguardedCheck(params); err != nil {
			return collection.Result{}, err
		}
	}
	env, err := sdk.EnvParam(params, paramEnv)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	target, err := winrmTarget(device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	timeout, err := secondsParam(params, paramTimeout)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	expectDisconnect := sdk.BoolParam(params, paramExpectDisconnect)
	reconnectTimeout, err := secondsParam(params, paramReconnectTimeout)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if reconnectTimeout == 0 {
		reconnectTimeout = defaultReconnectTimeout
	}
	if !expectDisconnect && params[paramReconnectTimeout] != nil {
		return collection.Result{}, fmt.Errorf(
			"%s: %s is set but %s is not, so nothing would ever wait: a reconnect only happens for a disconnect the task said to expect",
			fqcn, paramReconnectTimeout, paramExpectDisconnect)
	}

	// The credential vocabulary is read by pkg/winrmexec rather than
	// spelled out here, so a credential form added there reaches this
	// method without an edit.
	auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// The device's own pinned authority and server name, if it names any.
	opts := winrmexec.WithDeviceTLS(winrmexec.Options{Timeout: timeout}, devicetls.For(device))
	// A command that is expected to cut its own connection is doing its
	// job when the wait for it runs out, so it is left running; any other
	// command is stopped then, rather than left on the device.
	opts.LeaveRunningOnTimeout = expectDisconnect
	// A device that says where its interpreters live, or where commands
	// start, is honored; one that does not gets the stock paths.
	if dev, ok := device.(capability.WindowsShellCapable); ok {
		opts.CmdPath = dev.CmdPath()
		opts.PowerShellPath = dev.PowerShellPath()
		opts.WorkingDirectory = dev.WorkingDirectory()
	}

	// The guard is read before anything runs, over the same connection
	// settings, and a run it short-circuits reports no change.
	if creates != "" || removes != "" {
		// timeout bounds the whole task, so the guard's read and the
		// script share one deadline rather than each getting all of it.
		whole := opts.Timeout
		if whole == 0 {
			whole = winrmexec.DefaultTimeout
		}
		started := time.Now()
		skip, err := guardSkips(ctx, target, auth, opts, creates, removes)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		left := whole - time.Since(started)
		if left <= 0 {
			return collection.Result{}, fmt.Errorf("%s: reading the creates or removes guard used the whole timeout of %s", fqcn, whole)
		}
		opts.Timeout = left
		if skip {
			if err := recordOutcome(rc, fqcn, winrmexec.Result{}, true, true); err != nil {
				return collection.Result{}, err
			}
			return collection.Result{Changed: false}, nil
		}
	}
	if mode == collection.ModeCheck {
		// The script would run. Its output and exit status are not known
		// without running it, so they are left out rather than guessed.
		if err := rc.SetStat(statSkipped, false); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: true}, nil
	}

	result, runErr := winrmexec.Execute(ctx, target, auth, winrmexec.Command{Shell: interpreter, Script: script, Env: env}, opts)
	if runErr != nil {
		if !expectDisconnect {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, runErr)
		}
		return handleExpectedDisconnect(ctx, rc, fqcn, target, auth, opts, reconnectTimeout, runErr)
	}

	if err := recordOutcome(rc, fqcn, result, true, false); err != nil {
		return collection.Result{}, err
	}

	if result.ExitCode != 0 {
		// The script's whole output is in the stdout and stderr stats; the
		// error quotes the end of each, where a script's reason comes,
		// bounded and quoted through pkg/farside so neither stream can put
		// its size or a control byte into the error.
		return collection.Result{}, fmt.Errorf("%s: script exited %d; stderr: %s; stdout: %s",
			fqcn, result.ExitCode, farside.OutputTail(result.Stderr), farside.OutputTail(result.Stdout))
	}

	return collection.Result{Changed: true}, nil
}

// handleExpectedDisconnect is what a task that declared expect_disconnect
// gets instead of a failure: a wait for the device to come back, and an
// honest account of what is and is not known afterward.
//
// The reconnect is the evidence, and that is the whole idea. The script's
// exit status and output went down with the socket and are not
// recoverable, so this cannot say the command succeeded and does not try.
// What it can establish is that a real device was on the other end and
// survived, because an address nothing answers does not come back. That
// is what separates "the change took effect and cost us the connection"
// from "this task was pointed at nothing", which are identical from the
// error alone.
//
// A device that never returns is a hard failure carrying the original
// transport error, because a half-applied change nobody can reach is the
// worst outcome this method has and it should read like one.
func handleExpectedDisconnect(
	ctx context.Context,
	rc sdk.RunbookContext,
	fqcn string,
	target winrmexec.Target,
	auth winrmexec.Auth,
	opts winrmexec.Options,
	reconnectTimeout time.Duration,
	runErr error,
) (collection.Result, error) {
	waitCtx, cancel := context.WithTimeout(ctx, reconnectTimeout)
	defer cancel()

	if err := winrmexec.WaitUntilReachable(waitCtx, target, auth, opts, 0); err != nil {
		// The wait sends the credential only once the listener answers, and
		// stops at the first refusal rather than send it again until the
		// timeout, which could lock the account.
		var refused *winrmexec.CredentialRefusedError
		if errors.As(err, &refused) {
			return collection.Result{}, fmt.Errorf(
				"%s: the connection was lost as expected and the device answered again, but refused the credential, so "+
					"the wait stopped rather than send it again; it is unknown whether the change applied: %w (original failure: %v)",
				fqcn, err, runErr)
		}
		return collection.Result{}, fmt.Errorf(
			"%s: the connection was lost as expected, but the device never came back within %s, so it is unknown "+
				"whether the change applied and this host may need attention at the console: %w (original failure: %v)",
			fqcn, reconnectTimeout, err, runErr)
	}

	if err := recordOutcome(rc, fqcn, winrmexec.Result{}, false, false); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, nil
}

// recordOutcome writes the stats for one run, and resultKnown is the
// field that keeps the other three honest.
//
// When a connection is destroyed mid-command there is no exit status and
// no output, and the tempting thing is to record a zero exit code and
// empty strings, which reads exactly like a script that succeeded
// silently. result_known says which of those two happened, so a later
// task's when_cel can tell them apart instead of trusting an exit code
// that was never received.
func recordOutcome(rc sdk.RunbookContext, fqcn string, result winrmexec.Result, resultKnown, skipped bool) error {
	stats := []struct {
		key   string
		value any
	}{
		{statSkipped, skipped},
		{statResultKnown, resultKnown},
		{statStdout, result.Stdout},
		{statStderr, result.Stderr},
	}
	if resultKnown {
		stats = append(stats, struct {
			key   string
			value any
		}{statExitCode, result.ExitCode})
	}

	for _, stat := range stats {
		if err := rc.SetStat(stat.key, stat.value); err != nil {
			return fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return nil
}

// winrmTarget reads the host and port off a WinRM-capable device.
//
// The capability is already required by this method's manifest and
// checked by engine.checkMethodCapabilities before Invoke runs, so
// reaching the error below means a device declared WinRMCapable without
// structurally implementing it, which HasCapability is supposed to make
// impossible. It is a defense-in-depth refusal rather than an expected
// path.
func winrmTarget(device inventory.InventoryItem) (winrmexec.Target, error) {
	if device == nil {
		return winrmexec.Target{}, fmt.Errorf("needs a target device")
	}
	dev, ok := device.(capability.WinRMCapable)
	if !ok {
		return winrmexec.Target{}, fmt.Errorf("device %q declares %s but does not implement its accessors",
			device.Name(), capability.NameWinRM)
	}
	return winrmexec.Target{Host: dev.WinRMHost(), Port: dev.WinRMPort()}, nil
}

// maxSeconds caps any seconds-valued parameter at one day, matching
// pleiades.builtin.wait.port's own ceiling. A value larger than this is
// almost always milliseconds written where seconds were meant.
const maxSeconds = 86400

// secondsParam reads one whole-seconds parameter, returning zero when it
// is absent so the caller can apply its own default.
//
// It is a near-copy of pleiades.builtin.wait.port's portSecondsParam, and
// that duplication is deliberate for now rather than overlooked: this is
// the third method in the catalog to need it, which is the point at which
// it is worth hoisting into pkg/sdk alongside RequiredStringParam, and
// doing that is a change to a shared package with its own tests rather
// than something to slip into a transport fix.
func secondsParam(params map[string]any, key string) (time.Duration, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return 0, nil
	}

	// The three shapes a whole number really arrives in: int from YAML on
	// the Crawl tier, int64 from a wide-integer decoder, and float64 after
	// the same value has crossed the runner's per-task subprocess
	// boundary as JSON. Refusing the last would make a working runbook
	// fail on one tier and not the other.
	var value int
	switch typed := raw.(type) {
	case int:
		value = typed
	case int64:
		value = int(typed)
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("%s %v is not a whole number of seconds", key, typed)
		}
		value = int(typed)
	default:
		return 0, fmt.Errorf("%s is %T, not a whole number of seconds", key, raw)
	}

	if value < 1 {
		return 0, fmt.Errorf("%s %d is below 1: a timeout of zero would give up before it looked", key, value)
	}
	if value > maxSeconds {
		return 0, fmt.Errorf("%s %d is more than the %d second ceiling (one day): a value this large is usually milliseconds written where seconds were meant",
			key, value, maxSeconds)
	}
	return time.Duration(value) * time.Second, nil
}

// guardScript reports whether a guard short-circuits the script: "skip"
// when the creates path exists or the removes path does not, "run"
// otherwise. Both paths arrive as environment values, never as script text,
// and are read as literal paths, never wildcard patterns. Existence is
// decided as Ansible's Test-AnsiblePath decides it: GetAttributes answering
// is there, FileNotFound or DirectoryNotFound is not, and anything else (an
// access denied, a share that does not answer) is left to fail the script,
// never read as absence. Test-Path is not used: it answers false for a path
// it could not read, which would make a removes skip and a creates rerun.
const guardScript = `$ErrorActionPreference = 'Stop'; ` +
	`function Test-Exists([string]$p) { try { [void][System.IO.File]::GetAttributes($p); $true } ` +
	`catch [System.IO.FileNotFoundException], [System.IO.DirectoryNotFoundException] { $false } }; ` +
	`$c = $env:PLEIADES_CREATES; $r = $env:PLEIADES_REMOVES; ` +
	`if ($c -and (Test-Exists $c)) { 'skip' } elseif ($r -and -not (Test-Exists $r)) { 'skip' } else { 'run' }`

// guardSkips reads the guard on the device, through PowerShell over the same
// connection settings the script uses.
func guardSkips(ctx context.Context, target winrmexec.Target, auth winrmexec.Auth, opts winrmexec.Options, creates, removes string) (bool, error) {
	env := map[string]string{sdk.EnvPrefix + "CREATES": creates, sdk.EnvPrefix + "REMOVES": removes}
	opts.LeaveRunningOnTimeout = false
	result, err := winrmexec.Execute(ctx, target, auth, winrmexec.Command{Shell: winrmexec.ShellPowerShell, Script: guardScript, Env: env}, opts)
	if err != nil {
		return false, fmt.Errorf("reading the creates or removes guard: %w", err)
	}
	return guardAnswer(result)
}

// guardAnswer reads guardScript's answer: skip, run, or anything else as an
// error, so a guard that could not be read never passes for one that says run.
func guardAnswer(result winrmexec.Result) (bool, error) {
	switch out := strings.TrimSpace(result.Stdout); {
	case result.ExitCode == 0 && out == "skip":
		return true, nil
	case result.ExitCode == 0 && out == "run":
		return false, nil
	default:
		return false, fmt.Errorf("reading the creates or removes guard: exited %d: %s", result.ExitCode, farside.OutputTail(result.Stderr+result.Stdout))
	}
}

// guardParams reads creates and removes, each an absolute Windows path (a
// drive letter, or a UNC share) when set: a relative one would be resolved
// against wherever the WinRM session starts, which is the device's setting,
// not the task's, and a guard read in the wrong place never fires.
func guardParams(params map[string]any) (creates, removes string, err error) {
	for _, key := range []string{paramCreates, paramRemoves} {
		raw, present := params[key]
		if !present || raw == nil {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return "", "", fmt.Errorf("%s must be a path, not %T", key, raw)
		}
		// Empty is unset, as exec.command reads it, so a template that
		// renders no guard runs unguarded rather than failing.
		if value == "" {
			continue
		}
		if !windowsAbsolute(value) {
			return "", "", fmt.Errorf("%s %q is not an absolute Windows path (C:\\... or \\\\server\\share\\...)", key, value)
		}
		if key == paramCreates {
			creates = value
		} else {
			removes = value
		}
	}
	return creates, removes, nil
}

// windowsAbsolute reports whether p is a drive-letter path (C:\ or C:/) or a
// UNC path (\\server\share\...), with no NUL. The device-namespace forms
// (\\?\ and \\.\) and a server named ".", ".." or "?" are refused: nothing
// has shown how they are read, and a guard read as absent fails open.
func windowsAbsolute(p string) bool {
	if strings.ContainsRune(p, 0) {
		return false
	}
	if len(p) >= 3 && (p[0]|0x20) >= 'a' && (p[0]|0x20) <= 'z' && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	if !strings.HasPrefix(p, `\\`) {
		return false
	}
	server, rest, ok := strings.Cut(p[2:], `\`)
	switch server {
	case "", ".", "..", "?":
		return false
	}
	return ok && strings.Trim(rest, `\`) != ""
}

// unguardedCheck is the check-time refusal of a call no guard makes
// checkable, set as Descriptor.CheckCall so validation refuses check_mode on
// one before anything runs, and called by CheckShell for the same answer at
// run time.
func unguardedCheck(params map[string]any) error {
	if sdk.StringParam(params, paramCreates) != "" || sdk.StringParam(params, paramRemoves) != "" {
		return nil
	}
	return collection.CannotCheck("what a script changes cannot be known without running it; " +
		"a creates or removes guard would say what its having run looks like, and a check would read that")
}
