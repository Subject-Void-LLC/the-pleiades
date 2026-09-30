// Package winrm implements internal/transport.Transport and
// transport.ShellTransport over WinRM. It is the Adapter that lets the
// engine's winrm_exec action reach a Windows device.
//
// It is deliberately thin, the same shape internal/transport/ssh and
// internal/transport/telnet establish: the real work (the WS-Man
// exchange, the three execution modes, quoting and encoding) lives in
// pkg/winrmexec, which a Collection can also import, and this package
// translates between the transport port and that package rather than
// keeping a second copy of any of it.
//
// # What Exec means here
//
// Exec's promise on every Adapter is a command that reaches its program
// exactly as written, with no shell acting on it. Over WinRM that is
// ShellNone. The WinRM service always starts a command through cmd.exe,
// and cannot be told not to, so pkg/winrmexec escapes the line until that
// cmd.exe passes it through unchanged and the program it names parses its
// own arguments. ExecShell adds the two Windows shells for a task that
// asks for one by name.
//
// # Retries
//
// A failure is retried only when pkg/winrmexec reports it as a
// NotStartedError caused by the network: the connection was refused,
// reset or timed out before any command was sent. Nothing ran, so a
// retry cannot run anything twice. A rejected credential is not retried,
// because it will not change and repeating it can lock the account, and
// that includes a client certificate the host refuses with a TLS alert,
// which Go reports as a network error but is the host answering. Nothing
// that fails after the command started is ever retried, because a
// command is not known to be safe to repeat.
//
// # The circuit breaker
//
// The same network failures count toward a per-address circuit breaker,
// pkg/breaker, the one pkg/remoteexec uses for SSH: after five in a row
// against one address, calls to it fail fast for thirty seconds with no
// traffic, then one probe is let through. It is scoped to the failure
// domain PLAN.md names, the network, so only what the retry treats as a
// network failure opens it. A refused credential, a refused command or a
// failure after the command started proves the host answered, and a
// mistake in the task itself never reached the network at all; neither
// says the path to the device is down. One breaker belongs to one
// transport, so a composition root that builds one transport shares it
// across every task it runs.
//
// # Hop chains are refused, not ignored
//
// A Target with a Route (a bastion in front of the device) is refused
// with an error naming the missing support. Dialing the device directly
// instead would reach a different network path than the inventory
// describes, which is the kind of silent substitution this platform does
// not make.
package winrm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/retry"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// Retry policy for a failure before the command started: three attempts,
// backing off from half a second to four.
const (
	maxAttempts  = 3
	retryBase    = 500 * time.Millisecond
	retryCeiling = 4 * time.Second
)

// executeFunc is winrmexec.Execute's shape.
type executeFunc func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Command, winrmexec.Options) (winrmexec.Result, error)

// winrmTransport is the Adapter behind transport.ShellTransport.
// Construct one with New.
type winrmTransport struct {
	opts winrmexec.Options
	// breaker holds this transport's circuits, keyed by the address each
	// call dials. It is unexported on purpose: pkg/breaker is public, but
	// nothing outside this transport may open or close its circuits.
	breaker *breaker.Breaker
	// execute is winrmexec.Execute, a field so tests can observe what is
	// sent and script what comes back without a Windows host.
	execute executeFunc
}

// New returns a WinRM transport configured by opts. A zero Options is
// pkg/winrmexec's documented default: HTTP on 5985 with NTLM message
// encryption, a sixty second bound on each command. The interpreter
// paths and working directory in opts are overridden per command by a
// ShellRequest that names its own.
func New(opts winrmexec.Options) transport.ShellTransport {
	return newTransport(opts, winrmexec.Execute)
}

// newTransport builds the transport around execute with a fresh circuit
// breaker, so no caller, tests included, can build one without it.
func newTransport(opts winrmexec.Options, execute executeFunc) *winrmTransport {
	return &winrmTransport{
		opts:    opts,
		breaker: breaker.New(breaker.DefaultThreshold, breaker.DefaultCooldown),
		execute: execute,
	}
}

// Exec implements transport.Transport: command is a Windows command line,
// run with no shell.
func (t *winrmTransport) Exec(ctx context.Context, target transport.Target, cred credential.Credential, command string) (transport.Result, error) {
	return t.ExecShell(ctx, target, cred, transport.ShellRequest{Shell: transport.ShellNone, Script: command})
}

// ExecShell implements transport.ShellTransport.
func (t *winrmTransport) ExecShell(ctx context.Context, target transport.Target, cred credential.Credential, req transport.ShellRequest) (transport.Result, error) {
	ep, ok := target.Endpoint.(transport.NetworkEndpoint)
	if !ok {
		return transport.Result{}, fmt.Errorf("winrm: target endpoint is %T, not a transport.NetworkEndpoint", target.Endpoint)
	}
	if len(target.Route) > 0 {
		return transport.Result{}, fmt.Errorf("winrm: this device is configured behind %d hop(s), and WinRM through a hop chain is not supported yet; it will not dial the device directly instead", len(target.Route))
	}
	shell, err := toWinRMShell(req.Shell)
	if err != nil {
		return transport.Result{}, err
	}

	opts := winrmexec.WithDeviceTLS(t.opts, target.TLS)
	if req.WorkingDirectory != "" {
		opts.WorkingDirectory = req.WorkingDirectory
	}
	switch shell {
	case winrmexec.ShellCmd:
		if req.Interpreter != "" {
			opts.CmdPath = req.Interpreter
		}
	case winrmexec.ShellPowerShell:
		if req.Interpreter != "" {
			opts.PowerShellPath = req.Interpreter
		}
	}

	// Through the one shared credential vocabulary, the same path a
	// Collection's secrets take, so every stored form works here: a
	// password, a PEM certificate and key, or a PKCS#12 bundle unlocked
	// by its passphrase in memory. Mapping the fields by hand missed the
	// bundle, and a device whose credential was a PFX reached WinRM with
	// no credential at all.
	auth, err := winrmexec.AuthFromSecrets(credential.Flatten(cred))
	if err != nil {
		return transport.Result{}, fmt.Errorf("winrm: %w", err)
	}
	command := winrmexec.Command{Shell: shell, Script: req.Script, Env: req.Env}
	wTarget := winrmexec.Target{Host: ep.Host, Port: ep.Port}

	// The circuit is keyed by the address the call really dials, so a
	// device written with a bracketed IPv6 literal and one written bare
	// share a circuit, and certificate authentication's switch to HTTPS
	// is already applied.
	addr := winrmexec.Addr(wTarget, auth, opts)
	// Only a look here, never a claim: the probe belongs to the attempt
	// that dials (FAILURE_PATTERNS 146).
	if !t.breaker.Permitted(addr) {
		return transport.Result{}, circuitOpen(addr)
	}

	delay := func(attempt int) time.Duration { return retry.Backoff(retryBase, retryCeiling, attempt) }
	result, err := retry.Do(ctx, delay, maxAttempts, retryable, func(ctx context.Context) (winrmexec.Result, error) {
		// A done context first, so a canceled call never takes the
		// half-open probe (FAILURE_PATTERNS 398).
		if err := ctx.Err(); err != nil {
			return winrmexec.Result{}, err
		}
		if !t.breaker.Allow(addr) {
			return winrmexec.Result{}, circuitOpen(addr)
		}
		res, err := t.execute(ctx, wTarget, auth, command, opts)
		t.record(addr, err)
		return res, err
	})
	if err != nil {
		return transport.Result{}, err
	}
	return transport.Result{Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode}, nil
}

// toWinRMShell maps the port's Shell onto pkg/winrmexec's. The two enums
// are separate because pkg/ cannot import internal/ and the port must not
// depend on one Adapter's package.
func toWinRMShell(s transport.Shell) (winrmexec.Shell, error) {
	switch s {
	case transport.ShellNone:
		return winrmexec.ShellNone, nil
	case transport.ShellCmd:
		return winrmexec.ShellCmd, nil
	case transport.ShellPowerShell:
		return winrmexec.ShellPowerShell, nil
	default:
		return 0, fmt.Errorf("winrm: unknown shell %v", s)
	}
}

// circuitOpen is the refusal a call gets from an open circuit. It wraps
// breaker.ErrOpen, so it is never retried, and it says no traffic was
// sent.
func circuitOpen(addr string) error {
	return fmt.Errorf("winrm: %w for %s, too many recent failures; nothing was sent", breaker.ErrOpen, addr)
}

// record tells the breaker what one attempt proved about the path to
// addr, and nothing it did not prove.
//
// A network failure before the shell opened is a failure. A command that
// finished, or a refusal the service sent back before running anything
// (a credential it rejected, a fault instead of a shell), proves the
// host answered, which is what a probe needs to close the circuit. Every
// other error is left unrecorded, because it is either a mistake in the
// task that never reached the network or a failure after the command
// started, and neither is certain evidence about the network either
// way. A probe that ends that way is not lost: pkg/breaker hands out a
// fresh one after a cooldown.
func (t *winrmTransport) record(addr string, err error) {
	var notStarted *winrmexec.NotStartedError
	switch {
	case err == nil:
		t.breaker.RecordSuccess(addr)
	case networkNotStarted(err):
		t.breaker.RecordFailure(addr)
	case errors.As(err, &notStarted):
		t.breaker.RecordSuccess(addr)
	}
}

// retryable reports whether err is a network failure before the command
// was sent. See the package doc for why nothing else is retried.
func retryable(err error) bool {
	return networkNotStarted(err)
}

// networkNotStarted reports whether err is a failure of the network
// itself before the command was sent: the one kind of failure that is
// safe to retry and that counts toward the circuit breaker.
//
// It is a connection that failed or timed out, or the operation's
// deadline passing before a shell opened.
//
// A TLS alert is excluded even though Go reports it as a *net.OpError,
// with Op "remote error": the host sent it, so the host is reachable and
// is refusing something, most often the client certificate. Retrying it
// would present the same certificate again, and counting it would let a
// wrong credential open a circuit that every other task to that address
// then shares.
func networkNotStarted(err error) bool {
	var notStarted *winrmexec.NotStartedError
	if !errors.As(err, &notStarted) {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op != "remote error"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}
	// The operation's own deadline passed before a shell opened: the host
	// never answered, which is what a host silently dropping packets
	// looks like when the bound is shorter than the connect timeout. A
	// caller's cancellation (context.Canceled) is not the network and
	// does not count.
	return errors.Is(err, context.DeadlineExceeded)
}
