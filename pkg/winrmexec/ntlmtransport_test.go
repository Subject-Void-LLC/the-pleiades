// Tests for sealedNTLMTransport against a local HTTP server that records
// every request it receives: what crosses the network when the handshake
// fails, and how each failure is named. That a real Windows host accepts
// the sealed messages is proven on the lab's win-check (FAILURE_PATTERNS
// 532), since a server-side NTLM to seal against does not exist here.
package winrmexec

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/masterzen/winrm"
)

// marker is text a command holds that must never cross unsealed.
const marker = "PLEIADES-SECRET-MARKER-7d1f"

// recordingHost answers every request with status (and a WWW-Authenticate
// challenge, when status is 401), and keeps every body it received.
type recordingHost struct {
	mu     sync.Mutex
	bodies []string
	status int
}

func (h *recordingHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	h.bodies = append(h.bodies, string(b))
	h.mu.Unlock()
	if h.status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Negotiate")
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(h.status)
}

// exchangeTo builds the exchange newExchange builds for a password over
// HTTP, aimed at server.
func exchangeTo(t *testing.T, server *httptest.Server) *exchange {
	t.Helper()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	x, err := newExchange(Target{Host: host, Port: p}, Auth{Username: `LAB\administrator`, Password: "pw"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return x
}

// TestSealedTransport_AFailedHandshakeSendsNothing: when the handshake
// does not complete, whether the host answers 503 or refuses the login,
// the call fails naming why and nothing of the message reaches the host.
// The library's own winrm.Encryption, which this transport replaced, posted
// the message unsealed instead and failed with "invalid content type".
func TestSealedTransport_AFailedHandshakeSendsNothing(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusServiceUnavailable, "ended with HTTP 503 and no sealed session"},
		{http.StatusUnauthorized, "refused the login"},
	} {
		host := &recordingHost{status: tc.status}
		server := httptest.NewServer(host)
		x := exchangeTo(t, server)
		message := winrm.NewExecuteCommandRequest(x.url, "shell-1", "Write-Output "+marker, nil, &x.params)
		_, err := x.transport.Post(x.client, message)
		server.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "nothing of the message was sent") {
			t.Errorf("HTTP %d: err = %v, want %q", tc.status, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "invalid content type") {
			t.Errorf("HTTP %d: the library's unsealed fallback ran: %v", tc.status, err)
		}
		if tc.status == http.StatusUnauthorized && !errors.Is(err, errLoginRefused) {
			t.Errorf("a refused login is not errLoginRefused: %v", err)
		}
		host.mu.Lock()
		for i, b := range host.bodies {
			if strings.Contains(b, marker) || strings.Contains(b, "Envelope") {
				t.Errorf("HTTP %d: request %d carried the message unsealed: %.120q", tc.status, i, b)
			}
		}
		host.mu.Unlock()
	}
}

// TestSealedTransport_Edges: a transport used before Transport built it
// fails rather than sending, and an account splits into user and domain
// the way the library splits it.
func TestSealedTransport_Edges(t *testing.T) {
	if _, err := (&sealedNTLMTransport{}).Post(nil, winrm.NewOpenShellRequest("http://h/wsman", winrm.DefaultParameters)); err == nil {
		t.Error("an unbuilt transport sent")
	}
	for in, want := range map[string][2]string{`LAB\admin`: {"admin", "LAB"}, "admin@lab.example": {"admin", "lab.example"}, "admin": {"admin", ""}} {
		if u, d := splitAccount(in); u != want[0] || d != want[1] {
			t.Errorf("%s = %q %q", in, u, d)
		}
	}
}

// identity is a security session whose seal is no seal, so the framing can
// be tested apart from the cryptography, which a real host exercised.
type identity struct{}

func (identity) Unwrap(sealed, _ []byte) ([]byte, error) { return sealed, nil }

// TestFrame_RoundTripsAndRefusesMalformedReplies: a framed message reads
// back as the message, in the WS-MV layout Windows accepts (tab-indented
// part headers, the plain length declared, the closing boundary), and a
// reply missing a part, its length, or holding a signature past its end or
// a message of another length is refused.
func TestFrame_RoundTripsAndRefusesMalformedReplies(t *testing.T) {
	msg := []byte("<s:Envelope>hello</s:Envelope>")
	framed := frame([]byte("SIGNATURE-16-BYT"), msg, len(msg))
	for _, want := range []string{
		"--Encrypted Boundary\r\n\tContent-Type: application/HTTP-SPNEGO-session-encrypted\r\n",
		"\tOriginalContent: type=application/soap+xml;charset=UTF-8;Length=30\r\n",
		"--Encrypted Boundary\r\n\tContent-Type: application/octet-stream\r\n\x10\x00\x00\x00SIGNATURE-16-BYT<s:Envelope>",
	} {
		if !strings.Contains(string(framed), want) {
			t.Errorf("the frame lacks %q:\n%q", want, framed)
		}
	}
	if !strings.HasSuffix(string(framed), "--Encrypted Boundary--\r\n") {
		t.Errorf("the frame does not close: %q", framed)
	}
	plain, contentType, err := unseal(identity{}, framed, sealedContentType)
	if err != nil || string(plain) != string(msg) || contentType != soapContentType {
		t.Fatalf("unseal = %q %q %v", plain, contentType, err)
	}
	for name, reply := range map[string][]byte{
		"no octet-stream part": []byte("--Encrypted Boundary\r\n\tContent-Type: x\r\n"),
		"no length":            []byte("x" + octetStreamHeader + "\x00\x00\x00\x00abc"),
		"a length in words":    []byte("Length=ten\r\n" + octetStreamHeader + "\x00\x00\x00\x00abc"),
		"too short":            []byte("Length=1\r\n" + octetStreamHeader + "ab"),
		"signature past end":   []byte("Length=1\r\n" + octetStreamHeader + "\xff\x00\x00\x00ab"),
		"another length":       frame([]byte("SIG"), msg, len(msg)+1),
	} {
		if _, _, err := unseal(identity{}, reply, sealedContentType); err == nil {
			t.Errorf("%s was read", name)
		}
	}
}
