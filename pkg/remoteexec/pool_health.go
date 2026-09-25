// A pooled connection's health checks, made before it is lent again: the
// known_hosts file it was verified against is unchanged, and the device
// still answers on it.
package remoteexec

import (
	"context"
	"crypto/sha256"
	"os"
	"time"
)

// keepaliveRequest is OpenSSH's global liveness request. A server that
// does not know it still answers with a failure reply, and any reply at
// all proves the connection carries traffic both ways.
const keepaliveRequest = "keepalive@openssh.com"

// liveness is the outcome of one keepalive.
type liveness int

const (
	// answered means the device replied, success or failure alike.
	answered liveness = iota
	// dead means the connection failed under the request, or the
	// caller's context ended first, so it is closed and the dial that
	// follows reports the cancellation.
	dead
	// silent means no reply came before the timeout.
	silent
)

// alive sends one keepalive on c and waits up to timeout for its reply.
// On anything but answered the caller must close c, which also ends the
// request still waiting on a silent connection.
func alive(ctx context.Context, c *Conn, timeout time.Duration) liveness {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	reply := make(chan error, 1)
	go func() {
		_, _, err := c.client.SendRequest(keepaliveRequest, true, nil)
		reply <- err
	}()
	select {
	case err := <-reply:
		if err != nil {
			return dead
		}
		return answered
	case <-timer.C:
		return silent
	case <-ctx.Done():
		return dead
	}
}

// knownHostsStamp returns a digest of the known_hosts file key's
// connection will be verified against, as it is now, or a zero digest
// when verification is skipped. A file that cannot be read is an error:
// the connection is then not pooled, and the dial reports the problem
// itself.
//
// The whole content is hashed rather than compared by size and time,
// because a same-length edit inside one timestamp tick would pass that.
// Reading the file costs less than the parse a fresh dial makes of it.
func knownHostsStamp(key poolKey) ([sha256.Size]byte, error) {
	if key.insecure {
		return [sha256.Size]byte{}, nil
	}
	data, err := os.ReadFile(key.knownHosts)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(data), nil
}

// stampUnchanged reports whether e's known_hosts file still holds what
// its connection was verified against. Any edit fails this, so a removed
// or replaced host key is checked again by a fresh dial rather than
// trusted from an earlier login.
func stampUnchanged(e *pooled) bool {
	now, err := knownHostsStamp(e.key)
	return err == nil && now == e.stamp
}
