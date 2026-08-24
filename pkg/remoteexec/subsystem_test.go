package remoteexec

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"
)

// subsystemHandler is what a fake server does with one accepted
// subsystem channel: it receives the requested subsystem name and the
// channel itself, and runs in its own goroutine.
type subsystemHandler func(name string, channel ssh.Channel)

// newFakeSubsystemSSHServer starts an in-process, loopback-TCP-backed
// SSH server whose session channel answers "subsystem" instead of
// "exec" or "pty-req"/"shell", mirroring newFakeSSHServer's and
// newFakePTYSSHServer's own real-handshake construction (see
// runner_test.go's localPipe for why net.Pipe is unusable for an SSH
// handshake).
//
// accept decides whether the subsystem request is granted, so the
// refusal path a real device takes when the requested subsystem is not
// enabled is exercised against a genuine SSH channel-request rejection
// rather than a substituted error value.
func newFakeSubsystemSSHServer(t *testing.T, accept bool, handler subsystemHandler) dialFunc {
	t.Helper()
	hostSigner := generateTestHostKey(t)

	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	config.AddHostKey(hostSigner)

	dial := func(ctx context.Context, addr string, clientConfig *ssh.ClientConfig) (*ssh.Client, error) {
		clientConn, serverConn := localPipe(t)
		go serveOneSubsystemConnection(serverConn, config, accept, handler)
		sshConn, chans, reqs, err := ssh.NewClientConn(clientConn, addr, clientConfig)
		if err != nil {
			return nil, err
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}
	return dial
}

func serveOneSubsystemConnection(conn net.Conn, config *ssh.ServerConfig, accept bool, handler subsystemHandler) {
	sConn, chans, reqs, err := ssh.NewServerConn(conn, config)
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
		go handleFakeSubsystemSession(channel, requests, accept, handler)
	}
}

// handleFakeSubsystemSession answers exactly one "subsystem" request,
// declining anything else the way handleFakeSession and
// handleFakePTYSession both do.
//
// RFC 4254 section 6.5's payload is a single string, the subsystem name,
// in the same wire form an "exec" request's command carries, so it
// unmarshals through the identical one-field struct.
func handleFakeSubsystemSession(channel ssh.Channel, requests <-chan *ssh.Request, accept bool, handler subsystemHandler) {
	defer channel.Close()
	for req := range requests {
		if req.Type != "subsystem" {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}

		var payload struct{ Name string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			req.Reply(false, nil)
			return
		}

		if !accept {
			// A real device that has not enabled the requested
			// subsystem commonly explains itself on standard error and
			// then fails the request; both halves are reproduced here,
			// stderr first, because that ordering is what makes the
			// explanation available when the failure is reported.
			channel.Stderr().Write([]byte("subsystem request failed on channel 0"))
			if req.WantReply {
				req.Reply(false, nil)
			}
			return
		}

		if req.WantReply {
			req.Reply(true, nil)
		}
		if handler != nil {
			handler(payload.Name, channel)
		}
		return
	}
}

// echoSubsystem copies whatever it is sent back to the caller, the
// smallest handler that proves bytes cross a real SSH channel in both
// directions.
func echoSubsystem(name string, channel ssh.Channel) {
	_, _ = io.Copy(channel, channel)
}

func TestSubsystem_RoundTripsBytesOverARealChannel(t *testing.T) {
	dial := newFakeSubsystemSSHServer(t, true, echoSubsystem)
	conn := newTestConn(t, dial)
	defer conn.Close()

	sub, err := conn.Subsystem(context.Background(), "netconf")
	if err != nil {
		t.Fatalf("Subsystem() error = %v, want nil", err)
	}
	defer sub.Close()

	const sent = "<hello xmlns=\"urn:ietf:params:xml:ns:netconf:base:1.0\"/>]]>]]>"
	if _, err := io.WriteString(sub, sent); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}

	got := make([]byte, len(sent))
	if _, err := io.ReadFull(sub, got); err != nil {
		t.Fatalf("ReadFull() error = %v, want nil", err)
	}
	if string(got) != sent {
		t.Errorf("read back %q, want %q", got, sent)
	}
}

// TestSubsystem_SatisfiesReadWriteCloser is a compile-time-shaped
// assertion of the property the protocol packages above this one
// actually depend on: being a plain io.ReadWriteCloser is what lets a
// NETCONF session be driven over an in-memory pipe in a unit test and
// over a real SSH channel in a release gate through one identical code
// path, instead of the protocol package growing a mock of the transport
// it is meant to be proving.
func TestSubsystem_SatisfiesReadWriteCloser(t *testing.T) {
	var _ io.ReadWriteCloser = (*Subsystem)(nil)
}

func TestSubsystem_RefusedRequestReportsTheServerExplanation(t *testing.T) {
	dial := newFakeSubsystemSSHServer(t, false, nil)
	conn := newTestConn(t, dial)
	defer conn.Close()

	sub, err := conn.Subsystem(context.Background(), "netconf")
	if err == nil {
		sub.Close()
		t.Fatal("Subsystem() error = nil, want a refusal")
	}

	// All three halves matter to an operator reading this: which
	// subsystem was refused, on which device, and what the server said
	// about it. The last is the whole reason this type drains standard
	// error at all.
	for _, want := range []string{"netconf", testTarget.Addr(), "subsystem request failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Subsystem() error = %q, want it to contain %q", err, want)
		}
	}
}

func TestSubsystem_StderrIsCompleteAfterClose(t *testing.T) {
	const diagnostic = "netconf-yang is not configured"

	dial := newFakeSubsystemSSHServer(t, true, func(name string, channel ssh.Channel) {
		channel.Stderr().Write([]byte(diagnostic))
		// Held open so the test controls when the session ends, which
		// is what makes this about Close's own wait rather than about
		// the server happening to finish first.
		time.Sleep(50 * time.Millisecond)
	})
	conn := newTestConn(t, dial)
	defer conn.Close()

	sub, err := conn.Subsystem(context.Background(), "netconf")
	if err != nil {
		t.Fatalf("Subsystem() error = %v, want nil", err)
	}

	if err := sub.Close(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Close() error = %v", err)
	}
	if got := sub.Stderr(); !strings.Contains(got, diagnostic) {
		t.Errorf("Stderr() after Close = %q, want it to contain %q", got, diagnostic)
	}
}

func TestSubsystem_StderrIsBoundedAndMarkedTruncated(t *testing.T) {
	flood := strings.Repeat("x", maxSubsystemStderrBytes*2)

	dial := newFakeSubsystemSSHServer(t, true, func(name string, channel ssh.Channel) {
		channel.Stderr().Write([]byte(flood))
		time.Sleep(50 * time.Millisecond)
	})
	conn := newTestConn(t, dial)
	defer conn.Close()

	sub, err := conn.Subsystem(context.Background(), "netconf")
	if err != nil {
		t.Fatalf("Subsystem() error = %v, want nil", err)
	}
	if err := sub.Close(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Close() error = %v", err)
	}

	got := sub.Stderr()
	if !strings.HasSuffix(got, "... (truncated)") {
		t.Errorf("Stderr() = %d bytes ending %q, want it marked truncated", len(got), got[max(0, len(got)-20):])
	}
	if body := strings.TrimSuffix(got, "... (truncated)"); len(body) > maxSubsystemStderrBytes {
		t.Errorf("Stderr() retained %d bytes, want at most %d", len(body), maxSubsystemStderrBytes)
	}
}

// TestSubsystem_ContextGovernsTheWholeSession is the behavioral
// difference from Shell worth pinning: io.Reader takes no context, so
// the context handed to Subsystem has to govern the session's whole
// lifetime, and a caller whose deadline passes mid-read must see its
// own cancellation rather than the I/O failure that closing the session
// out from under it produces.
func TestSubsystem_ContextGovernsTheWholeSession(t *testing.T) {
	dial := newFakeSubsystemSSHServer(t, true, func(name string, channel ssh.Channel) {
		// Never writes, so the client's Read below can only end by way
		// of the context.
		time.Sleep(2 * time.Second)
	})
	conn := newTestConn(t, dial)
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		cancel()
		t.Fatalf("Subsystem() error = %v, want nil", err)
	}
	defer sub.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err = io.ReadFull(sub, make([]byte, 1))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() after cancel returned %v, want it to wrap context.Canceled", err)
	}
}

func TestSubsystem_CloseIsIdempotent(t *testing.T) {
	dial := newFakeSubsystemSSHServer(t, true, echoSubsystem)
	conn := newTestConn(t, dial)
	defer conn.Close()

	sub, err := conn.Subsystem(context.Background(), "netconf")
	if err != nil {
		t.Fatalf("Subsystem() error = %v, want nil", err)
	}

	first := sub.Close()
	if second := sub.Close(); second != first {
		t.Errorf("second Close() = %v, want the same value as the first (%v): Close is guarded by a sync.Once", second, first)
	}
}

// TestSubsystem_LeavesNoGoroutinesBehind covers the two goroutines this
// type starts per session (the context watchdog and the standard error
// drain), following tunnel_test.go's own local-snapshot goleak
// convention. A leak here would be per-session rather than per-process,
// so a Controller opening one NETCONF session per task would accumulate
// them for as long as it ran.
func TestSubsystem_LeavesNoGoroutinesBehind(t *testing.T) {
	dial := newFakeSubsystemSSHServer(t, true, echoSubsystem)
	conn := newTestConn(t, dial)

	leakOpts := goleak.IgnoreCurrent()

	sub, err := conn.Subsystem(context.Background(), "netconf")
	if err != nil {
		conn.Close()
		t.Fatalf("Subsystem() error = %v, want nil", err)
	}
	if _, err := io.WriteString(sub, "ping"); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}
	if _, err := io.ReadFull(sub, make([]byte, 4)); err != nil {
		t.Fatalf("ReadFull() error = %v, want nil", err)
	}
	sub.Close()
	conn.Close()

	goleak.VerifyNone(t, leakOpts)
}
