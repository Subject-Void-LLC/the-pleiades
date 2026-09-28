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
// nothing that fails after the command started is ever retried, because
// a command is not known to be safe to repeat.
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

// winrmTransport is the Adapter behind transport.ShellTransport.
// Construct one with New.
type winrmTransport struct {
	opts winrmexec.Options
	// execute is winrmexec.Execute, a field so tests can observe what is
	// sent and script what comes back without a Windows host.
	execute func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Command, winrmexec.Options) (winrmexec.Result, error)
}

// New returns a WinRM transport configured by opts. A zero Options is
// pkg/winrmexec's documented default: HTTP on 5985 with NTLM message
// encryption, a sixty second bound on each command. The interpreter
// paths and working directory in opts are overridden per command by a
// ShellRequest that names its own.
func New(opts winrmexec.Options) transport.ShellTransport {
	return &winrmTransport{opts: opts, execute: winrmexec.Execute}
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

	delay := func(attempt int) time.Duration { return retry.Backoff(retryBase, retryCeiling, attempt) }
	result, err := retry.Do(ctx, delay, maxAttempts, retryable, func(ctx context.Context) (winrmexec.Result, error) {
		return t.execute(ctx, wTarget, auth, command, opts)
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

// retryable reports whether err is a network failure before the command
// was sent. See the package doc for why nothing else is retried.
func retryable(err error) bool {
	var notStarted *winrmexec.NotStartedError
	if !errors.As(err, &notStarted) {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr) && urlErr.Timeout()
}
