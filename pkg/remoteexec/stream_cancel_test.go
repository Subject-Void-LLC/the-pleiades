//go:build !windows

// Tests for a started command's stream when its context ends or its exec is
// refused: the error is the context's or ErrCommandRefused, never a status
// the ending produced, and never one carrying the command.
package remoteexec

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// refusingDial is a dialFunc to a server that accepts sessions and refuses
// every exec request, as OpenSSH does when it cannot fork for the account.
// confirmDelay holds each session's channel confirmation that long. execs
// counts the exec requests the server received.
func refusingDial(t *testing.T, confirmDelay time.Duration, execs *atomic.Int64) dialFunc {
	t.Helper()
	config := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(generateTestHostKey(t))
	serve := func(conn net.Conn) {
		sConn, chans, reqs, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer func() { _ = sConn.Close() }()
		go ssh.DiscardRequests(reqs)
		for newChannel := range chans {
			time.Sleep(confirmDelay)
			channel, requests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go func() {
				defer func() { _ = channel.Close() }()
				for req := range requests {
					if req.Type == "exec" {
						execs.Add(1)
					}
					if req.WantReply {
						_ = req.Reply(false, nil)
					}
				}
			}()
		}
	}
	return func(_ context.Context, addr string, clientConfig *ssh.ClientConfig) (*ssh.Client, error) {
		clientConn, serverConn := localPipe(t)
		go serve(serverConn)
		sshConn, chans, reqs, err := ssh.NewClientConn(clientConn, addr, clientConfig)
		if err != nil {
			return nil, err
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}
}

// TestRefusedExec_NamesNoCommand: a refused exec, from Run and from Start, is
// ErrCommandRefused, and the error holds neither the command nor its
// shell-encoded form.
func TestRefusedExec_NamesNoCommand(t *testing.T) {
	var execs atomic.Int64
	conn, err := newTestRunner(refusingDial(t, 0, &execs), Options{}).Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	command := "echo " + QuoteArg("s3cr'et-planted")
	_, runErr := conn.Run(context.Background(), command)
	_, startErr := conn.Start(context.Background(), command)
	for what, err := range map[string]error{"Run": runErr, "Start": startErr} {
		if !errors.Is(err, ErrCommandRefused) {
			t.Fatalf("%s = %v; want ErrCommandRefused", what, err)
		}
		if strings.Contains(err.Error(), "planted") || strings.Contains(err.Error(), "echo") {
			t.Fatalf("%s's error holds the command: %v", what, err)
		}
	}
}

// TestWithoutCommand_KeepsOtherErrors: a start error that does not carry the
// line is returned as it is, and nil stays nil.
func TestWithoutCommand_KeepsOtherErrors(t *testing.T) {
	if withoutCommand(nil, "x") != nil {
		t.Fatal("withoutCommand(nil) is not nil")
	}
	dropped := io.ErrUnexpectedEOF
	if got := withoutCommand(dropped, "echo hi"); got != dropped {
		t.Fatalf("withoutCommand(%v) = %v; want it unchanged", dropped, got)
	}
	// A short line (a Raw view's command) inside some other start error is
	// not a refusal: only x/crypto/ssh's whole "ssh: command <line> failed"
	// is. Matched by substring, this channel failure read as one.
	other := errors.New("ssh: rejected: administratively prohibited (open failed)")
	if got := withoutCommand(other, "open"); got != other {
		t.Fatalf("withoutCommand(%v, open) = %v; want it unchanged", other, got)
	}
	if got := withoutCommand(errors.New("ssh: command open failed"), "open"); !errors.Is(got, ErrCommandRefused) {
		t.Fatalf("the refusal itself = %v; want ErrCommandRefused", got)
	}
}

// TestStart_AContextAlreadyDoneStartsNothing: Start on a done context opens no
// session, so nothing reaches the server.
func TestStart_AContextAlreadyDoneStartsNothing(t *testing.T) {
	srv := startPoolServer(t)
	conn, err := New(Options{InsecureSkipHostKeyVerify: true}).Connect(context.Background(), nil, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := conn.Start(ctx, "true"); !errors.Is(err, context.Canceled) {
		if p != nil {
			_ = p.Close()
		}
		t.Fatalf("Start = %v; want context.Canceled", err)
	}
	if got := srv.Commands(); len(got) != 0 {
		t.Fatalf("the server received %q; want nothing", got)
	}
}

// TestStart_AContextEndingWhileTheSessionOpensIsACancellation: a server slow
// to confirm the session answers after the context ended; the stream is not
// started and the error is the context's.
func TestStart_AContextEndingWhileTheSessionOpensIsACancellation(t *testing.T) {
	var execs atomic.Int64
	conn, err := newTestRunner(refusingDial(t, 500*time.Millisecond, &execs), Options{}).Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if p, err := conn.Start(ctx, "true"); !errors.Is(err, context.DeadlineExceeded) {
		if p != nil {
			_ = p.Close()
		}
		t.Fatalf("Start = %v; want context.DeadlineExceeded", err)
	}
	// Long enough for an exec sent after the late confirmation to arrive.
	time.Sleep(300 * time.Millisecond)
	if n := execs.Load(); n != 0 {
		t.Fatalf("the server received %d exec requests after the context ended; want none", n)
	}
}

// TestProcess_ACancelledCommandOnASilentConnectionIsBounded: a started command
// whose server stops answering, then whose context ends, returns the
// context's error from Wait within the abandon grace: the connection is
// closed under the wait.
func TestProcess_ACancelledCommandOnASilentConnectionIsBounded(t *testing.T) {
	conn, freeze := connectThroughProxy(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := conn.Start(ctx, "echo started; sleep 3")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	buf := make([]byte, 8)
	if _, err := io.ReadFull(p, buf); err != nil {
		t.Fatalf("reading the command's first output: %v", err)
	}
	freeze()
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.Wait()
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Wait = %v; want context.Canceled", err)
		}
	case <-time.After(abandonGrace + 15*time.Second):
		t.Fatal("Wait never returned on a silent connection")
	}
}
