package remoteexec

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestConn_ReusesOneConnectionForManyCommands is the reason Conn is
// exported rather than hidden inside Run.
//
// A module that has to look before it leaps needs two or three commands
// to decide whether it changed anything, and paying for a fresh TCP
// connect, key exchange and authentication round for each one would make
// the idempotence check the expensive part of the task. This proves the
// second and third commands cost no extra dial.
func TestConn_ReusesOneConnectionForManyCommands(t *testing.T) {
	var dialCount int32
	fake, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		return "ran:" + cmd, "", 0
	})
	counting := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCount, 1)
		return fake(ctx, addr, config)
	}
	r := newTestRunner(counting, Options{})

	conn, err := r.Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	for _, command := range []string{"first", "second", "third"} {
		result, err := conn.Run(context.Background(), command)
		if err != nil {
			t.Fatalf("Run(%q): %v", command, err)
		}
		if result.Stdout != "ran:"+command {
			t.Errorf("Run(%q) stdout = %q, want %q", command, result.Stdout, "ran:"+command)
		}
	}

	if got := atomic.LoadInt32(&dialCount); got != 1 {
		t.Errorf("three commands over one Conn cost %d dials, want exactly 1", got)
	}
}

// TestConn_RunWithStdinPipesInputToTheRemoteCommand proves a caller can
// stream bytes into a remote command's standard input, which is what
// lets a module write a file's contents to a device without this package
// growing a file-transfer protocol of its own.
func TestConn_RunWithStdinPipesInputToTheRemoteCommand(t *testing.T) {
	const payload = "line one\nline two\n"

	// The fake server's handler cannot see the channel's stdin, so the
	// server side echoes back what it read by way of the handler's own
	// closure: readAll below is wired into the session by
	// newFakeSSHServerReadingStdin.
	var received string
	dial := newFakeSSHServerReadingStdin(t, func(cmd string, stdin string) (string, string, int) {
		received = stdin
		return "wrote " + cmd, "", 0
	})
	r := newTestRunner(dial, Options{})

	conn, err := r.Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	result, err := conn.RunWithStdin(context.Background(), "cat > /tmp/x", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("RunWithStdin: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
	if received != payload {
		t.Errorf("the remote command read %q from stdin, want %q", received, payload)
	}
}

// TestConn_RunHonorsContextCancellation proves a canceled context aborts
// a command that has already started running, rather than leaving the
// caller blocked on a remote side that has decided to take forever.
//
// This is what makes a task timeout reach the device. Without it a
// runaway command would hold the exclusive per-device lock its task
// acquired until the remote side gave up on its own, which for a
// genuinely stuck command is never.
func TestConn_RunHonorsContextCancellation(t *testing.T) {
	// The handler blocks until the test releases it, standing in for a
	// remote command that does not return.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	dial, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		<-release
		return "too late", "", 0
	})
	r := newTestRunner(dial, Options{})

	conn, err := r.Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = conn.Run(ctx, "sleep forever")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when the context expires mid-command")
	}
	// The error must name the cancellation, not whatever I/O failure the
	// session close produced: the mechanism is not the cause, and a
	// reader chasing a network fault that never happened is exactly the
	// wrong outcome.
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("error = %v, want it to name the expired context", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected cancellation to abort the command promptly, took %v", elapsed)
	}
}

// TestConn_CloseIsReportedOnce proves Close returns the underlying
// connection's own result, so a caller that checks it gets a real answer
// rather than a swallowed one.
func TestConn_CloseIsReportedOnce(t *testing.T) {
	dial, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		return "", "", 0
	})
	r := newTestRunner(dial, Options{})

	conn, err := r.Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// A second Close reports the connection is already gone rather than
	// panicking, which is what a deferred Close alongside an explicit one
	// would hit.
	if err := conn.Close(); err == nil {
		t.Error("expected the second Close to report the connection is already closed")
	}
}

// TestConn_SessionOpenFailureIsReported covers the branch where the
// connection is live but the remote refuses a session channel, which is
// a different failure from a dial error and must not be reported as one.
func TestConn_SessionOpenFailureIsReported(t *testing.T) {
	hostSigner := generateTestHostKey(t)
	srvConfig := &ssh.ServerConfig{NoClientAuth: true}
	srvConfig.AddHostKey(hostSigner)

	dial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		clientConn, serverConn := localPipe(t)
		go func() {
			sConn, chans, reqs, err := ssh.NewServerConn(serverConn, srvConfig)
			if err != nil {
				return
			}
			defer sConn.Close()
			go ssh.DiscardRequests(reqs)
			for newChannel := range chans {
				newChannel.Reject(ssh.Prohibited, "session channels are refused by this test server")
			}
		}()
		sshConn, chans, reqs, err := ssh.NewClientConn(clientConn, addr, config)
		if err != nil {
			return nil, err
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}
	r := newTestRunner(dial, Options{})

	conn, err := r.Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.Run(context.Background(), "echo hi")
	if err == nil {
		t.Fatal("expected an error when the remote refuses a session channel")
	}
	if !strings.Contains(err.Error(), "open session") {
		t.Errorf("error = %v, want it to name the session it could not open", err)
	}
}

// newFakeSSHServerReadingStdin is newFakeSSHServer with a handler that
// also receives everything the client wrote to the session's standard
// input, so RunWithStdin can be tested against a server that genuinely
// reads it rather than one that ignores it.
func newFakeSSHServerReadingStdin(t *testing.T, handler func(command, stdin string) (stdout, stderr string, exitCode int)) dialFunc {
	t.Helper()
	hostSigner := generateTestHostKey(t)

	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	config.AddHostKey(hostSigner)

	return func(ctx context.Context, addr string, clientConfig *ssh.ClientConfig) (*ssh.Client, error) {
		clientConn, serverConn := localPipe(t)
		go func() {
			sConn, chans, reqs, err := ssh.NewServerConn(serverConn, config)
			if err != nil {
				return
			}
			defer sConn.Close()
			go ssh.DiscardRequests(reqs)
			for newChannel := range chans {
				if newChannel.ChannelType() != "session" {
					newChannel.Reject(ssh.UnknownChannelType, "only session channels supported")
					continue
				}
				channel, requests, err := newChannel.Accept()
				if err != nil {
					continue
				}
				go serveStdinSession(channel, requests, handler)
			}
		}()

		sshConn, chans, reqs, err := ssh.NewClientConn(clientConn, addr, clientConfig)
		if err != nil {
			return nil, err
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}
}

// serveStdinSession answers one exec request, reading the channel to EOF
// first so the handler sees whatever the client piped in.
func serveStdinSession(channel ssh.Channel, requests <-chan *ssh.Request, handler func(command, stdin string) (string, string, int)) {
	defer channel.Close()
	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}

		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			req.Reply(false, nil)
			return
		}
		req.Reply(true, nil)

		// A command name of "ignores-stdin" or "fails" stands in for the
		// large family of real commands that exit without draining their
		// input; everything else reads to EOF, which is what a command
		// like cat does. The distinction is the subject of
		// TestConn_StdinTheRemoteNeverReadsIsNotAFailure.
		var stdin []byte
		if payload.Command != "ignores-stdin" && payload.Command != "fails" {
			// The client closes its write side once the reader it was given
			// is drained, which is what ends this read.
			stdin, _ = io.ReadAll(channel)
		}

		stdout, stderr, exitCode := handler(payload.Command, string(stdin))
		channel.Write([]byte(stdout))
		channel.Stderr().Write([]byte(stderr))
		channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ Status uint32 }{uint32(exitCode)}))
		return
	}
}

// TestConn_StdinTheRemoteNeverReadsIsNotAFailure states the contract: a
// command that exits cleanly without draining its input succeeded, and
// its exit status is the answer.
//
// It is NOT the regression test for the defect that contract was written
// after. That failure needs a real /bin/sh with real os/exec plumbing
// between the channel and the process to develop its timing; this file's
// in-process handler passed against the broken version, which was found
// by restoring the broken version and watching this test stay green.
// TestCommand_LargeStdinACommandNeverReadsStillSucceeds
// (internal/catalog/exec) is the test that actually fails, and it is
// where the mechanism is written down.
func TestConn_StdinTheRemoteNeverReadsIsNotAFailure(t *testing.T) {
	const payloadSize = 1 << 20

	// The handler returns at once without reading a single byte of the
	// channel, which is exactly what a command ignoring stdin does.
	dial := newFakeSSHServerReadingStdin(t, func(cmd, _ string) (string, string, int) {
		return "done", "", 0
	})
	r := newTestRunner(dial, Options{})

	conn, err := r.Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	result, err := conn.RunWithStdin(context.Background(), "ignores-stdin", strings.NewReader(strings.Repeat("x", payloadSize)))
	if err != nil {
		t.Fatalf("a command that exited cleanly without draining %d bytes of stdin was reported as an error: %v", payloadSize, err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "done" {
		t.Errorf("stdout = %q, want %q: the real output must survive an undrained stdin", result.Stdout, "done")
	}
}

// TestConn_StdinFailureDoesNotMaskARealExitStatus proves the same input
// against a command that fails still reports the command's own status
// rather than anything about the copy.
func TestConn_StdinFailureDoesNotMaskARealExitStatus(t *testing.T) {
	dial := newFakeSSHServerReadingStdin(t, func(cmd, _ string) (string, string, int) {
		return "", "it went wrong", 3
	})
	r := newTestRunner(dial, Options{})

	conn, err := r.Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	result, err := conn.RunWithStdin(context.Background(), "fails", strings.NewReader(strings.Repeat("y", 1<<20)))
	if err != nil {
		t.Fatalf("RunWithStdin: %v", err)
	}
	if result.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", result.ExitCode)
	}
	if result.Stderr != "it went wrong" {
		t.Errorf("stderr = %q, want the command's own message", result.Stderr)
	}
}
