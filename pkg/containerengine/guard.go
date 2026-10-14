// The guard: the one wrapper every session connection passes through, which
// refuses an undeclared request before forwarding a byte and bounds the answer.
package containerengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
)

// maxRequestLine bounds how much of a request the guard buffers looking for
// its first line. An Engine API request line is a method, a path and a
// query; anything longer is refused rather than buffered without end.
const maxRequestLine = 16 << 10

// guard returns a Dialer whose every connection refuses, before forwarding a
// byte, a request outside ops. Keep-alive is off for every session
// (session.go), so a connection carries one request: an ordinary one whose
// response ends it, or a hijacked one (exec, attach) whose request line is
// followed by a raw stream. Either way the first line is the whole decision.
func guard(dial Dialer, ops []Op) Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		conn, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		return &guardedConn{Conn: conn, ops: ops}, nil
	}
}

// guardedConn holds back what is written to it until the request line is
// complete and allowed.
type guardedConn struct {
	net.Conn
	ops []Op

	mu      sync.Mutex
	decided bool
	refused error
	pending []byte

	// bound is the most the answer to the request this connection carries
	// may hold (boundFor), and read is how much of it has arrived.
	bound int64
	read  int64
}

// errRefused wraps every refusal, so a caller can tell the guard's answer
// from the engine's.
var errRefused = errors.New("refused by the session's declared operations")

// errTooLarge wraps a refusal of an engine's answer that passed its bound.
var errTooLarge = errors.New("refused an engine answer larger than its bound")

// Write forwards b once the request line is known to be allowed, and refuses
// it, forwarding nothing, otherwise. The lock covers the decision only, never
// a write to the connection: an exec or attach stream writes and reads at
// once, and a write that waits on the engine must not keep the reader from
// draining what the engine sends.
func (g *guardedConn) Write(b []byte) (int, error) {
	g.mu.Lock()
	if g.refused != nil {
		err := g.refused
		g.mu.Unlock()
		return 0, err
	}
	if g.decided {
		g.mu.Unlock()
		return g.Conn.Write(b)
	}
	g.pending = append(g.pending, b...)
	end := bytes.Index(g.pending, []byte("\r\n"))
	if end < 0 {
		if len(g.pending) > maxRequestLine {
			g.refuse(fmt.Errorf("%w: a request line longer than %d bytes", errRefused, maxRequestLine))
			err := g.refused
			g.mu.Unlock()
			return 0, err
		}
		// The line is not complete yet; nothing is forwarded until it is.
		g.mu.Unlock()
		return len(b), nil
	}
	fields := bytes.Fields(g.pending[:end])
	if len(fields) != 3 || (string(fields[2]) != "HTTP/1.1" && string(fields[2]) != "HTTP/1.0") {
		g.refuse(fmt.Errorf("%w: a malformed request line", errRefused))
		err := g.refused
		g.mu.Unlock()
		return 0, err
	}
	op, err := allowed(g.ops, string(fields[0]), string(fields[1]))
	if err != nil {
		g.refuse(fmt.Errorf("%w: %w", errRefused, err))
		err := g.refused
		g.mu.Unlock()
		return 0, err
	}
	g.decided = true
	g.bound = boundFor(op)
	pending := g.pending
	g.pending = nil
	g.mu.Unlock()
	if _, err := g.Conn.Write(pending); err != nil {
		return 0, err
	}
	return len(b), nil
}

// Read reads the engine's answer, or reports the refusal on a connection
// the guard refused, so the error a caller sees says why rather than that
// the connection closed. An answer past its operation's bound is cut off
// with an error rather than read without end: an engine's answer is
// untrusted, and a process that decodes it whole must not be made to hold
// whatever an engine chooses to send.
func (g *guardedConn) Read(b []byte) (int, error) {
	g.mu.Lock()
	refused, bound, read := g.refused, g.bound, g.read
	g.mu.Unlock()
	if refused != nil {
		return 0, refused
	}
	if bound != unbounded {
		if read >= bound {
			return 0, g.overflow(bound)
		}
		if left := bound - read; int64(len(b)) > left {
			b = b[:left]
		}
	}
	n, err := g.Conn.Read(b)
	g.mu.Lock()
	g.read += int64(n)
	if err != nil && g.refused != nil {
		err = g.refused
	}
	g.mu.Unlock()
	return n, err
}

// overflow ends a connection whose answer reached its bound, and says so.
func (g *guardedConn) overflow(bound int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.refused == nil {
		g.refuse(fmt.Errorf("%w: the engine's answer passed the %d bytes this operation's answer may hold", errTooLarge, bound))
	}
	return g.refused
}

// refuse records err and closes the connection, so nothing more can be sent
// on it. The caller holds g.mu.
func (g *guardedConn) refuse(err error) {
	g.refused = err
	g.pending = nil
	_ = g.Conn.Close()
}

// CloseWrite passes a half-close through when the carrier supports one, as
// an exec or attach stream with standard input needs.
func (g *guardedConn) CloseWrite() error {
	if cw, ok := g.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
