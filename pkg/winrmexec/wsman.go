// The WS-Man exchange this package speaks to a WinRM service: open a
// shell, run one command in it, write its stdin, collect its output, and
// close the shell.
//
// # Why this package builds two of the messages itself
//
// masterzen/winrm builds every message, and two of them cannot carry what
// this platform needs:
//
//   - The Command message wraps the command in CDATA by concatenation,
//     with no escaping of a literal "]]>", so a command containing that
//     sequence closes the section early and injects raw XML into the
//     request. Here the command is ordinary XML text, escaped with the
//     predefined entities only (escapeXML), and text XML cannot carry at
//     all is refused before it gets that far (CheckText).
//   - The shell create message cannot carry environment variables or a
//     working directory, which is how runbook data reaches a command as
//     data rather than as command text.
//
// The Command message's WINRS_SKIP_CMD_SHELL option is sent as FALSE,
// the same as the library sends. TRUE would ask the service to start the
// command without cmd.exe, and Windows does not honor it: measured, it
// wraps the command in `cmd.exe /C` either way, and refuses the option
// outright when told it must comply (cmdexe.go). Sending TRUE would state
// something that does not happen, so every mode is instead built to pass
// through that cmd.exe unchanged.
//
// Everything else is the library's, unchanged: its authenticated,
// encrypted transports post the messages built here exactly as they post
// their own, and its parsers read the replies. The same move
// certtransport.go makes for TLS, taken for the same reason: the fix is
// small, and owning the piece is the only way to reach it.
package winrmexec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/masterzen/winrm"
	"github.com/masterzen/winrm/soap"
)

// WS-Man vocabulary this file uses. The resource URI names the Windows
// remote shell; the actions are WS-Transfer's Create and WinRS's
// Command.
const (
	resourceURIShell = "http://schemas.microsoft.com/wbem/wsman/1/windows/shell/cmd"
	actionCreate     = "http://schemas.xmlsoap.org/ws/2004/09/transfer/Create"
	actionCommand    = "http://schemas.microsoft.com/wbem/wsman/1/windows/shell/Command"
	replyAnonymous   = "http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous"
)

// stdinChunk is how much stdin one Send message carries. Base64 makes it
// about 87 KiB on the wire, well inside the 150 KiB envelope the library
// negotiates.
const stdinChunk = 64 * 1024

// operationTimeout is the WS-Man OperationTimeout on every message. A
// Receive with no output to return waits this long and then answers with
// an OperationTimeout fault, which the receive loop treats as "nothing
// yet" and asks again. It is shorter than any HTTP timeout on the path,
// so the service always answers before a client-side timer could fire.
const operationTimeout = "PT20S"

// poster is the one method this file needs from a winrm.Transporter: post
// a message and return the reply body.
type poster interface {
	Post(client *winrm.Client, message *soap.SoapMessage) (string, error)
}

// exchange is one conversation with one WinRM endpoint, over the
// transport the client was built with.
type exchange struct {
	client    *winrm.Client
	transport poster
	url       string
	params    winrm.Parameters
}

// shellSpec is what a shell create message carries beyond the defaults.
type shellSpec struct {
	// env is set in the shell, one variable each, in this order.
	env []envVar
	// workingDirectory is where the command starts; empty leaves it to
	// the service.
	workingDirectory string
	// noProfile asks the service not to load the account's Windows user
	// profile. See Options.NoProfile.
	noProfile bool
}

// envVar is one environment variable a shell create message sets.
type envVar struct {
	name  string
	value string
}

// NotStartedError reports a failure before the command was sent: the
// connection, the authentication or the shell itself. Nothing ran on the
// device, so a caller may retry it without running anything twice, which
// is the one distinction a retry policy needs and the reason this is a
// type rather than a message.
type NotStartedError struct {
	// Err is what failed.
	Err error
}

// Error implements error.
func (e *NotStartedError) Error() string { return "winrm: opening a shell: " + e.Err.Error() }

// Unwrap exposes Err to errors.Is and errors.As.
func (e *NotStartedError) Unwrap() error { return e.Err }

// run opens a shell, runs line in it with stdin, and returns what it
// produced. The shell is closed on every path out.
func (x *exchange) run(ctx context.Context, line, stdin string, spec shellSpec) (Result, error) {
	reply, err := x.post(openShellMessage(x.url, x.params, spec))
	if err != nil {
		return Result{}, &NotStartedError{Err: describeFault(err)}
	}
	shellID, err := winrm.ParseOpenShellResponse(reply)
	if err != nil {
		return Result{}, &NotStartedError{Err: err}
	}
	// Best effort: a shell left open is reclaimed by the service's own
	// idle timeout, so a failure here is not worth failing the task over.
	defer func() { _, _ = x.post(winrm.NewDeleteShellRequest(x.url, shellID, &x.params)) }()

	reply, err = x.post(commandMessage(x.url, x.params, shellID, line, stdin != ""))
	if err != nil {
		// The service refuses a program it cannot start (a path that
		// does not exist, for one) here, before anything ran.
		return Result{}, fmt.Errorf("winrm: starting the command: %w", describeFault(err))
	}
	commandID, err := winrm.ParseExecuteCommandResponse(reply)
	if err != nil {
		return Result{}, fmt.Errorf("winrm: starting the command: %w", err)
	}
	if err := x.sendStdin(shellID, commandID, stdin); err != nil {
		x.terminate(shellID, commandID)
		return Result{}, err
	}
	return x.receive(ctx, shellID, commandID)
}

// sendStdin writes stdin to the command in chunks and then closes it, so
// a script reading to the end of its input sees end of file. An empty
// stdin still sends the close.
func (x *exchange) sendStdin(shellID, commandID, stdin string) error {
	data := []byte(stdin)
	for {
		n := min(len(data), stdinChunk)
		last := n == len(data)
		if _, err := x.post(winrm.NewSendInputRequest(x.url, shellID, commandID, data[:n], last, &x.params)); err != nil {
			return fmt.Errorf("winrm: writing stdin: %w", describeFault(err))
		}
		if last {
			return nil
		}
		data = data[n:]
	}
}

// receive collects the command's output until the service reports it
// done, or ctx ends, in which case the command is told to terminate.
func (x *exchange) receive(ctx context.Context, shellID, commandID string) (Result, error) {
	var stdout, stderr bytes.Buffer
	for {
		if err := ctx.Err(); err != nil {
			x.terminate(shellID, commandID)
			return Result{}, err
		}
		reply, err := x.post(winrm.NewGetOutputRequest(x.url, shellID, commandID, "stdout stderr", &x.params))
		if err != nil {
			if isNoOutputYet(err) {
				// Nothing to return within the operation timeout is the
				// normal state of a command that is still working.
				continue
			}
			return Result{}, fmt.Errorf("winrm: receiving output: %w", describeFault(err))
		}
		done, code, err := winrm.ParseSlurpOutputErrResponse(reply, &stdout, &stderr)
		if err != nil {
			return Result{}, fmt.Errorf("winrm: reading output: %w", err)
		}
		if done {
			return Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}, nil
		}
	}
}

// terminate asks the service to stop the command. Best effort: it runs
// on paths that are already failing, and the shell's deletion follows.
func (x *exchange) terminate(shellID, commandID string) {
	_, _ = x.post(winrm.NewSignalRequest(x.url, shellID, commandID, &x.params))
}

// post sends one message and frees it.
func (x *exchange) post(message *soap.SoapMessage) (string, error) {
	defer message.Free()
	return x.transport.Post(x.client, message)
}

// isNoOutputYet reports whether err is the service saying a Receive had
// nothing to return in time, or the client timing out the same wait,
// rather than a real failure.
func isNoOutputYet(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "OperationTimeout")
}

// header starts a message's header with what every message here carries.
func header(message *soap.SoapMessage, uri string, params winrm.Parameters, action string) *soap.SoapHeader {
	return message.Header().
		To(uri).
		ReplyTo(replyAnonymous).
		MaxEnvelopeSize(params.EnvelopeSize).
		Id(messageID()).
		Locale(params.Locale).
		Timeout(operationTimeout).
		Action(action).
		ResourceURI(resourceURIShell)
}

// openShellMessage builds the WinRS shell create message.
//
// WINRS_CODEPAGE 65001 makes the shell's console UTF-8, so output
// decodes the way it was written. WINRS_NOPROFILE is FALSE unless the
// spec asks otherwise: see Options.NoProfile for why loading the user
// profile is the default here.
func openShellMessage(uri string, params winrm.Parameters, spec shellSpec) *soap.SoapMessage {
	noProfile := "FALSE"
	if spec.noProfile {
		noProfile = "TRUE"
	}
	message := soap.NewMessage()
	header(message, uri, params, actionCreate).
		AddOption(soap.NewHeaderOption("WINRS_NOPROFILE", noProfile)).
		AddOption(soap.NewHeaderOption("WINRS_CODEPAGE", "65001")).
		Build()

	body := message.CreateBodyElement("Shell", soap.DOM_NS_WIN_SHELL)
	message.CreateElement(body, "InputStreams", soap.DOM_NS_WIN_SHELL).SetContent("stdin")
	message.CreateElement(body, "OutputStreams", soap.DOM_NS_WIN_SHELL).SetContent("stdout stderr")
	if spec.workingDirectory != "" {
		message.CreateElement(body, "WorkingDirectory", soap.DOM_NS_WIN_SHELL).SetContent(escapeXML(spec.workingDirectory))
	}
	if len(spec.env) > 0 {
		environment := message.CreateElement(body, "Environment", soap.DOM_NS_WIN_SHELL)
		for _, v := range spec.env {
			variable := message.CreateElement(environment, "Variable", soap.DOM_NS_WIN_SHELL)
			// Names are restricted to letters, digits and underscore
			// before they get here (envSpec), so the attribute needs no
			// escaping.
			variable.SetAttr("Name", v.name)
			variable.SetContent(escapeXML(v.value))
		}
	}
	return message
}

// commandMessage builds the WinRS Command message for line.
//
// WINRS_SKIP_CMD_SHELL is FALSE, which is what Windows does whatever it
// is asked (see the file doc). The whole line travels in the Command
// element; no Arguments elements are sent, because the service only joins
// them to Command with spaces, and command.go has already built the line
// that cmd.exe will pass on unchanged.
//
// WINRS_CONSOLEMODE_STDIN decides whether the process's standard input
// is a console or a pipe. A console suits a program that expects one and
// is given nothing, which is what the library always asks for; it is
// the wrong channel for data, since console input is line edited and
// treats some bytes as control keys. So a command with stdin to read
// gets a pipe, and one without gets a console, the split Ansible's own
// WinRM connection makes for the same reason.
func commandMessage(uri string, params winrm.Parameters, shellID, line string, hasStdin bool) *soap.SoapMessage {
	consoleStdin := "TRUE"
	if hasStdin {
		consoleStdin = "FALSE"
	}
	message := soap.NewMessage()
	header(message, uri, params, actionCommand).
		ShellId(shellID).
		AddOption(soap.NewHeaderOption("WINRS_CONSOLEMODE_STDIN", consoleStdin)).
		AddOption(soap.NewHeaderOption("WINRS_SKIP_CMD_SHELL", "FALSE")).
		Build()

	body := message.CreateBodyElement("CommandLine", soap.DOM_NS_WIN_SHELL)
	message.CreateElement(body, "Command", soap.DOM_NS_WIN_SHELL).SetContent(escapeXML(line))
	return message
}

// escapeXML escapes s as XML character data using the three predefined
// entities it needs and nothing else. The library's DOM writes element
// content verbatim, so this is the only thing standing between a "<" in a
// command and the request's own structure.
//
// Not xml.EscapeText: that writes a double quote, a tab and a line break
// as numeric character references (&#34;, &#x9;, &#xA;), and the WinRM
// service does not decode them. Measured: a command holding "quoted"
// reached cmd.exe as &#34;quoted&#34;, while &amp; arrived as &. None of
// the three needs escaping in element content, so they travel as
// themselves. s has passed CheckText, so every character in it is one XML
// can carry.
func escapeXML(s string) string {
	return xmlEscaper.Replace(s)
}

// xmlEscaper replaces the characters element content cannot hold as
// themselves. ">" is included so "]]>" can never appear in the output.
var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// messageID returns a fresh WS-Addressing message ID, a random UUID in
// the form the library's own messages use.
func messageID() string {
	var b [16]byte
	// crypto/rand does not fail on any platform this runs on; a zero ID
	// would still be accepted by the service.
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b[:])
	return "uuid:" + h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// faultText finds the human message inside a WS-Man fault: WinRS puts it
// in f:Message, and SOAP in s:Text.
var faultText = regexp.MustCompile(`<(?:f:Message|s:Text)[^>]*>([^<]+)<`)

// describeFault replaces an error carrying a whole SOAP fault with the
// sentence inside it, which is the part an operator can act on ("The
// system cannot find the file specified"). An error with no fault in it
// is returned unchanged.
func describeFault(err error) error {
	if m := faultText.FindStringSubmatch(err.Error()); m != nil {
		return &faultError{message: strings.TrimSpace(m[1]), err: err}
	}
	return err
}

// faultError is an error whose text is a fault's own sentence, keeping
// the original error underneath for errors.Is and errors.As.
type faultError struct {
	message string
	err     error
}

// Error implements error.
func (e *faultError) Error() string { return e.message }

// Unwrap exposes the original error.
func (e *faultError) Unwrap() error { return e.err }
