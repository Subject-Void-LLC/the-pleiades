// Package serialtcp implements internal/transport.Transport over a raw
// TCP passthrough connection to a console or terminal server. It is the
// Adapter that lets the engine's serialtcp_exec action reach an actual
// console-server-attached device.
//
// It is deliberately thin, the same shape internal/transport/ssh and
// internal/transport/serial already establish: the real work (dialing,
// deciding when output has gone quiet) lives in pkg/serialtcp, and this
// package delegates to it rather than keeping a second copy, for the
// same pkg/-may-import-nothing-else-in-this-module constraint those two
// packages' own doc comments state.
//
// # This transport is gated behind a loud opt-in, not reachable by accident
//
// Raw TCP passthrough moves bytes with ZERO framing, ZERO
// authentication, and ZERO encryption at the protocol level (see
// pkg/serialtcp's own doc comment for the full reasoning). This package
// cannot fix that and does not try to; the binding that reaches it
// (engine.TransportBinding.RequireOptInParam) is what refuses to
// dispatch here without an explicit, named runbook parameter, the same
// shape sdk.ParamInsecureSkipHostKeyVerify already established for SSH's
// own escape hatch.
//
// # A configured Route is honored, not silently ignored
//
// A console server on a management network, reachable only through one
// or more SSH bastions, is exactly Phase 73's own bastion-proof
// scenario. When target.Route is non-empty, Exec tunnels through it via
// pkg/remoteexec.Runner.DialThroughHops (internal/transport/hopchain
// translates Route into the hop chain that call needs) and speaks
// pkg/serialtcp's own protocol over the resulting net.Conn
// (pkg/serialtcp.ExecOverConn) instead of dialing net.Host:net.Port
// directly. An empty Route behaves exactly as it always has: a direct
// dial, costing nothing extra.
package serialtcp

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport/hopchain"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	realserialtcp "github.com/Subject-Void-LLC/the-pleiades/pkg/serialtcp"
)

// rawPassthroughTransport is the Adapter behind transport.Transport.
// Construct one with New.
type rawPassthroughTransport struct {
	opts realserialtcp.Options

	// runner owns the dial policy and this transport's own circuit
	// breaker for hop dialing. It is deliberately built with
	// remoteexec.New rather than remoteexec.Shared, mirroring
	// internal/transport/ssh.New's own stated reasoning: a composition
	// root builds exactly one Transport and holds it for the life of the
	// process, so it already has a natural place to keep breaker state,
	// and sharing it with ssh_exec's own unrelated Runner would make one
	// subsystem's failures another's fast-fail.
	runner *remoteexec.Runner
}

// New returns a transport.Transport backed by a raw TCP passthrough
// connection, configured by opts. A zero Options{} takes pkg/serialtcp's
// own documented defaults. hopOpts configures the SSH hop chain used
// only when a dispatched device's Target carries a Route; a zero
// hopOpts{} takes pkg/remoteexec's own documented defaults, and costs
// nothing at all for a device with no Route.
func New(opts realserialtcp.Options, hopOpts remoteexec.Options) transport.Transport {
	return &rawPassthroughTransport{opts: opts, runner: remoteexec.New(hopOpts)}
}

// Exec implements transport.Transport. See transport.Result's own doc
// comment for ExitStatusUnknown: a raw byte pipe has no exit status at
// all, so this is always true here, never inferred from a zero
// ExitCode.
//
// command runs VERBATIM, terminated by pkg/serialtcp's own "\r\n": no
// local shell, and no concatenation with the target device's own
// identifier or anything else.
func (t *rawPassthroughTransport) Exec(ctx context.Context, target transport.Target, cred credential.Credential, command string) (transport.Result, error) {
	// Raw passthrough only ever speaks to a network host:port pair --
	// the console server's own address, exactly like SSH's own target
	// shape, since what changes between the two is the protocol spoken
	// once connected, not what is dialed. Any other Endpoint kind
	// reaching this Adapter is a binding-configuration bug, so it is
	// reported as a clear type error. Named ep, not net, so it is never
	// confused with the net package this file also imports.
	ep, ok := target.Endpoint.(transport.NetworkEndpoint)
	if !ok {
		return transport.Result{}, fmt.Errorf("serialtcp: target endpoint is %T, not a transport.NetworkEndpoint", target.Endpoint)
	}

	var result realserialtcp.Result
	var err error
	if len(target.Route) == 0 {
		result, err = realserialtcp.Exec(ctx, ep.Host, ep.Port, t.opts, command)
	} else {
		hops, hopErr := hopchain.Convert(target.Route)
		if hopErr != nil {
			return transport.Result{}, fmt.Errorf("serialtcp: %w", hopErr)
		}
		conn, dialErr := t.runner.DialThroughHops(ctx, hops, remoteexec.Target{Host: ep.Host, Port: ep.Port})
		if dialErr != nil {
			return transport.Result{}, fmt.Errorf("serialtcp: %w", dialErr)
		}
		defer func() { _ = conn.Close() }()
		result, err = realserialtcp.ExecOverConn(ctx, conn, t.opts, command)
	}
	if err != nil {
		return transport.Result{}, fmt.Errorf("serialtcp: %w", err)
	}

	return transport.Result{
		Stdout:            result.Stdout,
		ExitStatusUnknown: result.ExitStatusUnknown,
	}, nil
}
