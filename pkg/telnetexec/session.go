// Package telnetexec: an interactive Telnet session that logs in when the
// device asks, for a caller that sends more than one line (pkg/netcli).
package telnetexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Login is the account a session answers a device's login prompts with.
// The zero Login answers nothing: a device that asks is refused with
// ErrLoginNeeded rather than sent an empty name.
type Login struct {
	Username string
	Password string
}

// ErrLoginNeeded is a device that asked for a login a session was given
// no account for.
var ErrLoginNeeded = errors.New("telnetexec: the device asks for a login and no credential was given")

// ErrLoginRefused is a device that turned down the login it was given.
var ErrLoginRefused = errors.New("telnetexec: the device refused the login")

// The prompts a login answers, each anchored to the end of what the
// device has sent so far, so a banner that merely mentions a password is
// not taken for a question. IOS asks "Username:", Linux "login:".
var (
	usernamePrompt = regexp.MustCompile(`(?i)(user ?name|login)\s*:\s*$`)
	passwordPrompt = regexp.MustCompile(`(?i)pass(word|code)\s*:\s*$`)
	// loginRefusal is what IOS, Linux and BusyBox say to a wrong login.
	loginRefusal = regexp.MustCompile(`(?i)(login invalid|login incorrect|authentication failed|access denied|bad passwords?)`)
)

// maxLoginSteps bounds the questions a login answers: a name, a password,
// and a second password some devices ask for an enable secret are all a
// login needs; a device that keeps asking is refused.
const maxLoginSteps = 4

// Session is one Telnet connection, its option negotiation answered (every
// option refused) and its login done. Its WriteLine, ReadUntil and Close
// are pkg/netcli's Shell, so a Dialect runs over it as over SSH.
type Session struct {
	conn  net.Conn
	f     *iacFilter
	opts  Options
	addr  string
	carry []byte
}

// Dial connects to host:port and opens a Session on the connection. The
// Session owns the connection; Close closes it.
func Dial(ctx context.Context, host string, port int, opts Options, login Login) (*Session, error) {
	opts = opts.withDefaults()
	dialCtx, cancel := context.WithTimeout(ctx, opts.DialTimeout)
	defer cancel()
	var d net.Dialer
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("telnetexec: dialing %s: %w", addr, err)
	}
	s, err := Open(ctx, conn, opts, login)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

// Open reads conn's opening negotiation and banner and, when the device
// asks, logs in: a name prompt is answered with login's Username, a
// password prompt with its Password, each once. Everything read after the
// login is kept for the first ReadUntil, so a prompt the device printed
// then is not lost. The Session owns conn once Open succeeds.
func Open(ctx context.Context, conn net.Conn, opts Options, login Login) (*Session, error) {
	opts = opts.withDefaults()
	s := &Session{conn: conn, f: &iacFilter{}, opts: opts, addr: conn.RemoteAddr().String()}
	seen, err := s.quiet(ctx)
	if err != nil {
		return nil, err
	}
	sentPassword := false
	for range maxLoginSteps {
		tail := lastLine(seen)
		var answer string
		switch {
		case sentPassword && loginRefusal.Match(seen):
			return nil, fmt.Errorf("%w at %s", ErrLoginRefused, s.addr)
		case usernamePrompt.MatchString(tail):
			if sentPassword {
				return nil, fmt.Errorf("%w at %s: it asked for the name again", ErrLoginRefused, s.addr)
			}
			if login.Username == "" {
				return nil, fmt.Errorf("%w (%s asked %q)", ErrLoginNeeded, s.addr, tail)
			}
			answer = login.Username
		case passwordPrompt.MatchString(tail):
			if login.Password == "" && login.Username == "" {
				return nil, fmt.Errorf("%w (%s asked %q)", ErrLoginNeeded, s.addr, tail)
			}
			answer, sentPassword = login.Password, true
		default:
			s.carry = seen
			return s, nil
		}
		if err := writeData(conn, []byte(answer+"\r\n")); err != nil {
			return nil, fmt.Errorf("telnetexec: answering the login at %s: %w", s.addr, err)
		}
		if seen, err = s.answer(ctx); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w at %s: it kept asking", ErrLoginRefused, s.addr)
}

// answer reads the device's reply to a login answer: until it has said
// something besides blank lines, for up to the login timeout, and then
// until it goes quiet. A login checker may wait seconds before answering
// (BusyBox's login waits about three before "Login incorrect", measured
// 2026-10-23), and a read that took that silence for a quiet device took
// a refused login for an accepted one.
func (s *Session) answer(ctx context.Context) ([]byte, error) {
	deadline := time.Now().Add(s.opts.LoginTimeout)
	var seen []byte
	for {
		more, err := s.quiet(ctx)
		if err != nil {
			return nil, err
		}
		seen = append(seen, more...)
		if len(bytes.TrimSpace(seen)) > 0 || time.Now().After(deadline) {
			return seen, nil
		}
	}
}

// quiet reads until the device goes quiet, bounded by the session's
// output limit.
func (s *Session) quiet(ctx context.Context) ([]byte, error) {
	out, err := readUntilQuiet(ctx, s.conn, s.f, s.opts.ReadTimeout, s.opts.MaxOutputBytes)
	if err != nil {
		return nil, fmt.Errorf("telnetexec: reading from %s: %w", s.addr, err)
	}
	return out, nil
}

// lastLine is the text after the last line break, with trailing NULs (a
// Telnet CR NUL) dropped: where a device's question waits.
func lastLine(b []byte) string {
	b = bytes.TrimRight(b, "\x00")
	if i := bytes.LastIndexAny(b, "\r\n"); i >= 0 {
		b = b[i+1:]
	}
	return string(b)
}

// WriteLine submits line followed by CR LF, Telnet's end of line (RFC
// 854), and refuses a line holding a line terminator of its own, which
// would submit a second line nobody asked for.
func (s *Session) WriteLine(ctx context.Context, line string) error {
	if strings.ContainsAny(line, "\r\n") {
		return fmt.Errorf("telnetexec: a line for %s contains an embedded line terminator, which would submit an unintended second line", s.addr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.conn.SetWriteDeadline(time.Now().Add(s.opts.DialTimeout)); err != nil {
		return fmt.Errorf("telnetexec: writing to %s: %w", s.addr, err)
	}
	if err := writeData(s.conn, []byte(line+"\r\n")); err != nil {
		return fmt.Errorf("telnetexec: writing to %s: %w", s.addr, err)
	}
	return nil
}

// ReadError is a ReadUntil that ended without matching. Its message is the
// read's own; Partial is what had been read, which the message never
// includes, since it may hold anything the device printed.
type ReadError struct {
	Partial []byte
	err     error
}

// Error is the read's own message.
func (e *ReadError) Error() string { return e.err.Error() }

// Unwrap returns the cause: the context's error, the connection's, or the
// byte bound.
func (e *ReadError) Unwrap() error { return e.err }

// PartialRead returns Partial (pkg/netcli's partial read).
func (e *ReadError) PartialRead() []byte { return e.Partial }

// readSlice is how long one read waits before the context is looked at
// again, so a cancelled run stops a session waiting on a quiet device.
const readSlice = 250 * time.Millisecond

// ReadUntil reads until pattern matches what has been read, returning it
// up to and including the match and keeping the rest for the next call.
// It reads at most maxBytes; past that, or when ctx ends or the
// connection fails, it closes the session and returns a *ReadError.
func (s *Session) ReadUntil(ctx context.Context, pattern *regexp.Regexp, maxBytes int) (string, error) {
	buf := s.carry
	s.carry = nil
	chunk := make([]byte, 4096)
	fail := func(err error) (string, error) {
		_ = s.conn.Close()
		return "", &ReadError{Partial: buf, err: err}
	}
	for {
		if loc := pattern.FindIndex(buf); loc != nil {
			s.carry = append([]byte(nil), buf[loc[1]:]...)
			return string(buf[:loc[1]]), nil
		}
		if len(buf) >= maxBytes {
			return fail(fmt.Errorf("telnetexec: %s: read %d bytes without matching %s, exceeding the %d-byte bound", s.addr, len(buf), pattern, maxBytes))
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := s.conn.SetReadDeadline(time.Now().Add(readSlice)); err != nil {
			return fail(fmt.Errorf("telnetexec: %s: %w", s.addr, err))
		}
		n, err := s.conn.Read(chunk)
		if n > 0 {
			data, ferr := s.f.feed(s.conn, chunk[:n])
			if ferr != nil {
				return fail(fmt.Errorf("telnetexec: answering %s's negotiation: %w", s.addr, ferr))
			}
			buf = append(buf, data...)
		}
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return fail(fmt.Errorf("telnetexec: reading from %s: %w", s.addr, err))
		}
	}
}

// Close closes the connection. It is safe to call more than once.
func (s *Session) Close() error {
	if err := s.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

// withDefaults fills the zero fields of opts.
func (o Options) withDefaults() Options {
	if o.DialTimeout <= 0 {
		o.DialTimeout = DefaultDialTimeout
	}
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = DefaultReadTimeout
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = DefaultMaxOutputBytes
	}
	if o.LoginTimeout <= 0 {
		o.LoginTimeout = DefaultLoginTimeout
	}
	return o
}
