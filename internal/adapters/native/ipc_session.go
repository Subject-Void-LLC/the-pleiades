// The parent side of a dispatch's collection session: one long-lived
// child per dispatch whose connections persist, so its tasks against the
// device share one SSH login, where the one-shot path spawns a child, and
// logs in, per task.
package native

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// sessionOutputCap bounds how much of a session child's stdout and stderr
// is kept for the log. A session outlives many tasks, so unlike a one-shot
// child's its output accumulates, and a chatty method must not be able to
// grow the Runner without limit.
const sessionOutputCap = 1 << 20

// ipcSession runs one dispatch's Collection calls through one child. The
// child is started at the first call and ended when the dispatch ends, a
// call is cancelled, the exchange breaks, or a method that changes what a
// login carries has run (collection.Manifest.EndsLoginSession); the next
// call then starts a fresh child, whose pool logs in again.
//
// It is safe for concurrent use, though a dispatch serializes its tasks
// on the device lock: a call arriving while another holds the child runs
// in a one-shot child of its own rather than waiting.
type ipcSession struct {
	oneShot *ipcCollectionExecutor
	secrets []string

	mu    sync.Mutex
	child *sessionChild
}

// sessionChild is one running session child and the ends of its pipes.
type sessionChild struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	enc       *json.Encoder
	responses chan sessionAnswer
	// stop ends the response reader when the parent stops listening;
	// exited is closed once the process has been waited for.
	stop           chan struct{}
	exited         chan struct{}
	stdout, stderr *cappedBuffer
}

// sessionAnswer is one decoded response, or why none could be read.
type sessionAnswer struct {
	resp wire.ChildResponse
	err  error
}

// newSession returns the session for one dispatch whose payload carries
// secrets, which are masked out of anything the child sends back.
func (e *ipcCollectionExecutor) newSession(payload wire.DispatchPayload) *ipcSession {
	return &ipcSession{oneShot: e, secrets: secretValues(payload.Secrets)}
}

// invoke implements engine.CollectionInvoker through the session child.
func (s *ipcSession) invoke(ctx context.Context, desc collection.Descriptor, device inventory.InventoryItem, params map[string]interface{}, mode collection.Mode) (collection.Result, map[string]interface{}, error) {
	wd, ok := device.(*wireDevice)
	if !ok {
		return collection.Result{}, nil, fmt.Errorf("ipc collection session requires a *wireDevice, got %T", device)
	}
	// An external Collection is its own process already, and a call that
	// finds the child busy does not wait for it: both take the one-shot
	// path, which decides the first case itself.
	if desc.Provider != nil || !s.mu.TryLock() {
		return s.oneShot.invoke(ctx, desc, device, params, mode)
	}
	defer s.mu.Unlock()

	child, err := s.start()
	if err != nil {
		return collection.Result{}, nil, fmt.Errorf("collection method %q: %w", desc.Name, err)
	}
	req := childRequest(desc, mode, params, wd.Payload())
	if err := child.enc.Encode(&req); err != nil {
		stderr := s.end(true)
		return collection.Result{}, nil, fmt.Errorf("collection method %q: failed to send to the session child: %w (stderr: %s)", desc.Name, err, stderr)
	}

	var answer sessionAnswer
	select {
	case answer = <-child.responses:
	case <-ctx.Done():
		// The method may be mid-command; ending the child is the only way
		// to stop it, and its connection's state is unknown anyway.
		s.end(true)
		return collection.Result{}, nil, fmt.Errorf("collection method %q: %w", desc.Name, ctx.Err())
	case <-child.exited:
		select {
		case answer = <-child.responses:
		default:
			answer.err = io.ErrUnexpectedEOF
		}
	}
	if answer.err != nil {
		stderr := s.end(true)
		return collection.Result{}, nil, fmt.Errorf("collection method %q: failed to read the session child's response: %w (stderr: %s)", desc.Name, answer.err, stderr)
	}
	if mode == collection.ModeExecute && desc.Manifest.EndsLoginSession {
		s.end(false)
	}
	return childAnswer(desc.Name, mode, answer.resp, s.secrets)
}

// start returns the running child, starting one when there is none.
// s.mu must be held.
func (s *ipcSession) start() (*sessionChild, error) {
	if s.child != nil {
		return s.child, nil
	}
	responseRead, responseWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open the session response pipe: %w", err)
	}
	// #nosec G204 -- exePath is os.Executable()'s own resolved path, and
	// the one argument is the compile-time InternalCollectionSessionArg;
	// every task-controlled value travels on stdin, never argv.
	cmd := exec.Command(s.oneShot.exePath, InternalCollectionSessionArg)
	c := &sessionChild{
		cmd:       cmd,
		responses: make(chan sessionAnswer),
		stop:      make(chan struct{}),
		exited:    make(chan struct{}),
		stdout:    &cappedBuffer{max: sessionOutputCap},
		stderr:    &cappedBuffer{max: sessionOutputCap},
	}
	cmd.Stdout, cmd.Stderr = c.stdout, c.stderr
	cmd.ExtraFiles = []*os.File{responseWrite}
	if c.stdin, err = cmd.StdinPipe(); err != nil {
		_ = responseRead.Close()
		_ = responseWrite.Close()
		return nil, fmt.Errorf("failed to open the session child's stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = responseRead.Close()
		_ = responseWrite.Close()
		return nil, fmt.Errorf("failed to start the session child: %w", err)
	}
	// The child holds its own copy of the write end; this one must close
	// so the reader sees the end of the stream when the child exits.
	_ = responseWrite.Close()
	c.enc = json.NewEncoder(c.stdin)
	go c.readResponses(responseRead)
	go func() {
		_ = cmd.Wait() // how it ended is read from stderr, which Wait has finished copying
		close(c.exited)
	}()
	s.child = c
	return c, nil
}

// readResponses decodes the child's answers in order until the stream
// ends or the parent stops listening.
func (c *sessionChild) readResponses(r io.ReadCloser) {
	defer func() { _ = r.Close() }()
	dec := json.NewDecoder(r)
	for {
		var a sessionAnswer
		a.err = dec.Decode(&a.resp)
		select {
		case c.responses <- a:
		case <-c.stop:
			return
		}
		if a.err != nil {
			return
		}
	}
}

// end stops the child and returns its stderr, masked, for an error
// message. Graceful closes its input, the ordinary end of a session, so
// it closes its pool before exiting; otherwise it is sent SIGTERM at
// once. Either way it is killed if it has not exited within
// childWaitDelay. s.mu must be held.
func (s *ipcSession) end(kill bool) string {
	c := s.child
	if c == nil {
		return ""
	}
	s.child = nil
	close(c.stop)
	_ = c.stdin.Close() // the child's exit is what is waited for below
	if kill {
		_ = c.cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-c.exited:
	case <-time.After(childWaitDelay):
		_ = c.cmd.Process.Kill()
		<-c.exited
	}
	stdout := redact.Text(s.secrets, c.stdout.String())
	stderr := redact.Text(s.secrets, c.stderr.String())
	if s.oneShot.logger != nil && (stdout != "" || stderr != "") {
		s.oneShot.logger.Debug("collection session output", slog.String("stdout", stdout), slog.String("stderr", stderr))
	}
	return stderr
}

// Close ends the session at the end of its dispatch.
func (s *ipcSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.end(false)
}

// cappedBuffer keeps the first max bytes written to it and counts the
// rest. The child's output is copied into it from os/exec's own
// goroutines, so it locks.
type cappedBuffer struct {
	mu      sync.Mutex
	buf     []byte
	max     int
	dropped int
}

// Write keeps what fits and reports every byte written, so the copy
// feeding it never stalls the child.
func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	keep := min(len(p), b.max-len(b.buf))
	b.buf = append(b.buf, p[:keep]...)
	b.dropped += len(p) - keep
	return len(p), nil
}

// String returns what was kept, noting how much was not.
func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dropped > 0 {
		return fmt.Sprintf("%s\n[%d more bytes not kept]", b.buf, b.dropped)
	}
	return string(b.buf)
}
