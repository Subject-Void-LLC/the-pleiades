// Detached commands: under an async task (internal/engine's async), a
// command is started in the background on the device and polled to its end,
// so it outlives a dropped connection, and the async limit's stop is a real
// stop of the work it started.
package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

// detachStart starts "$1" in the background in a fresh private directory
// under $HOME/.pleiades-async-<uid> (the account's id, since sudo may keep
// the login's HOME, and a directory become made is not the login's), in its own session where setsid exists and
// otherwise in its own process group through job control (so a stop reaches
// everything it started), and prints the directory. The exit
// status is written last, through a rename, so a poll never reads half of it.
const detachStart = `umask 077
base="$HOME/.pleiades-async-$(id -u)"
mkdir -p "$base" || exit 70
d=$(mktemp -d "$base/job.XXXXXXXX") || exit 70
job='sh -c "$1" >"$2/out" 2>"$2/err" </dev/null; echo $? >"$2/rc.tmp"; mv "$2/rc.tmp" "$2/rc"'
if command -v setsid >/dev/null 2>&1; then
  setsid sh -c "$job" job "$1" "$d" >/dev/null 2>&1 &
else
  set -m 2>/dev/null
  sh -c "$job" job "$1" "$d" >/dev/null 2>&1 &
fi
echo $! >"$d/pid"
echo "$d"`

// jobDir is what a started job's directory must look like before any later
// command names it.
var jobDir = regexp.MustCompile(`^/[^\x00]*/\.pleiades-async-[0-9]+/job\.[A-Za-z0-9]+$`)

// Detached-command polling: the first poll soon, then backing off to a
// steady interval.
const (
	detachFirstPoll = 200 * time.Millisecond
	detachMaxPoll   = 3 * time.Second
	// detachRedials is how many times in a row a detached command's polls
	// reconnect before giving up on the device.
	detachRedials = 5
)

// runDetached runs command detached: started in the background, polled for
// its exit status (reconnecting once per drop through c.redial), its output
// read back held to limit, and its directory removed. When ctx ends first,
// the job's process group is sent SIGTERM, then SIGKILL, and the error is
// ctx's.
func (c *Conn) runDetached(ctx context.Context, command string, limit int) (Result, error) {
	plain := withoutDetach(ctx)
	started, err := c.run(plain, QuoteCommand([]string{"sh", "-c", detachStart, "start", command}), nil, limit)
	if err != nil {
		return Result{}, err
	}
	if started.ExitCode != 0 {
		// The script ends with an echo, so a failure means it stopped before
		// starting anything: no POSIX sh (a Windows OpenSSH device) or no
		// private directory. The command runs where it stands instead, still
		// under the async limit, which stops it with its connection.
		return c.run(plain, command, nil, limit)
	}
	dir := strings.TrimSpace(started.Stdout)
	if !jobDir.MatchString(dir) {
		// Started, maybe: never run it a second time.
		return Result{}, fmt.Errorf("remoteexec: a detached command on %s answered with no job directory, and may be running: %q %s", c.addr, boundedText(dir), strings.TrimSpace(boundedText(started.Stderr)))
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
		rc, err := c.run(plain, "cat "+qdir+"/rc 2>/dev/null", nil, 64)
		if err != nil && ctx.Err() != nil {
			return stopped()
		}
		if err != nil {
			if redials >= detachRedials || !c.reconnect(plain, err) {
				return Result{}, fmt.Errorf("remoteexec: lost a detached command on %s, which may still be running in %s: %w", c.addr, dir, err)
			}
			redials++
			continue
		}
		redials = 0
		if rc.ExitCode != 0 {
			continue // not finished
		}
		code, err := strconv.Atoi(strings.TrimSpace(rc.Stdout))
		if err != nil {
			return Result{}, fmt.Errorf("remoteexec: a detached command on %s left an exit status that is not a number: %q", c.addr, rc.Stdout)
		}
		out, err := c.run(plain, "cat "+qdir+"/out", nil, limit)
		if err != nil && ctx.Err() != nil {
			return stopped()
		}
		if err != nil {
			return Result{}, err
		}
		errOut, err := c.run(plain, "cat "+qdir+"/err", nil, limit)
		if err != nil && ctx.Err() != nil {
			return stopped()
		}
		if err != nil {
			return Result{}, err
		}
		_, _ = c.run(plain, "rm -rf "+qdir, nil, 64)
		return Result{Stdout: out.Stdout, Stderr: errOut.Stdout, ExitCode: code}, nil
	}
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
	return true
}

// stopDetached ends a detached job that ran past its limit: SIGTERM to its
// process group (or the process, with no setsid), SIGKILL after a grace,
// then its directory removed. The group is named as -pid after the signal,
// not after "--", which dash's kill refuses. It runs on a fresh context, the
// job's own having ended, and on a fresh connection, as the same account,
// when the job's own is already gone: a Runner's child is told to stop by
// the same signal that closes its connections, so the one the job started on
// is often closed by the time the stop runs.
func (c *Conn) stopDetached(qdir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = withoutDetach(ctx)
	stop := QuoteCommand([]string{"sh", "-c", `p=$(cat ` + qdir + `/pid 2>/dev/null) || exit 0
kill -TERM -"$p" 2>/dev/null || kill -TERM "$p" 2>/dev/null
i=0; while [ $i -lt 3 ] && kill -0 "$p" 2>/dev/null; do sleep 1; i=$((i+1)); done
kill -KILL -"$p" 2>/dev/null || kill -KILL "$p" 2>/dev/null
rm -rf ` + qdir})
	if _, err := c.run(ctx, stop, nil, 4096); err == nil || c.redial == nil {
		return
	}
	fresh, err := c.redial(ctx)
	if err != nil {
		return
	}
	defer func() { _ = fresh.Close() }()
	fresh.become = c.become
	_, _ = fresh.run(ctx, stop, nil, 4096)
}
