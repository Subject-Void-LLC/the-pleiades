// Package exec implements the "exec.*" namespaced Collection methods:
// running a command on a device.
//
// This file holds what they share: opening the connection, reading the
// parameters that describe where and how a command runs, and the two
// existence checks that make a command idempotent.
//
// Like every Collection package it imports only pkg/, which is why the
// SSH mechanism lives in pkg/remoteexec rather than in
// internal/transport/ssh. That constraint is not incidental: it is the
// same one a third-party Collection will have to satisfy once Part X's
// OCI distribution exists, so a built-in that quietly reached into
// internal/ would be proving a pattern nobody else can follow.
package exec

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names shared by every method in this namespace.
//
// They are Ansible's own names wherever Ansible has one, because this
// platform is a superset of Ansible rather than a new vocabulary: a
// person converting a playbook should be renaming nothing. chdir,
// creates, removes and stdin are all straight from ansible.builtin's
// command and shell modules and mean exactly what they mean there.
const (
	paramChdir   = "chdir"
	paramCreates = "creates"
	paramRemoves = "removes"
	paramStdin   = "stdin"

	// paramInsecureSkipHostKeyVerify is the escape hatch for a target
	// with no known_hosts entry yet, off by default. Skipping host key
	// verification is a real MITM exposure, so it must be set explicitly
	// and loudly by the runbook author, never assumed.
	//
	// It is a task parameter here only because this platform has no
	// per-device connection configuration yet. Host key policy is a
	// property of the connection, not of the command being run, so its
	// eventual home is the inventory item; net.ssh.ping already carries
	// the same parameter for the same interim reason, and the two should
	// move together when that home exists.
	paramInsecureSkipHostKeyVerify = "insecure_skip_host_key_verify"
)

// Stat keys these methods emit, matching what Ansible's command module
// returns so a converted playbook's later tasks read the same names.
const (
	statRC      = "rc"
	statStdout  = "stdout"
	statStderr  = "stderr"
	statCmd     = "cmd"
	statSkipped = "skipped"
)

// connect opens one SSH connection to the device a task targets. The
// caller must Close the returned connection.
//
// It returns one connection rather than running one command because a
// method in this namespace may need two or three: a creates or removes
// check before the command, and the command itself. Paying for a fresh
// TCP connect, key exchange and authentication round for each would make
// the idempotence check the expensive part of the task.
func connect(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (*remoteexec.Conn, error) {
	if device == nil {
		return nil, fmt.Errorf("%s: no target device: set the task's target or the runbook's hosts", fqcn)
	}

	sshDev, ok := device.(capability.SSHTransportCapable)
	if !ok {
		return nil, fmt.Errorf("%s: device %q is not reachable over SSH (it does not implement %s)",
			fqcn, device.Name(), capability.NameSSHTransport)
	}

	// Credentials arrive through InjectSecrets rather than through params,
	// because params come from the runbook file and a runbook file is
	// committed to version control.
	auth, err := remoteexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return nil, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}

	// Shared rather than New: a Collection method is invoked once per task
	// with nowhere to keep a Runner in between, so a fresh one every time
	// would carry a circuit breaker that has never seen a failure and
	// could therefore never open.
	runner := remoteexec.Shared(remoteexec.Options{
		InsecureSkipHostKeyVerify: boolParam(params, paramInsecureSkipHostKeyVerify),
	})

	conn, err := runner.Connect(ctx, remoteexec.Target{Host: sshDev.SSHHost(), Port: sshDev.SSHPort()}, auth)
	if err != nil {
		return nil, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	return conn, nil
}

// workingDirectory decides which directory a command runs in: the task's
// own chdir parameter when it sets one, otherwise whatever the device
// declares through capability.CommandExecCapable, otherwise nothing.
//
// Reading the device's answer is what makes the CommandExecCapable
// requirement on these manifests mean something rather than being a
// label. An empty result means "wherever this account lands on login,"
// which is what a person running the same command by hand would get.
//
// The capability read is optional rather than a hard type assertion on
// purpose. Admission has already checked that the device declares
// CommandExecCapable, but the Runner's own device adapter carries a
// declared capability list without the accessors behind it, so requiring
// the accessor here would refuse a dispatch that the Controller
// correctly admitted.
func workingDirectory(device inventory.InventoryItem, params map[string]any) string {
	if dir := stringParam(params, paramChdir); dir != "" {
		return dir
	}
	if execDev, ok := device.(capability.CommandExecCapable); ok {
		return execDev.WorkingDirectory()
	}
	return ""
}

// cdFailedExitCode is the status changeDirectory makes a failed cd
// report, chosen so it cannot be confused with any answer the command
// after it might give.
//
// It matters most for the existence checks. POSIX test answers 0 for
// "exists" and 1 for "does not", so without a distinct third value a
// directory that could not be entered would read as "does not exist",
// and a removes guard would then skip a task whose work had never been
// done. 121 is outside test's range and clear of the shell's own
// reserved 126 (cannot execute) and 127 (not found).
const cdFailedExitCode = 121

// changeDirectory returns the shell prefix that enters dir, or the empty
// string when there is no directory to enter.
//
// Two details in it are load-bearing. The "--" terminates cd's own
// options, without which a directory literally named "-P" or "-L" is
// consumed as a flag and cd silently succeeds into the home directory
// instead of failing: quoting defeats word splitting and expansion but
// not option parsing. And a failed cd stops what follows rather than
// letting it run somewhere else, which is the failure that turns a
// typo'd chdir into a change applied to the wrong path.
func changeDirectory(dir string) string {
	if dir == "" {
		return ""
	}
	return "cd -- " + remoteexec.QuoteArg(dir) + " || exit " + strconv.Itoa(cdFailedExitCode) + "; "
}

// commandLine builds the single string an SSH exec request carries, from
// an already-split argument vector and an optional working directory.
//
// Every element is quoted, so the remote login shell that inevitably
// parses this string finds nothing in it to interpret.
func commandLine(argv []string, dir string) string {
	return changeDirectory(dir) + remoteexec.QuoteCommand(argv)
}

// pathExists reports whether path exists on the far end of conn,
// resolved from dir exactly as the task's own command will be.
//
// Passing the same directory in is the whole point. The guard and the
// command have to agree about where a relative path lives, and they did
// not: the command ran under chdir while the check ran wherever the
// account happened to log in, so a relative creates never fired and a
// relative removes skipped a task whose file was sitting untouched in
// chdir. Ansible's own command module resolves creates after changing
// directory, and this platform claims to mean what Ansible means.
//
// POSIX test's exit status is the answer: zero when the path exists, one
// when it does not. A directory that cannot be entered is neither, and
// is reported as an error rather than as an absence, because treating it
// as "does not exist" is what silently skips real work.
func pathExists(ctx context.Context, conn *remoteexec.Conn, dir, path string) (bool, error) {
	result, err := conn.Run(ctx, changeDirectory(dir)+"test -e "+remoteexec.QuoteArg(path))
	if err != nil {
		return false, err
	}
	if result.ExitCode == cdFailedExitCode {
		return false, fmt.Errorf("cannot enter directory %q: %s", dir, strings.TrimSpace(result.Stderr))
	}
	return result.ExitCode == 0, nil
}

// skipDecision is why a method decided not to run its command, or the
// empty string when it is going ahead.
type skipDecision string

// shouldSkip applies the creates and removes guards, which are the only
// way a command becomes idempotent: a command cannot be inspected, so
// the author is the one who says what its having-already-happened looks
// like.
//
// creates names a path whose existence means the work is done. removes
// names a path whose absence means the same. Neither is checked when the
// task does not set it, and both are resolved from dir, the same
// directory the command itself will run in.
func shouldSkip(ctx context.Context, conn *remoteexec.Conn, dir string, params map[string]any) (skipDecision, error) {
	if creates := stringParam(params, paramCreates); creates != "" {
		exists, err := pathExists(ctx, conn, dir, creates)
		if err != nil {
			return "", fmt.Errorf("checking creates path %q: %w", creates, err)
		}
		if exists {
			return skipDecision(fmt.Sprintf("skipped, since %s exists", creates)), nil
		}
	}

	if removes := stringParam(params, paramRemoves); removes != "" {
		exists, err := pathExists(ctx, conn, dir, removes)
		if err != nil {
			return "", fmt.Errorf("checking removes path %q: %w", removes, err)
		}
		if !exists {
			return skipDecision(fmt.Sprintf("skipped, since %s does not exist", removes)), nil
		}
	}

	return "", nil
}

// recordSkip writes the stats a skipped task reports, mirroring what
// Ansible's command module returns when creates or removes short-circuit
// it: a skipped flag and an explanatory message, with an empty rc,
// stdout and stderr because no command ran.
//
// The stats are written even though nothing ran, so a later task's
// when_cel reads the same shape either way instead of having to tell an
// absent stat apart from an empty one.
func recordSkip(rc sdk.RunbookContext, why skipDecision) error {
	for key, value := range map[string]any{
		statSkipped: true,
		statRC:      0,
		statStdout:  "",
		statStderr:  "",
		statCmd:     "",
		"msg":       string(why),
	} {
		if err := rc.SetStat(key, value); err != nil {
			return err
		}
	}
	return nil
}

// recordResult writes the stats a command that actually ran reports.
func recordResult(rc sdk.RunbookContext, command string, result remoteexec.Result) error {
	for key, value := range map[string]any{
		statSkipped: false,
		statRC:      result.ExitCode,
		statStdout:  strings.TrimRight(result.Stdout, "\r\n"),
		statStderr:  strings.TrimRight(result.Stderr, "\r\n"),
		statCmd:     command,
	} {
		if err := rc.SetStat(key, value); err != nil {
			return err
		}
	}
	return nil
}

// stringParam reads a string task parameter, treating a missing or
// non-string value as absent.
func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

// boolParam reads a boolean task parameter, treating a missing or
// non-boolean value as false.
//
// It accepts a real bool only, not the string "true". YAML already
// decodes an unquoted true into a bool, and accepting the string form
// would mean silently honoring a quoted "false" as true-ish somewhere
// down the line. That matters more than usual here: the one parameter
// this reads turns off host key verification.
func boolParam(params map[string]any, key string) bool {
	v, ok := params[key].(bool)
	return ok && v
}

// stringSlice reads a list-of-strings task parameter.
//
// A non-string element is refused rather than rendered with %v. A YAML
// author who wrote a bare 8080 in an argument list meant the text 8080,
// but a value that arrived as a float64 across the Runner's task
// subprocess boundary would render as "8080" in one tier and "8080.000"
// in another, and a module that silently produced two different command
// lines depending on which tier ran it is worse than one that refuses.
func stringSlice(params map[string]any, key string) ([]string, bool, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return nil, false, nil
	}

	items, ok := raw.([]any)
	if !ok {
		return nil, true, fmt.Errorf("%s must be a list of strings", key)
	}

	out := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, true, fmt.Errorf("%s[%d] is %T, not a string: quote it in the runbook", key, i, item)
		}
		out = append(out, s)
	}
	return out, true, nil
}
