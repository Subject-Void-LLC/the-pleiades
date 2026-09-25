// Capturing the host key an SSH server presents, bounded.
package testsupport

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// hostKeyAttemptTimeout bounds one attempt at CaptureHostKey's handshake.
const hostKeyAttemptTimeout = 15 * time.Second

// CaptureHostKey returns the host key the SSH server at addr presents,
// the role ssh-keyscan plays when an operator first pins a host, after a
// handshake that also proves user and password are accepted. It retries
// until the server answers or SSHDStartupTimeout has passed.
//
// Every attempt is bounded, and that is the point of this helper.
// ssh.ClientConfig.Timeout bounds only ssh.Dial's TCP connect: a handshake
// over a connection the caller dialed has no deadline of its own, and a
// published container port can accept a connection before the server
// behind it listens (Docker's port proxy does, FAILURE_PATTERNS 346). The
// copies this replaces read the version banner from such a connection
// with no deadline, and waited until go test's thirty minute timeout
// killed the package (FAILURE_PATTERNS 351).
func CaptureHostKey(t testing.TB, addr, user, password string) ssh.PublicKey {
	t.Helper()
	key, err := captureHostKey(addr, user, password, SSHDStartupTimeout, hostKeyAttemptTimeout)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// captureHostKey is CaptureHostKey with its two bounds as parameters: the
// whole wait, and each attempt within it.
func captureHostKey(addr, user, password string, overall, attempt time.Duration) (ssh.PublicKey, error) {
	deadline := time.Now().Add(overall)
	var lastErr error
	for {
		key, err := captureHostKeyOnce(addr, user, password, attempt)
		if err == nil {
			return key, nil
		}
		lastErr = err
		if !time.Now().Add(200 * time.Millisecond).Before(deadline) {
			return nil, fmt.Errorf("no SSH server at %s completed a handshake within %v: %w", addr, overall, lastErr)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// captureHostKeyOnce makes one handshake with addr under a deadline of
// timeout, and returns the key the server presented.
func captureHostKeyOnce(addr, user, password string, timeout time.Duration) (ssh.PublicKey, error) {
	var captured ssh.PublicKey
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			captured = key
			return nil
		},
		Timeout: timeout,
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		return nil, err
	}
	_ = ssh.NewClient(sshConn, chans, reqs).Close()
	if captured == nil {
		return nil, errors.New("the handshake completed and presented no host key")
	}
	return captured, nil
}
