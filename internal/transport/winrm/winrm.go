// Package winrm implements internal/transport.Transport and
// internal/transport.ShellTransport over real WinRM connections. It is
// the Adapter that lets a Windows device be reached at all, and it is
// the second real protocol in this module, which makes it the first test
// of a claim internal/engine/action_ssh.go has made since Phase W6: that
// "a second protocol (NETCONF, WinRM, ...) is a new TransportBinding
// entry plus a new transport.Transport implementation, never a change to
// TransportActionExecutor."
//
// The claim survives with exactly one named exception. Selecting a
// Windows shell needs a second port method (transport.ShellTransport),
// and the executor has to type-assert for it, which is a real change to
// dispatch logic. It is additive and total: ShellNone is the zero value,
// so no existing binding changes behavior.
//
// # What works, what does not, and why that is not a matter of taste
//
// A Windows target can run a command three ways, and this package
// supports two of them today:
//
//   - ShellPowerShell works. The script is UTF-16LE encoded and base64ed
//     into "powershell.exe -EncodedCommand <base64>".
//   - ShellCmd works. The script goes to cmd.exe, which is the only way
//     to reach a cmd builtin (dir, set, for, %ERRORLEVEL%).
//   - ShellNone, meaning direct exec of a program with an argument
//     vector nothing parses, is REFUSED rather than approximated.
//
// The refusal is the honest option, not a missing feature this package
// declined to finish. [MS-WSMV] 3.1.4.11 defines a WS-Man option,
// WINRS_SKIP_CMD_SHELL, that decides whether the service runs the
// command directly or hands it to cmd.exe, and it defaults to FALSE. The
// library below hardcodes it to "FALSE" while building the Command
// message inline (request.go:76-77), and exposes no seam to change it:
// NewExecuteCommandRequest is exported but Client.sendRequest,
// Shell.client, Shell.id and newCommand are all unexported, so a
// caller can build a corrected request and has no way to post it.
//
// That leaves two choices for Exec. Run the command under cmd.exe and
// keep quiet, which would mean transport.Transport.Exec silently means
// something different depending on which adapter is behind the
// interface, when SSH's own Exec promises the command "runs VERBATIM: no
// local shell invocation, and no string concatenation ... a hard
// security requirement". Or refuse, and say why. This package refuses.
// Getting ShellNone requires an upstream pull request threading an
// option set through that constructor, or a fork; until then the mode is
// unavailable rather than quietly wrong.
//
// # Why the two supported modes are safe under a cmd.exe this package did not ask for
//
// Because the option is stuck at FALSE, every command this package sends
// is parsed by cmd.exe on the far side. For ShellCmd that is precisely
// what the caller asked for. For ShellPowerShell it is harmless, and the
// reason is worth stating rather than assuming: the command line is
// "powershell.exe -EncodedCommand" followed by base64, whose alphabet is
// A-Z, a-z, 0-9, "+", "/" and "=". Not one cmd.exe metacharacter appears
// in that set, so no script content, however hostile, can reach cmd.exe
// as syntax. The encoding that exists to carry a script through a
// command line safely is also what makes this specific defect
// unreachable on this specific path.
//
// The library's second defect needs a real defense here. It wraps the
// command in CDATA by concatenation, `"<![CDATA[" + command + "]]>"`
// (request.go:83), with no escaping of a literal "]]>" in the content. A
// command containing that sequence closes the CDATA section early and
// injects raw XML into the SOAP body, which is a live injection vector
// with nothing to do with any shell's metacharacters. ExecShell rejects
// it on the ShellCmd path, where a caller's bytes reach the SOAP body
// raw. The PowerShell path needs no such check for the same reason as
// above: "]" and ">" are not in the base64 alphabet, so the sequence
// cannot survive encoding into the message.
package winrm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/masterzen/winrm"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

// Default ports, from [MS-WSMV] and the defaults Enable-PSRemoting sets.
const (
	// DefaultPort is the WinRM HTTP port.
	DefaultPort = 5985
	// DefaultPortHTTPS is the WinRM HTTPS port.
	DefaultPortHTTPS = 5986
)

// cdataTerminator is the sequence that ends a CDATA section, and the one
// the library below fails to escape.
const cdataTerminator = "]]>"

// Options configures a Transport backed by real WinRM connections. The
// zero value is usable and conservative: HTTP on the default port, with
// message encryption on.
type Options struct {
	// HTTPS selects WinRM over TLS on DefaultPortHTTPS rather than
	// cleartext HTTP on DefaultPort.
	HTTPS bool

	// Insecure skips TLS certificate verification when HTTPS is set.
	//
	// It is named for what it does rather than for the situation that
	// tempts an operator into it, following the same rule
	// pkg/remoteexec applies to host key verification: a bypass must
	// read as a bypass at the call site.
	Insecure bool

	// CACert is a PEM bundle to verify the server certificate against
	// when HTTPS is set. Empty means the system pool.
	CACert []byte

	// Timeout bounds a single operation. Zero means DefaultTimeout.
	Timeout time.Duration

	// DisableEncryption turns off WinRM message encryption on the HTTP
	// path. It exists to be refused loudly rather than to be used: see
	// New, which rejects the combination that would put a credential and
	// a script on the wire in cleartext.
	DisableEncryption bool
}

// DefaultTimeout bounds one WinRM operation when Options.Timeout is zero.
const DefaultTimeout = 60 * time.Second

// winrmTransport is the Adapter behind transport.Transport and
// transport.ShellTransport. Construct one with New; the zero value is not
// usable.
type winrmTransport struct {
	opts Options
}

// Compile-time proof that this adapter satisfies both ports. Without
// these, a signature drift on either interface would surface as a
// confusing failure at the composition root instead of here.
var (
	_ transport.Transport      = (*winrmTransport)(nil)
	_ transport.ShellTransport = (*winrmTransport)(nil)
)

// New returns a Transport backed by real WinRM connections.
//
// It refuses the one configuration that cannot be made safe: cleartext
// HTTP with message encryption disabled. WinRM over HTTP carries the
// NTLM exchange and then every SOAP body, including the script and
// anything the script prints, and without SPNEGO session encryption all
// of that is readable on the wire. Windows itself refuses this by
// default (AllowUnencrypted is false after Enable-PSRemoting), so an
// operator reaching for DisableEncryption is asking this platform to be
// weaker than the service it is dialing.
func New(opts Options) (transport.Transport, error) {
	if opts.DisableEncryption && !opts.HTTPS {
		return nil, fmt.Errorf("winrm: DisableEncryption requires HTTPS: over plain HTTP it would send the credential exchange and every script in cleartext")
	}
	return &winrmTransport{opts: opts}, nil
}

// Exec implements transport.Transport, and always fails.
//
// See this package's doc comment for the full reasoning. In short, Exec
// on SSH means the command runs verbatim with no interpreter, the
// underlying library gives this adapter no way to make that true, and an
// Exec that quietly ran the caller's command under cmd.exe would make
// one interface mean two different things.
func (t *winrmTransport) Exec(_ context.Context, _ transport.Target, _ credential.Credential, _ string) (transport.Result, error) {
	return transport.Result{}, fmt.Errorf(
		"winrm: direct exec (shell %q) is not available: the WinRM service decides between direct execution and cmd.exe "+
			"with the WINRS_SKIP_CMD_SHELL option, and this adapter cannot set it, so it will not claim a command runs "+
			"verbatim when it would actually be parsed by cmd.exe. Name the interpreter instead: shell %q or shell %q",
		transport.ShellNone, transport.ShellPowerShell, transport.ShellCmd)
}

// ExecShell implements transport.ShellTransport.
//
// The Result and error contract is Transport.Exec's exactly: a non-zero
// Result.ExitCode is the remote script reporting failure and is not a Go
// error, while a non-nil error means the outcome could not be determined
// at all.
func (t *winrmTransport) ExecShell(ctx context.Context, target transport.Target, cred credential.Credential, shell transport.Shell, script string) (transport.Result, error) {
	if script == "" {
		return transport.Result{}, fmt.Errorf("winrm: empty script")
	}

	client, err := t.client(target, cred)
	if err != nil {
		return transport.Result{}, err
	}

	var stdout, stderr string
	var code int

	switch shell {
	case transport.ShellPowerShell:
		// No CDATA check here on purpose: the library encodes this to
		// base64 before it reaches the SOAP body, and neither "]" nor
		// ">" is in the base64 alphabet.
		stdout, stderr, code, err = client.RunPSWithContext(ctx, script)

	case transport.ShellCmd:
		// This path puts the caller's bytes into the SOAP body raw, so
		// it is the one that has to defend against the library's
		// unescaped CDATA terminator.
		if strings.Contains(script, cdataTerminator) {
			return transport.Result{}, fmt.Errorf(
				"winrm: script contains %q, which would close the CDATA section in the SOAP request early and inject raw XML",
				cdataTerminator)
		}
		stdout, stderr, code, err = client.RunWithContextWithString(ctx, script, "")

	case transport.ShellNone:
		return transport.Result{}, fmt.Errorf(
			"winrm: shell %q is not available on this transport: name the interpreter instead, shell %q or shell %q",
			transport.ShellNone, transport.ShellPowerShell, transport.ShellCmd)

	default:
		return transport.Result{}, fmt.Errorf("winrm: unknown shell %v", shell)
	}

	if err != nil {
		return transport.Result{}, fmt.Errorf("winrm: %w", err)
	}

	return transport.Result{Stdout: stdout, Stderr: stderr, ExitCode: code}, nil
}

// client builds a winrm.Client for one operation.
//
// A client is built per call rather than cached per target because the
// credential is an argument to the call, not a property of the adapter,
// and a cache keyed by target alone would hand one device's session to
// whichever credential asked for it second.
func (t *winrmTransport) client(target transport.Target, cred credential.Credential) (*winrm.Client, error) {
	if cred.Username == "" || cred.Password == "" {
		return nil, fmt.Errorf("winrm: needs a username and a password (WinRM authenticates with NTLM, not with a key)")
	}

	port := target.Port
	if port == 0 {
		port = DefaultPort
		if t.opts.HTTPS {
			port = DefaultPortHTTPS
		}
	}

	timeout := t.opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	endpoint := winrm.NewEndpoint(target.Host, port, t.opts.HTTPS, t.opts.Insecure, t.opts.CACert, nil, nil, timeout)

	params := *winrm.DefaultParameters
	if !t.opts.DisableEncryption && !t.opts.HTTPS {
		// SPNEGO session encryption over HTTP. Windows refuses
		// unencrypted WinRM by default, so without this the very first
		// request comes back 415 rather than working and then leaking.
		params.TransportDecorator = func() winrm.Transporter {
			enc, err := winrm.NewEncryption("ntlm")
			if err != nil {
				// Unreachable for the literal "ntlm": NewEncryption
				// only rejects protocols it does not know. Falling back
				// to a plain NTLM transport keeps this from panicking,
				// and the request then fails loudly at the service.
				return &winrm.ClientNTLM{}
			}
			return enc
		}
	} else {
		params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
	}

	client, err := winrm.NewClientWithParameters(endpoint, cred.Username, cred.Password, &params)
	if err != nil {
		return nil, fmt.Errorf("winrm: building client for %s:%d: %w", target.Host, port, err)
	}
	return client, nil
}
