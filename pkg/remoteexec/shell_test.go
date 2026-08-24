package remoteexec

import (
	"bufio"
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// newFakePTYSSHServer starts an in-process, loopback-TCP-backed SSH
// server whose session channel answers "pty-req" and "shell" instead of
// "exec", mirroring newFakeSSHServer's own real-handshake,
// real-loopback-socket construction in runner_test.go (see that
// function's own comment for why net.Pipe is unusable here: the
// handshake writes from both sides without waiting for a matching read
// first). sessionFunc receives the accepted channel, itself an
// io.ReadWriter, once the client's "shell" request has been accepted,
// and runs in its own goroutine per connection: a stateful closure over
// mutable state is how a test proves behavior (paging disable,
// configuration-mode tracking) spanning more than one exchange, the
// same reason netcli_ssh_test.go builds a scripted, stateful fake IOS
// responder on top of this same helper.
func newFakePTYSSHServer(t *testing.T, sessionFunc func(ssh.Channel)) dialFunc {
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
		go serveOnePTYConnection(serverConn, config, sessionFunc)
		sshConn, chans, reqs, err := ssh.NewClientConn(clientConn, addr, clientConfig)
		if err != nil {
			return nil, err
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}
	return dial
}

// serveOnePTYConnection completes the server side of one SSH handshake
// over conn and dispatches every resulting "session" channel to
// handleFakePTYSession; any other channel type is rejected outright,
// since nothing under test here needs a direct-tcpip forward the way
// runner_test.go's own bastion tests do.
func serveOnePTYConnection(conn net.Conn, config *ssh.ServerConfig, sessionFunc func(ssh.Channel)) {
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
		go handleFakePTYSession(channel, requests, sessionFunc)
	}
}

// handleFakePTYSession accepts exactly one "pty-req" and one "shell"
// request, then hands the channel to sessionFunc and returns once it
// does. Any other request type is declined, matching
// handleFakeSession's own "decline anything unexpected" convention.
func handleFakePTYSession(channel ssh.Channel, requests <-chan *ssh.Request, sessionFunc func(ssh.Channel)) {
	defer channel.Close()
	for req := range requests {
		switch req.Type {
		case "pty-req":
			if req.WantReply {
				req.Reply(true, nil)
			}
		case "shell":
			if req.WantReply {
				req.Reply(true, nil)
			}
			sessionFunc(channel)
			return
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// readCRLine reads one line terminated by a single "\r" from r, with the
// terminator stripped, mirroring WriteLine's own line-terminator
// convention (see WriteLine's doc comment for why a bare "\r", not
// "\r\n", is what a real interactive session actually expects). It is
// this file's own test-fixture counterpart, not production code.
func readCRLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\r')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r"), nil
}

// newTestConn dials dial directly into a bare Conn, bypassing Runner
// entirely: Shell's own tests are about the session primitive, not
// about dialing, retry or the circuit breaker, which runner_test.go and
// dial_test.go already cover.
func newTestConn(t *testing.T, dial dialFunc) *Conn {
	t.Helper()
	client, err := dial(context.Background(), testTarget.Addr(), &ssh.ClientConfig{
		User:            "u",
		Auth:            []ssh.AuthMethod{ssh.Password("p")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106 -- test fixture, mirrors runner_test.go's own newTestRunner
	})
	if err != nil {
		t.Fatalf("dialing the fake server: %v", err)
	}
	return &Conn{client: client, chain: []*ssh.Client{client}, addr: testTarget.Addr()}
}

// TestShell_PTYRoundTrip proves a real PTY round trip end to end: a
// genuine pty-req and shell request, a first-prompt read, one command
// sent and its reply read back to the next prompt. This is the
// mechanism pkg/netcli's Session is built on, proven here with no
// vendor Dialect involved at all.
func TestShell_PTYRoundTrip(t *testing.T) {
	dial := newFakePTYSSHServer(t, func(channel ssh.Channel) {
		reader := bufio.NewReader(channel)
		channel.Write([]byte("device> "))
		line, err := readCRLine(reader)
		if err != nil {
			return
		}
		if line == "show version" {
			channel.Write([]byte("Cisco IOS Software, C8000V\r\ndevice> "))
		}
	})

	conn := newTestConn(t, dial)
	defer conn.Close()

	ctx := context.Background()
	shell, err := conn.Shell(ctx, ShellOptions{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer shell.Close()

	promptPattern := regexp.MustCompile(`device> $`)

	first, err := shell.ReadUntil(ctx, promptPattern, 4096)
	if err != nil {
		t.Fatalf("ReadUntil (first prompt): %v", err)
	}
	if first != "device> " {
		t.Errorf("first prompt = %q, want %q", first, "device> ")
	}

	if err := shell.WriteLine(ctx, "show version"); err != nil {
		t.Fatalf("WriteLine: %v", err)
	}

	reply, err := shell.ReadUntil(ctx, promptPattern, 4096)
	if err != nil {
		t.Fatalf("ReadUntil (reply): %v", err)
	}
	if !strings.Contains(reply, "Cisco IOS Software, C8000V") {
		t.Errorf("reply = %q, want it to contain the show version output", reply)
	}
	if !strings.HasSuffix(reply, "device> ") {
		t.Errorf("reply = %q, want it to end at the next prompt", reply)
	}
}

// TestShell_WriteLineRefusesEmbeddedLineTerminator proves the
// injection-defense refusal fires before any I/O: it is checked against
// a Shell with a nil session and nil stdin, so a WriteLine call that
// reached the I/O layer anyway would panic instead of returning
// cleanly, which is what makes this test a real proof of ordering, not
// just of the error message.
func TestShell_WriteLineRefusesEmbeddedLineTerminator(t *testing.T) {
	shell := &Shell{addr: "device:22"}

	for _, line := range []string{"show version\r\n; reload", "show version\ndisable", "show version\rreload"} {
		if err := shell.WriteLine(context.Background(), line); err == nil {
			t.Errorf("WriteLine(%q) = nil error, want a refusal", line)
		}
	}

	// A line with no embedded terminator is not refused by this check;
	// it would proceed to the nil stdin and panic, which is exactly the
	// proof that the check above runs first and unconditionally.
}

// TestShell_ReadUntilRespectsMaxBytes proves the bound is enforced
// rather than advisory: a server that writes well past maxBytes without
// ever producing a match must not be allowed to buffer forever, and the
// session it closes must stay closed.
func TestShell_ReadUntilRespectsMaxBytes(t *testing.T) {
	dial := newFakePTYSSHServer(t, func(channel ssh.Channel) {
		channel.Write([]byte(strings.Repeat("x", 4096)))
		// Block rather than close, so the client's read genuinely
		// exceeds the bound rather than racing a server-side close.
		<-make(chan struct{})
	})

	conn := newTestConn(t, dial)
	defer conn.Close()

	ctx := context.Background()
	shell, err := conn.Shell(ctx, ShellOptions{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer shell.Close()

	_, err = shell.ReadUntil(ctx, regexp.MustCompile(`never matches`), 100)
	if err == nil {
		t.Fatal("ReadUntil exceeding maxBytes returned no error")
	}
	if !strings.Contains(err.Error(), "100-byte bound") {
		t.Errorf("error = %v, want it to name the exceeded bound", err)
	}

	// The session is left unusable: a second call must also fail.
	if _, err := shell.ReadUntil(ctx, regexp.MustCompile(`.`), 10); err == nil {
		t.Error("ReadUntil on an already-bound-exceeded Shell returned no error")
	}
}

// TestShell_ReadUntilHonorsContextCancellation proves a caller's ctx
// timeout reaches a session stuck waiting on a remote that never
// answers, the same guarantee RunWithStdin already gives a one-shot
// exec command.
func TestShell_ReadUntilHonorsContextCancellation(t *testing.T) {
	dial := newFakePTYSSHServer(t, func(channel ssh.Channel) {
		<-make(chan struct{}) // never write anything
	})

	conn := newTestConn(t, dial)
	defer conn.Close()

	shell, err := conn.Shell(context.Background(), ShellOptions{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer shell.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = shell.ReadUntil(ctx, regexp.MustCompile(`never matches`), 1<<20)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("ReadUntil past its ctx deadline returned no error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("ReadUntil took %v to honor a 200ms deadline", elapsed)
	}
}

// TestShell_ReadUntilCarriesBytesPastMatch proves bytes read past a
// match are kept for the next call rather than discarded. The server
// writes both prompts in a SINGLE Write, and only that one: if the
// carry-over logic were missing, the second ReadUntil call would have
// nothing left to read and would hang until its own bound or ctx fired,
// not return the second prompt.
func TestShell_ReadUntilCarriesBytesPastMatch(t *testing.T) {
	var writes int32
	dial := newFakePTYSSHServer(t, func(channel ssh.Channel) {
		atomic.AddInt32(&writes, 1)
		channel.Write([]byte("first#second#"))
		<-make(chan struct{}) // prove the second read needs no further data
	})

	conn := newTestConn(t, dial)
	defer conn.Close()

	ctx := context.Background()
	shell, err := conn.Shell(ctx, ShellOptions{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer shell.Close()

	pattern := regexp.MustCompile(`#`)

	first, err := shell.ReadUntil(ctx, pattern, 4096)
	if err != nil {
		t.Fatalf("ReadUntil (first): %v", err)
	}
	if first != "first#" {
		t.Errorf("first = %q, want %q", first, "first#")
	}

	second, err := shell.ReadUntil(ctx, pattern, 4096)
	if err != nil {
		t.Fatalf("ReadUntil (second, from carry): %v", err)
	}
	if second != "second#" {
		t.Errorf("second = %q, want %q", second, "second#")
	}

	if got := atomic.LoadInt32(&writes); got != 1 {
		t.Errorf("server issued %d session(s), want exactly 1", got)
	}
}
