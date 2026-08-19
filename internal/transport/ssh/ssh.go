// Package ssh implements internal/transport.Transport over real SSH
// connections. It is the Adapter that lets the engine's ssh_exec action
// reach an actual device.
//
// It is deliberately thin. Everything that is genuinely hard about
// talking SSH to a device (retry with backoff, a per-target circuit
// breaker, fail-closed known_hosts verification, turning a secret into
// exactly one authentication method) lives in pkg/remoteexec, and this
// package delegates to it rather than keeping a second copy.
//
// The reason that split exists is a constraint this package cannot
// satisfy from where it sits. A Collection method may import pkg/ and
// the standard library and nothing else in this module, enforced by
// internal/archtest, so no Collection can import this package no matter
// how much of the same work it needs. Before pkg/remoteexec, the one
// SSH-backed Collection method hand-rolled its own dial and its own host
// key check as a result. Two implementations of host key verification is
// one implementation and one liability, so the mechanism moved to where
// both callers can reach it and this package became the Adapter that
// translates for the internal side: transport.Target into
// remoteexec.Target, credential.Credential into remoteexec.Auth,
// remoteexec.Result into transport.Result.
//
// The contract this package presents is unchanged by that move, and
// ssh_container_test.go against a real, independent sshd is what proves
// it.
package ssh

import (
	"fmt"

	"context"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Options configures a Transport backed by real SSH connections.
//
// It is an alias for remoteexec.Options rather than a struct of its own
// with the same six fields, because the two would have to be kept
// identical by hand and the only thing this package could add is a
// second place for a default to drift. Every field is documented on
// remoteexec.Options; a zero Options{} is usable and conservative.
type Options = remoteexec.Options

// sshTransport is the Adapter behind transport.Transport. Construct one
// with New; the zero value is not usable.
type sshTransport struct {
	// runner owns the dial policy and this transport's own circuit
	// breaker. It is deliberately built with remoteexec.New rather than
	// remoteexec.Shared: a composition root builds exactly one Transport
	// and holds it for the life of the process, so it already has a
	// natural place to keep breaker state, and sharing it with unrelated
	// callers would make one subsystem's failures another's fast-fail.
	runner *remoteexec.Runner
}

// New returns a transport.Transport backed by real SSH connections,
// configured by opts. Any zero-valued field in opts takes its documented
// default from pkg/remoteexec.
func New(opts Options) transport.Transport {
	return &sshTransport{runner: remoteexec.New(opts)}
}

// Exec implements transport.Transport. See transport.Transport's own doc
// comment for the load-bearing distinction between a non-zero
// Result.ExitCode (not a Go error: the command ran and reported failure)
// and a non-nil error (the outcome could not be determined at all); this
// method honors that contract exactly, because pkg/remoteexec draws the
// same line for the same reason.
//
// Retry is scoped strictly to the dial phase. Once the command has been
// sent to the remote side it is never retried, since it may already have
// partially run and re-sending it could apply an unknown side effect
// twice. command runs VERBATIM: no local shell, and no concatenation
// with target.Host or anything else.
func (t *sshTransport) Exec(ctx context.Context, target transport.Target, cred credential.Credential, command string) (transport.Result, error) {
	// A credential that cannot produce a usable authentication method is
	// a hard error before any network I/O. This never proceeds with an
	// empty Auth, which would be an unauthenticated login attempt against
	// a device whose credential simply was not found.
	auth, err := remoteexec.AuthFrom(cred.Username, cred.Password, cred.PrivateKeyPEM, cred.Passphrase)
	if err != nil {
		return transport.Result{}, fmt.Errorf("ssh: %w", err)
	}

	result, err := t.runner.Run(ctx, remoteexec.Target{Host: target.Host, Port: target.Port}, auth, command)
	if err != nil {
		return transport.Result{}, fmt.Errorf("ssh: %w", err)
	}

	return transport.Result{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}, nil
}
