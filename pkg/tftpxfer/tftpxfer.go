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
// constructing a traversal-shaped remote filename — but TFTP itself has
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
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
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
	// fixed size) in place.
	BlockSize int
}

const (
	// DefaultTimeout is how long the client waits for a single network
	// round trip before retrying.
	DefaultTimeout = 5 * time.Second

	// DefaultRetries is how many times the client retransmits an
	// unacknowledged packet before giving up.
	DefaultRetries = 5
)

// mode is always "octet" (binary): TFTP's other transfer mode,
// "netascii", exists to reinterpret line endings for text files
// crossing an OS boundary, a translation this package has no reason to
// perform on a caller's behalf and every reason not to — it would
// silently alter binary content (a firmware image, a compiled artifact)
// that happened to contain a byte sequence netascii treats specially.
const mode = "octet"

// validateFilename refuses a remote filename containing a path
// traversal segment or an absolute path — see the package doc comment
// for exactly what this does and does not defend against.
func validateFilename(filename string) error {
	if filename == "" {
		return errors.New("filename must not be empty")
	}
	if strings.HasPrefix(filename, "/") || strings.HasPrefix(filename, `\`) {
		return fmt.Errorf("filename %q must not be an absolute path", filename)
	}
	// Refuses a Windows drive-letter path (e.g. "C:\Windows") too: ":"
	// is never meaningful in a legitimate TFTP filename, and without
	// this check such a path slips past the leading-slash test above
	// entirely -- found by this package's own test suite hitting a real
	// ~30s network retry against an unreachable port instead of an
	// instant refusal, exactly the gap this check now closes.
	if strings.Contains(filename, ":") {
		return fmt.Errorf("filename %q must not contain \":\"", filename)
	}
	for _, seg := range strings.FieldsFunc(filename, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return fmt.Errorf("filename %q must not contain a \"..\" path segment", filename)
		}
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
	if err := validateFilename(remoteFilename); err != nil {
		return 0, fmt.Errorf("tftpxfer: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("tftpxfer: %w", err)
	}

	c, err := newClient(host, port, opts)
	if err != nil {
		return 0, err
	}

	wt, err := c.Receive(remoteFilename, mode)
	if err != nil {
		return 0, fmt.Errorf("tftpxfer: requesting %s: %w", remoteFilename, err)
	}

	n, err := wt.WriteTo(w)
	if err != nil {
		return n, fmt.Errorf("tftpxfer: receiving %s: %w", remoteFilename, err)
	}
	return n, nil
}

// Put uploads the contents of r to a TFTP server at host:port as
// remoteFilename, returning the number of bytes transferred.
func Put(ctx context.Context, host string, port int, opts Options, remoteFilename string, r io.Reader) (int64, error) {
	if err := validateFilename(remoteFilename); err != nil {
		return 0, fmt.Errorf("tftpxfer: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("tftpxfer: %w", err)
	}

	c, err := newClient(host, port, opts)
	if err != nil {
		return 0, err
	}

	rf, err := c.Send(remoteFilename, mode)
	if err != nil {
		return 0, fmt.Errorf("tftpxfer: requesting to send %s: %w", remoteFilename, err)
	}

	n, err := rf.ReadFrom(r)
	if err != nil {
		return n, fmt.Errorf("tftpxfer: sending %s: %w", remoteFilename, err)
	}
	return n, nil
}
