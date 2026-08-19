// Package remoteexectest provides a real SSH server, in the calling
// process, that hands every command it receives to a real POSIX shell on
// the local machine.
//
// Neither half of that is a mock. The SSH side is
// golang.org/x/crypto/ssh doing a genuine key exchange, a genuine
// authentication round and a genuine session channel, and the shell side
// is /bin/sh, so "test -e", "cd", an exit status and a piped stdin all
// behave exactly as they do on a device. That is what makes it usable
// under RULE 0 for testing what a Collection method BUILDS and how it
// reads the answer: a stand-in that returned canned bytes would only
// prove the canned bytes were canned.
//
// What it is NOT is a remote machine. A method's Release Gate covers
// that, against a real independent sshd in a container reached over a
// real network hop, and that is what flips a method's status.
//
// # Why it lives in pkg/
//
// It started as one Collection package's own test file. It moved here
// the moment a second package needed it, and it moved to pkg/ rather
// than internal/testsupport for the same reason pkg/remoteexec itself
// exists: a Collection may import only pkg/, and that applies to a
// third-party Collection's tests too. A harness under internal/ would be
// one more thing a built-in can use and nobody else can, which is the
// pattern this layout exists to avoid. pkg/inventory/inventorytest is
// the established precedent for a fixture package sitting beside the
// thing it helps test.
//
// It deliberately does NOT import "testing", following that same
// precedent. Importing testing from a non-test package registers test
// flags into any binary that links it, so Start returns an error and a
// Server the caller Closes, and a test wraps that in t.Cleanup itself.
package remoteexectest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"sync/atomic"

	cryptossh "golang.org/x/crypto/ssh"
)

// Options configures a Server.
type Options struct {
	// SessionLimit caps how many session channels the server accepts
	// before rejecting every further one. Nil means unlimited, so the
	// zero Options is a normal server.
	//
	// A POINTER, deliberately, and the first version of this got it
	// wrong. Zero is a meaningful budget here: it means "reject the very
	// first session", which is how a test reaches the branch where
	// authentication succeeds and the session does not. So zero cannot
	// also be the unset sentinel, and an int field would have silently
	// turned every "reject everything" test into an unlimited server that
	// passed by doing the opposite of what it asked for. Use Limit.
	//
	// It exists to reach the error branches only a connection failing
	// partway through a task can produce. A converging method opens
	// several sessions (a probe, then the change), and a budget of one is
	// the only way to make the second fail while the first succeeds.
	// Rejecting a channel is a real protocol-level refusal rather than an
	// injected Go error, so the branch under test sees the same shape a
	// device dropping the connection would produce.
	SessionLimit *int

	// Username and Password are the one credential the server accepts.
	// Both default when empty.
	Username string
	Password string
}

// Limit returns a SessionLimit for n, so a caller can write
// Options{SessionLimit: remoteexectest.Limit(0)} without a temporary.
func Limit(n int) *int { return &n }

// Default credentials, used when Options leaves them empty.
const (
	DefaultUsername = "testuser"
	DefaultPassword = "testpass"
)

// Server is a running in-process SSH server. Close it when done.
type Server struct {
	// Host and Port are where it is listening, always on loopback.
	Host string
	Port int

	// HostKey is the key it presents, so a caller can write a real
	// known_hosts entry and exercise host key verification rather than
	// skipping it.
	HostKey cryptossh.PublicKey

	// Username and Password are the credential it accepts.
	Username string
	Password string

	listener net.Listener
	serving  *sync.WaitGroup
	closed   sync.Once
}

// Addr returns the "host:port" a known_hosts entry and a dialer both
// need.
func (s *Server) Addr() string {
	return net.JoinHostPort(s.Host, fmt.Sprintf("%d", s.Port))
}

// Close stops the listener and waits for every connection goroutine to
// finish, which is what keeps a still-running /bin/sh from outliving the
// test that started it. It is safe to call more than once.
func (s *Server) Close() {
	s.closed.Do(func() {
		_ = s.listener.Close()
		s.serving.Wait()
	})
}

// Start brings up a server on a loopback port chosen by the kernel.
func Start(opts Options) (*Server, error) {
	username := opts.Username
	if username == "" {
		username = DefaultUsername
	}
	password := opts.Password
	if password == "" {
		password = DefaultPassword
	}
	budget := -1 // unlimited unless the caller set a limit
	if opts.SessionLimit != nil {
		budget = *opts.SessionLimit
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("remoteexectest: generating a host key: %w", err)
	}
	signer, err := cryptossh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("remoteexectest: building a host key signer: %w", err)
	}

	config := &cryptossh.ServerConfig{
		PasswordCallback: func(c cryptossh.ConnMetadata, pass []byte) (*cryptossh.Permissions, error) {
			if c.User() != username || string(pass) != password {
				return nil, fmt.Errorf("denied")
			}
			return &cryptossh.Permissions{}, nil
		},
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("remoteexectest: listening: %w", err)
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, fmt.Errorf("remoteexectest: listener address %T is not TCP", listener.Addr())
	}

	remaining := int64(budget)
	serving := &sync.WaitGroup{}

	srv := &Server{
		Host:     "127.0.0.1",
		Port:     tcpAddr.Port,
		HostKey:  signer.PublicKey(),
		Username: username,
		Password: password,
		listener: listener,
		serving:  serving,
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			serving.Add(1)
			go func() {
				defer serving.Done()
				serveConn(conn, config, &remaining)
			}()
		}
	}()

	return srv, nil
}

// Secrets returns the credential map a Collection method reads through
// sdk.RunbookContext.InjectSecrets, keyed the way the wire protocol keys
// it, so a test does not have to remember the spelling.
func (s *Server) Secrets() map[string]string {
	return map[string]string{"username": s.Username, "password": s.Password}
}

// serveConn completes one handshake and serves its session channels.
//
// Errors are dropped rather than reported. The listener closing at
// cleanup is the ordinary way this ends, and this runs on a background
// goroutine that may outlive the test's own failure reporting.
func serveConn(conn net.Conn, config *cryptossh.ServerConfig, remaining *int64) {
	defer func() { _ = conn.Close() }()

	serverConn, chans, reqs, err := cryptossh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer func() { _ = serverConn.Close() }()
	go cryptossh.DiscardRequests(reqs)

	var sessions sync.WaitGroup
	defer sessions.Wait()

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(cryptossh.UnknownChannelType, "only sessions")
			continue
		}
		if !claimSession(remaining) {
			_ = newChannel.Reject(cryptossh.Prohibited, "session budget exhausted")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			return
		}
		sessions.Add(1)
		go func() {
			defer sessions.Done()
			serveSession(channel, requests)
		}()
	}
}

// serveSession answers one exec request by running the command through
// /bin/sh, exactly as a real sshd hands it to the account's login shell.
func serveSession(channel cryptossh.Channel, requests <-chan *cryptossh.Request) {
	defer func() { _ = channel.Close() }()

	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		if req.WantReply {
			_ = req.Reply(true, nil)
		}

		command := decodeExecPayload(req.Payload)

		// Running the command through a real shell is the entire point of
		// this harness: a stand-in that pattern-matched on the command
		// string would be asserting against its own idea of a shell rather
		// than a shell.
		cmd := exec.Command("/bin/sh", "-c", command) // #nosec G204 -- test harness; the command is the calling test's own input, see this package's doc comment
		cmd.Stdin = channel
		cmd.Stdout = channel
		cmd.Stderr = channel.Stderr()

		exitStatus := runAndReportExit(cmd)
		_, _ = channel.SendRequest("exit-status", false,
			cryptossh.Marshal(struct{ Status uint32 }{exitStatus}))
		return
	}
}

// claimSession consumes one unit of a server's session budget, reporting
// whether there was one to consume. A negative budget is unlimited and is
// never decremented.
func claimSession(remaining *int64) bool {
	for {
		left := atomic.LoadInt64(remaining)
		if left < 0 {
			return true
		}
		if left == 0 {
			return false
		}
		if atomic.CompareAndSwapInt64(remaining, left, left-1) {
			return true
		}
	}
}

// runAndReportExit runs cmd and returns the exit status a remote sshd
// would report: the process's own status, or 255 for a failure that never
// produced one.
func runAndReportExit(cmd *exec.Cmd) uint32 {
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code < 0 {
			return 255
		}
		return uint32(code) // #nosec G115 -- guarded non-negative immediately above
	}
	return 255
}

// decodeExecPayload reads RFC 4254's exec request payload: a 32-bit
// big-endian length followed by the command string.
func decodeExecPayload(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := binary.BigEndian.Uint32(payload[:4])
	if int(n) > len(payload)-4 {
		return ""
	}
	return string(payload[4 : 4+n])
}
