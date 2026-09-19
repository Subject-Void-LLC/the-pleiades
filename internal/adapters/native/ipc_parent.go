package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// childWaitDelay bounds how long a canceled collection subprocess is given
// to exit after exec.Cmd.Cancel (SIGTERM, below) before Go's own os/exec
// machinery escalates to killing it outright. This is what makes an
// interruptible job's self-abort (internal/runner's executeWithLease)
// actually reach the subprocess promptly rather than leaving it running
// past the ctx cancellation that was supposed to stop it.
const childWaitDelay = 5 * time.Second

// ipcCollectionExecutor is the parent side of the per-task subprocess
// boundary PLAN.md Section 17.5 requires: its invoke method satisfies
// engine.CollectionInvoker, spawning one child process per Collection
// method invocation (Phase 16's own design decision: isolating
// third-party Collection *code*, per Phase 42's stated future need, not
// this platform's own DAG orchestration) rather than one per whole job.
type ipcCollectionExecutor struct {
	// exePath is this process's own executable path, resolved once at
	// construction (os.Executable), re-exec'd with
	// InternalCollectionRunnerArg to become the child.
	exePath string
	logger  *slog.Logger
}

// newIPCCollectionExecutor resolves this process's own executable path
// once and returns the parent-side subprocess spawner. Fails closed at
// construction, the same shape every other Runner-mesh dependency already
// is: a Runner that cannot even find its own binary path has no business
// starting up.
func newIPCCollectionExecutor(logger *slog.Logger) (*ipcCollectionExecutor, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve own executable path: %w", err)
	}
	return &ipcCollectionExecutor{exePath: exePath, logger: logger}, nil
}

// invoke implements engine.CollectionInvoker, spawning
// exePath--internal-collection-runner, writing one wire.ChildRequest to
// its stdin, and reading one wire.ChildResponse back off a dedicated pipe
// (never stdout, which the Collection method's own arbitrary output uses
// instead, captured and masked separately below).
func (e *ipcCollectionExecutor) invoke(ctx context.Context, desc collection.Descriptor, device inventory.InventoryItem, params map[string]interface{}, mode collection.Mode) (collection.Result, map[string]interface{}, error) {
	wd, ok := device.(*wireDevice)
	if !ok {
		return collection.Result{}, nil, fmt.Errorf("ipc collection executor requires a *wireDevice, got %T", device)
	}
	payload := wd.Payload()

	// An external Collection already runs in a process of its own: its
	// registered method is the loader's proxy, which starts the external
	// program for every call. Re-executing this binary around it would add
	// a second process that could not even run it, since a fresh child
	// never loaded the external program's methods. So it runs here, with
	// the payload's secrets (the credential the Controller resolved at
	// fan-out) in a context zeroed the moment it returns, and the external
	// program is the boundary. Provider is set only by the loader when it
	// registers such a method, so this is the one statement of which
	// methods are external.
	if desc.Provider != nil {
		return e.invokeInProcess(ctx, desc, device, params, mode, payload.Secrets)
	}

	req := wire.ChildRequest{
		FQCN:         desc.Name,
		Mode:         string(mode),
		Params:       params,
		JobID:        payload.JobID,
		DeviceID:     payload.DeviceID,
		DeviceName:   payload.DeviceName,
		DeviceHost:   payload.DeviceHost,
		SSHPort:      payload.SSHPort,
		Capabilities: payload.Capabilities,
		Secrets:      payload.Secrets,
	}
	reqBytes, err := json.Marshal(&req)
	if err != nil {
		return collection.Result{}, nil, fmt.Errorf("failed to marshal child request: %w", err)
	}

	secrets := secretValues(payload.Secrets)

	// A pipe, not a second exec.Cmd-managed stream: ExtraFiles hands the
	// child a raw, unmanaged file descriptor (fd 3), so this package is
	// responsible for both ends. The response is read concurrently with
	// cmd.Run below, in a goroutine, rather than after it returns: reading
	// only after Run returns would deadlock if the child ever wrote more
	// than the OS pipe buffer holds before this process started draining
	// it (the classic exec-plus-pipe deadlock), even though a
	// ChildResponse is small in practice.
	responseRead, responseWrite, err := os.Pipe()
	if err != nil {
		return collection.Result{}, nil, fmt.Errorf("failed to open response pipe: %w", err)
	}
	defer responseRead.Close()

	type readResult struct {
		resp wire.ChildResponse
		err  error
	}
	respCh := make(chan readResult, 1)
	go func() {
		var resp wire.ChildResponse
		err := json.NewDecoder(responseRead).Decode(&resp)
		respCh <- readResult{resp: resp, err: err}
	}()

	// #nosec G204 -- exePath is os.Executable()'s own resolved path (this
	// process's own binary, set once at construction), and the sole
	// argument is the compile-time constant InternalCollectionRunnerArg.
	// Neither is caller- or network-controlled input; nothing about req
	// (which carries the actual task-controlled data) reaches argv at
	// all, only stdin, per PLAN.md Section 17.5.
	cmd := exec.CommandContext(ctx, e.exePath, InternalCollectionRunnerArg)
	cmd.Stdin = bytes.NewReader(reqBytes)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.ExtraFiles = []*os.File{responseWrite}
	// Cancel + WaitDelay (Go 1.20+): ctx cancellation sends SIGTERM first,
	// giving the child a chance to exit cleanly, and only escalates to a
	// forced kill after childWaitDelay if it has not.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = childWaitDelay

	runErr := cmd.Run()
	// This process's own copy of the write end must close for the reader
	// goroutine to see EOF if the child crashed without ever writing a
	// response: the child's own copy closes automatically on exit, but an
	// os.Pipe's read end only sees EOF once every writer copy is closed,
	// and this process holds one too. A close error here is logged, not
	// fatal to the overall invocation: it would only ever occur on an
	// already-invalid file descriptor, which changes nothing about the
	// response the reader goroutine either already received or is about
	// to see as EOF.
	if closeErr := responseWrite.Close(); closeErr != nil && e.logger != nil {
		e.logger.Debug("failed to close response pipe write end", slog.String("error", closeErr.Error()))
	}

	capturedOut := redact.Text(secrets, stdout.String())
	capturedErr := redact.Text(secrets, stderr.String())

	if e.logger != nil && (capturedOut != "" || capturedErr != "") {
		e.logger.Debug("collection subprocess output",
			slog.String("fqcn", desc.Name),
			slog.String("stdout", capturedOut),
			slog.String("stderr", capturedErr))
	}

	if runErr != nil {
		return collection.Result{}, nil, fmt.Errorf("collection method %q: subprocess failed: %w (stderr: %s)", desc.Name, runErr, capturedErr)
	}

	read := <-respCh
	if read.err != nil {
		return collection.Result{}, nil, fmt.Errorf("collection method %q: failed to decode subprocess response: %w (stderr: %s)", desc.Name, read.err, capturedErr)
	}
	if read.resp.CannotCheck {
		// The method says it cannot check this call (a
		// collection.CannotCheckError in the child). Honored only when a
		// check was asked for, and never read as a success: see
		// wire.ChildResponse.CannotCheck.
		if mode != collection.ModeCheck {
			return collection.Result{}, nil, fmt.Errorf("collection method %q answered that it cannot check a call that was not a check", desc.Name)
		}
		reason := redact.Text(secrets, read.resp.Error)
		if reason == "" {
			reason = "the method gave no reason"
		}
		return collection.Result{}, nil, collection.CannotCheck(reason)
	}
	if read.resp.Error != "" {
		return collection.Result{}, nil, errors.New(redact.Text(secrets, read.resp.Error))
	}

	return collection.Result{Changed: read.resp.Changed}, read.resp.Facts, nil
}

// invokeInProcess runs desc in mode in this process, for a method whose
// registered function already crosses a process boundary of its own (an
// external Collection's loader proxy). The secrets reach it through the
// same zeroing context a real child uses, and its facts come back the same
// way, so the engine sees an identical result either way.
func (e *ipcCollectionExecutor) invokeInProcess(ctx context.Context, desc collection.Descriptor, device inventory.InventoryItem, params map[string]interface{}, mode collection.Mode, secrets map[string]string) (collection.Result, map[string]interface{}, error) {
	method, err := desc.MethodFor(mode)
	if err != nil {
		return collection.Result{}, nil, err
	}
	rc := external.NewRunbookContext(secrets)
	defer rc.Zero()

	result, err := method(ctx, rc, device, params)
	if err != nil {
		return collection.Result{}, nil, err
	}
	return result, rc.Facts(), nil
}

// secretValues extracts every value from secrets, the shape
// redact.Text needs and the shape Flatten's own keys are irrelevant
// to: masking cares only about which strings must never appear in output,
// never which secret each one was.
func secretValues(secrets map[string]string) []string {
	values := make([]string, 0, len(secrets))
	for _, v := range secrets {
		values = append(values, v)
	}
	return values
}

var _ engine.CollectionInvoker = (*ipcCollectionExecutor)(nil).invoke
