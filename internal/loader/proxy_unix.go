//go:build unix

// Package loader: the proxy, the collection.Method registered for every
// external method, which runs the method's program once per call.
package loader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// program is one loaded external Collection program, as the proxies for
// its methods see it. It never changes after Load, so any number of
// calls may share it at once.
type program struct {
	path   string
	dir    string
	digest string
	opts   Options
}

// method returns the proxy for fqcn in mode: the collection.Method that
// Load registers as the method's Invoke (ModeExecute) or Check
// (ModeCheck).
func (p *program) method(fqcn string, mode collection.Mode) collection.Method {
	return func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		return p.invoke(ctx, fqcn, mode, rc, device, params)
	}
}

// invoke runs the program once for one call: verify it, send it the
// request on stdin, and turn what came back into a Result, recording its
// stats into rc.
func (p *program) invoke(ctx context.Context, fqcn string, mode collection.Mode, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	if rc == nil {
		return collection.Result{}, fmt.Errorf("external collection method %q was called with no runbook context", fqcn)
	}
	// Verified at the point of execution, not only at load: the file on
	// disk is what runs, and it could have changed, or lost its approval,
	// since. What runs is this open file, not whatever holds the name.
	prog, err := p.openVerified()
	if err != nil {
		return collection.Result{}, fmt.Errorf("external collection method %q refused: %w", fqcn, err)
	}
	defer func() { _ = prog.Close() }()

	req := buildRequest(fqcn, mode, params, device, rc.InjectSecrets())
	secrets := redact.MapValues(req.Secrets)
	body, err := json.Marshal(&req)
	if err != nil {
		return collection.Result{}, fmt.Errorf("external collection method %q: failed to encode its request: %w", fqcn, err)
	}
	// The encoded request holds the credential in plain bytes. They are
	// overwritten as soon as the program has read them, which Wait
	// guarantees has happened by the time run returns.
	out := p.run(ctx, body, prog)
	clear(body)

	p.logOutput(ctx, fqcn, mode, out, secrets)
	resp, err := p.interpret(ctx, fqcn, mode, out, secrets)
	if err != nil {
		return collection.Result{}, err
	}
	for key, value := range resp.Facts {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, fmt.Errorf("external collection method %q returned stat %q, which the run refused: %w", fqcn, truncateForMessage(key), err)
		}
	}
	return collection.Result{Changed: resp.Changed}, nil
}

// openVerified re-applies Load's checks to the directory and the
// program, refuses a program whose bytes no longer hash to the digest
// pinned when it was loaded (naming both) or whose approval has since
// been withdrawn, and returns the program open, for the run to execute
// through that same file. The caller closes it.
func (p *program) openVerified() (*os.File, error) {
	resolved, err := checkDir(p.dir)
	if err != nil {
		return nil, err
	}
	if resolved != p.dir {
		return nil, fmt.Errorf("directory %s now resolves to %s", p.dir, resolved)
	}
	prog, digest, err := openProgram(p.path)
	if err != nil {
		return nil, fmt.Errorf("program %s %w", p.path, err)
	}
	refuse := func(err error) (*os.File, error) {
		_ = prog.Close()
		return nil, err
	}
	if digest != p.digest {
		return refuse(fmt.Errorf("program %s changed after it was loaded (loaded as %s, now %s); it must be loaded again before it can run", p.path, p.digest, digest))
	}
	approvals, err := readApprovals(p.dir)
	if err != nil {
		return refuse(err)
	}
	if err := checkApproved(approvedDigests(approvals), p.path, digest); err != nil {
		return refuse(fmt.Errorf("%w (its approval was withdrawn after it was loaded)", err))
	}
	return prog, nil
}

// buildRequest builds the one request a call sends. device may be nil for
// a task with no target. Its address is sent only when the device is
// reachable over SSH, since that is the only address a method can use.
func buildRequest(fqcn string, mode collection.Mode, params map[string]any, device inventory.InventoryItem, secrets map[string]string) wire.ChildRequest {
	req := wire.ChildRequest{FQCN: fqcn, Mode: string(mode), Params: params, Secrets: secrets}
	if device != nil {
		req.DeviceID = string(device.ID())
		req.DeviceName = device.Name()
		req.Capabilities = device.Capabilities()
		if ssh, ok := device.(capability.SSHTransportCapable); ok {
			req.DeviceHost = ssh.SSHHost()
			req.SSHPort = ssh.SSHPort()
		}
	}
	return req
}

// runOutcome is everything one run produced, gathered before any of it is
// judged.
type runOutcome struct {
	stdout   *cappedBuffer
	stderr   *cappedBuffer
	startErr error
	runErr   error
	timedOut bool
	canceled error
	resp     responseRead
	heldOpen bool
}

// run starts the program with body on stdin and the response channel as
// file descriptor 3, waits for it within InvokeTimeout (and ctx), and
// collects the response.
func (p *program) run(ctx context.Context, body []byte, prog *os.File) runOutcome {
	o := p.opts
	out := runOutcome{stdout: newCappedBuffer(o.MaxOutput), stderr: newCappedBuffer(o.MaxOutput)}

	ictx, cancel := context.WithTimeout(ctx, o.InvokeTimeout)
	defer cancel()

	sb, err := newSandbox(p.path, o)
	if err != nil {
		out.startErr = err
		return out
	}
	defer sb.close()

	ch, write, err := openResponseChannel(o.MaxResponse)
	if err != nil {
		out.startErr = fmt.Errorf("failed to open the response channel: %w", err)
		return out
	}

	cmd := newCommand(ictx, p.path, external.CommandInvoke, prog, o, sb)
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stdout = out.stdout
	cmd.Stderr = out.stderr
	// ExtraFiles[0] is file descriptor 3 in the child, which is
	// external.ResponseFD; ExtraFiles[1] is the program itself
	// (newCommand).
	cmd.ExtraFiles[0] = write

	if o.beforeStart != nil {
		o.beforeStart()
	}
	startErr := sb.start(cmd)
	// The program holds its own copy of the write end now, or never will.
	// This process's copy must close either way, or the reader would never
	// see end of file: a pipe reports it only once every writer is gone.
	_ = write.Close()
	if startErr != nil {
		out.startErr = startErr
		out.resp, _ = ch.collect(0)
		return out
	}

	out.runErr = cmd.Wait()
	if out.runErr != nil && ctx.Err() != nil {
		out.canceled = ctx.Err()
	} else if out.runErr != nil && ictx.Err() != nil {
		out.timedOut = true
	}
	out.resp, out.heldOpen = ch.collect(o.graceAfterExit)
	return out
}

// interpret turns a run's outcome into the program's response, or into
// an error naming exactly which way the run went wrong. Every piece of
// program output quoted into an error has secrets masked out of it first.
func (p *program) interpret(ctx context.Context, fqcn string, mode collection.Mode, out runOutcome, secrets []string) (wire.ChildResponse, error) {
	name := fmt.Sprintf("external collection method %q (%s)", fqcn, p.path)
	stderr := stderrForMessage(redact.Text(secrets, out.stderr.String()))

	switch {
	case out.startErr != nil && ctx.Err() != nil:
		return wire.ChildResponse{}, fmt.Errorf("%s was canceled before it started: %w", name, ctx.Err())
	case out.startErr != nil:
		return wire.ChildResponse{}, fmt.Errorf("%s could not be started: %w", name, out.startErr)
	case out.canceled != nil:
		return wire.ChildResponse{}, fmt.Errorf("%s was canceled: %w (stderr: %q)", name, out.canceled, stderr)
	case out.timedOut:
		return wire.ChildResponse{}, fmt.Errorf("%s did not finish within %s and was stopped (stderr: %q)", name, p.opts.InvokeTimeout, stderr)
	case out.resp.oversize:
		return wire.ChildResponse{}, fmt.Errorf("%s wrote a response over the %d-byte limit (stderr: %q)", name, p.opts.MaxResponse, stderr)
	case out.runErr != nil && !errors.Is(out.runErr, exec.ErrWaitDelay):
		return wire.ChildResponse{}, fmt.Errorf("%s exited with an error: %v (stderr: %q)", name, out.runErr, stderr)
	case out.resp.err != nil && !out.heldOpen:
		return wire.ChildResponse{}, fmt.Errorf("%s: reading its response failed: %w", name, out.resp.err)
	}

	resp, err := decodeResponse(out.resp.data)
	if err != nil {
		switch {
		case errors.Is(err, errEmpty) && out.heldOpen:
			return wire.ChildResponse{}, fmt.Errorf("%s exited without writing a response, and a process it left behind still held the response channel open", name)
		case errors.Is(err, errEmpty):
			return wire.ChildResponse{}, fmt.Errorf("%s exited without writing a response (stderr: %q)", name, stderr)
		case errors.Is(err, io.ErrUnexpectedEOF):
			return wire.ChildResponse{}, fmt.Errorf("%s closed its response channel partway through the response (stderr: %q)", name, stderr)
		case errors.Is(err, errTrailing):
			return wire.ChildResponse{}, fmt.Errorf("%s wrote data after its response", name)
		default:
			return wire.ChildResponse{}, fmt.Errorf("%s wrote a malformed response: %v", name, err)
		}
	}
	if out.heldOpen {
		// A complete response arrived, so the call succeeded. What is
		// still running is worth an operator's attention all the same.
		p.opts.Logger.Warn("external collection left a process holding its response channel open",
			"program", p.path, "fqcn", fqcn)
	}
	if resp.CannotCheck {
		// The program says it cannot check this call. Honored only when a
		// check was asked for, and never read as a success whatever else
		// the response carries: an answer of "cannot check" to a real run
		// is a malformed response, and one with no reason still means the
		// task was not checked.
		if mode != collection.ModeCheck {
			return wire.ChildResponse{}, fmt.Errorf("%s answered that it cannot check a call that was not a check", name)
		}
		reason := termsafe.EscapeLine(redact.Text(secrets, resp.Error))
		if reason == "" {
			reason = "the program gave no reason"
		}
		return wire.ChildResponse{}, collection.CannotCheck(reason)
	}
	if resp.Error != "" {
		// The method's own reported failure, returned as a plain error the
		// way an in-process method's would be, so the engine cannot tell
		// the two apart.
		// Escaped onto one line as well as masked: the message is a third
		// party's text, and it is printed as the task's failure, where a
		// newline or an escape sequence could fake lines of output.
		return wire.ChildResponse{}, errors.New(termsafe.EscapeLine(redact.Text(secrets, resp.Error)))
	}
	return resp, nil
}

// errUnsafeFactKey is decodeResponse's refusal of a stat name.
var errUnsafeFactKey = errors.New("a stat name may not hold a newline, a tab or a character a terminal acts on")

// decodeResponse decodes one response frame and refuses a stat whose
// name could forge output where it is printed, or break the one-line
// records (the journal's stat keys) that carry it. Stat values are left
// as the program sent them: a command's output legitimately spans lines,
// and every place that prints one escapes it (termsafe.Escape).
func decodeResponse(data []byte) (wire.ChildResponse, error) {
	var resp wire.ChildResponse
	if err := decodeOne(data, &resp); err != nil {
		return wire.ChildResponse{}, err
	}
	for key := range resp.Facts {
		if termsafe.Check(key) != nil || strings.ContainsAny(key, "\n\t") {
			return wire.ChildResponse{}, fmt.Errorf("%w: %q", errUnsafeFactKey, truncateForMessage(key))
		}
	}
	return resp, nil
}

// logOutput logs what a run printed, masked, at debug level. Nothing
// printed is ever parsed: stdout and stderr are for people.
func (p *program) logOutput(ctx context.Context, fqcn string, mode collection.Mode, out runOutcome, secrets []string) {
	logger := p.opts.Logger
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	stdout, stderr := out.stdout.String(), out.stderr.String()
	if stdout == "" && stderr == "" {
		return
	}
	logger.Debug("external collection output",
		"program", p.path,
		"fqcn", fqcn,
		"mode", string(mode),
		"stdout", redact.Text(secrets, stdout),
		"stderr", redact.Text(secrets, stderr),
		"stdout_truncated", out.stdout.Truncated(),
		"stderr_truncated", out.stderr.Truncated())
}
