// Detached commands: under an async task (internal/engine's async), a
// command is started in the background on the device and polled to its end,
// so it outlives a dropped connection, and the async limit's stop is a real
// stop of the work it started.
package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// detachKey marks a context whose commands run detached.
type detachKey struct{}

// WithDetach returns ctx marked so every Run on a Conn under it is started
// detached on the device and polled to its end (an async task's commands).
func WithDetach(ctx context.Context) context.Context {
	return context.WithValue(ctx, detachKey{}, true)
}

// detached reports whether ctx asks for detached commands.
func detached(ctx context.Context) bool {
	on, _ := ctx.Value(detachKey{}).(bool)
	return on
}

// Detached reports whether ctx was marked by WithDetach, so a caller handing
// a call to another process can say so there (pkg/wire.ChildRequest.Detach).
func Detached(ctx context.Context) bool { return detached(ctx) }

// withoutDetach returns ctx with detaching turned off, for the commands that
// start and poll a detached one.
func withoutDetach(ctx context.Context) context.Context {
	return context.WithValue(ctx, detachKey{}, false)
}

// redialer opens a fresh connection to the same device the way the first was
// opened (Runner.Connect sets it), for a detached command whose connection
// dropped.
type redialer func(ctx context.Context) (*Conn, error)

// Detached-command polling: the first poll soon, then backing off to a
// steady interval.
const (
	detachFirstPoll = 200 * time.Millisecond
	detachMaxPoll   = 3 * time.Second
	// detachRedials is how many times in a row a detached command's polls
	// reconnect before giving up on the device.
	detachRedials = 5
)

// DetachStopBudget is the most the stop of a detached command takes once its
// limit lands: a redial when its connection is gone, SIGTERM, a grace, then
// SIGKILL. A parent that ends a process running a detached command (a
// Runner's collection child, the loader's external program) gives it at
// least this long after its SIGTERM before killing it, or the kill lands
// before the stop reaches the device and the job runs on.
const DetachStopBudget = 20 * time.Second

// runDetached runs command detached: started in the background, polled for
// its exit status (reconnecting once per drop through c.redial), its output
// read back held to limit, and its directory removed. When ctx ends first,
// the job's process group is sent SIGTERM, then SIGKILL, and the error is
// ctx's. A start or a poll that fails with the job possibly running stops
// the job before the error is returned, so no job outlives its task unseen.
func (c *Conn) runDetached(ctx context.Context, command string, limit int) (Result, error) {
	plain := withoutDetach(ctx)
	name := newJobName()
	// The command travels on standard input, never in an argument, with its
	// length so a cut-off send is never started.
	started, err := c.run(plain, QuoteCommand([]string{"sh", "-c", detachStart, "start", c.jobShell(), strconv.Itoa(len(command)), name}), strings.NewReader(command), detachAnswerLimit)
	if err != nil {
		// The command may have reached the device and started before the
		// call failed: stop it by the name it was given. A Raw view's device
		// has no sh to have started it, and its CLI would take the stop
		// script for a command.
		if c.raw {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("remoteexec: a detached command's start on %s failed, and the job, if it started, %s: %w", c.addr, c.stopDetached(jobByName(name)), err)
	}
	switch started.ExitCode {
	case 0:
	case detachIncomplete:
		return Result{}, fmt.Errorf("remoteexec: a detached command reached %s incomplete, so nothing was started", c.addr)
	default:
		// The script stopped before starting anything: no POSIX sh (a
		// Windows OpenSSH device), no private directory it may safely use, no
		// way to stop the job as a whole, or an MSYS shell. The command runs
		// where it stands instead, still under the async limit, which stops
		// it with its connection.
		return c.run(plain, command, nil, limit)
	}
	dir := lastLine(started.Stdout)
	if !jobDir.MatchString(dir) {
		// Started, maybe: never run it a second time, and stop it by name.
		return Result{}, fmt.Errorf("remoteexec: a detached command on %s answered with no job directory, and the job, if it started, %s: %q %q", c.addr, c.stopDetached(jobByName(name)), boundedText(dir), strings.TrimSpace(boundedText(started.Stderr)))
	}
	qdir := QuoteArg(dir)

	// The limit can land between polls or inside one, whose command then
	// fails with the context's error: either way the job is stopped.
	stopped := func() (Result, error) {
		c.stopDetached(qdir)
		return Result{}, fmt.Errorf("remoteexec: run command on %s: %w", c.addr, ctx.Err())
	}
	wait := detachFirstPoll
	redials := 0
	for {
		select {
		case <-ctx.Done():
			return stopped()
		case <-time.After(wait):
		}
		wait = min(wait*2, detachMaxPoll)
		rc, err := c.run(plain, "printf '"+rcPrefix+"'; cat "+qdir+"/rc 2>/dev/null", nil, detachAnswerLimit)
		if err != nil && ctx.Err() != nil {
			return stopped()
		}
		if err != nil {
			if redials >= detachRedials || !c.reconnect(plain, err) {
				// A poll that cannot be made is no reason to leave the job
				// running past its task: it is stopped, on a fresh
				// connection when this one is gone.
				switch c.stopDetached(qdir) {
				case stopSignalled:
					return Result{}, fmt.Errorf("remoteexec: lost a detached command on %s, and stopped it: %w", c.addr, err)
				case stopEnded:
					return Result{}, fmt.Errorf("remoteexec: lost a detached command on %s, which had already finished: %w", c.addr, err)
				}
				return Result{}, fmt.Errorf("remoteexec: lost a detached command on %s, which may still be running in %q: %w", c.addr, dir, err)
			}
			redials++
			continue
		}
		redials = 0
		if rc.ExitCode != 0 {
			continue // not finished
		}
		code, err := pollRC(rc.Stdout)
		if err != nil {
			c.stopDetached(qdir)
			return Result{}, fmt.Errorf("remoteexec: a detached command on %s left an exit status that is not a number: %w", c.addr, err)
		}
		// The job has finished: its directory, which holds the command, is
		// removed however its output reads.
		remove := func() { _, _ = c.run(plain, "rm -rf "+qdir, nil, detachAnswerLimit) }
		out, err := c.readJobFile(plain, qdir+"/out", name, limit)
		if err != nil && ctx.Err() != nil {
			return stopped()
		}
		if err != nil {
			remove()
			return Result{}, err
		}
		errOut, err := c.readJobFile(plain, qdir+"/err", name, limit)
		if err != nil && ctx.Err() != nil {
			return stopped()
		}
		if err != nil {
			remove()
			return Result{}, err
		}
		remove()
		return Result{Stdout: out, Stderr: errOut, ExitCode: code}, nil
	}
}

// readJobFile reads a finished job's file at path, held to limit: printed
// after a marker line naming the job, so whatever a login shell's startup
// files print ahead of it is not taken for the file's contents.
func (c *Conn) readJobFile(ctx context.Context, path, name string, limit int) (string, error) {
	marker := "pleiades-job-" + name
	res, err := c.run(ctx, "echo "+marker+"; cat "+path, nil, limit+detachAnswerLimit)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("remoteexec: reading a detached command's output on %s exited %d: %s", c.addr, res.ExitCode, strings.TrimSpace(boundedText(res.Stderr)))
	}
	text, ok := afterMarker(res.Stdout, marker)
	if !ok {
		return "", fmt.Errorf("remoteexec: reading a detached command's output on %s: no start line in the answer", c.addr)
	}
	if len(text) > limit {
		return "", fmt.Errorf("remoteexec: run command on %s: %w (%d bytes on one stream)", c.addr, ErrOutputTooLarge, limit)
	}
	return text, nil
}

// reconnect replaces c's connection with a fresh one when err is a dropped
// connection and c can redial, reporting whether it did. The old connection
// is tainted so a pool closes it, and the new one is closed with c.
func (c *Conn) reconnect(ctx context.Context, err error) bool {
	var exit *ssh.ExitError
	if c.redial == nil || errors.As(err, &exit) || errors.Is(err, ErrOutputTooLarge) || ctx.Err() != nil {
		return false
	}
	fresh, derr := c.redial(ctx)
	if derr != nil {
		return false
	}
	c.taint()
	c.extra = append(c.extra, fresh.chain...)
	c.client = fresh.client
	// A route's connection is cut through its route; the old one's is kept,
	// since that connection is still this Conn's to close.
	if fresh.severVia != nil {
		old, next := c.severVia, fresh.severVia
		c.severVia = func() {
			if old != nil {
				old()
			}
			next()
		}
	}
	return true
}

// stopOutcome is what the stop of a detached job found on the device.
type stopOutcome int

const (
	// stopUnknown: the stop did not run, or found no job it could stop
	// (nothing to read, or a base it may not trust).
	stopUnknown stopOutcome = iota
	// stopSignalled: the job's process group was running and was stopped.
	stopSignalled
	// stopEnded: the job's process group had already ended.
	stopEnded
)

// String is how an error names the job after the stop, so it never claims a
// stop that did not happen, nor leaves a finished job reading as one that
// may run on.
func (o stopOutcome) String() string {
	switch o {
	case stopSignalled:
		return "was stopped"
	case stopEnded:
		return "had already finished"
	}
	return "may be running"
}

// stopDetached ends a detached job that ran past its limit through
// detachStop's script. It runs on a fresh context, the job's own having
// ended, and on a fresh connection, as the same account, when the job's own
// is already gone: a Runner's child is told to stop by the same signal that
// closes its connections, so the one the job started on is often closed by
// the time the stop runs. It reports what the stop found.
func (c *Conn) stopDetached(qdir string) stopOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), DetachStopBudget)
	defer cancel()
	ctx = withoutDetach(ctx)
	stop := QuoteCommand([]string{"sh", "-c", detachStop(qdir)})
	if res, err := c.run(ctx, stop, nil, detachAnswerLimit); err == nil || c.redial == nil {
		return stopResult(res, err)
	}
	fresh, err := c.redial(ctx)
	if err != nil {
		return stopUnknown
	}
	defer func() { _ = fresh.Close() }()
	fresh.become, fresh.raw = c.become, c.raw
	return stopResult(fresh.run(ctx, stop, nil, detachAnswerLimit))
}

// stopResult reads a stop script's answer.
func stopResult(res Result, err error) stopOutcome {
	switch {
	case err != nil:
		return stopUnknown
	case res.ExitCode == 0:
		return stopSignalled
	case res.ExitCode == detachStopEnded:
		return stopEnded
	}
	return stopUnknown
}
