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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
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
// purpose, and the reason is worth stating accurately because an earlier
// version of this comment stated it wrongly.
//
// It is NOT that nothing checks the device declares CommandExecCapable:
// the engine does, before this method runs, on the CLI and on a Runner
// alike (engine.checkMethodCapabilities), and it checks the device is
// reachable over this method's transport too (collection.CheckTransports).
// That second check is what lets a Windows server have CommandExecCapable,
// through WindowsShellCapable, without these SSH methods running on it.
// The Controller's own admission still consults only
// engine.ActionCapability, so a launch is not refused there.
//
// The reason is the second one, which does hold: the Runner's own device
// adapter carries a declared capability list with no accessors behind
// it, so a hard assertion here would refuse a dispatch at the far end of
// a network hop with a message about a Go interface. An absent working
// directory is a perfectly good answer, so asking is better than
// demanding.
func workingDirectory(device inventory.InventoryItem, params map[string]any) string {
	if dir := sdk.StringParam(params, paramChdir); dir != "" {
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
	if creates := sdk.StringParam(params, paramCreates); creates != "" {
		exists, err := pathExists(ctx, conn, dir, creates)
		if err != nil {
			return "", fmt.Errorf("checking creates path %q: %w", creates, err)
		}
		if exists {
			return skipDecision(fmt.Sprintf("skipped, since %s exists", creates)), nil
		}
	}

	if removes := sdk.StringParam(params, paramRemoves); removes != "" {
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

// unguardedCheck is a check's answer for a command no creates or removes
// guards: whether it would change anything cannot be known without running
// it, so the check says it cannot check this call rather than guessing.
// Nil when a guard is set, since the guard is what a check can read.
//
// It is also both methods' Descriptor.CheckCall, so validation refuses
// check_mode on an unguarded command with this same answer before a run
// starts.
func unguardedCheck(params map[string]any) error {
	if sdk.StringParam(params, paramCreates) != "" || sdk.StringParam(params, paramRemoves) != "" {
		return nil
	}
	return collection.CannotCheck("what a command changes cannot be known without running it; " +
		"a creates or removes guard would say what its having run looks like, and a check would read that")
}

// recordWouldRun writes the stats a check predicts for a command its
// guard would let run: not skipped, and the command line that would run.
// Its exit status and output are not known without running it, so they
// are left out rather than guessed.
func recordWouldRun(rc sdk.RunbookContext, command string) error {
	for key, value := range map[string]any{statSkipped: false, statCmd: command} {
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
