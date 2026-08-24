package remoteexec

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ShellOptions configures the PTY a Shell opens. Every field has a
// documented default applied when left at its Go zero value, matching
// Options's own convention.
type ShellOptions struct {
	// Term is the TERM value advertised to the remote side (e.g.
	// "vt100", "xterm"). Defaults to "vt100", the terminal type every
	// network CLI this package has been proven against expects and the
	// most conservative choice for a device whose terminal emulation is
	// unknown.
	Term string

	// Width and Height are the terminal's column and row count. They
	// matter beyond cosmetics: a device's own pager (Cisco IOS's
	// "--More--", for one) triggers off the advertised height, so a
	// Dialect's DisablePaging command exists precisely because a real
	// terminal size was advertised here in the first place. Default to
	// 200x50, wide and tall enough that a device's own line wrapping
	// rarely breaks output mid-token, with paging still disabled
	// explicitly rather than relied upon.
	Width, Height int
}

// Shell is one live, interactive PTY-backed session on a Conn, in place
// of Run/RunWithStdin's one-shot exec request. pkg/netcli builds vendor
// CLI sessions on top of it; nothing in this package interprets a
// prompt or a configuration mode, the same division RunWithStdin draws
// between "ship bytes over SSH" and "interpret what came back."
//
// A Shell is not safe for concurrent use by multiple goroutines, the
// same restriction Conn itself carries and for the same reason: nothing
// here serializes two callers driving one session, and no caller in
// this codebase needs that.
//
// A ctx deadline firing on any call, or ReadUntil's own byte bound being
// exceeded, closes the underlying session and leaves the Shell
// permanently unusable afterward. This is the same simplicity choice
// Conn.Run's own cancellation handling already makes: an interactive
// session has no natural end-of-file the way a one-shot command's
// stream does, so a hung remote or a wrong prompt pattern gets a clean,
// immediate failure rather than an attempt at partial resynchronization
// this package cannot verify succeeded.
type Shell struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader

	// carry holds bytes ReadUntil read past its last match, so a second
	// prompt already sitting in the same read is not silently dropped.
	// Only ReadUntil touches it, consistent with this type's own
	// not-safe-for-concurrent-use rule.
	carry []byte

	// addr names the target in error messages, mirroring Conn's own
	// field of the same purpose.
	addr string
}

// Shell opens an interactive, PTY-backed session on this connection.
//
// It shares c's already-authenticated *ssh.Client: opening one costs a
// new SSH channel, not a new TCP connection, key exchange or
// authentication round, which is the whole reason this lives on Conn
// rather than a second, duplicate dialer. See this file's own package
// position in pkg/netcli's doc comment for why: an interactive CLI
// opens the exact same "session" channel type Run/RunWithStdin already
// do, just with a pty-req and a shell request instead of an exec
// request, so there is nothing here for a second implementation to earn
// its cost against.
func (c *Conn) Shell(ctx context.Context, opts ShellOptions) (*Shell, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("remoteexec: open session on %s: %w", c.addr, err)
	}

	term := opts.Term
	if term == "" {
		term = "vt100"
	}
	width := opts.Width
	if width == 0 {
		width = 200
	}
	height := opts.Height
	if height == 0 {
		height = 50
	}

	// Cancellation is enforced by closing the session out from under
	// whichever of RequestPty/StdinPipe/StdoutPipe/Shell below is
	// blocked, the same shape RunWithStdin's own goroutine uses around
	// session.Run. Without this a caller's ctx timeout could not reach
	// a session stuck negotiating a pty-req against an unresponsive
	// device.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close() // #nosec G104 -- intentional, see comment above
		case <-done:
		}
	}()

	if err := session.RequestPty(term, height, width, ssh.TerminalModes{}); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("remoteexec: request pty on %s: %w", c.addr, wrapCtx(ctx, err))
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("remoteexec: open shell stdin on %s: %w", c.addr, err)
	}

	// A PTY merges stderr into stdout at the terminal layer: once a
	// pty-req has been made there is one combined stream, exactly as
	// there is at a real serial console, so only StdoutPipe is wired
	// here. A caller has no separate stream to read.
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("remoteexec: open shell stdout on %s: %w", c.addr, err)
	}

	if err := session.Shell(); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("remoteexec: start shell on %s: %w", c.addr, wrapCtx(ctx, err))
	}

	return &Shell{session: session, stdin: stdin, stdout: stdout, addr: c.addr}, nil
}

// WriteLine sends line to the session, followed by a single carriage
// return to submit it, and refuses a line already containing a
// carriage return or line feed of its own.
//
// A bare "\r" and not "\r\n" is deliberate, and it is not cosmetic:
// verified directly against a real Cisco IOS XE device
// (pkg/netcli/live_probe_test.go), sending "\r\n" makes the device's own
// line discipline treat the trailing "\n" as a second, empty Enter
// press, and IOS answers an empty line by reprinting its prompt with no
// output at all -- the same thing a bare Enter at a real terminal does.
// Every command then appeared to complete twice: once for real, once
// for nothing, leaving that second, empty prompt sitting unread in
// Shell's own carry buffer to corrupt the START of whatever the caller
// read next. A single "\r" is also what a real interactive terminal
// emulator sends for the Enter key in the first place, so this is
// bringing WriteLine in line with a real client's own behavior, not a
// device-specific workaround.
//
// The refusal is this package's whole injection defense, and it is
// sufficient: an interactive CLI has no shell-metacharacter concept the
// way a POSIX command word does (no quoting, no pipes, no
// substitution), so the only way a caller-supplied value could inject a
// second command is by embedding a line terminator of its own,
// submitting a new line to the session the caller never asked to send.
// Checked before any I/O, this closes the whole class the same way
// QuoteArg and pkg/winrmsvc's quotePS each close theirs with one
// narrow, sufficient mechanism rather than an attempted allowlist.
func (s *Shell) WriteLine(ctx context.Context, line string) error {
	if strings.ContainsAny(line, "\r\n") {
		return fmt.Errorf("remoteexec: shell line for %s contains an embedded line terminator, which would submit an unintended second line to the session", s.addr)
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = s.session.Close() // #nosec G104 -- intentional, see Shell's own doc comment
		case <-done:
		}
	}()

	if _, err := io.WriteString(s.stdin, line+"\r"); err != nil {
		return fmt.Errorf("remoteexec: write to shell on %s: %w", s.addr, wrapCtx(ctx, err))
	}
	return nil
}

// ReadUntil reads from the session until it has accumulated a byte
// sequence pattern matches, and returns everything read up to and
// including that match. Bytes read past the match are kept and
// prepended to the next ReadUntil call's own read, so a second prompt
// that arrived in the same underlying read is never silently dropped.
//
// maxBytes bounds how much this call will read before giving up, and
// the bound is not optional: an interactive session has no natural
// end-of-file the way a one-shot exec command's stream does, so a wrong
// prompt pattern, or a device stuck on a paging prompt this package was
// never told to disable, would otherwise buffer forever. This follows
// pkg/catalystcenter/client.go's own maxResponseBytes precedent and its
// stated reason: a device is a trusted-ish upstream, but "trusted" is
// not "allowed to exhaust this process's memory."
//
// Exceeding maxBytes, like a ctx deadline firing, closes the underlying
// session; see Shell's own doc comment for why leaving it usable
// afterward is not attempted.
func (s *Shell) ReadUntil(ctx context.Context, pattern *regexp.Regexp, maxBytes int) (string, error) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = s.session.Close() // #nosec G104 -- intentional, see Shell's own doc comment
		case <-done:
		}
	}()

	buf := s.carry
	s.carry = nil

	chunk := make([]byte, 4096)
	for {
		if loc := pattern.FindIndex(buf); loc != nil {
			s.carry = append([]byte(nil), buf[loc[1]:]...)
			return string(buf[:loc[1]]), nil
		}
		if len(buf) >= maxBytes {
			_ = s.session.Close()
			return "", fmt.Errorf("remoteexec: shell on %s: read %d bytes without matching %s, exceeding the %d-byte bound", s.addr, len(buf), pattern, maxBytes)
		}

		n, err := s.stdout.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			return "", fmt.Errorf("remoteexec: read from shell on %s: %w", s.addr, wrapCtx(ctx, err))
		}
	}
}

// Close closes the underlying session. It is safe to call once; a Shell
// is not reusable afterward, matching Conn.Close's own contract.
func (s *Shell) Close() error {
	return s.session.Close()
}

// wrapCtx substitutes ctx's own error for err when ctx is what actually
// ended the operation, so a caller sees "context deadline exceeded"
// rather than the raw I/O failure a closed-out-from-under-it session
// produces, which describes the mechanism rather than the cause. This
// is the same substitution RunWithStdin performs inline; it is
// extracted here because Shell's three methods each need it against a
// different underlying error.
func wrapCtx(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
