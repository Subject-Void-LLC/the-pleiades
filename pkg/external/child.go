// Package external: the child side of one call: decoding the request, running
// the method, writing the response.
package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ResponseFD is the file descriptor a child writes its one
// wire.ChildResponse to. It is never stdout: an arbitrary fmt.Println
// inside a Collection method's own code must not be able to corrupt this
// frame by interleaving with it on a shared stream. The parent passes the
// write end of a pipe as exec.Cmd.ExtraFiles[0], which Go documents as
// becoming file descriptor 3 in the child.
const ResponseFD = 3

// LookupFunc resolves a fully-qualified method name to its registered
// Descriptor. collection.Lookup is the one every real caller passes; it is
// a parameter so a test can hand in a table of its own.
type LookupFunc func(name string) (collection.Descriptor, bool)

// ServeChild is the whole body of a child process: read one
// wire.ChildRequest from in, run the named method in the requested mode,
// and write one wire.ChildResponse to response. It returns the exit code
// the process should end with, and never calls os.Exit itself, so every
// branch is reachable from an ordinary test holding buffers.
//
// It is the one implementation of the child side of the boundary. The
// Runner's own per-task child (internal/adapters/native) and every
// external Collection (Serve, below) both run it, which is what makes a
// method's result indistinguishable to the engine whichever of the two
// ran it.
//
// A method's own failure is not a failed process: it travels back as
// ChildResponse.Error and the exit code is 0. A non-zero exit means the
// exchange itself broke (an unreadable request, an unwritable response),
// which the parent reports differently.
func ServeChild(ctx context.Context, lookup LookupFunc, in io.Reader, response io.Writer, errOut io.Writer) int {
	return ServeChildWith(ctx, lookup, in, response, errOut, nil)
}

// DeviceBuilder builds the device a method runs against from what the
// dispatch said about it. The Runner's own child passes one that rebuilds
// the real device type; a nil builder means NewDevice, the address-only
// device every external program gets.
type DeviceBuilder func(wire.DispatchPayload) (inventory.InventoryItem, error)

// ServeChildWith is ServeChild with the device built by build.
func ServeChildWith(ctx context.Context, lookup LookupFunc, in io.Reader, response io.Writer, errOut io.Writer, build DeviceBuilder) int {
	req, err := ReadChildRequest(in)
	if err != nil {
		fmt.Fprintln(errOut, "collection child: failed to decode request:", err)
		return 1
	}

	resp := InvokeRequestFor(ctx, lookup, req, nil, build)

	if err := WriteChildResponse(response, resp); err != nil {
		fmt.Fprintln(errOut, "collection child: failed to write response:", err)
		return 1
	}
	return 0
}

// InvokeRequest runs req's named method in req's mode and builds the
// response to send back, isolated from any stream so it can be tested
// directly against hand-built requests.
//
// The mode is resolved through collection.ParseMode and
// Descriptor.MethodFor, the same two functions the engine uses in-process,
// so a check requested across the boundary reaches Check and nothing else.
// An unknown mode is refused before anything runs.
func InvokeRequest(ctx context.Context, lookup LookupFunc, req wire.ChildRequest) wire.ChildResponse {
	return InvokeRequestWithPool(ctx, lookup, req, nil)
}

// InvokeRequestWithPool is InvokeRequest for a child that serves many
// requests in one process, as the Runner's per-dispatch session child
// does: the method's RunbookContext lends SSH connections from pool, so
// the dispatch's tasks against its one device share a login. A nil pool
// is exactly InvokeRequest.
func InvokeRequestWithPool(ctx context.Context, lookup LookupFunc, req wire.ChildRequest, pool *remoteexec.Pool) wire.ChildResponse {
	return InvokeRequestFor(ctx, lookup, req, pool, nil)
}

// InvokeRequestFor is InvokeRequestWithPool with the device built by build
// (nil for NewDevice). A device that cannot be built fails the call with
// the reason, never a silently different device.
func InvokeRequestFor(ctx context.Context, lookup LookupFunc, req wire.ChildRequest, pool *remoteexec.Pool, build DeviceBuilder) wire.ChildResponse {
	desc, ok := lookup(req.FQCN)
	if !ok {
		return wire.ChildResponse{Error: fmt.Sprintf("collection method %q is not registered", req.FQCN)}
	}
	if desc.Manifest.Status != collection.StatusImplemented || desc.Invoke == nil {
		return wire.ChildResponse{Error: fmt.Sprintf("collection method %q is declared but not implemented", req.FQCN)}
	}

	mode, err := collection.ParseMode(req.Mode)
	if err != nil {
		return wire.ChildResponse{Error: fmt.Sprintf("collection method %q: %v", req.FQCN, err)}
	}
	method, err := desc.MethodFor(mode)
	if err != nil {
		return wire.ChildResponse{Error: err.Error()}
	}

	payload := wire.DispatchPayload{
		JobID:            req.JobID,
		DeviceID:         req.DeviceID,
		DeviceName:       req.DeviceName,
		DeviceHost:       req.DeviceHost,
		SSHPort:          req.SSHPort,
		Capabilities:     req.Capabilities,
		DeviceType:       req.DeviceType,
		DeviceProperties: req.DeviceProperties,
	}
	var device inventory.InventoryItem = NewDevice(payload)
	if build != nil {
		built, err := build(payload)
		if err != nil {
			return wire.ChildResponse{Error: fmt.Sprintf("collection method %q: device %q: %v", req.FQCN, req.DeviceName, err)}
		}
		device = built
	}

	rc := NewRunbookContext(req.Secrets)
	rc.pool = pool
	// PLAN.md Section 17.5: zero the secret memory this process holds the
	// instant after use, success or failure. Best-effort against this
	// type's own storage; see RunbookContext's own doc comment for why a
	// stronger guarantee needs a breaking sdk.RunbookContext change.
	defer rc.Zero()

	result, err := method(ctx, rc, device, req.Params)
	if err != nil {
		var cannot *collection.CannotCheckError
		if mode == collection.ModeCheck && errors.As(err, &cannot) {
			return wire.ChildResponse{Error: cannot.Reason, CannotCheck: true}
		}
		return wire.ChildResponse{Error: err.Error()}
	}
	return wire.ChildResponse{Changed: result.Changed, Facts: rc.Facts()}
}

// ReadChildRequest decodes exactly one wire.ChildRequest from r.
func ReadChildRequest(r io.Reader) (wire.ChildRequest, error) {
	var req wire.ChildRequest
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return wire.ChildRequest{}, fmt.Errorf("failed to decode child request: %w", err)
	}
	return req, nil
}

// WriteChildResponse encodes resp as a single newline-terminated JSON
// value to w. json.Encoder.Encode appends the trailing newline itself.
func WriteChildResponse(w io.Writer, resp wire.ChildResponse) error {
	return json.NewEncoder(w).Encode(&resp)
}
