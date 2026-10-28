// Tests that drive each operation through the real pkg/winrmexec client,
// over real TLS with a client certificate, to an endpoint answering the
// WS-Management shell conversation as a Windows host does, so what a
// non-zero PowerShell exit becomes is read off the whole path rather than
// off a constructed result.
package winrmsvc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// The WS-Management replies a Windows host sends for the shell conversation
// pkg/winrmexec holds: a created shell, a started command, and a finished
// command's streams and exit code.
const (
	wsmanOK      = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body/></s:Envelope>`
	wsmanShell   = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:rsp="http://schemas.microsoft.com/wbem/wsman/1/windows/shell"><s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/transfer/CreateResponse</a:Action></s:Header><s:Body><rsp:Shell><rsp:ShellId>SHELL-1</rsp:ShellId></rsp:Shell></s:Body></s:Envelope>`
	wsmanCommand = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:rsp="http://schemas.microsoft.com/wbem/wsman/1/windows/shell"><s:Header><a:Action>http://schemas.microsoft.com/wbem/wsman/1/windows/shell/CommandResponse</a:Action></s:Header><s:Body><rsp:CommandResponse><rsp:CommandId>CMD-1</rsp:CommandId></rsp:CommandResponse></s:Body></s:Envelope>`
	wsmanDoneFmt = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:rsp="http://schemas.microsoft.com/wbem/wsman/1/windows/shell"><s:Body><rsp:ReceiveResponse><rsp:Stream Name="stdout" CommandId="CMD-1">%s</rsp:Stream><rsp:Stream Name="stderr" CommandId="CMD-1">%s</rsp:Stream><rsp:CommandState CommandId="CMD-1" State="http://schemas.microsoft.com/wbem/wsman/1/windows/shell/CommandState/Done"><rsp:ExitCode>%d</rsp:ExitCode></rsp:CommandState></rsp:ReceiveResponse></s:Body></s:Envelope>`
)

// wsmanAction returns the short name of a request's WS-Addressing action:
// Create, Command, Receive, Signal or Delete.
func wsmanAction(doc string) string {
	start := strings.Index(doc, "Action")
	if start < 0 {
		return ""
	}
	end := strings.Index(doc[start:], "</")
	value := doc[start : start+end]
	return value[strings.LastIndex(value, "/")+1:]
}

// clientKeyPair returns a self-signed client certificate and its key, PEM
// encoded, generated per test so no fixture expires.
func clientKeyPair(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "pleiades-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// windowsHost starts a TLS endpoint that requires a client certificate and
// answers every command with stdout, stderr and exitCode, and returns a
// Session reaching it with certificate authentication and the endpoint's
// own certificate pinned, and the Command requests the endpoint was sent.
func windowsHost(t *testing.T, stdout, stderr string, exitCode int) (Session, *[]string) {
	t.Helper()
	var sent []string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/soap+xml;charset=UTF-8")
		switch wsmanAction(string(body)) {
		case "Create":
			_, _ = w.Write([]byte(wsmanShell))
		case "Command":
			sent = append(sent, string(body))
			_, _ = w.Write([]byte(wsmanCommand))
		case "Receive":
			enc := base64.StdEncoding.EncodeToString
			_, _ = fmt.Fprintf(w, wsmanDoneFmt, enc([]byte(stdout)), enc([]byte(stderr)), exitCode)
		default:
			_, _ = w.Write([]byte(wsmanOK))
		}
	}))
	// WinRM maps the client certificate to an account, so the endpoint
	// insists on one; pkg/winrmexec caps the certificate path at TLS 1.2.
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert, MaxVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)

	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatalf("splitting the endpoint address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parsing the endpoint port: %v", err)
	}
	serverCA := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	certPEM, keyPEM := clientKeyPair(t)
	return Session{
		Target:  winrmexec.Target{Host: host, Port: port},
		Auth:    winrmexec.Auth{CertificatePEM: certPEM, PrivateKeyPEM: keyPEM},
		Options: winrmexec.Options{HTTPS: true, CACert: serverCA, Timeout: 10 * time.Second},
	}, &sent
}

// TestOperations_AFailedScriptSaysWhatRanAndWhy runs each operation against
// a host whose PowerShell exits 1 with an error record over two lines. The
// error names what ran and the exit code, and carries the whole record on
// one quoted line.
func TestOperations_AFailedScriptSaysWhatRanAndWhy(t *testing.T) {
	record := "Start-Service : Service 'Spooler' cannot be started due to the following error: Access\r\nis denied\r\n"
	for _, tc := range []struct {
		name string
		call func(Session) error
		what string
	}{
		{"Status", func(s Session) error { _, err := Status(context.Background(), s, "Spooler"); return err }, "reading the state of Spooler: powershell exited 1: "},
		{"Start", func(s Session) error { return Start(context.Background(), s, "Spooler") }, "Start-Service Spooler exited 1: "},
		{"Stop", func(s Session) error { return Stop(context.Background(), s, "Spooler") }, "Stop-Service Spooler exited 1: "},
		{"Restart", func(s Session) error { return Restart(context.Background(), s, "Spooler") }, "Restart-Service Spooler exited 1: "},
		{"Enable", func(s Session) error { return Enable(context.Background(), s, "Spooler") }, "Set-Service Spooler -StartupType Automatic exited 1: "},
		{"Disable", func(s Session) error { return Disable(context.Background(), s, "Spooler") }, "Set-Service Spooler -StartupType Disabled exited 1: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, _ := windowsHost(t, "", record, 1)
			err := tc.call(session)
			if err == nil {
				t.Fatal("a script that exited 1 returned no error")
			}
			want := tc.what + `"Start-Service : Service 'Spooler' cannot be started due to the following error: Access is denied"`
			if err.Error() != want {
				t.Errorf("error = %q\nwant    %q", err, want)
			}
		})
	}
}

// TestOperations_ASucceedingScriptIsNoError runs each operation against a
// host whose PowerShell exits 0: Status reads the state the script printed,
// each change returns no error, and each sent the host one command.
func TestOperations_ASucceedingScriptIsNoError(t *testing.T) {
	session, sent := windowsHost(t, `{"Exists":true,"Status":"Running","StartType":"Automatic"}`+"\r\n", "", 0)
	state, err := Status(context.Background(), session, "Spooler")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if want := (State{Name: "Spooler", Exists: true, Status: "Running", StartType: "Automatic"}); state != want {
		t.Errorf("Status = %+v, want %+v", state, want)
	}
	for name, call := range map[string]func(context.Context, Session, string) error{
		"Start": Start, "Stop": Stop, "Restart": Restart, "Enable": Enable, "Disable": Disable,
	} {
		if err := call(context.Background(), session, "Spooler"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(*sent) != 6 {
		t.Errorf("the host was sent %d commands, want 6", len(*sent))
	}
}
