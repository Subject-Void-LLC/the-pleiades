//go:build unix

// Package loader: starting a program, and the describe run Load asks of
// each one.
package loader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// newCommand builds the exec.Cmd every run of an external program uses,
// so describe and invoke cannot differ in how a program is started.
//
// command is the program's one argument, always one of pkg/external's two
// command words. The working directory is the program's own directory,
// the environment is sb's (scrubbedEnv's short allowlist, with TMPDIR at
// the run's scratch directory), and a canceled ctx sends SIGTERM first,
// so a program can clean up, with os/exec escalating to a kill once
// waitDelay has passed. The caller starts it with sb.start, which is what
// confines it.
//
// The program is executed through prog, the file openProgram checked,
// hashed and found approved, as /proc/self/fd/<programFD> in the child,
// never through path: a file put in path's place after the check is not
// what runs. path is still the program's argv[0] and names it in every
// message. The caller may put the response channel in ExtraFiles[0].
func newCommand(ctx context.Context, path, command string, prog *os.File, o Options, sb *sandbox) *exec.Cmd {
	target := fmt.Sprintf("/proc/self/fd/%d", programFD)
	if o.execByPath {
		target = path
	}
	// #nosec G204 -- target is the open file of a program Load vetted in a
	// directory only this user or root can write, hashed and found
	// approved through that same descriptor immediately before this run,
	// and command is one of pkg/external's two compile-time command
	// words. Nothing a runbook or a device controls reaches argv: task
	// data travels on stdin only.
	cmd := exec.CommandContext(ctx, target, command)
	cmd.Args[0] = path
	cmd.ExtraFiles = []*os.File{nil, prog}
	cmd.Dir = filepath.Dir(path)
	cmd.Env = sb.env()
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = o.waitDelay
	return cmd
}

// programFD is the file descriptor a program is started through in its
// own process: the one after external.ResponseFD, which ExtraFiles[1]
// becomes.
const programFD = external.ResponseFD + 1

// runDescribe runs path with the describe command and decodes the
// Description it prints.
//
// The program gets no stdin, the scrubbed environment, DescribeTimeout to
// finish, MaxResponse bytes of stdout and MaxOutput bytes of stderr. Each
// way that can go wrong is refused with its own message, carrying the
// program's (capped, masked) stderr where there is any, since that is
// usually where the program said what went wrong.
func runDescribe(ctx context.Context, path string, prog *os.File, o Options) (external.Description, error) {
	dctx, cancel := context.WithTimeout(ctx, o.DescribeTimeout)
	defer cancel()

	sb, err := newSandbox(path, o)
	if err != nil {
		return external.Description{}, err
	}
	defer sb.close()

	stdout := newCappedBuffer(o.MaxResponse)
	stderr := newCappedBuffer(o.MaxOutput)
	cmd := newCommand(dctx, path, external.CommandDescribe, prog, o, sb)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := sb.start(cmd)
	if runErr == nil {
		runErr = cmd.Wait()
	}
	// No secret has been handed to anything yet, but the shared pattern
	// rules (a PEM block, a token) still apply to whatever a program
	// prints about itself.
	errText := stderrForMessage(redact.Text(nil, stderr.String()))

	if o.Logger != nil && (stdout.Truncated() || stderr.String() != "") {
		o.Logger.Debug("external collection describe output",
			"program", path,
			"stderr", redact.Text(nil, stderr.String()),
			"stdout_truncated", stdout.Truncated(),
			"stderr_truncated", stderr.Truncated())
	}

	switch {
	case runErr != nil && ctx.Err() != nil:
		return external.Description{}, fmt.Errorf("describe was canceled: %w", ctx.Err())
	case runErr != nil && dctx.Err() != nil:
		return external.Description{}, fmt.Errorf("describe did not finish within %s and was stopped (stderr: %q)", o.DescribeTimeout, errText)
	case runErr != nil && !errors.Is(runErr, exec.ErrWaitDelay):
		return external.Description{}, fmt.Errorf("describe failed: %v (stderr: %q)", runErr, errText)
	case stdout.Truncated():
		return external.Description{}, fmt.Errorf("describe printed more than %d bytes", o.MaxResponse)
	}

	desc, err := parseDescription(stdout.Bytes())
	if err != nil {
		return external.Description{}, fmt.Errorf("%w (stderr: %q)", err, errText)
	}
	return desc, nil
}
