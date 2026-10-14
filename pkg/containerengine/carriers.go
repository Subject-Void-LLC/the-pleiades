// The carriers: the ways a session's connections reach an engine's API,
// one Dialer each.
package containerengine

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Dialer opens one connection to an engine's API. A session calls it once
// per request, since keep-alive is off.
type Dialer func(ctx context.Context) (net.Conn, error)

// Local dials socket on the machine running this process. It is for a device
// advertising capability.EngineLocalCapable: socket must be the path
// onboarding proved on this same machine, never a value a record was given.
func Local(socket capability.SocketAddress) Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", string(socket))
	}
}

// SSHSocket opens a channel to socket on the device through conn, the
// login every other SSH task uses (capability.EngineSocketCapable).
func SSHSocket(conn *remoteexec.Conn, socket capability.SocketAddress) Dialer {
	return func(ctx context.Context) (net.Conn, error) { return conn.DialUnix(ctx, string(socket)) }
}

// TLS dials addr and completes a TLS handshake with cfg, which comes from
// pkg/devicetls, so the device's pinned authority and the TLS 1.2 floor
// apply exactly as they do to every other device connection
// (capability.EngineTLSCapable).
func TLS(addr string, cfg *tls.Config) Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		d := tls.Dialer{Config: cfg}
		return d.DialContext(ctx, "tcp", addr)
	}
}

// podmanServiceIdle is how long a Podman API service a session started
// waits with no request before it exits on its own, so a session that ends
// without stopping it, or a login that drops, leaves nothing running for
// long.
const podmanServiceIdle = 60 * time.Second

// stdioCarrier runs the engine's own `system dial-stdio` over an SSH exec
// channel for each connection (capability.EngineStdioCapable), which works
// where sshd refuses forwarding. For Podman with no API socket it first
// starts one API service for the whole session, on a socket in a private
// directory, and stops it on Close.
type stdioCarrier struct {
	conn   *remoteexec.Conn
	kind   capability.EngineKind
	socket capability.SocketAddress

	mu      sync.Mutex
	service *remoteexec.Process
	dir     string
	// open holds every dial-stdio connection not yet closed, so the
	// carrier's own close can end them in order before the login goes back.
	open map[*processConn]struct{}
}

// SSHStdio returns a dialer through conn's `dial-stdio` for kind's engine at
// socket, and a function that releases what the carrier started. socket
// empty means the engine's default for Docker, and a service started for
// the session for Podman.
func SSHStdio(conn *remoteexec.Conn, kind capability.EngineKind, socket capability.SocketAddress) (Dialer, func()) {
	c := &stdioCarrier{conn: conn, kind: kind, socket: socket}
	return c.dial, c.close
}

// dial starts one dial-stdio process and returns it as a connection.
func (c *stdioCarrier) dial(ctx context.Context) (net.Conn, error) {
	argv, err := c.argv(ctx)
	if err != nil {
		return nil, err
	}
	// ctx is the dial's: it bounds starting the process, and the process
	// then lives until the connection's Close. Tied to ctx for good, the
	// session would close when the HTTP transport ends the dial's context
	// after its request, often before dial-stdio could report its exit, and
	// an exit nobody saw keeps the login out of the pool (measured
	// 2026-10-14: 46 of 201 requests). The transport still cancels a
	// request by closing its connection, so cancellation is not lost.
	life, end := context.WithCancel(context.WithoutCancel(ctx))
	detach := context.AfterFunc(ctx, end)
	proc, err := c.conn.Start(life, remoteexec.QuoteCommand(argv))
	detach()
	if err != nil {
		end()
		return nil, fmt.Errorf("containerengine: starting %s system dial-stdio: %w", c.kind, err)
	}
	pc := &processConn{proc: proc, end: end, carrier: c}
	c.mu.Lock()
	if c.open == nil {
		c.open = map[*processConn]struct{}{}
	}
	c.open[pc] = struct{}{}
	c.mu.Unlock()
	return pc, nil
}

// argv is the dial-stdio command for this carrier's engine and socket.
func (c *stdioCarrier) argv(ctx context.Context) ([]string, error) {
	switch c.kind {
	case capability.EngineDocker:
		if c.socket == "" {
			return []string{"docker", "system", "dial-stdio"}, nil
		}
		return []string{"docker", "-H", "unix://" + string(c.socket), "system", "dial-stdio"}, nil
	case capability.EnginePodman:
		socket := c.socket
		if socket == "" {
			started, err := c.startService(ctx)
			if err != nil {
				return nil, err
			}
			socket = started
		}
		return []string{"podman", "--url", "unix://" + string(socket), "system", "dial-stdio"}, nil
	default:
		return nil, fmt.Errorf("containerengine: no dial-stdio for engine %q", c.kind)
	}
}

// startService starts the session's Podman API service once and returns its
// socket. The service runs as the login user, in a directory only that user
// can open, and exits after podmanServiceIdle with no request even if
// nothing stops it.
func (c *stdioCarrier) startService(ctx context.Context) (capability.SocketAddress, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.service != nil {
		return capability.SocketAddress(c.dir + "/api.sock"), nil
	}
	res, err := c.conn.Run(ctx, "umask 077 && mktemp -d")
	if err != nil {
		return "", fmt.Errorf("containerengine: making a private directory for the Podman service: %w", err)
	}
	dir := strings.TrimSpace(res.Stdout)
	if res.ExitCode != 0 || !strings.HasPrefix(dir, "/") || strings.ContainsAny(dir, " \t\n'\"\\$`") {
		return "", fmt.Errorf("containerengine: mktemp answered %q (exit %d)", dir, res.ExitCode)
	}
	socket := dir + "/api.sock"
	idle := strconv.Itoa(int(podmanServiceIdle / time.Second))
	// The service records its own process ID before it starts, so close can
	// stop it: ending an SSH exec channel with no terminal does not signal
	// the process on the far side (measured 2026-10-13: the service kept
	// running until its idle time ran out).
	startScript := `echo $$ > "$1/pid" && exec podman system service --time="$2" "unix://$1/api.sock"`
	service, err := c.conn.Start(ctx, remoteexec.QuoteCommand([]string{"sh", "-c", startScript, "sh", dir, idle}))
	if err != nil {
		return "", fmt.Errorf("containerengine: starting a Podman API service: %w", err)
	}
	// The service creates its socket a moment after it starts; wait for it,
	// bounded by ctx, rather than racing the first request.
	wait := remoteexec.QuoteCommand([]string{"sh", "-c", `i=0; while [ ! -S "$1" ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done; [ -S "$1" ]`, "sh", socket})
	if res, err := c.conn.Run(ctx, wait); err != nil || res.ExitCode != 0 {
		stopScript := `[ -f "$1/pid" ] && kill "$(cat "$1/pid")" 2>/dev/null; rm -rf "$1"`
		_, _ = c.conn.Run(context.Background(), remoteexec.QuoteCommand([]string{"sh", "-c", stopScript, "sh", dir}))
		_ = service.Close()
		return "", fmt.Errorf("containerengine: the Podman API service did not listen (%s): %v", strings.TrimSpace(service.Stderr()), err)
	}
	c.service, c.dir = service, dir
	return capability.SocketAddress(socket), nil
}

// close stops the session's Podman service and removes its directory. A
// failure here leaves a service that exits on its own when idle.
func (c *stdioCarrier) close() {
	// The HTTP transport closes a finished request's connection on its own
	// goroutine, so the session's last one can still be open here. Each is
	// ended now, in order, before the login it rides is given back:
	// released with a process still open, the login would be closed rather
	// than pooled, and the process would never report its exit (measured
	// 2026-10-14: the last request of every session).
	c.mu.Lock()
	open := make([]*processConn, 0, len(c.open))
	for pc := range c.open {
		open = append(open, pc)
	}
	c.mu.Unlock()
	for _, pc := range open {
		_ = pc.Close()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.service == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopScript := `[ -f "$1/pid" ] && kill "$(cat "$1/pid")" 2>/dev/null; rm -rf "$1"`
	_, _ = c.conn.Run(ctx, remoteexec.QuoteCommand([]string{"sh", "-c", stopScript, "sh", c.dir}))
	// Seen to exit after the kill, the service leaves the login reusable.
	awaitExit(c.service, nil)
	_ = c.service.Close()
	c.service, c.dir = nil, ""
}

// processConn is a dial-stdio process as a connection.
type processConn struct {
	proc *remoteexec.Process
	// end releases the process's own context once it is closed.
	end context.CancelFunc
	// carrier forgets this connection when it closes.
	carrier *stdioCarrier
	once    sync.Once
}

// Read reads the engine's answer.
func (p *processConn) Read(b []byte) (int, error) { return p.proc.Read(b) }

// Write sends a request.
func (p *processConn) Write(b []byte) (int, error) { return p.proc.Write(b) }

// CloseWrite half-closes the request side, as a hijacked stream with
// standard input needs.
func (p *processConn) CloseWrite() error { return p.proc.CloseWrite() }

// exitGrace is how long a process this carrier started is given to report
// its exit once its work is done. Seen to exit, it leaves nothing on the
// login, which then goes back to the run's pool for the device's next task;
// not seen in time, the login is closed with it instead (remoteexec's
// rule for streamed processes).
const exitGrace = 2 * time.Second

// Close ends the dial-stdio process: its input first, so it sees end of
// file and exits once the engine has answered (it already has: keep-alive
// is off), then whatever output is left is drained, since a process blocked
// writing never exits, and its exit is awaited for at most exitGrace.
func (p *processConn) Close() error {
	var err error
	p.once.Do(func() {
		_ = p.proc.CloseWrite()
		awaitExit(p.proc, p.proc)
		err = p.proc.Close()
		if p.end != nil {
			p.end()
		}
		if p.carrier != nil {
			p.carrier.mu.Lock()
			delete(p.carrier.open, p)
			p.carrier.mu.Unlock()
		}
	})
	return err
}

// awaitExit drains out and waits for proc to report its exit, for at most
// exitGrace; the caller closes proc either way.
func awaitExit(proc *remoteexec.Process, out io.Reader) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if out != nil {
			_, _ = io.Copy(io.Discard, out)
		}
		_, _ = proc.Wait()
	}()
	select {
	case <-done:
	case <-time.After(exitGrace):
	}
}

// LocalAddr is a fixed address: a process has none.
func (p *processConn) LocalAddr() net.Addr { return stdioAddr{} }

// RemoteAddr is a fixed address: a process has none.
func (p *processConn) RemoteAddr() net.Addr { return stdioAddr{} }

// SetDeadline is unsupported; a session bounds a request by its context,
// which closes the connection.
func (p *processConn) SetDeadline(time.Time) error { return errDeadline }

// SetReadDeadline is unsupported, as SetDeadline.
func (p *processConn) SetReadDeadline(time.Time) error { return errDeadline }

// SetWriteDeadline is unsupported, as SetDeadline.
func (p *processConn) SetWriteDeadline(time.Time) error { return errDeadline }

// errDeadline says a dial-stdio connection takes no deadline.
var errDeadline = errors.New("containerengine: a dial-stdio connection takes no deadline; cancel its context instead")

// stdioAddr names a dial-stdio connection's ends.
type stdioAddr struct{}

// Network implements net.Addr.
func (stdioAddr) Network() string { return "stdio" }

// String implements net.Addr.
func (stdioAddr) String() string { return path.Join("dial-stdio") }
