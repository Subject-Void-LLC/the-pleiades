// Package serial implements internal/transport.Transport over a local
// serial line. It is the Adapter that lets the engine's serial_exec
// action reach an actual serial-attached device.
//
// It is deliberately thin, the same shape internal/transport/ssh
// already established: everything that is genuinely hard about talking
// to a local serial line (opening the port, translating line settings,
// deciding when a command's output has gone quiet) lives in
// pkg/serialexec, and this package delegates to it rather than keeping
// a second copy. The reason that split exists is the same constraint
// internal/transport/ssh's own doc comment states: a Collection method
// may import pkg/ and the standard library and nothing else in this
// module, enforced by internal/archtest, so no Collection can import
// this package no matter how much of the same work it needs.
package serial

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialexec"
)

// serialTransport is the Adapter behind transport.Transport. Construct
// one with New.
type serialTransport struct {
	opts serialexec.Options
}

// New returns a transport.Transport backed by a local serial line,
// configured by opts. A zero Options{} takes pkg/serialexec's own
// documented defaults.
func New(opts serialexec.Options) transport.Transport {
	return &serialTransport{opts: opts}
}

// Exec implements transport.Transport. See transport.Result's own doc
// comment for ExitStatusUnknown: a serial console has no exit status at
// all, so this is always true here, never inferred from a zero
// ExitCode.
//
// command runs VERBATIM, terminated by pkg/serialexec's own "\r\n": no
// local shell, and no concatenation with the target device's own
// identifier or anything else.
func (t *serialTransport) Exec(ctx context.Context, target transport.Target, cred credential.Credential, command string) (transport.Result, error) {
	// serial only ever speaks to a local serial line. Any other Endpoint
	// kind reaching this Adapter is a binding-configuration bug, not a
	// protocol-level failure, so it is reported as a clear type error.
	ep, ok := target.Endpoint.(transport.SerialEndpoint)
	if !ok {
		return transport.Result{}, fmt.Errorf("serial: target endpoint is %T, not a transport.SerialEndpoint", target.Endpoint)
	}

	result, err := serialexec.Exec(ctx, ep.Device, ep.Line, t.opts, command)
	if err != nil {
		return transport.Result{}, fmt.Errorf("serial: %w", err)
	}

	return transport.Result{
		Stdout:            result.Stdout,
		ExitStatusUnknown: result.ExitStatusUnknown,
	}, nil
}
