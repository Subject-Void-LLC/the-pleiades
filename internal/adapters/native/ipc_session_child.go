// The child side of a dispatch's collection session: one process serving
// every Collection call of one dispatch, with an SSH connection pool that
// lives as long as it does.
package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// InternalCollectionSessionArg is the hidden re-exec argument for a
// session child: InternalCollectionRunnerArg's sibling for a dispatch
// whose connections persist. It is routed as early and as quietly as that
// one (cmd/runner's routeFor), for the same reasons.
const InternalCollectionSessionArg = "--internal-collection-session"

// RunCollectionSession is the whole body of a session child: serve
// requests from stdin until it closes, answering each on
// external.ResponseFD, then close every pooled connection and return the
// exit code cmd/runner/main.go passes to os.Exit.
func RunCollectionSession(ctx context.Context) int {
	return runCollectionSession(ctx, os.Stdin, os.NewFile(uintptr(external.ResponseFD), "collection-response"), os.Stderr)
}

// RunChildFor runs the child process args select, returning its exit code
// and true, or false when args select none. A test binary that can play
// the Runner's child calls it first thing in TestMain, so it cannot serve
// one kind of child and re-run its own whole suite as the other.
func RunChildFor(ctx context.Context, args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case InternalCollectionRunnerArg:
		return RunCollectionChild(ctx), true
	case InternalCollectionSessionArg:
		return RunCollectionSession(ctx), true
	}
	return 0, false
}

// runCollectionSession is RunCollectionSession with its streams passed in.
//
// Each request is answered exactly as a one-shot child answers it
// (external.InvokeRequestWithPool is InvokeRequest with a pool), in
// order, one at a time: the parent sends the next request only after
// reading the last answer. The end of input is the ordinary end of the
// dispatch; anything else that stops the loop is a broken exchange, a
// non-zero exit the parent reports.
func runCollectionSession(ctx context.Context, in io.Reader, response io.Writer, errOut io.Writer) int {
	pool := remoteexec.NewPool(remoteexec.DefaultPoolIdle)
	defer func() { _ = pool.Close() }() // the session is over; there is no one left to report a close to
	dec := json.NewDecoder(in)
	for {
		var req wire.ChildRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			fmt.Fprintln(errOut, "collection session: failed to decode request:", err)
			return 1
		}
		resp := external.InvokeRequestWithPool(ctx, collection.Lookup, req, pool)
		// The request's own copy of the secrets is dropped once answered;
		// the method's context already zeroed its copy.
		clear(req.Secrets)
		if err := external.WriteChildResponse(response, resp); err != nil {
			fmt.Fprintln(errOut, "collection session: failed to write response:", err)
			return 1
		}
	}
}
