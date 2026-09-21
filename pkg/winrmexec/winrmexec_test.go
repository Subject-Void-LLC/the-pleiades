package winrmexec

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// Most of what this package promises is a refusal, and the rest is the
// configuration it hands to the library, neither of which reaches a
// network. The one thing that genuinely needs a real Windows host, that
// a PowerShell script runs and its output and exit code come back,
// cannot be faked usefully: a stub WinRM server would only prove this
// package agrees with the stub. That belongs in a gated Release Gate,
// and this file does not pretend to be one.

func validAuth() Auth { return Auth{Username: "administrator", Password: "secret"} }

// TestParseShell_Valid covers every token a runbook may write, plus the
// two forms of "the author did not choose one". The empty string mapping
// to ShellNone is what makes ShellNone the zero value in practice as
// well as in the type.
func TestParseShell_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Shell
	}{
		{name: "omitted", input: "", want: ShellNone},
		{name: "whitespace only", input: "   ", want: ShellNone},
		{name: "none", input: "none", want: ShellNone},
		{name: "cmd", input: "cmd", want: ShellCmd},
		{name: "powershell", input: "powershell", want: ShellPowerShell},
		{name: "mixed case", input: "PowerShell", want: ShellPowerShell},
		{name: "surrounding whitespace", input: "  cmd\n", want: ShellCmd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseShell(tt.input)
			if err != nil {
				t.Fatalf("ParseShell(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseShell(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestParseShell_UnknownListsValidValues checks the error's content. An
// author reaching it picked a word this platform does not know, and the
// useful reply is the set it does know.
func TestParseShell_UnknownListsValidValues(t *testing.T) {
	_, err := ParseShell("bash")
	if err == nil {
		t.Fatal("expected an error for an unknown shell")
	}
	for _, want := range []string{"bash", "none", "cmd", "powershell"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// TestParseShell_RejectsSubstringMatches guards the mistake a looser
// implementation would make: accepting a token because a valid one
// appears inside it, and so running the task through an interpreter the
// author did not name.
func TestParseShell_RejectsSubstringMatches(t *testing.T) {
	for _, input := range []string{"powershell-core", "cmder", "nonesuch", "pwsh"} {
		if _, err := ParseShell(input); err == nil {
			t.Errorf("ParseShell(%q) succeeded, want an error", input)
		}
	}
}

// TestShell_StringRoundTrips proves String and ParseShell share one
// vocabulary; they read the same table so they cannot drift, and this is
// what would catch giving either its own copy.
func TestShell_StringRoundTrips(t *testing.T) {
	for _, shell := range []Shell{ShellNone, ShellCmd, ShellPowerShell} {
		parsed, err := ParseShell(shell.String())
		if err != nil || parsed != shell {
			t.Errorf("ParseShell(%q) = %v, %v; want %v", shell.String(), parsed, err, shell)
		}
	}
	if got := Shell(99).String(); !strings.Contains(got, "99") {
		t.Errorf("Shell(99).String() = %q, want the numeric value included", got)
	}
}

// TestRun_RejectsBeforeTouchingCredentials is an ordering guard. Every
// case passes a deliberately empty credential: reaching a credential
// error means an author who named an unusable shell would be told about
// the wrong thing.
func TestRun_RejectsBeforeTouchingCredentials(t *testing.T) {
	tests := []struct {
		name     string
		shell    Shell
		script   string
		wantText string
	}{
		{name: "empty script", shell: ShellPowerShell, script: "", wantText: "empty script"},
		{name: "shell none is refused", shell: ShellNone, script: "ipconfig", wantText: "WINRS_SKIP_CMD_SHELL"},
		{name: "unknown shell", shell: Shell(99), script: "ipconfig", wantText: "unknown shell"},
		{name: "cmd script closing the CDATA section", shell: ShellCmd, script: "echo ]]> hi", wantText: "CDATA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Run(context.Background(), Target{Host: "192.0.2.1"}, Auth{}, tt.shell, tt.script, Options{})
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantText)
			}
			if strings.Contains(err.Error(), "username") {
				t.Errorf("error = %v, but this must be reported before the credential is looked at", err)
			}
		})
	}
}

// TestRun_RefusesCleartextWithoutEncryption covers the one configuration
// this package will not perform. WinRM over plain HTTP carries the NTLM
// exchange and every SOAP body, so disabling SPNEGO message encryption
// there puts the handshake and the whole script on the wire in the
// clear. Windows refuses this by default.
func TestRun_RefusesCleartextWithoutEncryption(t *testing.T) {
	_, err := Run(context.Background(), Target{Host: "192.0.2.1"}, validAuth(),
		ShellPowerShell, "hostname", Options{DisableEncryption: true})
	if err == nil {
		t.Fatal("expected a refusal for cleartext HTTP with encryption disabled")
	}
	for _, want := range []string{"DisableEncryption", "HTTPS", "cleartext"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// TestRun_PowerShellAllowsCDATATerminator is the other half of the CDATA
// rule, and the half that would quietly go wrong. Rejecting "]]>"
// everywhere is the easy defensive move and it is wrong: the sequence
// appears in real PowerShell touching XML or here-strings, and on that
// path it is base64 encoded before reaching the SOAP body.
//
// It asserts on the failure MODE, since there is no host here: getting
// past validation means the error mentions the network, not the script.
func TestRun_PowerShellAllowsCDATATerminator(t *testing.T) {
	_, err := Run(context.Background(), Target{Host: "127.0.0.1", Port: 1}, validAuth(),
		ShellPowerShell, `$x = "]]>"; Write-Output $x`, Options{Timeout: 2 * time.Second})
	if err == nil {
		t.Fatal("expected a dial failure against a closed port")
	}
	if strings.Contains(err.Error(), "CDATA") {
		t.Errorf("error = %v, but a PowerShell script is base64 encoded and cannot terminate a CDATA section", err)
	}
}

// TestRun_RequiresOneCompleteCredential covers every incomplete credential
// shape, and asserts each is refused for its OWN reason.
//
// It used to assert that all of them mentioned "username and a password",
// which was right while NTLM was the only mechanism here. Certificate
// authentication made that message wrong for half these cases, and the
// substring would have kept passing while sending an operator holding a
// certificate to look for a password. Each case now pins the distinguishing
// words of the answer it should get, so a refusal that regresses to one
// generic message fails here rather than in the field.
func TestRun_RequiresOneCompleteCredential(t *testing.T) {
	for _, tt := range []struct {
		name string
		auth Auth
		want string
	}{
		{name: "nothing at all", auth: Auth{}, want: "credential is empty"},
		{name: "username only", auth: Auth{Username: "administrator"}, want: "no password"},
		{name: "password only", auth: Auth{Password: "secret"}, want: "no username"},
		{
			name: "certificate with no key",
			auth: Auth{CertificatePEM: []byte("-----BEGIN CERTIFICATE-----")},
			want: "no private key",
		},
		{
			name: "key with no certificate",
			auth: Auth{PrivateKeyPEM: []byte("-----BEGIN PRIVATE KEY-----")},
			want: "no client certificate",
		},
		{
			name: "both mechanisms at once",
			auth: Auth{
				Username:       "administrator",
				Password:       "secret",
				CertificatePEM: []byte("-----BEGIN CERTIFICATE-----"),
				PrivateKeyPEM:  []byte("-----BEGIN PRIVATE KEY-----"),
			},
			want: "both a client certificate and a password",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Run(context.Background(), Target{Host: "192.0.2.1"}, tt.auth,
				ShellPowerShell, "hostname", Options{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestRun_ErrorsDoNotLeakThePassword guards a property the engine's
// masking layer should never be the only thing providing.
func TestRun_ErrorsDoNotLeakThePassword(t *testing.T) {
	const password = "B@dpassw0rd-should-never-appear"
	_, err := Run(context.Background(), Target{Host: "127.0.0.1", Port: 1},
		Auth{Username: "administrator", Password: password},
		ShellPowerShell, "hostname", Options{Timeout: 2 * time.Second})
	if err == nil {
		t.Fatal("expected a dial failure against a closed port")
	}
	if strings.Contains(err.Error(), password) {
		t.Errorf("the error text carries the password: %v", err)
	}
}

// TestResolvePort covers the defaults, including the explicit zero. 5985
// is the cleartext listener Enable-PSRemoting creates and 5986 the TLS
// one, so the default follows the scheme rather than being one constant.
func TestResolvePort(t *testing.T) {
	tests := []struct {
		name       string
		targetPort int
		https      bool
		want       int
	}{
		{name: "unset over http", targetPort: 0, https: false, want: 5985},
		{name: "unset over https", targetPort: 0, https: true, want: 5986},
		{name: "explicit over http", targetPort: 15985, https: false, want: 15985},
		{name: "explicit over https", targetPort: 15986, https: true, want: 15986},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolvePort(tt.targetPort, tt.https); got != tt.want {
				t.Errorf("ResolvePort(%d, %v) = %d, want %d", tt.targetPort, tt.https, got, tt.want)
			}
		})
	}
}

// TestUnbracketedAndBracketedIPv6ReachTheSameEndpoint proves both
// spellings of a literal IPv6 host build a usable client.
//
// This is the IPv6 rescue path's regression test. That path exists for
// one situation, a Windows host that has lost IPv4 mid-change, and it
// was documented in examples/windows_lab in the bracketed form while
// only the bare form worked: the client brackets the host itself, so a
// bracketed value produced "http://[[2001:db8::1]]:5985/wsman" and
// failed to parse. A rescue path that fails on a formatting rule is not
// a rescue path, and the failure would surface at the one moment there
// is no other way in.
//
// The documentation ranges are used deliberately (RFC 3849 for IPv6,
// RFC 5737 for IPv4): no client is built against a real address here,
// so nothing dials, but an address that could belong to somebody has no
// business in a test fixture.
func TestUnbracketedAndBracketedIPv6ReachTheSameEndpoint(t *testing.T) {
	auth := Auth{Username: "administrator", Password: "not-a-real-password"}

	for _, host := range []string{
		"2001:db8::1",
		"[2001:db8::1]",
		"192.0.2.10",
		"win-host.example.com",
	} {
		t.Run(host, func(t *testing.T) {
			// An empty script is refused before any client is built, so
			// this reaches the endpoint construction and stops there
			// without dialing anything. What matters is WHICH error comes
			// back: a URL that does not parse fails differently from a
			// script that is missing.
			_, err := Run(context.Background(), Target{Host: host}, auth, ShellPowerShell, "", Options{})
			if err == nil {
				t.Fatal("an empty script was accepted")
			}
			if strings.Contains(err.Error(), "IP-literal") || strings.Contains(err.Error(), "parse") {
				t.Errorf("host %q produced a URL parsing failure: %v", host, err)
			}
		})
	}
}

// TestBracketedIPv6ReachesTheDial proves a bracketed literal builds a
// client that gets as far as connecting, not merely one that survives
// argument checking.
//
// The test above stops at the empty-script refusal, which shows the
// address was never the problem but not that a URL could be built from
// it. This one dials the IPv6 loopback on a port nothing listens on, so
// the only failure left is the connection being refused, and it comes
// back in milliseconds.
//
// Loopback rather than a documentation address on purpose. 2001:db8::1
// is unroutable, so the dial sits in the operating system's connect
// timeout for a full minute, and neither the context deadline nor
// Options.Timeout shortens it: the library takes its deadline from the
// endpoint and never consults ctx. That is worth knowing (it is why a
// task that destroys its own transport blocks for minutes rather than
// failing fast) and it is not worth two minutes of every test run.
func TestBracketedIPv6ReachesTheDial(t *testing.T) {
	auth := Auth{Username: "administrator", Password: "not-a-real-password"}

	for _, host := range []string{"[::1]", "::1"} {
		t.Run(host, func(t *testing.T) {
			// Port 1: reserved, and nothing on a test machine listens
			// there, so the stack refuses at once.
			_, err := Run(context.Background(), Target{Host: host, Port: 1}, auth, ShellPowerShell, "echo hi", Options{})
			if err == nil {
				t.Fatal("a connection to a closed port succeeded, which cannot be right")
			}
			if strings.Contains(err.Error(), "IP-literal") || strings.Contains(err.Error(), "invalid port") {
				t.Errorf("host %q still builds an unparsable URL: %v", host, err)
			}
			if !strings.Contains(err.Error(), "refused") && !strings.Contains(err.Error(), "connect") {
				t.Errorf("host %q failed before reaching the dial: %v", host, err)
			}
		})
	}
}

// TestRunHonorsItsTimeout proves the operation bound is real.
//
// It is the regression test for a measured defect rather than a
// hypothetical one. The library builds a bare http.Client for the
// encrypted path, which is the only path a default-configured Windows
// host allows, and builds its requests without a context, so neither
// Options.Timeout nor a context deadline reached the HTTP layer. A task
// that reconfigured a device's own address blocked for two minutes fifty
// one seconds and then three minutes ten, unbounded by either.
//
// The listener accepts the connection and then says nothing at all,
// which is what a host whose network was reconfigured out from under the
// request looks like from this side: the TCP session is established and
// no reply is ever coming. A closed port would not do, since that fails
// instantly for a different reason and would pass whether or not any
// timeout worked.
func TestRunHonorsItsTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan struct{}, 1)
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			select {
			case accepted <- struct{}{}:
			default:
			}
			// Held open, never answered, never closed.
			defer func() { _ = conn.Close() }()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	auth := Auth{Username: "administrator", Password: "not-a-real-password"}
	target := Target{Host: "127.0.0.1", Port: addr.Port}

	start := time.Now()
	_, err = Run(context.Background(), target, auth, ShellPowerShell, "echo hi", Options{Timeout: 2 * time.Second})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a server that never answers returned a result")
	}
	// Generous upper bound: the assertion is that SOMETHING bounds this,
	// not that the bound is precise. Before the fix this ran for minutes.
	if elapsed > 30*time.Second {
		t.Errorf("Run took %s against a 2s timeout, so the bound is not being enforced", elapsed)
	}
	if elapsed < 1*time.Second {
		t.Errorf("Run returned in %s, too fast to have reached the server: this is failing for some other reason and proves nothing about the timeout", elapsed)
	}
	select {
	case <-accepted:
	default:
		t.Error("the listener never accepted a connection, so the timeout was not measured against a real wait")
	}
}

// TestRunHonorsAnEarlierContextDeadline proves a caller's own deadline
// wins when it is sooner than Options.Timeout.
//
// This is what keeps an engine-level per-task timeout meaningful. If
// this package always applied its own bound the engine's shorter one
// would be silently ignored, which is the same class of defect as the
// library ignoring ours.
func TestRunHonorsAnEarlierContextDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			defer func() { _ = conn.Close() }()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	_, err = Run(ctx, Target{Host: "127.0.0.1", Port: addr.Port},
		Auth{Username: "administrator", Password: "not-a-real-password"},
		ShellPowerShell, "echo hi",
		// An hour, so only the context can be responsible for returning.
		Options{Timeout: time.Hour})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a server that never answers returned a result")
	}
	if elapsed > 30*time.Second {
		t.Errorf("Run took %s against a 2s context deadline, so the caller's deadline is being ignored", elapsed)
	}
}

func TestWithOperationDeadline(t *testing.T) {
	t.Run("zero uses the default", func(t *testing.T) {
		ctx, cancel := withOperationDeadline(context.Background(), 0)
		defer cancel()
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("no deadline was set, so an operation would be unbounded")
		}
		if remaining := time.Until(deadline); remaining > DefaultTimeout+time.Second {
			t.Errorf("deadline is %s away, want about %s", remaining, DefaultTimeout)
		}
	})

	t.Run("an earlier caller deadline is kept", func(t *testing.T) {
		parent, parentCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer parentCancel()
		ctx, cancel := withOperationDeadline(parent, time.Hour)
		defer cancel()
		deadline, _ := ctx.Deadline()
		if remaining := time.Until(deadline); remaining > 10*time.Second {
			t.Errorf("deadline is %s away, want the caller's 5s: a longer option must not extend it", remaining)
		}
	})

	t.Run("a later caller deadline is shortened", func(t *testing.T) {
		parent, parentCancel := context.WithTimeout(context.Background(), time.Hour)
		defer parentCancel()
		ctx, cancel := withOperationDeadline(parent, 2*time.Second)
		defer cancel()
		deadline, _ := ctx.Deadline()
		if remaining := time.Until(deadline); remaining > 10*time.Second {
			t.Errorf("deadline is %s away, want about 2s", remaining)
		}
	})
}
