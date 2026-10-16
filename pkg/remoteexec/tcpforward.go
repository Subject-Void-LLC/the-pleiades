// Opening a channel to a TCP endpoint as the far host sees it.
package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// DialTCP opens a direct-tcpip channel from this connection to address
// (host:port), connected from the far host, and returns it as a net.Conn.
// It answers whether the device reaches an endpoint, with no program on the
// device to ask: a gate check's "requires tcp" precondition is the first
// caller.
//
// It rides this connection exactly as DialUnix does (the host key check,
// the hop chain, the breaker, the pool), and a refusal leaves the
// connection usable. OpenSSH answers "connect failed" both when the
// endpoint refuses or does not exist and when its own settings deny the
// channel (AllowTcpForwarding no, DisableForwarding yes, a key restricted
// with no-port-forwarding), so the error names both rather than guessing.
func (c *Conn) DialTCP(ctx context.Context, address string) (net.Conn, error) {
	if err := c.usable(); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || strings.ContainsAny(host, "\x00 \n\r") {
		return nil, fmt.Errorf("remoteexec: %q is not a host:port", address)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("remoteexec: %q has no port from 1 to 65535", address)
	}
	return c.openForwarded(ctx, "tcp", address, describeTCPRefusal)
}

// describeTCPRefusal turns sshd's answer to a direct-tcpip open into the
// causes it may mean, keeping the original error wrapped.
func describeTCPRefusal(err error) error {
	var open *ssh.OpenChannelError
	if !errors.As(err, &open) {
		return err
	}
	switch open.Reason {
	case ssh.Prohibited:
		return fmt.Errorf("sshd refuses TCP forwarding: %w", err)
	case ssh.ConnectionFailed:
		return fmt.Errorf("the endpoint refused or is unreachable from the device, or sshd denies TCP forwarding (AllowTcpForwarding no, "+
			"DisableForwarding yes, a key restricted with no-port-forwarding); sshd's own log says which: %w", err)
	default:
		return err
	}
}
