// Package winrmexec runs a command on a Windows device over WinRM, and is
// the single place this platform does that.
//
// It exists for the reason pkg/remoteexec and pkg/remotefile exist. A
// Collection may import pkg/ and nothing else in this module, which
// internal/archtest enforces, so a Collection method reaching a Windows
// host cannot use an adapter living under internal/ no matter how much
// of the same work it needs.
//
// # Three execution modes, each with exactly one parser
//
// A Windows target can run a command three genuinely different ways, and
// Shell names which:
//
//   - ShellNone runs a command line directly: a program and its
//     arguments, parsed only by that program (CommandLine quotes one for
//     the standard Windows parser). Nothing else reads it.
//   - ShellCmd runs a one-line script through cmd.exe, which is the only
//     way to reach a cmd builtin (dir, set, for, %ERRORLEVEL%).
//   - ShellPowerShell runs a script through powershell.exe, base64
//     encoded as -EncodedCommand requires.
//
// The WinRM service starts every command through `cmd.exe /C` and cannot
// be told not to: the WS-Man option that asks it to is not honored by
// Windows (measured; cmdexe.go has the detail). So every mode's line is
// escaped until that cmd.exe passes it through unchanged, and the parser
// that acts on a command's bytes is still only the one the caller chose:
// the program itself for ShellNone, the cmd.exe ShellCmd names, or the
// powershell.exe ShellPowerShell names. This package builds the two WS-Man
// messages it needs itself (wsman.go) and posts them through the
// library's own authenticated, encrypted transport.
//
// # Data travels as data
//
// A script is the caller's own text and runs verbatim. Values a script
// needs arrive beside it rather than inside it: as environment variables
// set on the shell (Command.Env), or on stdin (Command.Stdin), which is
// also the only channel for a secret, since a command line and an
// environment are both visible to other processes on the device. Neither
// needs escaping for either shell, which matters because cmd.exe and
// PowerShell have disjoint metacharacter sets and no single escape is
// safe for both. Where a value must be spliced into PowerShell text
// anyway, QuotePS is the one correct way to do it.
//
// Text is checked before it is sent (CheckText): a WS-Man message is XML,
// which cannot carry most control characters, and an escaper that
// replaced them would run a different command than the one written.
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
	// ShellNone runs a command line directly: a program and its
	// arguments, parsed only by that program. See the package doc.
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

// Auth is how to authenticate, in exactly one of two ways.
//
// A username and password authenticate over NTLM, which is what a stock
// Enable-PSRemoting host accepts and what this package did exclusively
// until Phase 78d.
//
// A certificate and its private key authenticate over TLS mutual
// authentication instead. That path is HTTPS only and carries no
// username: WinRM maps the certificate to a local account on the Windows
// side, so the account is named by the certificate rather than by this
// struct. See certauth.go for the whole mechanism and for what the target
// has to be configured with before it works.
//
// The two are alternatives, never a combination. Validate is where that
// is enforced and where the reasoning lives.
type Auth struct {
	// Username is the account to authenticate as, for password
	// authentication. It is empty for certificate authentication, where
	// the certificate names the account.
	Username string
	// Password is the password for Username.
	Password string
	// CertificatePEM is a PEM X.509 client certificate to present.
	CertificatePEM []byte
	// PrivateKeyPEM is the PEM private key proving CertificatePEM. A
	// certificate without it cannot complete a handshake, which is why
	// Validate refuses half a pair rather than trying.
	PrivateKeyPEM []byte
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

	// ServerName is the name the server's certificate is checked against
	// when it is not the host being dialed, as when a host is reached by
	// an address its certificate does not name. Empty means the host.
	ServerName string

	// Timeout bounds a single operation, enforced by this package
	// rather than by the library underneath it. Zero means
	// DefaultTimeout. A deadline already on the caller's context wins
	// when it is sooner.
	Timeout time.Duration

	// DisableEncryption turns off WinRM message encryption on the HTTP
	// path. It exists to be refused loudly rather than used: see Execute.
	DisableEncryption bool

	// CmdPath and PowerShellPath are the interpreters ShellCmd and
	// ShellPowerShell start, as absolute paths on the device. Empty means
	// DefaultCmdPath and DefaultPowerShellPath. See those constants for
	// why a bare program name is never used.
	CmdPath        string
	PowerShellPath string

	// WorkingDirectory is where a command starts on the device. Empty
	// leaves the choice to the WinRM service, which is the account's
	// profile directory, rather than guessing a drive root.
	WorkingDirectory string

	// NoProfile asks the service not to load the account's Windows user
	// profile, which saves the cost of creating one on a first login.
	// It is off by default because a command without the profile sees
	// the Default profile's registry and folders instead of its own
	// account's, so a per-user tool (VirtualBox, which keeps its VM
	// registry under %USERPROFILE%, is the case that decided this) finds
	// nothing it was set up with. The WS-Man option is WINRS_NOPROFILE.
	NoProfile bool
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

// newExchange builds the client, and the transport it posts through, for
// one operation.
//
// A client is built per call rather than cached per target because the
// credential is an argument to the call, not a property of this package,
// and a cache keyed by target alone would hand one device's session to
// whichever credential asked for it second.
func newExchange(target Target, auth Auth, opts Options) (*exchange, error) {
	if err := auth.Validate(); err != nil {
		return nil, err
	}
	opts = opts.resolve(auth)
	// A pinned authority or server name over HTTP would be ignored, and a
	// caller who pinned one believes the host is being verified.
	if !opts.HTTPS && (len(opts.CACert) > 0 || opts.ServerName != "") {
		return nil, fmt.Errorf("winrm: a pinned authority or TLS server name applies only over HTTPS, and this " +
			"connection is HTTP (a password credential uses NTLM message encryption over HTTP): use a certificate " +
			"credential on the HTTPS listener, or remove the pin")
	}

	port := ResolvePort(target.Port, opts.HTTPS)
	if auth.usesCertificate() {
		if err := checkCertificatePort(port); err != nil {
			return nil, err
		}
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	// The cert and key positions are empty for password authentication and
	// carry the pair for certificate authentication. The library reads them
	// only from the transport that needs them, so passing them
	// unconditionally would be harmless, but passing them conditionally
	// makes the two mechanisms visible in one place.
	endpoint := winrm.NewEndpoint(
		unbracket(target.Host), port, opts.HTTPS, opts.Insecure, opts.CACert,
		auth.CertificatePEM, auth.PrivateKeyPEM, timeout)
	endpoint.TLSServerName = opts.ServerName

	// The transport is built here rather than inside the library's
	// decorator so this package holds the same instance the client uses:
	// wsman.go posts the messages it builds itself through it.
	var transporter winrm.Transporter
	switch {
	case auth.usesCertificate():
		// TLS mutual authentication, through this package's own transport
		// rather than the library's ClientAuthRequest. certtransport.go says
		// why at length, and the short version is that the library's version
		// cannot complete this exchange against any current Windows host: it
		// offers TLS 1.3, which http.sys answers with a bare 503.
		//
		// It is an alternative to both NTLM branches below rather than a
		// variation on one, because it sends no Basic header at all.
		// Auth.Validate has already refused every combination that would
		// reach here with only half a pair.
		transporter = &certificateTransport{}

	case !opts.DisableEncryption && !opts.HTTPS:
		// SPNEGO session encryption over HTTP. Windows refuses
		// unencrypted WinRM by default, so without this the very first
		// request comes back 415 rather than working and then leaking.
		enc, err := winrm.NewEncryption("ntlm")
		if err != nil {
			// Unreachable for the literal "ntlm": NewEncryption only
			// rejects protocols it does not know.
			return nil, fmt.Errorf("winrm: building NTLM encryption: %w", err)
		}
		transporter = enc

	default:
		transporter = &winrm.ClientNTLM{}
	}
	params := *winrm.DefaultParameters
	params.TransportDecorator = func() winrm.Transporter { return transporter }

	// Username and password are empty on the certificate path, which is
	// correct rather than a gap: that transport sends no Basic header, and
	// the account is whatever the target maps the certificate to.
	client, err := winrm.NewClientWithParameters(endpoint, auth.Username, auth.Password, &params)
	if err != nil {
		// The library validates the keypair while building the transport, so
		// an unparsable certificate or a key that does not match it arrives
		// here rather than at the handshake.
		return nil, fmt.Errorf("winrm: building client for %s:%d: %w", target.Host, port, err)
	}
	return &exchange{client: client, transport: transporter, url: endpointURL(endpoint), params: client.Parameters}, nil
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
