// The cancellation watch over one command (conn.go's run): what ends a
// command whose context ended, and what tells run that its outcome came from
// that ending rather than from the command.
package remoteexec

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// abandonGrace is how long a cancelled command's session is given, after its
// SIGTERM and close, before its whole connection is closed under it.
//
// OpenSSH's sshd answers a client's close only once the command's output
// ends, and may not deliver the signal at all (measured on Debian's sshd:
// "sleep 60 & wait" ran its whole minute past a three-second limit), so a
// session close alone does not end the wait for it. Closing the connection
// does: it ends every channel on it, which returns Wait, a session open or
// exec request still waiting for its reply, and a standard input copy blocked
// on a window the remote stopped granting.
const abandonGrace = 2 * time.Second

// cancelWatch ends one command when its context ends: SIGTERM and a session
// close at once, then, if the command has not finished within abandonGrace,
// its connection closed. It also records whether it fired, since a command
// it fired on has an outcome caused by the firing (an exit status from the
// signal, a truncated read, or a remote that drained a cut-off input and
// exited 0), and that outcome is never reported as the command's.
type cancelWatch struct {
	// mu guards every field below it.
	mu sync.Mutex
	// session is the command's session once it is open, so a firing after
	// that signals and closes it.
	session *ssh.Session
	// fired is set when the context ended before finish was called.
	fired bool
	// finished is set by finish; a context ending after it changes nothing.
	finished bool
	// sever, once the watch fires or aborts, closes the connection after
	// abandonGrace unless finish stops it first.
	sever *time.Timer
	// clients are what sever closes, after severVia when it is set (a
	// connection opened through a route, whose clients' close would wait on
	// the route).
	clients  []*ssh.Client
	severVia func()
	// taint marks a borrowed connection not to be lent again once the
	// command is being ended, before its connection may be closed.
	taint func()
	// finishOnce makes finish safe to call again (a stream's wait, then its
	// close).
	finishOnce sync.Once

	// done is closed by finish, ending the goroutine if it has not fired.
	done chan struct{}
}

// watch starts the cancellation watch for one command on c. It is started
// before the session is opened, because opening it and starting the command
// each wait on a reply from the remote that only a closed connection ends.
// The clients it would close are the ones c reaches its device through now,
// copied, so a reconnect between commands never races it.
func (c *Conn) watch(ctx context.Context) *cancelWatch {
	clients := make([]*ssh.Client, 0, len(c.chain)+len(c.extra))
	clients = append(clients, c.chain...)
	clients = append(clients, c.extra...)
	w := &cancelWatch{done: make(chan struct{}), clients: clients, severVia: c.severVia, taint: c.taint}
	go func() {
		select {
		case <-w.done:
		case <-ctx.Done():
			w.end(true)
		}
	}()
	return w
}

// abort ends the command for a reason of run's own (its standard input could
// not be read), as a firing does but not recorded as the context's.
func (w *cancelWatch) abort() { w.end(false) }

// end signals and closes the command's session, if it is open, and arms the
// connection close; cancelled records that the context ended. It does
// nothing once finish has run, and arms the close only once.
func (w *cancelWatch) end(cancelled bool) {
	w.mu.Lock()
	if w.finished {
		w.mu.Unlock()
		return
	}
	if cancelled {
		w.fired = true
	}
	w.taint()
	if w.sever == nil {
		// Armed before the signal is sent, since sending it can block on a
		// connection that stopped carrying anything, which is what the
		// timer is there to end.
		clients, severVia := w.clients, w.severVia
		w.sever = time.AfterFunc(abandonGrace, func() {
			if severVia != nil {
				severVia()
			}
			severClients(clients)
		})
	}
	session := w.session
	w.mu.Unlock()
	if session != nil {
		endSession(session)
	}
}

// attach hands the watch the command's session, once it is open. It reports
// false when the watch fired first, and has then signalled and closed nothing:
// the caller closes the session and reports the context's error.
func (w *cancelWatch) attach(session *ssh.Session) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fired {
		return false
	}
	w.session = session
	return true
}

// ending reports whether the command is being ended (the watch fired or
// aborted), after which nothing more is sent to it.
func (w *cancelWatch) ending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sever != nil
}

// finish ends the watch and reports whether it fired before the command's
// own end, in which case the command's outcome is the firing's. It stops a
// connection close not yet made; one already made stays made. Calling it
// again changes nothing and reports the same.
func (w *cancelWatch) finish() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.finished = true
	if w.sever != nil {
		w.sever.Stop()
	}
	w.finishOnce.Do(func() { close(w.done) })
	return w.fired
}

// ErrCommandRefused is the error for a command the server would not start
// (an exec request it answered with a failure, as OpenSSH does when it
// cannot fork for the account). It names no command.
var ErrCommandRefused = errors.New("the server refused to start the command")

// withoutCommand returns err from starting line with the command taken out.
// x/crypto/ssh reports a refused exec as "ssh: command <line> failed", and a
// line can carry a secret a method put in the command, in the shell-encoded
// form a mask by value does not match. Such an error becomes
// ErrCommandRefused; any other start error (a dropped connection) names no
// command and is returned as it is.
func withoutCommand(err error, line string) error {
	if err == nil {
		return nil
	}
	if line != "" && strings.Contains(err.Error(), line) {
		return ErrCommandRefused
	}
	return err
}

// endSession sends a cancelled command SIGTERM (an SSH "signal" request, RFC
// 4254 section 6.9, which OpenSSH's sshd delivers to the process it started)
// and closes its session. With no terminal, a session close alone sends the
// command no hangup, so without the signal a task's timeout would end the
// task and not the work. A server that ignores the request ends nothing; the
// connection close after abandonGrace still ends the wait.
func endSession(session *ssh.Session) {
	// The errors are not actionable: run reports the context's error, and a
	// failure here means the session or connection is already gone.
	_ = session.Signal(ssh.SIGTERM) // #nosec G104 -- intentional, see comment above
	_ = session.Close()             // #nosec G104 -- intentional, see comment above
}

// severClients closes each client in dial order, the first hop's own TCP
// connection first: that close cannot wait on a tunnel, where a tunneled
// client's close sends a message through a hop that may have stopped carrying
// anything. The connection is tainted already, so nothing reuses it.
func severClients(clients []*ssh.Client) {
	for _, client := range clients {
		_ = client.Close()
	}
}

// stdinSource reads a command's standard input and keeps the first read
// error that is not the input's end, so a failed read fails the call rather
// than being sent to the remote as a clean end of input, which would let
// "cat > f.tmp && mv f.tmp f" commit the part it got and report success.
// The command is then signalled and its session closed. That cannot promise
// the device kept nothing: sshd ends the command's input when the channel
// closes, so on a server that does not deliver the signal the command may
// still finish on the part it got. The call's error says it may have run in
// part.
type stdinSource struct {
	// r is the caller's standard input.
	r io.Reader
	// mu guards err, which run reads once the copy has ended.
	mu  sync.Mutex
	err error
}

// Read reads from the caller's input, recording a failure other than io.EOF.
func (s *stdinSource) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF {
		s.mu.Lock()
		if s.err == nil {
			s.err = err
		}
		s.mu.Unlock()
	}
	return n, err
}

// failed returns the read error recorded, or nil.
func (s *stdinSource) failed() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
