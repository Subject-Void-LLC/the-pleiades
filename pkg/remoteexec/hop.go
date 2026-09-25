package remoteexec

import (
	"context"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// Hop is one intermediate connection a Runner dials through before
// reaching its actual target: a bastion, a jump host, a management-network
// console server. A Hop is a full, independent SSH connection with its own
// address and its own authentication, never a property of the connection
// it is reached through.
//
// Route []Hop on a caller's own Target-equivalent (internal/transport's
// Target, for the one caller that resolves a device's configured route)
// is where a Hop chain is built; this package only ever sees the already
// -resolved, ordered list, dialed first to last, with the actual target
// dialed last of all.
type Hop struct {
	// Target is where this hop is reached: its own host and port.
	Target Target

	// Auth authenticates to Target. A bastion is a real device with its
	// own account and its own key rotation schedule; its credential is
	// never the final target's, and a caller must resolve it
	// independently (internal/credential.Store.Lookup under the hop's own
	// device name, for the one caller with a credential store to ask).
	Auth Auth

	// InsecureSkipHostKeyVerify bypasses host key verification for THIS
	// hop only, mirroring Options.InsecureSkipHostKeyVerify's own
	// "explicit, loud opt-in, never a fallback" rule at the per-hop
	// level: a lab bastion opting out of verification for itself must
	// never silently disable verification for the production device
	// reached through it.
	InsecureSkipHostKeyVerify bool
}

// dialThroughHop returns a dialFunc that reaches addr by opening a
// direct-tcpip channel through hopClient's already-authenticated
// connection, then running a SECOND, independent SSH handshake over that
// channel. hopClient (and anything watching its network segment) sees only
// ciphertext past this point: the channel carries the next hop's own
// encrypted SSH traffic, never its plaintext or its host key. This is the
// whole defense a compromised or hostile bastion is checked against, so
// getting the next line exactly right is load-bearing, not a style choice.
//
// addr is passed to ssh.NewClientConn EXPLICITLY, as the hostname a
// caller-supplied ssh.HostKeyCallback verifies against. This matters
// because (*ssh.Client).Dial/DialContext documents "the resulting
// connection has a zero LocalAddr() and RemoteAddr()"
// (golang.org/x/crypto@v0.54.0/ssh/tcpip.go), so a tunneled net.Conn's own
// RemoteAddr() is always "0.0.0.0:0" and useless for verification on its
// own. golang.org/x/crypto/ssh/knownhosts's own callback
// (knownhosts.go's hostKeyDB.check) computes its host-to-check from that
// RemoteAddr() first and ONLY THEN overrides it with the explicitly passed
// hostname when non-empty ("give preference to the hostname if
// available"), so passing addr here is what makes verification check the
// real next hop's key rather than failing closed against "0.0.0.0:0" (the
// safe failure) or, worse, checking the WRONG hop's address (an unsafe
// bug this comment exists to prevent a future edit from introducing:
// addr must always be the address of the connection being established
// right now, never the hop that is tunneling it).
func dialThroughHop(hopClient *ssh.Client) dialFunc {
	return func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		// The same bound realDial's handshake runs under. Without it a
		// target that accepts the forwarded connection and never sends
		// an SSH version held the run forever, whatever the caller's
		// context said (FAILURE_PATTERNS 352).
		dialCtx, cancel := handshakeContext(ctx, config)
		defer cancel()
		conn, err := hopClient.DialContext(dialCtx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("open tunneled channel to %s: %w", addr, err)
		}
		defer closeOnDone(dialCtx, conn)()

		sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
		if err != nil {
			return nil, fmt.Errorf("ssh handshake through tunnel to %s: %w", addr, err)
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}
}
