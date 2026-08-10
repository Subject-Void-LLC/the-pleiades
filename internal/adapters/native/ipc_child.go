package native

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// InternalCollectionRunnerArg is the hidden re-exec flag cmd/runner/main.go
// checks as its very first statement, before any flag parsing, NATS
// connection, or telemetry setup, so a spawned child pays for none of it.
// It is a plain positional argument, not a flag.Parse-recognized flag,
// deliberately: nothing about this codepath should be discoverable or
// runnable via the ordinary `pleiades-runner --help` surface.
const InternalCollectionRunnerArg = "--internal-collection-runner"

// responseFD is the file descriptor a child process's own ChildResponse is
// written to. It is never stdout: an arbitrary fmt.Println inside a
// Collection method's own code must not be able to corrupt this frame by
// interleaving with it on a shared stream. exec.Cmd.ExtraFiles documents
// that entry i becomes file descriptor 3+i in the child, so the single
// entry ipcCollectionExecutor.invoke sets becomes exactly this fd.
const responseFD = 3

// RunCollectionChild is the entire body of a spawned collection-runner
// child process: read one wire.ChildRequest from stdin, invoke the named
// Collection method, and write one wire.ChildResponse to responseFD. It
// returns the process exit code cmd/runner/main.go should pass to
// os.Exit; RunCollectionChild itself never calls os.Exit, so it stays
// directly testable.
func RunCollectionChild(ctx context.Context) int {
	return runCollectionChild(ctx, os.Stdin, os.NewFile(uintptr(responseFD), "collection-response"), os.Stderr)
}

// runCollectionChild is RunCollectionChild's whole body with its three
// real streams passed in rather than reached for, so every branch is
// reachable from an ordinary test holding buffers. RunCollectionChild
// itself is then the thin wrapper that names os.Stdin, fd 3, and
// os.Stderr, and holds no logic of its own to test. This mirrors
// readChildRequest and writeChildResponse below, which already take an
// io.Reader and an io.Writer for the identical reason.
func runCollectionChild(ctx context.Context, in io.Reader, response io.Writer, errOut io.Writer) int {
	req, err := readChildRequest(in)
	if err != nil {
		fmt.Fprintln(errOut, "collection child: failed to decode request:", err)
		return 1
	}

	resp := invokeChild(ctx, req)

	if err := writeChildResponse(response, resp); err != nil {
		fmt.Fprintln(errOut, "collection child: failed to write response:", err)
		return 1
	}
	return 0
}

// invokeChild runs req's named Collection method and builds the
// ChildResponse to report back, isolated from stdin/fd-3 I/O so it can be
// tested directly against hand-built requests (this package's own
// build-sequence plan: prove the IPC protocol against a fake child before
// wiring the real os/exec spawn).
func invokeChild(ctx context.Context, req wire.ChildRequest) wire.ChildResponse {
	desc, ok := collection.Lookup(req.FQCN)
	if !ok {
		return wire.ChildResponse{Error: fmt.Sprintf("collection method %q is not registered", req.FQCN)}
	}
	if desc.Manifest.Status != collection.StatusImplemented || desc.Invoke == nil {
		return wire.ChildResponse{Error: fmt.Sprintf("collection method %q is declared but not implemented", req.FQCN)}
	}

	device := newWireDevice(wire.DispatchPayload{
		JobID:        req.JobID,
		DeviceID:     req.DeviceID,
		DeviceName:   req.DeviceName,
		DeviceHost:   req.DeviceHost,
		SSHPort:      req.SSHPort,
		Capabilities: req.Capabilities,
	})

	rc := newChildRunbookContext(req.Secrets)
	// PLAN.md Section 17.5: zero the secret memory this process holds the
	// instant after use, success or failure. Best-effort against this
	// type's own storage; see childRunbookContext's own doc comment for
	// why a stronger guarantee needs a breaking sdk.RunbookContext change.
	defer rc.zero()

	result, err := desc.Invoke(ctx, rc, device, req.Params)
	if err != nil {
		return wire.ChildResponse{Error: err.Error()}
	}
	return wire.ChildResponse{Changed: result.Changed, Facts: rc.Facts()}
}

// readChildRequest decodes exactly one wire.ChildRequest from r (r is
// os.Stdin in production; a bytes.Reader in tests).
func readChildRequest(r io.Reader) (wire.ChildRequest, error) {
	var req wire.ChildRequest
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return wire.ChildRequest{}, fmt.Errorf("failed to decode child request: %w", err)
	}
	return req, nil
}

// writeChildResponse encodes resp as a single newline-terminated JSON
// value to w (the fd-3 pipe in production; a bytes.Buffer in tests).
// json.Encoder.Encode appends the trailing newline itself.
func writeChildResponse(w io.Writer, resp wire.ChildResponse) error {
	return json.NewEncoder(w).Encode(&resp)
}

// childRunbookContext is the per-invocation sdk.RunbookContext the IPC
// child hands to a Collection method's Invoke call. Secrets are held as
// mutable []byte internally, never string (which Go cannot zero once
// created: every string value is an immutable, potentially-shared,
// garbage-collector-managed byte sequence), so zero can overwrite them
// the instant the invocation returns. This is a best-effort measure
// against the storage this type itself controls: it cannot reach a copy
// the Collection method's own code retained past its Invoke call (Go's
// own string immutability means InjectSecrets's own map[string]string
// return value is already a copy the caller could keep indefinitely). A
// stronger guarantee would need a breaking sdk.RunbookContext change (a
// Secret type wrapping []byte with an explicit Zero() method), out of
// scope for this phase; see this phase's own plan for that tradeoff.
//
// SetStat and EmitFact both write into the same facts map, mirroring
// internal/engine/runbook_context.go's own established convention for the
// identical reason: nothing downstream of this package distinguishes a
// "stat" from a "fact" today.
type childRunbookContext struct {
	mu      sync.Mutex
	secrets map[string][]byte
	facts   map[string]interface{}
}

var _ sdk.RunbookContext = (*childRunbookContext)(nil)

// newChildRunbookContext builds a childRunbookContext over secrets, which
// it copies into its own mutable storage rather than referencing the
// caller's map, so zero never risks mutating memory the parent's own
// wire.ChildRequest decode still owns.
func newChildRunbookContext(secrets map[string]string) *childRunbookContext {
	stored := make(map[string][]byte, len(secrets))
	for k, v := range secrets {
		stored[k] = []byte(v)
	}
	return &childRunbookContext{secrets: stored, facts: map[string]interface{}{}}
}

// InjectSecrets implements sdk.RunbookContext.
func (c *childRunbookContext) InjectSecrets() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.secrets))
	for k, v := range c.secrets {
		out[k] = string(v)
	}
	return out
}

// SetStat implements sdk.RunbookContext.
func (c *childRunbookContext) SetStat(key string, value interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.facts[key] = value
	return nil
}

// EmitFact implements sdk.RunbookContext.
func (c *childRunbookContext) EmitFact(key string, value interface{}) error {
	return c.SetStat(key, value)
}

// Facts implements engine.FactCollector, returning a snapshot copy of
// every SetStat/EmitFact call this invocation made.
func (c *childRunbookContext) Facts() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]interface{}, len(c.facts))
	for k, v := range c.facts {
		out[k] = v
	}
	return out
}

// zero overwrites every stored secret's backing bytes with 0 and drops
// this context's own reference to them. Call it via defer immediately
// after the Collection method's Invoke call returns, on every path
// (success or failure).
func (c *childRunbookContext) zero() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.secrets {
		for i := range v {
			v[i] = 0
		}
		delete(c.secrets, k)
	}
}
