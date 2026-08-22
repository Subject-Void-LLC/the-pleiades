// Package telnet implements internal/transport.Transport over a bare
// interactive Telnet session. It is the Adapter that lets the engine's
// telnet_exec action reach an actual Telnet-only device.
//
// It is deliberately thin, the same shape internal/transport/ssh,
// internal/transport/serial, and internal/transport/serialtcp already
// establish: the real work (dialing, IAC negotiation, deciding when
// output has gone quiet) lives in pkg/telnetexec, and this package
// delegates to it rather than keeping a second copy, for the same
// pkg/-may-import-nothing-else-in-this-module constraint those packages'
// own doc comments state.
//
// # This transport is gated behind a loud opt-in, not reachable by accident
//
// Telnet sends everything, credentials included, in cleartext with no
// encryption at any layer (see pkg/telnetexec's own doc comment for the
// full reasoning). This package cannot fix that and does not try to; the
// binding that reaches it (engine.TransportBinding.RequireOptInParam) is
// what refuses to dispatch here without an explicit, named runbook
// parameter, the same shape pkg/serialtcp's own raw-passthrough binding
// already established.
//
// # A configured Route is honored, not silently ignored
//
// A Telnet-only device on a management network, reachable only through
// one or more SSH bastions, is exactly Phase 73's own bastion-proof
// scenario. When target.Route is non-empty, Exec tunnels through it via
// pkg/remoteexec.Runner.DialThroughHops (internal/transport/hopchain
// translates Route into the hop chain that call needs) and speaks
// pkg/telnetexec's own protocol over the resulting net.Conn
// (pkg/telnetexec.ExecOverConn) instead of dialing net.Host:net.Port
// directly. An empty Route behaves exactly as it always has: a direct
// dial, costing nothing extra.
package telnet

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport/hopchain"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	realtelnetexec "github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
)

// telnetTransport is the Adapter behind transport.Transport. Construct
// one with New.
type telnetTransport struct {
	opts realtelnetexec.Options

	// runner owns the dial policy and this transport's own circuit
	// breaker for hop dialing. Deliberately built with remoteexec.New
	// rather than remoteexec.Shared: see
	// internal/transport/serialtcp.rawPassthroughTransport's own runner
	// field for the identical, fuller reasoning.
	runner *remoteexec.Runner
}

// New returns a transport.Transport backed by a bare Telnet session,
// configured by opts. A zero Options{} takes pkg/telnetexec's own
// documented defaults. hopOpts configures the SSH hop chain used only
// when a dispatched device's Target carries a Route; a zero hopOpts{}
// takes pkg/remoteexec's own documented defaults, and costs nothing at
// all for a device with no Route.
func New(opts realtelnetexec.Options, hopOpts remoteexec.Options) transport.Transport {
	return &telnetTransport{opts: opts, runner: remoteexec.New(hopOpts)}
}

// Exec implements transport.Transport. See transport.Result's own doc
// comment for ExitStatusUnknown: a bare Telnet session has no exit
// status at all, so this is always true here, never inferred from a
// zero ExitCode.
//
// command runs VERBATIM, terminated by pkg/telnetexec's own "\r\n": no
// local shell, and no concatenation with the target device's own
// identifier or anything else.
func (t *telnetTransport) Exec(ctx context.Context, target transport.Target, cred credential.Credential, command string) (transport.Result, error) {
	// Bare Telnet only ever speaks to a network host:port pair -- the
	// device's own address, exactly like SSH's and raw passthrough's own
	// target shape, since what changes between the three is the
	// protocol spoken once connected, not what is dialed. Any other
	// Endpoint kind reaching this Adapter is a binding-configuration bug,
	// so it is reported as a clear type error. Named ep, not net, so it
	// is never confused with the net package this file also imports.
	ep, ok := target.Endpoint.(transport.NetworkEndpoint)
	if !ok {
		return transport.Result{}, fmt.Errorf("telnet: target endpoint is %T, not a transport.NetworkEndpoint", target.Endpoint)
	}

	var result realtelnetexec.Result
	var err error
	if len(target.Route) == 0 {
		result, err = realtelnetexec.Exec(ctx, ep.Host, ep.Port, t.opts, command)
	} else {
		hops, hopErr := hopchain.Convert(target.Route)
		if hopErr != nil {
			return transport.Result{}, fmt.Errorf("telnet: %w", hopErr)
		}
		conn, dialErr := t.runner.DialThroughHops(ctx, hops, remoteexec.Target{Host: ep.Host, Port: ep.Port})
		if dialErr != nil {
			return transport.Result{}, fmt.Errorf("telnet: %w", dialErr)
		}
		defer func() { _ = conn.Close() }()
		result, err = realtelnetexec.ExecOverConn(ctx, conn, t.opts, command)
	}
	if err != nil {
		return transport.Result{}, fmt.Errorf("telnet: %w", err)
	}

	return transport.Result{
		Stdout:            result.Stdout,
		ExitStatusUnknown: result.ExitStatusUnknown,
	}, nil
}
