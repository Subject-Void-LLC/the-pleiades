// Package ssh: tests that a command whose context ends, or whose output passes
// its bound, never comes back from a real OpenSSH sshd as a completed command. sshd reports an exit status after
// the client's close (the signal's, or the remote's own once it drained a
// cut-off input), which the in-process server in pkg/remoteexec never does,
// so only a real sshd shows it.
package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// connectToContainer opens a connection to the shared sshd container.
func connectToContainer(t *testing.T) *remoteexec.Conn {
	t.Helper()
	host, port := requireSSHContainer(t)
	auth, err := remoteexec.AuthFrom(containerSSHUser, containerSSHPassword, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).
		Connect(context.Background(), nil, remoteexec.Target{Host: host, Port: port}, auth)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// runOnFreshConnection runs command on a connection of its own and returns
// its standard output, for checking what an earlier command left behind.
func runOnFreshConnection(t *testing.T, command string) string {
	t.Helper()
	conn := connectToContainer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := conn.Run(ctx, command)
	if err != nil {
		t.Fatalf("checking with %q: %v", command, err)
	}
	return res.Stdout
}

// TestSSHContainer_ACancelledCommandIsNotACompletedOne: a command cancelled by
// its context returns the context's error and an empty Result, never the
// status the cancellation's SIGTERM produced (143) as if the command had
// finished and failed.
func TestSSHContainer_ACancelledCommandIsNotACompletedOne(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	conn := connectToContainer(t)
	for i := range 3 {
		// Long enough that the deadline lands while the command runs, not
		// while its session is being opened, even on a loaded machine.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		res, err := conn.Run(ctx, "echo started; sleep 30")
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("run %d = %+v, %v; want context.DeadlineExceeded", i, res, err)
		}
		if res != (remoteexec.Result{}) {
			t.Fatalf("run %d returned %+v alongside its cancellation; want an empty Result", i, res)
		}
		// The connection was tainted by the cancellation; a fresh one runs
		// the next attempt.
		conn = connectToContainer(t)
	}
}

// TestSSHContainer_ACancelledInputIsNotReportedAsCompleted: a command reading
// standard input, cancelled partway through it, returns the context's error,
// even when sshd hands the command an end of input on the client's close and
// the command then exits 0 for the part it got.
func TestSSHContainer_ACancelledInputIsNotReportedAsCompleted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	conn := connectToContainer(t)
	dest := fmt.Sprintf("/tmp/cancelled-input-%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := &cancelMidStream{first: bytes.Repeat([]byte("x"), 4096), cancel: cancel, ctx: ctx}
	res, err := conn.RunWithStdin(ctx, "cat > "+dest+".tmp && mv "+dest+".tmp "+dest+" && echo committed", in)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunWithStdin = %+v, %v; want context.Canceled", res, err)
	}
	// What the device did is reported for the record: on a server that hands
	// the command an end of input, the prefix may be committed, which is why
	// the call must never say the write succeeded.
	t.Logf("after the cancellation the device holds: %q", strings.TrimSpace(runOnFreshConnection(t, "wc -c < "+dest+" 2>/dev/null || echo absent")))
}

// TestSSHContainer_AContextAlreadyDoneRunsNothing: a context that ended before
// the call sends nothing, so the command's marker is never written.
func TestSSHContainer_AContextAlreadyDoneRunsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	conn := connectToContainer(t)
	marker := fmt.Sprintf("/tmp/done-context-%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := conn.Run(ctx, "touch "+marker); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	// Long enough for a command that was sent to have run.
	time.Sleep(time.Second)
	if got := strings.TrimSpace(runOnFreshConnection(t, "[ -e "+marker+" ] && echo present || echo absent")); got != "absent" {
		t.Fatalf("the marker is %s; a command was sent on a context already done", got)
	}
}

// cancelMidStream returns first, then cancels its context and ends the input
// once the context is done: a transfer cut off by the task's limit.
type cancelMidStream struct {
	first  []byte
	sent   bool
	cancel context.CancelFunc
	ctx    context.Context
}

// Read returns the first chunk, then cancels and waits for the cancellation.
func (r *cancelMidStream) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.first), nil
	}
	r.cancel()
	<-r.ctx.Done()
	// Give the cancellation time to close the session, so what sshd gets is
	// the close and not this end of input.
	time.Sleep(200 * time.Millisecond)
	return 0, io.EOF
}

// TestSSHContainer_ACommandCutForItsOutputIsEndedNotWaitedOn: a command whose
// output passes its bound is ended as a cancelled one is, so the call
// returns ErrOutputTooLarge within the abandon grace rather than when the
// command ends by itself (the old code waited out the command's own sleep).
// The command outlives the call's deadline by far, so a call that waits on
// it ends with the deadline instead; no wall-clock bound is asserted.
func TestSSHContainer_ACommandCutForItsOutputIsEndedNotWaitedOn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	conn := connectToContainer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := conn.RunWithStdinBounded(ctx, "head -c 100000 /dev/zero; sleep 600", nil, 1000)
	if !errors.Is(err, remoteexec.ErrOutputTooLarge) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunWithStdinBounded = %v; want ErrOutputTooLarge before the deadline", err)
	}
}

// TestSSHContainer_AStartedCommandCancelledIsNotACompletedOne: a command
// started as a live Process whose context ends reports the context's error
// from Wait, never the 143 its SIGTERM produced; and a context already done
// starts nothing.
func TestSSHContainer_AStartedCommandCancelledIsNotACompletedOne(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	conn := connectToContainer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p, err := conn.Start(ctx, "echo started; sleep 30")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Close() }()
	_, _ = io.ReadAll(p)
	code, err := p.Wait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait = %d, %v; want context.DeadlineExceeded", code, err)
	}

	marker := fmt.Sprintf("/tmp/done-start-%d", time.Now().UnixNano())
	done, cancelDone := context.WithCancel(context.Background())
	cancelDone()
	if p, err := connectToContainer(t).Start(done, "touch "+marker); err == nil {
		_ = p.Close()
		t.Fatal("Start on a context already done started a command")
	}
	time.Sleep(time.Second)
	if got := strings.TrimSpace(runOnFreshConnection(t, "[ -e "+marker+" ] && echo present || echo absent")); got != "absent" {
		t.Fatalf("the marker is %s; a command was started on a context already done", got)
	}
}
