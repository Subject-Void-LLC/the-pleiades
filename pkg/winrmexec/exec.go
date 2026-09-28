// Running one command: Execute, and the Run and RunWithStdin shorthands
// every existing caller uses.
package winrmexec

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/masterzen/winrm"
)

// Command is one thing to run on a Windows device.
type Command struct {
	// Shell is the execution mode: which parser, if any, reads Script.
	Shell Shell

	// Script is what runs. For ShellNone it is a complete command line,
	// a program and its arguments (CommandLine builds one). For ShellCmd
	// it is one line of cmd.exe script, and for ShellPowerShell a
	// PowerShell script of any length the command line can carry.
	Script string

	// Stdin is written to the command's standard input, which is then
	// closed. It is the channel for a secret or for data too large for a
	// command line: see RunWithStdin.
	Stdin string

	// Env sets environment variables in the shell before the command
	// starts, read in a script as $env:NAME in PowerShell or !NAME! in
	// cmd.exe, never %NAME%, which cmd.exe expands before parsing and so
	// turns a value into syntax (see cmdLine). Names are letters,
	// digits and underscores, not starting with a digit. An environment is
	// visible to other processes on the device, so a secret belongs on
	// Stdin, never here (PLAN.md Section 17.5 forbids both argv and the
	// environment for one).
	Env map[string]string
}

// envName is what an environment variable name may be. It keeps a name
// usable from both shells unquoted, and keeps it safe as an XML
// attribute with no escaping.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Run executes script on target through shell, authenticating with auth.
//
// The Result and error contract is pkg/remoteexec's exactly: a non-zero
// Result.ExitCode is the remote script reporting failure and is not a Go
// error, while a non-nil error means the outcome could not be determined
// at all.
func Run(ctx context.Context, target Target, auth Auth, shell Shell, script string, opts Options) (Result, error) {
	return Execute(ctx, target, auth, Command{Shell: shell, Script: script}, opts)
}

// RunWithStdin is Run with stdin written to the script's standard input,
// which is then closed so a script reading to the end sees end of file.
//
// It exists for two limits a script cannot get around from inside the
// command line it travels in.
//
// Length. A script reaches the device inside a command line that the
// service's cmd.exe caps at 8191 characters, in every mode, and a
// PowerShell script travels as UTF-16LE base64, which more than doubles
// it. Stdin has no such cap: it travels in WS-Man Send messages, not on
// any command line.
//
// Secrecy. A command line is visible to every process on the device that
// can list processes, for as long as the command runs, and PLAN.md
// Section 17.5 keeps a secret off argv for exactly that reason. A value
// the script reads from stdin never appears on one.
//
// So the pattern this enables is a short script that reads its data from
// [Console]::In (PowerShell) or from its own standard input (a program
// under ShellNone), with the data here.
func RunWithStdin(ctx context.Context, target Target, auth Auth, shell Shell, script, stdin string, opts Options) (Result, error) {
	return Execute(ctx, target, auth, Command{Shell: shell, Script: script, Stdin: stdin}, opts)
}

// Execute runs cmd on target, authenticating with auth. The Result and
// error contract is Run's.
//
// A *NotStartedError means nothing ran on the device, so retrying it
// cannot run anything twice. Any other error may have come after the
// command started.
func Execute(ctx context.Context, target Target, auth Auth, cmd Command, opts Options) (Result, error) {
	if cmd.Script == "" {
		return Result{}, fmt.Errorf("winrm: empty script")
	}
	// Resolved before the cleartext check below, because certificate
	// authentication selects HTTPS on its own and would otherwise be
	// refused here for a risk it does not take.
	opts = opts.resolve(auth)
	if opts.DisableEncryption && !opts.HTTPS {
		return Result{}, fmt.Errorf("winrm: DisableEncryption requires HTTPS: over plain HTTP it would send the credential exchange and every script in cleartext")
	}

	// Everything the caller can get wrong is checked before a client is
	// built or a credential is read, so an author's mistake is reported
	// as that mistake and not as a connection or credential problem.
	line, err := commandLine(cmd.Shell, cmd.Script, opts)
	if err != nil {
		return Result{}, err
	}
	if cmd.Shell == ShellCmd {
		if err := checkCmdEnvReads(cmd.Script, cmd.Env); err != nil {
			return Result{}, err
		}
	}
	spec, err := shellSpecFor(cmd, opts)
	if err != nil {
		return Result{}, err
	}

	x, err := newExchange(target, auth, opts)
	if err != nil {
		return Result{}, err
	}

	// The deadline is enforced HERE rather than handed to the library,
	// because the library discards both of the things that would
	// normally carry it.
	//
	// Windows refuses unencrypted WinRM by default, so nearly every
	// operation this package performs goes through winrm.Encryption. Its
	// Transport method builds a bare &http.Client{}: no Timeout, and the
	// default transport, whose ResponseHeaderTimeout is unset and
	// therefore unlimited. Its requests are built with http.NewRequest
	// rather than NewRequestWithContext, so no context reaches the HTTP
	// layer at all, and the field that would fix it is unexported.
	//
	// The measured consequence, which is what prompted this: a task that
	// reconfigured a device's own network address, destroying the
	// connection carrying it, blocked for two minutes fifty-one seconds
	// and then three minutes ten seconds on separate runs.
	//
	// What this does and does not buy is worth stating plainly. The
	// CALLER is released on time. The goroutine below is not: it stays
	// parked in the library until the operating system tears the socket
	// down, holding one connection and one goroutine until it does. The
	// exchange checks ctx between messages, so once the stuck request
	// returns it tells the command to stop and closes the shell rather
	// than carrying on.
	ctx, cancel := withOperationDeadline(ctx, opts.Timeout)
	defer cancel()

	// Buffered, so the goroutine below can always deliver and exit even
	// when nobody is left waiting for it.
	done := make(chan operationResult, 1)
	go func() {
		res, runErr := x.run(ctx, line, cmd.Stdin, spec)
		done <- operationResult{res: res, err: runErr}
	}()

	select {
	case out := <-done:
		if cmd.Shell == ShellPowerShell {
			out.res.Stderr = decodeCLIXML(out.res.Stderr)
		}
		return out.res, out.err
	case <-ctx.Done():
		if !opts.LeaveRunningOnTimeout && stopAbandoned(x, target, auth, opts) {
			return Result{}, fmt.Errorf(
				"winrm: %s: gave up waiting for %s after %s, then stopped the command and closed its shell; anything it "+
					"changed before that stays changed", ctx.Err(), target.Host, describeTimeout(opts.Timeout))
		}
		return Result{}, fmt.Errorf(
			"winrm: %s: gave up waiting for %s after %s. The command may still be running on the device, and if it "+
				"changed the network configuration it has probably already taken effect; this says only that no "+
				"answer came back in time",
			ctx.Err(), target.Host, describeTimeout(opts.Timeout))
	}
}

// abandonedCleanupBound is how long stopAbandoned waits for the device.
const abandonedCleanupBound = 10 * time.Second

// stopAbandoned tells the service to stop the command x started and to
// close its shell, and reports whether both were sent and answered.
//
// It uses a fresh exchange, because x's connection is still parked in the
// request that never came back, and the NTLM transport's message
// encryption is not safe to share between concurrent requests. It waits
// at most abandonedCleanupBound, since a device that stopped answering
// would otherwise hold the caller a second time; a shell the service
// never hears about is reclaimed by its own idle timeout, two hours by
// default, with its command still running until then. That is what this
// exists to prevent: a process that exits after a timeout cannot stop the
// command later.
func stopAbandoned(x *exchange, target Target, auth Auth, opts Options) bool {
	shellID, commandID := x.started()
	if shellID == "" {
		return false
	}
	done := make(chan bool, 1)
	go func() {
		fresh, err := newExchange(target, auth, opts)
		if err != nil {
			done <- false
			return
		}
		if commandID != "" {
			fresh.terminate(shellID, commandID)
		}
		_, err = fresh.post(winrm.NewDeleteShellRequest(fresh.url, shellID, &fresh.params))
		done <- err == nil
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(abandonedCleanupBound):
		return false
	}
}

// shellSpecFor checks and collects what the shell create message carries
// for cmd: its environment, in name order so the message is the same
// every time, and the device's working directory.
func shellSpecFor(cmd Command, opts Options) (shellSpec, error) {
	if err := CheckText("working directory", opts.WorkingDirectory); err != nil {
		return shellSpec{}, err
	}
	spec := shellSpec{workingDirectory: opts.WorkingDirectory, noProfile: opts.NoProfile}
	names := make([]string, 0, len(cmd.Env))
	for name := range cmd.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !envName.MatchString(name) {
			return shellSpec{}, fmt.Errorf("winrm: environment variable name %q must be letters, digits and underscores, not starting with a digit", name)
		}
		value := cmd.Env[name]
		if err := CheckText("environment variable "+name, value); err != nil {
			return shellSpec{}, err
		}
		spec.env = append(spec.env, envVar{name: name, value: value})
	}
	return spec, nil
}
