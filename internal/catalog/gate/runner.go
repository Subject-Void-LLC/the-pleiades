// Running one check on the device: its preconditions, then its command in
// a directory over SSH, or in a one-shot container on the device's engine.
package gate

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// pidMarker opens a directory check's output: the process ID the check
// runs as, so a run that ends early can stop it on the device.
const pidMarker = "pleiades-gate-pid "

// killAfter is how long the timeout command waits after its signal before
// killing a check that has not stopped.
const killAfter = "10s"

// probeTimeout bounds a check's precondition probe and a TCP precondition.
// It is a variable so a test can shorten it (export_test.go); nothing else
// assigns it.
var probeTimeout = 30 * time.Second

// stopTimeout bounds stopping a check on the device after its run ended.
const stopTimeout = 20 * time.Second

// timedOutCodes are how a check ends when the timeout command stopped it:
// 124 when it timed out, 137 and 143 when it was killed or terminated.
var timedOutCodes = []int{124, 137, 143}

// deviceRunner runs checks on one device for one task.
type deviceRunner struct {
	rc       sdk.RunbookContext
	device   inventory.InventoryItem
	params   map[string]any
	manifest string
	osName   string

	// dial serializes opening connections, so a run of sixteen checks does
	// not open sixteen logins at once and meet sshd's own startup limit.
	dial sync.Mutex
}

// connect opens one check's own connection: a Conn is not for concurrent
// use, and the pool lends a fresh one to each borrower.
func (d *deviceRunner) connect(ctx context.Context) (*remoteexec.Conn, error) {
	d.dial.Lock()
	defer d.dial.Unlock()
	return connectFor(ctx, d.rc, d.device, d.params, fqcnRun)
}

// run is the gate.Runner for this device.
func (d *deviceRunner) run(ctx context.Context, p gate.Planned) gate.Result {
	if p.Check.Image != "" {
		return d.runImage(ctx, p)
	}
	return d.runDir(ctx, p)
}

// failed is a FAIL result with reason.
func failed(reason string) gate.Result {
	return gate.Result{Status: gate.Fail, Reason: reason, ExitCode: gate.NoExit}
}

// runDir runs a directory check over SSH.
func (d *deviceRunner) runDir(ctx context.Context, p gate.Planned) gate.Result {
	c := p.Check
	conn, err := d.connect(ctx)
	if err != nil {
		return failed("could not reach the device: " + err.Error())
	}
	defer func() { _ = conn.Close() }()
	dir := dirOf(d.manifest, c)

	if result, settled := d.preconditions(ctx, conn, c, dir); settled {
		return result
	}

	start := time.Now()
	proc, err := conn.Start(ctx, dirCommand(c, dir, p.Timeout))
	if err != nil {
		return failed("could not start the check on the device: " + err.Error())
	}
	tail := gate.NewTail(tailLines, tailBytes)
	pid, readErr := readOutput(proc, tail)
	code, waitErr := proc.Wait()
	_ = proc.Close()
	elapsed := time.Since(start)

	if ctx.Err() != nil {
		d.stop(pid)
		return withOutput(gate.Result{Status: gate.Fail, Reason: stoppedReason(ctx), Duration: elapsed, ExitCode: gate.NoExit}, tail, proc.Stderr())
	}
	if waitErr != nil || readErr != nil {
		cause := waitErr
		if cause == nil {
			cause = readErr
		}
		d.stop(pid)
		return withOutput(gate.Result{Status: gate.Fail, Reason: "the session to the device ended before the check did: " + cause.Error(), Duration: elapsed, ExitCode: gate.NoExit}, tail, proc.Stderr())
	}
	return judge(c, code, elapsed, time.Duration(p.Timeout), slices.Contains(timedOutCodes, code), tail, proc.Stderr())
}

// dirCommand is the shell command a directory check runs as: into its
// directory, its process ID printed first, then the check itself under the
// timeout command, both of its streams joined into one.
func dirCommand(c gate.Check, dir string, timeout gate.Duration) string {
	secs := int64(math.Ceil(time.Duration(timeout).Seconds()))
	bound := []string{"timeout", "-k", killAfter, strconv.FormatInt(secs, 10) + "s"}
	var run string
	// No "--" after the duration: timeout stops reading options at the
	// duration, so a "--" there is taken as the command to run (GNU
	// coreutils says "failed to run command '--'"), and nothing after the
	// duration is ever read as an option.
	if len(c.Command) > 0 {
		run = remoteexec.QuoteCommand(append(bound, c.Command...))
	} else {
		run = remoteexec.QuoteCommand(append(bound, "sh", "-c", c.Shell))
	}
	return "cd " + remoteexec.QuoteArg(dir) + " && printf '" + pidMarker + "%s\\n' \"$$\" && exec " + run + " 2>&1"
}

// readOutput streams a check's output into tail, taking the process ID
// line the command prints first; a first line that is not one is output.
func readOutput(r io.Reader, tail *gate.Tail) (int, error) {
	br := bufio.NewReaderSize(r, 4096)
	pid := 0
	first, err := br.ReadSlice('\n')
	if v, ok := strings.CutPrefix(strings.TrimRight(string(first), "\n"), pidMarker); ok && err == nil {
		pid, _ = strconv.Atoi(strings.TrimSpace(v))
	} else {
		_, _ = tail.Write(first)
	}
	if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
		if errors.Is(err, io.EOF) {
			return pid, nil
		}
		return pid, err
	}
	if _, err := io.Copy(tail, br); err != nil {
		return pid, err
	}
	return pid, nil
}

// judge turns how a check ended into its result.
func judge(c gate.Check, code int, elapsed, timeout time.Duration, stoppedByTimeout bool, tail *gate.Tail, stderr string) gate.Result {
	r := gate.Result{Duration: elapsed, ExitCode: code}
	switch {
	case code == 0:
		r.Status = gate.Pass
		return r
	case stoppedByTimeout && elapsed >= timeout:
		r.Status, r.Reason = gate.Fail, fmt.Sprintf("timed out after %v", timeout)
	case slices.Contains(c.SkipExit, code):
		r.Status = gate.Skip
		r.Reason = fmt.Sprintf("it exited %d, which it declares means skipped", code)
		if last := tail.LastLine(); last != "" {
			r.Reason += ": " + last
		}
	case code == 127:
		r.Status, r.Reason = gate.Fail, "exited 127: the command was not found"
	default:
		r.Status, r.Reason = gate.Fail, fmt.Sprintf("exited %d", code)
	}
	return withOutput(r, tail, stderr)
}

// withOutput attaches the kept output, and anything the wrapper itself
// wrote to standard error (a missing directory), to a result.
func withOutput(r gate.Result, tail *gate.Tail, stderr string) gate.Result {
	r.Tail = tail.String()
	if extra := strings.TrimSpace(stderr); extra != "" {
		r.Tail = strings.TrimLeft(r.Tail+"\n"+gate.Escape(extra), "\n")
	}
	return r
}

// stoppedReason is why a running check was stopped when ctx ended.
func stoppedReason(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "stopped: the task's deadline passed while it ran"
	}
	return "stopped: the run was canceled while it ran"
}

// stop ends a directory check on the device, by the process ID it printed:
// TERM, which the timeout command passes on, then KILL if it is still there
// ten seconds later. A check whose ID never arrived cannot be stopped this
// way; its own timeout still ends it.
func (d *deviceRunner) stop(pid int) {
	if pid <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	conn, err := d.connect(ctx)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	p := strconv.Itoa(pid)
	_, _ = conn.Run(ctx, "kill -TERM "+p+" 2>/dev/null; i=0; while [ $i -lt 10 ] && kill -0 "+p+" 2>/dev/null; do sleep 1; i=$((i+1)); done; kill -KILL "+p+" 2>/dev/null; true")
}

// runImage runs an image check in a one-shot container on the device's
// engine.
func (d *deviceRunner) runImage(ctx context.Context, p gate.Planned) gate.Result {
	c := p.Check
	if result, settled := d.devicePreconditions(ctx, nil, c); settled {
		return result
	}
	d.dial.Lock()
	session, err := containerengine.Open(ctx, d.rc, d.device, d.params, fqcnRun, containerengine.RunOnceOps...)
	d.dial.Unlock()
	if err != nil {
		return failed("could not reach the device's container engine: " + err.Error())
	}
	defer session.Close()

	cmd := c.Command
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh", "-c", c.Shell}
	}
	tail := gate.NewTail(tailLines, tailBytes)
	start := time.Now()
	res, err := session.RunOnce(ctx, containerengine.RunOnceOptions{
		Image: c.Image, Cmd: cmd, Network: c.Network, Writable: c.Writable, Pull: c.Pull,
		Labels: map[string]string{"org.pleiades.gate": c.Name}, Timeout: time.Duration(p.Timeout), Output: tail,
	})
	elapsed := time.Since(start)
	switch {
	case ctx.Err() != nil:
		return withOutput(gate.Result{Status: gate.Fail, Reason: stoppedReason(ctx), Duration: elapsed, ExitCode: gate.NoExit}, tail, "")
	case err != nil:
		return withOutput(gate.Result{Status: gate.Fail, Reason: err.Error(), Duration: elapsed, ExitCode: gate.NoExit}, tail, "")
	}
	return judge(c, res.ExitCode, elapsed, time.Duration(p.Timeout), res.TimedOut, tail, "")
}
