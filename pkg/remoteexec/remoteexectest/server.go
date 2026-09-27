// Package remoteexectest provides a real SSH server, in the calling
// process, that hands every command it receives to a real POSIX shell on
// the local machine, and also genuinely forwards a direct-tcpip channel
// to whatever real destination the client asks for, so this same Server
// doubles as a bastion.
//
// Neither half of that is a mock. The SSH side is
// golang.org/x/crypto/ssh doing a genuine key exchange, a genuine
// authentication round and a genuine session channel, and the shell side
// is /bin/sh, so "test -e", "cd", an exit status and a piped stdin all
// behave exactly as they do on a device. That is what makes it usable
// under RULE 0 for testing what a Collection method BUILDS and how it
// reads the answer: a stand-in that returned canned bytes would only
// prove the canned bytes were canned. The forwarding half is the
// identical dial-first-accept-second-then-genuinely-pump-bytes shape
// pkg/remoteexec's own internal test fixture (runner_test.go's
// handleFakeDirectTCPIP/forwardDirectTCPIP) already established for that
// package's own hop-chain tests; it moved here, unmodified in substance,
// the moment a second and third package (internal/transport/serialtcp,
// internal/transport/telnet, Phase 73's own Route wiring) needed a real
// bastion in their own tests and could not reach that package's
// unexported fixture.
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
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
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

	// AuthorizedKey, when set, is a public key the server also accepts
	// for Username, so a test can log in by key.
	AuthorizedKey cryptossh.PublicKey

	// RefusePasswords makes the server refuse every password, as sshd
	// does for root with PermitRootLogin prohibit-password, Ubuntu's
	// default, so only a key logs in.
	RefusePasswords bool

	// Netconf, when set, makes this server answer a "netconf" subsystem
	// request by speaking RFC 6241, for the methods that configure a
	// device that way rather than over a terminal. See netconf_device.go.
	Netconf *NetconfDevice

	// Device, when set, makes this server answer a shell request with a
	// scripted CLI instead of declining it, for the methods that open an
	// interactive session rather than running a command. See device.go
	// for what such a script is and is not evidence of. Exec sessions are
	// unaffected: a server with a Device still runs commands through a
	// real shell.
	Device *Device
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
	log      *commandLog
	device   *Device
	netconf  *NetconfDevice

	// keyLogins counts the successful authentications made by key.
	keyLogins atomic.Int64

	// logins counts successful authentications, so a test can prove how
	// many real logins a run made rather than trusting the client's own
	// account of whether it reused a connection.
	logins atomic.Int64

	// live is every connection currently being served, so DropConnections
	// and Close can end them from the server's side.
	liveMu sync.Mutex
	live   map[*cryptossh.ServerConn]struct{}
}

// Logins returns how many times a client has authenticated successfully.
func (s *Server) Logins() int64 { return s.logins.Load() }

// KeyLogins returns how many of Logins were made by key.
func (s *Server) KeyLogins() int64 { return s.keyLogins.Load() }

// DropConnections closes every connection the server is serving, as a
// device that reboots or a firewall that forgets idle flows would. A
// client learns of it only when it next uses the connection.
func (s *Server) DropConnections() {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	for c := range s.live {
		_ = c.Close()
	}
}

// Live returns how many connections the server is serving now, so a test
// can prove a client really closed one rather than merely stopped using
// it.
func (s *Server) Live() int {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	return len(s.live)
}

// track registers c as live and returns the function that forgets it.
func (s *Server) track(c *cryptossh.ServerConn) func() {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	s.live[c] = struct{}{}
	return func() {
		s.liveMu.Lock()
		defer s.liveMu.Unlock()
		delete(s.live, c)
	}
}

// commandLog is every command the server was asked to run, in order.
type commandLog struct {
	mu       sync.Mutex
	commands []string
}

// add records one command.
func (l *commandLog) add(command string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.commands = append(l.commands, command)
}

// Commands returns every command the server has been asked to run, in
// the order they arrived. A check's test reads it to prove what the check
// sent the device, rather than trusting the method's own account.
func (s *Server) Commands() []string {
	s.log.mu.Lock()
	defer s.log.mu.Unlock()
	return append([]string(nil), s.log.commands...)
}

// Addr returns the "host:port" a known_hosts entry and a dialer both
// need.
func (s *Server) Addr() string {
	return net.JoinHostPort(s.Host, fmt.Sprintf("%d", s.Port))
}

// Close stops the listener, ends every live connection, and waits for
// every connection goroutine to finish, which is what keeps a
// still-running /bin/sh from outliving the test that started it. Ending
// live connections is what lets a test close the server while a client
// still holds one open, as a connection pool does. It is safe to call
// more than once.
func (s *Server) Close() {
	s.closed.Do(func() {
		_ = s.listener.Close()
		s.DropConnections()
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

	var srv *Server
	config := &cryptossh.ServerConfig{
		PasswordCallback: func(c cryptossh.ConnMetadata, pass []byte) (*cryptossh.Permissions, error) {
			if opts.RefusePasswords || c.User() != username || string(pass) != password {
				return nil, fmt.Errorf("denied")
			}
			srv.logins.Add(1)
			return &cryptossh.Permissions{}, nil
		},
	}
	if opts.AuthorizedKey != nil {
		authorized := opts.AuthorizedKey.Marshal()
		config.PublicKeyCallback = func(c cryptossh.ConnMetadata, key cryptossh.PublicKey) (*cryptossh.Permissions, error) {
			if c.User() != username || !bytes.Equal(key.Marshal(), authorized) {
				return nil, fmt.Errorf("denied")
			}
			srv.logins.Add(1)
			srv.keyLogins.Add(1)
			return &cryptossh.Permissions{}, nil
		}
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

	srv = &Server{
		Host:     "127.0.0.1",
		Port:     tcpAddr.Port,
		HostKey:  signer.PublicKey(),
		Username: username,
		Password: password,
		listener: listener,
		serving:  serving,
		log:      &commandLog{},
		device:   opts.Device,
		netconf:  opts.Netconf,
		live:     map[*cryptossh.ServerConn]struct{}{},
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
				serveConn(conn, config, &remaining, srv.log, srv.device, srv.netconf, srv.track)
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
func serveConn(conn net.Conn, config *cryptossh.ServerConfig, remaining *int64, log *commandLog, device *Device, netconf *NetconfDevice, track func(*cryptossh.ServerConn) func()) {
	defer func() { _ = conn.Close() }()

	serverConn, chans, reqs, err := cryptossh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer track(serverConn)()
	defer func() { _ = serverConn.Close() }()
	go cryptossh.DiscardRequests(reqs)

	var sessions sync.WaitGroup
	defer sessions.Wait()

	for newChannel := range chans {
		switch newChannel.ChannelType() {
		case "session":
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
				if netconf != nil {
					serveNetconfSubsystem(channel, requests, log, netconf)
					return
				}
				if device != nil {
					serveDeviceSession(channel, requests, log, device)
					return
				}
				serveSession(channel, requests, log)
			}()

		case "direct-tcpip":
			sessions.Add(1)
			go func() {
				defer sessions.Done()
				serveDirectTCPIP(newChannel)
			}()

		default:
			_ = newChannel.Reject(cryptossh.UnknownChannelType, "only session and direct-tcpip channels supported")
		}
	}
}

// directTCPIPPayload is RFC 4254 §7.2's direct-tcpip channel open
// payload: the address and port the client asked to reach, followed by
// the client's own originating address and port (both read, per the
// RFC's wire format, but only the destination is needed to forward).
type directTCPIPPayload struct {
	DestAddr string
	DestPort uint32
	OrigAddr string
	OrigPort uint32
}

// serveDirectTCPIP mirrors a real sshd's own forwarding behavior: it
// dials the requested destination FIRST, and only Accepts the channel
// once that dial has actually succeeded, Rejecting it otherwise.
// Accepting unconditionally and only discovering the destination is
// unreachable afterward would be a materially different, less realistic
// behavior than a real bastion's, and would make an unreachable
// destination surface to the client as a channel that opens and then
// silently closes rather than as ssh.NewChannel.Reject's own, real
// "could not connect" outcome, which is what
// (*ssh.Client).DialContext itself turns into a returned error - the
// exact distinction internal/transport/ssh's own hop-chain tests
// (TestConnect_HopChain_UnreachableTargetThroughBastionFailsWithChannelError)
// depend on.
func serveDirectTCPIP(newChannel cryptossh.NewChannel) {
	var payload directTCPIPPayload
	if err := cryptossh.Unmarshal(newChannel.ExtraData(), &payload); err != nil {
		_ = newChannel.Reject(cryptossh.ConnectionFailed, "malformed direct-tcpip payload")
		return
	}

	conn, err := net.Dial("tcp", net.JoinHostPort(payload.DestAddr, strconv.Itoa(int(payload.DestPort))))
	if err != nil {
		_ = newChannel.Reject(cryptossh.ConnectionFailed, err.Error())
		return
	}

	channel, requests, err := newChannel.Accept()
	if err != nil {
		_ = conn.Close()
		return
	}
	go cryptossh.DiscardRequests(requests)
	forwardDirectTCPIP(channel, conn)
}

// forwardDirectTCPIP genuinely forwards channel to conn, an
// already-dialed real TCP connection to the requested destination: real
// bytes cross a real SSH channel to a real second listener, not a mock
// that only asserts a function was called. Returns once both directions
// have finished (either side closing ends both, since each io.Copy's own
// Read then fails).
func forwardDirectTCPIP(channel cryptossh.Channel, conn net.Conn) {
	defer func() { _ = channel.Close() }()
	defer func() { _ = conn.Close() }()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(conn, channel)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(channel, conn)
	}()
	wg.Wait()
}

// serveSession answers one exec request by running the command through
// /bin/sh, exactly as a real sshd hands it to the account's login shell.
func serveSession(channel cryptossh.Channel, requests <-chan *cryptossh.Request, log *commandLog) {
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
		log.add(command)

		// Running the command through a real shell is the entire point of
		// this harness: a stand-in that pattern-matched on the command
		// string would be asserting against its own idea of a shell rather
		// than a shell.
		cmd := exec.Command("/bin/sh", "-c", command) // #nosec G204 -- test harness; the command is the calling test's own input, see this package's doc comment
		cmd.Stdout = channel
		cmd.Stderr = channel.Stderr()

		// Standard input goes through an explicit pipe rather than
		// cmd.Stdin = channel, so the session ends when the COMMAND ends,
		// the way OpenSSH's sshd behaves. With cmd.Stdin set to a
		// non-file, os/exec's Wait also waits for its own copy of
		// standard input to reach end-of-file, so a command that exited
		// without reading its input (a script refusing early) left the
		// session open until the client happened to close its side, and
		// a client waiting for the command's output to end waited
		// forever. Wait closes this pipe once the command has exited, and
		// the copy below then ends when the channel does.
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return
		}
		go func() {
			_, _ = io.Copy(stdin, channel)
			_ = stdin.Close()
		}()

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
