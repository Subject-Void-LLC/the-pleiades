// Opening a channel to a Unix socket on the far host.
package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// DialUnix opens a direct-streamlocal@openssh.com channel from this
// connection to the Unix socket at path on the far host, and returns it as
// a net.Conn. It is how a method reaches a service that listens only on a
// socket on the device, such as a container engine, without a program of
// its own on the device and without opening a network port.
//
// The channel rides this connection's already-authenticated *ssh.Client,
// so it inherits the host key check, the hop chain, the breaker and the
// pool the connection was built with. It opens as the login user, so the
// socket's own permissions decide whether it may be opened, exactly as
// they would for a program that user ran on the device.
//
// path must be absolute. It is sent to sshd as it is and never through a
// shell, so nothing in it is expanded.
//
// A refusal from sshd is not a broken connection: the server answered,
// and the connection stays as usable as it was. The error names every
// cause the answer allows. OpenSSH sends the same "connect failed" answer
// for every refusal: its forwarding settings (AllowTcpForwarding no, which
// it applies to Unix sockets too, DisableForwarding yes,
// AllowStreamLocalForwarding no) and a socket that cannot be opened all
// come back as "connect failed", with only its own log saying which
// (measured 2026-10-13 against OpenSSH on Alpine 3.24, whose default is
// AllowTcpForwarding no; internal/testsupport's
// TestDockerHost_SSHDRefusals). So the error names every cause, rather than
// guessing one. A channel open that ctx cancels
// partway taints a borrowed connection, since whether the far side opened
// it is then unknown, and so does a channel whose reads or writes failed.
func (c *Conn) DialUnix(ctx context.Context, path string) (net.Conn, error) {
	if err := c.refuseBecome("a forwarded Unix socket"); err != nil {
		return nil, err
	}
	if err := c.usable(); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) {
		return nil, fmt.Errorf("remoteexec: socket path %q on %s must be absolute and hold no NUL byte", path, c.addr)
	}

	return c.openForwarded(ctx, "unix", path, describeChannelRefusal)
}

// openForwarded opens a channel from this connection to address on the far
// side's network (network "unix" or "tcp"), describing a refusal with
// describe. DialUnix and DialTCP share it, so both take the same care with
// a cancellation and a channel that breaks.
func (c *Conn) openForwarded(ctx context.Context, network, address string, describe func(error) error) (net.Conn, error) {
	// ssh.Client.Dial takes no context, so the open runs in a goroutine and
	// a cancellation abandons it. The buffered channel lets that goroutine
	// finish and close what it opened even when nobody is waiting.
	type opened struct {
		conn net.Conn
		err  error
	}
	done := make(chan opened, 1)
	go func() {
		conn, err := c.client.Dial(network, address)
		done <- opened{conn: conn, err: err}
	}()

	select {
	case <-ctx.Done():
		// The open may or may not have reached the far side, so a borrowed
		// connection is not lent again with a channel of unknown state on it.
		c.taint()
		go func() {
			if r := <-done; r.conn != nil {
				_ = r.conn.Close()
			}
		}()
		return nil, fmt.Errorf("remoteexec: open a channel to %s on %s: %w", address, c.addr, ctx.Err())
	case r := <-done:
		if r.err != nil {
			return nil, fmt.Errorf("remoteexec: open a channel to %s on %s: %w", address, c.addr, describe(r.err))
		}
		return &unixChannel{Conn: r.conn, owner: c}, nil
	}
}

// describeChannelRefusal turns sshd's answer to a channel open into the
// cause an operator can act on, keeping the original error wrapped.
func describeChannelRefusal(err error) error {
	var open *ssh.OpenChannelError
	if !errors.As(err, &open) {
		return err
	}
	switch open.Reason {
	case ssh.Prohibited:
		return fmt.Errorf("sshd refuses forwarding to a Unix socket: %w", err)
	case ssh.ConnectionFailed:
		return fmt.Errorf("sshd denied the channel or could not open the socket. OpenSSH gives this one answer for AllowTcpForwarding no "+
			"(it applies to Unix sockets too; \"local\" or \"yes\" allows them), DisableForwarding yes, AllowStreamLocalForwarding no, "+
			"a key restricted with no-port-forwarding, a socket that is absent, and one the login user may not open; sshd's own log says which: %w", err)
	default:
		return err
	}
}

// unixChannel is a channel DialUnix or DialTCP opened. It taints the connection it
// rode on when a read or write failed for any reason other than the far
// side's clean end of stream, since the channel's state on the server is
// then unknown.
type unixChannel struct {
	net.Conn
	owner *Conn

	mu     sync.Mutex
	broken bool
}

// Read reads from the socket, remembering a failure.
func (u *unixChannel) Read(b []byte) (int, error) {
	n, err := u.Conn.Read(b)
	u.note(err)
	return n, err
}

// Write writes to the socket, remembering a failure.
func (u *unixChannel) Write(b []byte) (int, error) {
	n, err := u.Conn.Write(b)
	u.note(err)
	return n, err
}

// Close closes the channel, tainting the connection first if the channel
// broke.
func (u *unixChannel) Close() error {
	u.mu.Lock()
	broken := u.broken
	u.mu.Unlock()
	if broken {
		u.owner.taint()
	}
	return u.Conn.Close()
}

// note records err as a breakage unless it is nil or a clean end of
// stream.
func (u *unixChannel) note(err error) {
	if err == nil || errors.Is(err, io.EOF) {
		return
	}
	u.mu.Lock()
	u.broken = true
	u.mu.Unlock()
}
