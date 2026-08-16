package exec_test

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
	"testing"

	cryptossh "golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// This file is the harness the tests in this package run against: a real
// SSH server, in this process, that hands every command it receives to a
// real POSIX shell on this machine.
//
// Neither half of that is a mock. The SSH side is
// golang.org/x/crypto/ssh doing a genuine key exchange, a genuine
// authentication round and a genuine session channel, and the shell side
// is /bin/sh, so "test -e", "cd", an exit status and a piped stdin all
// behave exactly as they do on a device. That matters under RULE 0: the
// subject of these tests is what exec.command builds and how it reads
// the answer, and a server that returned canned bytes would only prove
// the canned bytes were canned.
//
// What it is NOT is a remote machine. exec_release_gate_test.go covers
// that, against a real, independent sshd in a container reached over a
// real network hop, which is what actually flips this method's status.

// testSSHServer is a running in-process SSH server.
type testSSHServer struct {
	host     string
	port     int
	hostKey  cryptossh.PublicKey
	username string
	password string
}

// startShellSSHServer starts an SSH server that accepts one fixed
// username and password and runs every exec request through /bin/sh.
func startShellSSHServer(t *testing.T) testSSHServer {
	return startShellSSHServerWithSessionBudget(t, -1)
}

// startShellSSHServerWithSessionBudget is startShellSSHServer with a cap
// on how many session channels it will accept before rejecting every
// further one. A negative budget means no cap.
//
// It exists to reach the error branches that only a connection failing
// partway through a task can produce. exec.command opens up to three
// sessions (a creates check, a removes check, the command), and a budget
// of one is the only way to make the second of them fail while the first
// succeeds. Rejecting a channel is a real protocol-level refusal, not an
// injected Go error, so the branch under test sees the same shape a
// device dropping the connection would produce.
func startShellSSHServerWithSessionBudget(t *testing.T, budget int) testSSHServer {
	t.Helper()

	const (
		username = "testuser"
		password = "testpass"
	)

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a host key: %v", err)
	}
	signer, err := cryptossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("building a host key signer: %v", err)
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
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	// serving tracks the connection goroutines so cleanup can wait for
	// them, which keeps a still-running /bin/sh from outliving the test
	// that started it.
	var serving sync.WaitGroup
	t.Cleanup(serving.Wait)

	// remaining counts down the session budget across every connection
	// this server accepts.
	remaining := int64(budget)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			serving.Add(1)
			go func() {
				defer serving.Done()
				serveShellConn(conn, config, &remaining)
			}()
		}
	}()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T is not TCP", listener.Addr())
	}
	return testSSHServer{
		host:     "127.0.0.1",
		port:     tcpAddr.Port,
		hostKey:  signer.PublicKey(),
		username: username,
		password: password,
	}
}

// serveShellConn completes one handshake and serves its session
// channels. Errors are dropped rather than reported: the listener
// closing at cleanup is the ordinary way this ends, and a t.Error from a
// background goroutine after the test returns would panic.
func serveShellConn(conn net.Conn, config *cryptossh.ServerConfig, remaining *int64) {
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
			serveShellSession(channel, requests)
		}()
	}
}

// serveShellSession answers one exec request by running the command
// through /bin/sh, exactly as a real sshd hands it to the account's
// login shell.
func serveShellSession(channel cryptossh.Channel, requests <-chan *cryptossh.Request) {
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

		// The command comes from this package's own tests, and running it
		// through a real shell is the entire point of this harness: a
		// stand-in that pattern-matched on the command string would be
		// asserting against its own idea of a shell rather than a shell.
		cmd := exec.Command("/bin/sh", "-c", command) // #nosec G204 -- test harness; the command is this package's own test input, see this file's doc comment
		cmd.Stdin = channel
		cmd.Stdout = channel
		cmd.Stderr = channel.Stderr()

		exitStatus := runAndReportExit(cmd)
		_, _ = channel.SendRequest("exit-status", false,
			cryptossh.Marshal(struct{ Status uint32 }{exitStatus}))
		return
	}
}

// claimSession consumes one unit of a server's session budget,
// reporting whether there was one to consume. A negative budget is
// unlimited and is never decremented.
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
// would report: the process's own status, or 255 for a failure that
// never produced one.
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

// sshDevice is a target reachable over SSH: the shared
// pkg/inventory/inventorytest.Stub plus the two accessors
// capability.SSHTransportCapable requires, which that stub deliberately
// does not provide.
type sshDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *sshDevice) SSHHost() string { return d.host }
func (d *sshDevice) SSHPort() int    { return d.port }

// execDevice adds the capability.CommandExecCapable accessor, so it is
// the shape a real linux.Server presents.
type execDevice struct {
	sshDevice
	workingDir string
}

func (d *execDevice) WorkingDirectory() string { return d.workingDir }

// newStub builds the base inventory item both device shapes wrap.
func newStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "exec-1",
		StubName:  "exec-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NameCommandExec},
		StubState: inventory.StateActive,
	}
}

// newDevice builds the target a test runs against: SSH-reachable, and
// declaring workingDir through CommandExecCapable.
func newDevice(server testSSHServer, workingDir string) *execDevice {
	return &execDevice{
		sshDevice:  sshDevice{Stub: newStub(), host: server.host, port: server.port},
		workingDir: workingDir,
	}
}

// newSSHOnlyDevice builds a target that is reachable over SSH and
// implements no CommandExecCapable accessor at all.
//
// That is the Runner's own device adapter exactly: it carries a declared
// capability list without the accessors behind it, so a method that
// type-asserted CommandExecCapable rather than reading it optionally
// would refuse a dispatch the Controller correctly admitted.
func newSSHOnlyDevice(server testSSHServer) *sshDevice {
	return &sshDevice{Stub: newStub(), host: server.host, port: server.port}
}

// newUnreachableDevice builds a target this method cannot reach at all:
// the bare stub, which implements InventoryItem and nothing else.
func newUnreachableDevice() inventory.InventoryItem {
	return newStub()
}

// stubContext is a minimal sdk.RunbookContext carrying a fixed secret
// set, standing in for the real one the composition root builds from the
// credential store (Walk tier) or the dispatch payload (Crawl tier).
type stubContext struct {
	secrets map[string]string
	stats   map[string]any

	// statErr, when set, makes SetStat fail, so a test can drive the
	// branch where the command succeeded and recording its answer did
	// not.
	statErr error
}

func newStubContext(server testSSHServer) *stubContext {
	return &stubContext{
		secrets: map[string]string{"username": server.username, "password": server.password},
		stats:   map[string]any{},
	}
}

func (c *stubContext) InjectSecrets() map[string]string { return c.secrets }

func (c *stubContext) SetStat(key string, value any) error {
	if c.statErr != nil {
		return c.statErr
	}
	if c.stats == nil {
		c.stats = map[string]any{}
	}
	c.stats[key] = value
	return nil
}

func (c *stubContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }
