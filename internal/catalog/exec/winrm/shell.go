// Package winrm implements the "exec.winrm.shell" Collection method:
// running a script on a Windows target through PowerShell or cmd.exe,
// over WinRM.
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
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// Parameter and stat names this method reads and emits.
const (
	paramCommand          = "command"
	paramShell            = "shell"
	paramTimeout          = "timeout"
	paramExpectDisconnect = "expect_disconnect"
	paramReconnectTimeout = "reconnect_timeout"

	statStdout      = "stdout"
	statStderr      = "stderr"
	statExitCode    = "exit_code"
	statResultKnown = "result_known"
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
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: false},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "This platform cannot see what a script did, so it cannot say what would undo it. The same " +
					"reasoning exec.command and exec.shell record: an arbitrary script's effect is opaque, and a " +
					"guessed inverse is worse than none because a rollback would run it believing it was true. A " +
					"task whose effect must be reversible should use a method that models the change it is making.",
			},
			Doc: shellDoc(),
		},
		Invoke: Shell,
	})
}

// shellDoc is this method's reference documentation. It must stay
// identical to the entry in internal/forge/catalogdata, which
// internal/archtest's TestCatalogDataDocsMatchTheRegistry enforces.
func shellDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Runs a script on a Windows target through PowerShell or cmd.exe, over WinRM.",
		Description: "Runs a script on a Windows host over WinRM, naming which interpreter runs it. This is exec.shell's Windows counterpart, and it is a separate FQCN for the same reason svc.systemd.start is separate from svc.start: the concrete method names the platform it actually speaks to. Two shells are available and the task must pick one. powershell encodes the script UTF-16LE and base64 into powershell.exe -EncodedCommand, which is also what makes it safe: the base64 alphabet contains no cmd.exe metacharacter, so no script content can reach a shell as syntax. cmd runs the script through cmd.exe, which is the only way to reach a cmd builtin such as dir, set or %ERRORLEVEL%. Running a program directly with an argument vector nothing parses is refused rather than approximated, because the WS-Man option that would make it true is not settable from here. A script cannot be inspected, so this reports changed every time it runs.",
		Params: []collection.Param{
			{Name: paramCommand, Type: "string", Required: true, Description: "The script to run. It is passed to the interpreter named by shell, verbatim, so every metacharacter that interpreter understands is syntax and any runbook value interpolated into it is code."},
			{Name: paramShell, Type: "string", Required: true, Default: "powershell", Description: "Which interpreter runs the script: powershell or cmd. Required rather than defaulted silently, because the two have disjoint metacharacter sets and a script written for one is not safe in the other. The value none is refused: it would mean running a program directly with no interpreter, and this transport cannot promise that."},
			{Name: paramTimeout, Type: "int", Default: "60", Description: "How many seconds to wait for the script to finish before giving up. This bounds the whole operation, including a device that accepts the connection and then never answers, which is what a host looks like after a script has reconfigured its own network. Raise it for an installer or an update run; the default is short because most work here is not."},
			{Name: paramExpectDisconnect, Type: "bool", Default: "false", Description: "Declare that this script is expected to destroy the connection carrying it, as an address change or a reboot does. The task then waits for the device to answer WinRM again instead of failing, and reports result_known false, because the script's exit status and output went down with the connection and are not recoverable. A device that never comes back is still a failure."},
			{Name: paramReconnectTimeout, Type: "int", Default: "300", Description: "How many seconds to wait for the device to answer again after an expected disconnect. Only meaningful with expect_disconnect, and setting it without that is refused rather than silently ignored."},
		},
		Returns: []collection.ReturnField{
			{Name: statStdout, Type: "string", Returned: "always", Description: "Everything the script wrote to standard output."},
			{Name: statStderr, Type: "string", Returned: "always", Description: "Everything the script wrote to standard error. PowerShell progress output is suppressed before the script runs, so this carries real errors rather than progress records."},
			{Name: statExitCode, Type: "int", Returned: "always", Description: "The script's exit status. A non-zero status fails the task."},
			{Name: statResultKnown, Type: "bool", Returned: "always", Description: "Whether this task actually saw the script finish. False only after an expected disconnect, where stdout, stderr and the exit status are all unavailable. Check this before trusting the other three: an unreceived result and a silent success are otherwise indistinguishable."},
		},
		Examples: []collection.Example{
			{Name: "Read a fact from a Windows host", RunbookYAML: "- name: Report the OS caption\n  exec.winrm.shell:\n    shell: powershell\n    command: (Get-CimInstance Win32_OperatingSystem).Caption\n  register: os_caption\n"},
			{Name: "Use a cmd builtin", RunbookYAML: "- name: Show the environment cmd sees\n  exec.winrm.shell:\n    shell: cmd\n    command: set\n"},
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
	shell, err := winrmexec.ParseShell(shellName)
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

	secrets := rc.InjectSecrets()
	auth := winrmexec.Auth{Username: secrets["username"], Password: secrets["password"]}
	opts := winrmexec.Options{Timeout: timeout}

	result, runErr := winrmexec.Run(ctx, target, auth, shell, script, opts)
	if runErr != nil {
		if !expectDisconnect {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, runErr)
		}
		return handleExpectedDisconnect(ctx, rc, fqcn, target, auth, opts, reconnectTimeout, runErr)
	}

	if err := recordOutcome(rc, fqcn, result, true); err != nil {
		return collection.Result{}, err
	}

	if result.ExitCode != 0 {
		return collection.Result{}, fmt.Errorf("%s: script exited %d\nstdout: %s\nstderr: %s",
			fqcn, result.ExitCode, result.Stdout, result.Stderr)
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
		return collection.Result{}, fmt.Errorf(
			"%s: the connection was lost as expected, but the device never came back within %s, so it is unknown "+
				"whether the change applied and this host may need attention at the console: %w (original failure: %v)",
			fqcn, reconnectTimeout, err, runErr)
	}

	if err := recordOutcome(rc, fqcn, winrmexec.Result{}, false); err != nil {
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
func recordOutcome(rc sdk.RunbookContext, fqcn string, result winrmexec.Result, resultKnown bool) error {
	stats := []struct {
		key   string
		value any
	}{
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
	// the Walk tier, int64 from a wide-integer decoder, and float64 after
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
