// Package winrmexec runs a script on a Windows device over WinRM, and is
// the single place this platform does that.
//
// It exists for the reason pkg/remoteexec and pkg/remotefile exist. A
// Collection may import pkg/ and nothing else in this module, which
// internal/archtest enforces, so a Collection method reaching a Windows
// host cannot use an adapter living under internal/ no matter how much
// of the same work it needs.
//
// # What works, what does not, and why that is not a matter of taste
//
// A Windows target can run a command three ways, and this package
// supports two of them:
//
//   - ShellPowerShell works. The script is UTF-16LE encoded and base64ed
//     into "powershell.exe -EncodedCommand <base64>".
//   - ShellCmd works. The script goes to cmd.exe, which is the only way
//     to reach a cmd builtin (dir, set, for, %ERRORLEVEL%).
//   - ShellNone, meaning direct execution of a program with an argument
//     vector nothing parses, is REFUSED rather than approximated.
//
// The refusal is the honest option rather than a missing feature.
// [MS-WSMV] 3.1.4.11 defines a WS-Man option, WINRS_SKIP_CMD_SHELL, that
// decides whether the service runs the command directly or hands it to
// cmd.exe, and it defaults to FALSE. The library below hardcodes it to
// "FALSE" while building the Command message inline (request.go:76-77)
// and exposes no seam to change it: NewExecuteCommandRequest is
// exported, but Client.sendRequest, Shell.client, Shell.id and
// newCommand are not, so a caller can build a corrected request and has
// no way to post it. Claiming a command runs verbatim while it is
// actually parsed by cmd.exe would be a lie with security consequences,
// so this package says no instead. Getting ShellNone requires an
// upstream pull request or a fork.
//
// # Why the two supported modes are safe under a cmd.exe nobody asked for
//
// Because the option is stuck at FALSE, every command this package sends
// is parsed by cmd.exe on the far side. For ShellCmd that is precisely
// what the caller asked for. For ShellPowerShell it is harmless, and the
// reason is worth stating rather than assuming: the command line is
// "powershell.exe -EncodedCommand" followed by base64, whose alphabet is
// A-Z, a-z, 0-9, "+", "/" and "=". Not one cmd.exe metacharacter appears
// in that set, so no script content, however hostile, can reach cmd.exe
// as syntax. The encoding that exists to carry a script through a
// command line safely is also what makes this defect unreachable there.
//
// The library's second defect needs a real defense. It wraps the command
// in CDATA by concatenation, `"<![CDATA[" + command + "]]>"`
// (request.go:83), with no escaping of a literal "]]>" in the content. A
// command containing that sequence closes the CDATA section early and
// injects raw XML into the SOAP body, which is a live injection vector
// with nothing to do with any shell's metacharacters. Run rejects it on
// the ShellCmd path, where a caller's bytes reach the SOAP body raw. The
// PowerShell path needs no such check for the same reason as above:
// neither "]" nor ">" is in the base64 alphabet.
package winrmexec

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/masterzen/winrm"
)

// Default ports, from [MS-WSMV] and what Enable-PSRemoting configures.
const (
	// DefaultPort is the WinRM HTTP listener.
	DefaultPort = 5985
	// DefaultPortHTTPS is the WinRM HTTPS listener.
	DefaultPortHTTPS = 5986
)

// DefaultTimeout bounds one WinRM operation when Options.Timeout is
// zero.
//
// Sixty seconds is short, deliberately, and it is worth saying why
// rather than treating it as an obvious number. This value used to be
// inert on the encrypted path (see Run), so in practice a script could
// run forever and a dead transport hung until the operating system gave
// up, measured at just under three minutes. Now that it is enforced,
// the choice is between a default that lets a long script finish and a
// default that fails fast when a connection is never coming back.
//
// Fast is the better default here. Most WinRM work is short: read a
// fact, set a value, check a service. The long cases are specific and
// known to whoever wrote them, an installer or an update run, and they
// can say so with the task's own timeout. The reverse arrangement asks
// every ordinary task to wait out a pathological one.
const DefaultTimeout = 60 * time.Second

// cdataTerminator is the sequence that ends a CDATA section, and the one
// the library below fails to escape.
const cdataTerminator = "]]>"

// Shell names which interpreter runs a script on the far side.
//
// This is an enum rather than a boolean because "no shell" is a third
// destination, not the absence of a setting, and the two Windows shells
// are not interchangeable: their metacharacter sets are disjoint (^
// against `, %VAR% against $(...), ; as an argument delimiter against ;
// as a statement separator), so a caller holding only a path string
// would have to recover which parser is on the far end by string
// matching. Naming the mode makes the answer data.
type Shell int

const (
	// ShellNone runs the program directly, with an argument vector no
	// interpreter parses. Not available over WinRM; see the package doc.
	ShellNone Shell = iota
	// ShellCmd runs the script through cmd.exe, the only way to reach a
	// cmd builtin.
	ShellCmd
	// ShellPowerShell runs the script through PowerShell.
	ShellPowerShell
)

// shellNames is the one table mapping a Shell to the token a runbook
// writes, so String and ParseShell cannot disagree and adding a mode is
// one line rather than two that can drift.
var shellNames = map[Shell]string{
	ShellNone:       "none",
	ShellCmd:        "cmd",
	ShellPowerShell: "powershell",
}

// String returns the runbook token for s.
func (s Shell) String() string {
	if name, ok := shellNames[s]; ok {
		return name
	}
	return fmt.Sprintf("Shell(%d)", int(s))
}

// ParseShell turns a runbook's shell token into a Shell. An empty string
// is ShellNone, so omitting the parameter and writing "none" mean the
// same thing rather than one being an error.
//
// The error lists every valid value, because the author reading it chose
// a word this platform does not know and the useful reply is the set it
// does know.
func ParseShell(name string) (Shell, error) {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == "" {
		return ShellNone, nil
	}
	for shell, token := range shellNames {
		if token == trimmed {
			return shell, nil
		}
	}
	valid := []string{shellNames[ShellNone], shellNames[ShellCmd], shellNames[ShellPowerShell]}
	return ShellNone, fmt.Errorf("unknown shell %q (valid values: %s)", name, strings.Join(valid, ", "))
}

// Target identifies where to connect.
type Target struct {
	// Host is the address or hostname. A literal IPv6 address may be
	// written either bare ("2001:db8::1") or bracketed
	// ("[2001:db8::1]"); both reach the same device.
	//
	// Accepting both is deliberate rather than lax. The underlying
	// client brackets the host itself when it builds the WS-Man URL, so
	// a bracketed value used to produce "http://[[2001:db8::1]]:5985/
	// wsman" and fail with "invalid IP-literal", while this field's own
	// documentation said bracketing was required. An operator following
	// the documentation got an unreachable device, and the example
	// inventory that documents the IPv6 rescue path shipped in exactly
	// that broken form. The whole value of that path is that it works
	// when IPv4 is gone, which is the worst possible moment to discover
	// a formatting rule.
	Host string
	// Port is the TCP port, or zero for the scheme's default.
	Port int
}

// Auth is how to authenticate. WinRM here speaks NTLM, which needs a
// username and a password; a key is not a usable credential.
type Auth struct {
	Username string
	Password string
}

// Result is what running one script produced. A non-zero ExitCode is the
// script's own answer and is NOT a Go error, matching pkg/remoteexec.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Options configures how connections are made. The zero value is usable
// and conservative: HTTP on the default port, message encryption on.
type Options struct {
	// HTTPS selects WinRM over TLS on DefaultPortHTTPS.
	HTTPS bool

	// Insecure skips TLS certificate verification when HTTPS is set. It
	// is named for what it does rather than for the situation that
	// tempts an operator into it, the same rule pkg/remoteexec applies
	// to host key verification: a bypass must read as a bypass.
	Insecure bool

	// CACert is a PEM bundle to verify the server certificate against
	// when HTTPS is set. Empty means the system pool.
	CACert []byte

	// Timeout bounds a single operation, enforced by this package
	// rather than by the library underneath it. Zero means
	// DefaultTimeout. A deadline already on the caller's context wins
	// when it is sooner.
	Timeout time.Duration

	// DisableEncryption turns off WinRM message encryption on the HTTP
	// path. It exists to be refused loudly rather than used: see Run.
	DisableEncryption bool
}

// Run executes script on target through shell, authenticating with auth.
//
// The Result and error contract is pkg/remoteexec's exactly: a non-zero
// Result.ExitCode is the remote script reporting failure and is not a Go
// error, while a non-nil error means the outcome could not be determined
// at all.
func Run(ctx context.Context, target Target, auth Auth, shell Shell, script string, opts Options) (Result, error) {
	if script == "" {
		return Result{}, fmt.Errorf("winrm: empty script")
	}
	if opts.DisableEncryption && !opts.HTTPS {
		return Result{}, fmt.Errorf("winrm: DisableEncryption requires HTTPS: over plain HTTP it would send the credential exchange and every script in cleartext")
	}

	// Everything the caller can get wrong is checked before a client is
	// built or a credential is read. Ordering these later would report a
	// credential problem to an author whose actual mistake was naming a
	// shell this transport cannot run.
	switch shell {
	case ShellPowerShell, ShellCmd:
		// Supported.
	case ShellNone:
		return Result{}, fmt.Errorf(
			"winrm: shell %q is not available: the WinRM service decides between direct execution and cmd.exe with the "+
				"WINRS_SKIP_CMD_SHELL option and this package cannot set it, so it will not claim a command runs "+
				"verbatim when it would be parsed by cmd.exe. Name the interpreter instead: shell %q or shell %q",
			ShellNone, ShellPowerShell, ShellCmd)
	default:
		return Result{}, fmt.Errorf("winrm: unknown shell %v", shell)
	}

	// The ShellCmd path puts the caller's bytes into the SOAP body raw,
	// so it is the one that must defend against the library's unescaped
	// CDATA terminator. ShellPowerShell needs no such check: the script
	// is base64 encoded before it reaches the body.
	if shell == ShellCmd && strings.Contains(script, cdataTerminator) {
		return Result{}, fmt.Errorf(
			"winrm: script contains %q, which would close the CDATA section in the SOAP request early and inject raw XML",
			cdataTerminator)
	}

	client, err := newClient(target, auth, opts)
	if err != nil {
		return Result{}, err
	}

	// The deadline is enforced HERE rather than handed to the library,
	// because the library discards both of the things that would
	// normally carry it.
	//
	// Windows refuses unencrypted WinRM by default, so every operation
	// this package performs goes through winrm.Encryption. Its
	// Transport method builds a bare &http.Client{}: no Timeout, and the
	// default transport, whose ResponseHeaderTimeout is unset and
	// therefore unlimited. The endpoint's own Timeout is applied by the
	// UNencrypted transport only, which is the path a default-configured
	// Windows host never takes. Its two requests are then built with
	// http.NewRequest rather than NewRequestWithContext, so the context
	// passed to RunPSWithContext never reaches the HTTP layer at all.
	// The field that would let a caller fix any of this, Encryption's
	// httpClient, is unexported.
	//
	// The measured consequence, which is what prompted this: a task that
	// reconfigured a device's own network address, destroying the
	// connection carrying it, blocked for two minutes fifty-one seconds
	// and then three minutes ten seconds on separate runs. Neither
	// Options.Timeout nor a context deadline shortened either one.
	//
	// What this fix does and does not buy is worth stating plainly. The
	// CALLER is released on time. The goroutine below is not: it stays
	// parked in the library until the operating system tears the socket
	// down, holding one connection and one goroutine until it does.
	// That is a real cost and it is the smaller one, because the
	// alternative is an operator watching a runbook hang with no way to
	// bound it. Genuinely cancelling the in-flight request needs the
	// library to accept a context, which is an upstream change.
	ctx, cancel := withOperationDeadline(ctx, opts.Timeout)
	defer cancel()

	// Buffered, so the goroutine below can always deliver and exit even
	// when nobody is left waiting for it.
	done := make(chan operationResult, 1)
	go func() {
		var stdout, stderr string
		var code int
		var runErr error
		if shell == ShellPowerShell {
			stdout, stderr, code, runErr = client.RunPSWithContext(ctx, script)
		} else {
			stdout, stderr, code, runErr = client.RunWithContextWithString(ctx, script, "")
		}
		done <- operationResult{res: Result{Stdout: stdout, Stderr: stderr, ExitCode: code}, err: runErr}
	}()

	select {
	case out := <-done:
		if out.err != nil {
			return Result{}, fmt.Errorf("winrm: %w", out.err)
		}
		return out.res, nil
	case <-ctx.Done():
		return Result{}, fmt.Errorf(
			"winrm: %s: gave up waiting for %s after %s. The script may still be running on the device, and if it "+
				"changed the network configuration it has probably already taken effect; this says only that no "+
				"answer came back in time",
			ctx.Err(), target.Host, describeTimeout(opts.Timeout))
	}
}

// operationResult is one finished WinRM operation, or the error that
// stopped it.
type operationResult struct {
	res Result
	err error
}

// withOperationDeadline bounds one operation, leaving an earlier
// deadline the caller already set alone.
//
// A caller's own deadline always wins when it is sooner, so an engine
// enforcing a per-task timeout is never overridden by this package's
// default.
func withOperationDeadline(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

// describeTimeout names the bound that expired, for an error message
// that tells an operator which knob to turn.
func describeTimeout(timeout time.Duration) string {
	if timeout <= 0 {
		return DefaultTimeout.String() + " (the default)"
	}
	return timeout.String()
}

// ResolvePort picks the port to dial: whatever the target names, or the
// default listener for the scheme.
//
// An explicit zero is treated as absent rather than honored, because
// dialing port 0 cannot succeed and an inventory entry that omits the
// port is the ordinary case, not an error to surface.
func ResolvePort(targetPort int, https bool) int {
	if targetPort != 0 {
		return targetPort
	}
	if https {
		return DefaultPortHTTPS
	}
	return DefaultPort
}

// unbracket strips the brackets from a bracketed IPv6 literal, leaving
// every other host untouched.
//
// The client this package wraps brackets the host itself when it builds
// the WS-Man URL, so a value that arrives already bracketed has to have
// them removed or the URL carries two pairs and does not parse. See
// Target.Host for why both spellings are accepted rather than one being
// declared correct.
func unbracket(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}

// newClient builds a winrm.Client for one operation.
//
// A client is built per call rather than cached per target because the
// credential is an argument to the call, not a property of this package,
// and a cache keyed by target alone would hand one device's session to
// whichever credential asked for it second.
func newClient(target Target, auth Auth, opts Options) (*winrm.Client, error) {
	if auth.Username == "" || auth.Password == "" {
		return nil, fmt.Errorf("winrm: needs a username and a password (WinRM authenticates with NTLM, not with a key)")
	}

	port := ResolvePort(target.Port, opts.HTTPS)
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	endpoint := winrm.NewEndpoint(unbracket(target.Host), port, opts.HTTPS, opts.Insecure, opts.CACert, nil, nil, timeout)

	params := *winrm.DefaultParameters
	if !opts.DisableEncryption && !opts.HTTPS {
		// SPNEGO session encryption over HTTP. Windows refuses
		// unencrypted WinRM by default, so without this the very first
		// request comes back 415 rather than working and then leaking.
		params.TransportDecorator = func() winrm.Transporter {
			enc, err := winrm.NewEncryption("ntlm")
			if err != nil {
				// Unreachable for the literal "ntlm": NewEncryption only
				// rejects protocols it does not know. Falling back keeps
				// this from panicking and the request then fails loudly
				// at the service.
				return &winrm.ClientNTLM{}
			}
			return enc
		}
	} else {
		params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
	}

	client, err := winrm.NewClientWithParameters(endpoint, auth.Username, auth.Password, &params)
	if err != nil {
		return nil, fmt.Errorf("winrm: building client for %s:%d: %w", target.Host, port, err)
	}
	return client, nil
}

// DefaultProbeInterval is how often WaitUntilReachable retries.
const DefaultProbeInterval = 5 * time.Second

// probeScript is the cheapest thing that proves a real WinRM session was
// established: it authenticates, opens a shell, runs, and returns output.
// A TCP connect would prove less (a listener can accept before WinRM is
// serving, which is exactly what a host part way through booting looks
// like) and would report reachable at the moment it is least true.
const probeScript = `'pleiades-probe'`

// Reachable reports whether target answers a real WinRM command right
// now, returning nil when it does.
func Reachable(ctx context.Context, target Target, auth Auth, opts Options) error {
	_, err := Run(ctx, target, auth, ShellPowerShell, probeScript, opts)
	return err
}

// WaitUntilReachable blocks until target answers WinRM, or until ctx
// expires, whichever comes first.
//
// It exists for the one operation this transport cannot otherwise
// report on honestly: a command that reconfigures the device's own
// network, or reboots it, destroys the connection carrying it before it
// can answer. The script's exit status and output are not recoverable
// after that. They were in flight on a socket that no longer exists,
// and no amount of reconnecting brings them back.
//
// What reconnecting DOES establish is that the host survived, and that
// is worth having on its own. It is also the only available evidence
// that the command reached a real device at all: a task pointed at an
// address nothing answers cannot come back, so "it returned" and "it
// disconnected and then returned" are distinguishable, while "it
// disconnected" alone is not distinguishable from "it was never there".
//
// Each probe gets its own short timeout rather than the caller's whole
// budget, so this polls instead of blocking once on a socket that will
// never answer.
func WaitUntilReachable(ctx context.Context, target Target, auth Auth, opts Options, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultProbeInterval
	}

	probeOpts := opts
	probeOpts.Timeout = interval * 2

	var attempts int
	var lastErr error
	for {
		// Checked before the first probe as well as between them: a
		// caller whose deadline has already passed should get an answer
		// rather than one more round trip.
		if err := ctx.Err(); err != nil {
			return fmt.Errorf(
				"winrm: %s did not answer after %d attempt(s): %w (last attempt: %v)",
				target.Host, attempts, err, lastErr)
		}

		attempts++
		if lastErr = Reachable(ctx, target, auth, probeOpts); lastErr == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"winrm: %s did not answer after %d attempt(s): %w (last attempt: %v)",
				target.Host, attempts, ctx.Err(), lastErr)
		case <-time.After(interval):
		}
	}
}
