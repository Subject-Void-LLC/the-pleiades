// Package tftpxfer moves a file to or from a TFTP server (RFC 1350, plus
// RFC 2347/2348 negotiated options and partial RFC 2349 via
// github.com/pin/tftp/v3) and is the single place this platform speaks
// that protocol.
//
// # No authentication, no encryption, ever
//
// TFTP has neither at the protocol level: any host that can reach the
// server's UDP port can read or write any file the server's own
// filesystem mapping allows. This package cannot fix that and must not
// appear to — the identical fact pkg/serialtcp and pkg/telnetexec's own
// doc comments state for their own protocols, and the reason a device
// that reaches this package still needs its own loud, explicit opt-in
// wherever a future Collection method wires one up.
//
// # Own interface, no TransportBinding
//
// A file transfer has no stdout and no exit status — it is not
// Exec-shaped, the identical reasoning pkg/rfc2217's own doc comment
// gives for staying out of engine.TransportBinding entirely.
//
// This is also why Phase 73's own adversarial testing checklist item
// "the opt-in gate cannot be satisfied by configuration alone" has no
// test against this package: there is no TransportBinding, so there is
// no RequireOptInParam gate here to attempt to satisfy in the first
// place. That checklist item applies once a future Collection method
// wires this package into a real dispatch path (the same "loud,
// explicit opt-in" the paragraph above already commits to); until one
// exists, the honest state is that the gate does not exist yet, not
// that it exists and was left untested.
//
// # This package refuses an obviously hostile remote filename, but that is not the whole defense
//
// validateFilename refuses a remote filename containing ".." or an
// absolute path, before this package ever sends a request naming it.
// That is a real, cheap guard against a caller (a future Collection
// method, or any other direct consumer) accidentally or maliciously
// constructing a traversal-shaped remote filename.
//
// It also refuses a name that the request itself cannot carry: one with a
// control or format character, one that is not valid UTF-8, and one longer
// than MaxFilenameBytes. A request ends the filename, the mode and each
// option with a NUL, so a NUL inside a name would rewrite the mode (to
// "netascii", which alters a binary image's bytes) or add an option, and
// github.com/pin/tftp/v3 packs the request into one fixed buffer with no
// bounds check. Options.BlockSize is held to the range the library can
// negotiate for the same reason: its digits share that buffer.
//
// None of that is the whole defense, because TFTP itself has
// no concept of a client-side root to escape FROM: the server alone
// decides what its own configured root is and how a requested filename
// maps onto it, and this package has no visibility into that mapping at
// all. The complementary half of "a filename cannot escape its root" —
// never combining an untrusted REMOTE filename with a LOCAL filesystem
// path via path.Join or similar without validating it first — is the
// responsibility of whatever future caller writes a Get'd file to local
// disk; this package only ever writes to the io.Writer a caller supplies
// directly, and never constructs a local path itself.
//
// # Cancellation is bounded, not immediate
//
// github.com/pin/tftp/v3's Client exposes no context-aware API: once
// ReadFrom or WriteTo is called, it blocks until the transfer completes,
// fails, or its own internal Timeout/Retries budget is exhausted — there
// is no hook this package can use to interrupt it early. Get and Put
// check ctx only before starting a transfer, not during one; a caller
// wanting a hard ceiling should set Options.Timeout (paired with
// Options.Retries) to bound the transfer's own worst case, since ctx
// cancellation alone cannot do it here. Stated plainly rather than
// silently retrofitting a goroutine-based cancellation that would leave
// the library's own internal goroutine running uninterruptibly anyway.
//
// # A panic inside the library comes back as an error
//
// Get and Put recover a panic raised inside github.com/pin/tftp/v3 and
// return it as an error, since a library bug reached through a request or
// a server's reply would otherwise end the whole process. The filename
// rules above make the one such panic known today unreachable; the
// recovery is for the next one. Every call builds its own client, so a
// panic leaves nothing half used that a later call would share: the
// library closes the call's socket as it unwinds from a request or an
// upload, and the garbage collector reclaims one a download had open. A
// panic in the caller's own reader or writer is not the library's, and is
// raised again unchanged.
//
// tftp.NewClient failing is real defensive coverage but not reachable
// from this package's own test suite: it only fails on a malformed
// address string, and newClient always builds one from a validated
// host/port pair via net.JoinHostPort. The identical class of gap
// pkg/serialexec, pkg/serialtcp, pkg/telnetexec, pkg/rfc2217, and
// pkg/dockerexec each already document for their own always-valid
// construction paths.
package tftpxfer

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	tftp "github.com/pin/tftp/v3"
)

// Options configures how Get and Put reach the server.
type Options struct {
	// Timeout bounds how long the client waits for a single network
	// round trip before retrying. Zero takes DefaultTimeout.
	Timeout time.Duration

	// Retries bounds how many times the client retransmits an
	// unacknowledged packet before giving up. Zero takes DefaultRetries.
	Retries int

	// BlockSize sets the transfer's negotiated block size (RFC 2348).
	// Zero leaves the library's own default (512, RFC 1350's original
	// fixed size) in place. Any other value must lie between MinBlockSize
	// and MaxBlockSize, and is refused before anything is sent.
	BlockSize int
}

const (
	// DefaultTimeout is how long the client waits for a single network
	// round trip before retrying.
	DefaultTimeout = 5 * time.Second

	// DefaultRetries is how many times the client retransmits an
	// unacknowledged packet before giving up.
	DefaultRetries = 5

	// MinBlockSize is the smallest Options.BlockSize accepted. RFC 2348
	// allows 8, but github.com/pin/tftp/v3 ignores a server's answer
	// below 512 and keeps reading 512-byte blocks, so the server's first
	// smaller block reads as the last one: a server honoring a smaller
	// request would end the transfer early with no error.
	MinBlockSize = 512

	// MaxBlockSize is the largest Options.BlockSize accepted: RFC 2348's
	// own maximum, and the longest value MaxFilenameBytes leaves room for.
	MaxBlockSize = 65464
)

// mode is always "octet" (binary): TFTP's other transfer mode,
// "netascii", exists to reinterpret line endings for text files
// crossing an OS boundary, a translation this package has no reason to
// perform on a caller's behalf and every reason not to — it would
// silently alter binary content (a firmware image, a compiled artifact)
// that happened to contain a byte sequence netascii treats specially.
const mode = "octet"

// preflight makes every refusal Get and Put share, before a socket is
// opened or a byte is sent.
func preflight(ctx context.Context, opts Options, remoteFilename string) error {
	if err := validateFilename(remoteFilename); err != nil {
		return fmt.Errorf("tftpxfer: %w", err)
	}
	if opts.BlockSize != 0 && (opts.BlockSize < MinBlockSize || opts.BlockSize > MaxBlockSize) {
		return fmt.Errorf("tftpxfer: block size %d is outside the %d to %d this client can negotiate",
			opts.BlockSize, MinBlockSize, MaxBlockSize)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("tftpxfer: %w", err)
	}
	return nil
}

func newClient(host string, port int, opts Options) (*tftp.Client, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	c, err := tftp.NewClient(addr)
	if err != nil {
		return nil, fmt.Errorf("tftpxfer: creating client for %s: %w", addr, err)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	c.SetTimeout(timeout)

	retries := opts.Retries
	if retries <= 0 {
		retries = DefaultRetries
	}
	c.SetRetries(retries)

	if opts.BlockSize > 0 {
		c.SetBlockSize(opts.BlockSize)
	}

	return c, nil
}

// Get downloads remoteFilename from a TFTP server at host:port and
// writes it to w, returning the number of bytes transferred.
func Get(ctx context.Context, host string, port int, opts Options, remoteFilename string, w io.Writer) (int64, error) {
	if err := preflight(ctx, opts, remoteFilename); err != nil {
		return 0, err
	}
	return receive(host, port, opts, remoteFilename, w)
}

// receive is Get past its refusals, kept apart so a test can hand the
// library a request those refusals would stop and prove its panic comes
// back as an error.
func receive(host string, port int, opts Options, remoteFilename string, w io.Writer) (n int64, err error) {
	dst := &callerWriter{w: w}
	defer func() {
		if v := recover(); v != nil {
			n, err = dst.n, recoverLibraryPanic(v, remoteFilename)
		}
	}()

	c, err := newClient(host, port, opts)
	if err != nil {
		return 0, err
	}

	wt, err := c.Receive(remoteFilename, mode)
	if err != nil {
		return 0, fmt.Errorf("tftpxfer: requesting %q: %w", remoteFilename, err)
	}

	n, err = wt.WriteTo(dst)
	if err != nil {
		return n, fmt.Errorf("tftpxfer: receiving %q: %w", remoteFilename, err)
	}
	return n, nil
}

// Put uploads the contents of r to a TFTP server at host:port as
// remoteFilename, returning the number of bytes transferred.
func Put(ctx context.Context, host string, port int, opts Options, remoteFilename string, r io.Reader) (int64, error) {
	if err := preflight(ctx, opts, remoteFilename); err != nil {
		return 0, err
	}
	return send(host, port, opts, remoteFilename, r)
}

// send is Put past its refusals, kept apart for the same reason as receive.
func send(host string, port int, opts Options, remoteFilename string, r io.Reader) (n int64, err error) {
	src := &callerReader{r: r}
	defer func() {
		if v := recover(); v != nil {
			n, err = src.n, recoverLibraryPanic(v, remoteFilename)
		}
	}()

	c, err := newClient(host, port, opts)
	if err != nil {
		return 0, err
	}

	rf, err := c.Send(remoteFilename, mode)
	if err != nil {
		return 0, fmt.Errorf("tftpxfer: requesting to send %q: %w", remoteFilename, err)
	}

	n, err = rf.ReadFrom(src)
	if err != nil {
		return n, fmt.Errorf("tftpxfer: sending %q: %w", remoteFilename, err)
	}
	return n, nil
}
