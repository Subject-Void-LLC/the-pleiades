// Tests for the WinRM Adapter's translation and retry policy.
//
// These swap winrmexec.Execute for a recording stand-in, so they prove
// what the Adapter asks pkg/winrmexec to do and how it treats each kind
// of failure. They prove nothing about a real WinRM service: the Release
// Gate in cmd/pleiades runs winrm_exec in all three modes against a real
// Windows host.
package winrm

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// call is one recorded winrmexec.Execute call.
type call struct {
	target winrmexec.Target
	auth   winrmexec.Auth
	cmd    winrmexec.Command
	opts   winrmexec.Options
}

// recording returns a transport whose execute records each call and
// answers with the next of results (the last repeats).
func recording(results ...func() (winrmexec.Result, error)) (*winrmTransport, *[]call) {
	var calls []call
	tr := &winrmTransport{opts: winrmexec.Options{Timeout: 5}}
	tr.execute = func(_ context.Context, target winrmexec.Target, auth winrmexec.Auth, cmd winrmexec.Command, opts winrmexec.Options) (winrmexec.Result, error) {
		calls = append(calls, call{target, auth, cmd, opts})
		next := results[min(len(calls), len(results))-1]
		return next()
	}
	return tr, &calls
}

// ok answers with a finished command.
func ok(stdout string, code int) func() (winrmexec.Result, error) {
	return func() (winrmexec.Result, error) { return winrmexec.Result{Stdout: stdout, ExitCode: code}, nil }
}

// fails answers with err.
func fails(err error) func() (winrmexec.Result, error) {
	return func() (winrmexec.Result, error) { return winrmexec.Result{}, err }
}

var (
	target = transport.Target{Endpoint: transport.NetworkEndpoint{Host: "win1", Port: 5985}}
	cred   = credential.Credential{Username: "lab", Password: "pw"}
)

func TestExec_IsShellNone(t *testing.T) {
	tr, calls := recording(ok("out", 7))
	res, err := tr.Exec(context.Background(), target, cred, `C:\x.exe a`)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Stdout != "out" || res.ExitCode != 7 {
		t.Errorf("result = %+v; a non-zero exit is a result, not an error", res)
	}
	c := (*calls)[0]
	if c.cmd.Shell != winrmexec.ShellNone || c.cmd.Script != `C:\x.exe a` {
		t.Errorf("command = %+v", c.cmd)
	}
	if c.target.Host != "win1" || c.target.Port != 5985 || c.auth.Username != "lab" || c.auth.Password != "pw" {
		t.Errorf("target/auth = %+v / %+v", c.target, c.auth)
	}
}

func TestExecShell_MapsShellInterpreterAndDirectory(t *testing.T) {
	tests := []struct {
		shell     transport.Shell
		want      winrmexec.Shell
		pathField func(winrmexec.Options) string
	}{
		{transport.ShellCmd, winrmexec.ShellCmd, func(o winrmexec.Options) string { return o.CmdPath }},
		{transport.ShellPowerShell, winrmexec.ShellPowerShell, func(o winrmexec.Options) string { return o.PowerShellPath }},
	}
	for _, tt := range tests {
		tr, calls := recording(ok("", 0))
		req := transport.ShellRequest{Shell: tt.shell, Script: "s", Env: map[string]string{"PLEIADES_X": "1"}, Interpreter: `D:\shell.exe`, WorkingDirectory: `C:\w`}
		if _, err := tr.ExecShell(context.Background(), target, cred, req); err != nil {
			t.Fatalf("%v: %v", tt.shell, err)
		}
		c := (*calls)[0]
		if c.cmd.Shell != tt.want || tt.pathField(c.opts) != `D:\shell.exe` || c.opts.WorkingDirectory != `C:\w` || c.cmd.Env["PLEIADES_X"] != "1" {
			t.Errorf("%v: call = %+v", tt.shell, c)
		}
	}
}

func TestExecShell_Refusals(t *testing.T) {
	tr, calls := recording(ok("", 0))
	routed := target
	routed.Route = []transport.Hop{{Host: "bastion", Port: 22}}
	if _, err := tr.ExecShell(context.Background(), routed, cred, transport.ShellRequest{Script: "x"}); err == nil || !strings.Contains(err.Error(), "hop") {
		t.Errorf("a routed target: err = %v", err)
	}
	serial := transport.Target{Endpoint: transport.SerialEndpoint{}}
	if _, err := tr.Exec(context.Background(), serial, cred, "x"); err == nil {
		t.Error("a serial endpoint was accepted")
	}
	if _, err := tr.ExecShell(context.Background(), target, cred, transport.ShellRequest{Shell: transport.Shell(9), Script: "x"}); err == nil {
		t.Error("an unknown shell was accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("%d calls reached winrmexec; every refusal must come first", len(*calls))
	}
}

func TestRetry(t *testing.T) {
	refused := &winrmexec.NotStartedError{Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}}
	badCredential := &winrmexec.NotStartedError{Err: errors.New("http error 401")}
	midCommand := errors.New("winrm: receiving output: connection reset")

	tests := []struct {
		name      string
		results   []func() (winrmexec.Result, error)
		wantCalls int
		wantErr   bool
	}{
		{name: "network failure before start is retried until it succeeds", results: []func() (winrmexec.Result, error){fails(refused), ok("", 0)}, wantCalls: 2},
		{name: "network failure before start gives up after three", results: []func() (winrmexec.Result, error){fails(refused)}, wantCalls: 3, wantErr: true},
		{name: "a rejected credential is not retried", results: []func() (winrmexec.Result, error){fails(badCredential)}, wantCalls: 1, wantErr: true},
		{name: "a failure after the command started is not retried", results: []func() (winrmexec.Result, error){fails(midCommand)}, wantCalls: 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, calls := recording(tt.results...)
			_, err := tr.Exec(context.Background(), target, cred, "x")
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if len(*calls) != tt.wantCalls {
				t.Errorf("calls = %d, want %d", len(*calls), tt.wantCalls)
			}
		})
	}
}

func TestNew_UsesWinrmexec(t *testing.T) {
	tr, ok := New(winrmexec.Options{}).(*winrmTransport)
	if !ok || tr.execute == nil {
		t.Fatal("New must wire winrmexec.Execute")
	}
}

// TestExecShell_UnlocksAPFXCredential proves a device whose credential
// is a PKCS#12 bundle reaches WinRM with its certificate and key. The
// real-host gate found the adapter sending no credential at all for one.
func TestExecShell_UnlocksAPFXCredential(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "lab"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := pkcs12.Modern.Encode(key, cert, nil, "bundle-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	tr, calls := recording(ok("", 0))
	pfxCred := credential.Credential{PFXBase64: base64.StdEncoding.EncodeToString(bundle), Passphrase: "bundle-passphrase"}
	if _, err := tr.Exec(context.Background(), target, pfxCred, "x"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	auth := (*calls)[0].auth
	if len(auth.CertificatePEM) == 0 || len(auth.PrivateKeyPEM) == 0 || auth.Password != "" {
		t.Errorf("auth = cert %d bytes, key %d bytes, password set %v; want the bundle's pair", len(auth.CertificatePEM), len(auth.PrivateKeyPEM), auth.Password != "")
	}
	// A wrong passphrase is refused before anything is sent.
	bad := pfxCred
	bad.Passphrase = "wrong"
	tr, calls = recording(ok("", 0))
	if _, err := tr.Exec(context.Background(), target, bad, "x"); err == nil || len(*calls) != 0 {
		t.Errorf("wrong passphrase: err = %v, calls = %d", err, len(*calls))
	}
}
