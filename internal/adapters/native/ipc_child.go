package native

import (
	"context"
	"io"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// InternalCollectionRunnerArg is the hidden re-exec flag cmd/runner/main.go
// checks as its very first statement, before any flag parsing, NATS
// connection, or telemetry setup, so a spawned child pays for none of it.
// It is a plain positional argument, not a flag.Parse-recognized flag,
// deliberately: nothing about this codepath should be discoverable or
// runnable via the ordinary `pleiades-runner --help` surface.
const InternalCollectionRunnerArg = "--internal-collection-runner"

// RunCollectionChild is the entire body of a spawned collection-runner
// child process: read one wire.ChildRequest from stdin, run the named
// Collection method in the requested mode, and write one
// wire.ChildResponse to external.ResponseFD. It returns the process exit
// code cmd/runner/main.go should pass to os.Exit; RunCollectionChild
// itself never calls os.Exit, so it stays directly testable.
func RunCollectionChild(ctx context.Context) int {
	return runCollectionChild(ctx, os.Stdin, os.NewFile(uintptr(external.ResponseFD), "collection-response"), os.Stderr)
}

// runCollectionChild is RunCollectionChild with its three streams passed
// in rather than reached for, so every branch is reachable from an
// ordinary test holding buffers.
//
// The body is external.ServeChild over the process-wide registry, the
// same function every external Collection runs over its own methods. The
// child side of the boundary has exactly one implementation, which is
// what lets a Collection result be indistinguishable to the engine
// whichever kind of child produced it.
func runCollectionChild(ctx context.Context, in io.Reader, response io.Writer, errOut io.Writer) int {
	return external.ServeChildWith(ctx, collection.Lookup, in, response, errOut, dispatchedDevice)
}
