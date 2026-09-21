// Unit tests for the client-certificate transport.
//
// An internal test package because the transport is unexported, and it is
// unexported because nothing outside this package should be choosing a
// WinRM transport. The Release Gate beside this file covers the happy path
// against a real peer; these cover the refusals and the error text, which is
// where this transport earns its existence (see certtransport.go).
package winrmexec

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/masterzen/winrm"
	"github.com/masterzen/winrm/soap"
)

// TestTransportRefusesUnusableMaterial covers the two ways the endpoint can
// be unusable before a connection is attempted.
func TestTransportRefusesUnusableMaterial(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := testKeyPair(t)

	t.Run("a keypair that is not a pair", func(t *testing.T) {
		t.Parallel()
		otherCert, _ := testKeyPair(t)
		ep := winrm.NewEndpoint("h", 5986, true, false, nil, otherCert, keyPEM, 0)
		err := (&certificateTransport{}).Transport(ep)
		if err == nil || !strings.Contains(err.Error(), "not a usable pair") {
			t.Errorf("error = %v, want it to name the mismatched pair", err)
		}
	})

	t.Run("a CA bundle holding no certificate", func(t *testing.T) {
		t.Parallel()
		ep := winrm.NewEndpoint("h", 5986, true, false, []byte("not a certificate"), certPEM, keyPEM, 0)
		err := (&certificateTransport{}).Transport(ep)
		if err == nil || !strings.Contains(err.Error(), "no certificate") {
			t.Errorf("error = %v, want it to name the unreadable bundle", err)
		}
	})
}

// TestTransportCapsTLSAtTwelve is the regression test for
// FAILURE_PATTERNS.md #275, and it is the reason this transport exists.
//
// Go's crypto/tls does not implement TLS 1.3 post-handshake client
// authentication, which is how Windows asks for a client certificate, so
// over TLS 1.3 the certificate is never sent and the request is refused
// with an empty 503. Nothing in a Linux unit test can reproduce that
// exchange, so what is asserted here is the thing under this package's
// control: the version cap. Without it the certificate path silently offers
// 1.3 again and stops working, with no test noticing.
//
// Go's omission is deliberate: RFC 8740 forbids PHA with HTTP/2 because it
// deadlocks multiplexed streams, and the Go team treats PHA as legacy
// complexity in the TLS state machine. They are unlikely to implement it, so
// this cap is a standing constraint rather than a TODO, and this test is
// expected to outlive whoever reads it next.
func TestTransportCapsTLSAtTwelve(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := testKeyPair(t)
	transport := &certificateTransport{}
	if err := transport.Transport(winrm.NewEndpoint("h", 5986, true, false, nil, certPEM, keyPEM, 0)); err != nil {
		t.Fatalf("Transport() error = %v", err)
	}

	config := transport.client.Transport.(*http.Transport).TLSClientConfig
	if config.MaxVersion != tls.VersionTLS12 {
		t.Errorf("MaxVersion = %#x, want TLS 1.2 (%#x): Go does not implement TLS 1.3 post-handshake client "+
			"authentication, so over 1.3 the certificate is never sent",
			config.MaxVersion, tls.VersionTLS12)
	}
	if config.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %#x, want TLS 1.2 (%#x)", config.MinVersion, tls.VersionTLS12)
	}
	if len(config.Certificates) != 1 {
		t.Error("the transport presents no client certificate")
	}
}

// TestPostReportsWhatTheServerActuallySaid covers the error text, which is
// the other half of why this transport is not the library's.
//
// The library answers every unexpected response with "invalid content type",
// discarding the status and the body. That is what hid #275 for an
// afternoon, so each shape is pinned here.
func TestPostReportsWhatTheServerActuallySaid(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		handler http.HandlerFunc
		want    []string
	}{
		{
			// The signature of a client certificate that never reached the
			// server, which is what Go produces over TLS 1.3 because it does
			// not implement post-handshake client authentication.
			name: "an empty 503",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
			want: []string{"503", "no content type at all", "saw no client certificate", "post-handshake"},
		},
		{
			name: "an HTML error page",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("<html><body>Bad Gateway</body></html>"))
			},
			want: []string{"502", `"text/html"`, "Bad Gateway"},
		},
		{
			name: "a SOAP fault with a non-200 status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/soap+xml;charset=UTF-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("<s:Fault>Access is denied.</s:Fault>"))
			},
			want: []string{"500", "Access is denied"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			transport := plainTransportTo(t, httptest.NewServer(tt.handler))
			_, err := transport.Post(nil, soap.NewMessage())
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to mention %q", err, want)
				}
			}
		})
	}
}

// TestPostReturnsASoapBody is the positive control: without it the tests
// above would pass against a transport that failed everything.
func TestPostReturnsASoapBody(t *testing.T) {
	t.Parallel()

	const body = "<s:Envelope>ok</s:Envelope>"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The two headers the WS-Man mutual profile requires, asserted here
		// because a transport that stopped sending them would still pass
		// every other test in this file.
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/soap+xml") {
			t.Errorf("Content-Type = %q, want SOAP", got)
		}
		if got := r.Header.Get("Authorization"); !strings.Contains(got, "secprofile/https/mutual") {
			t.Errorf("Authorization = %q, want the WS-Man mutual profile", got)
		}
		w.Header().Set("Content-Type", "application/soap+xml;charset=UTF-8")
		_, _ = w.Write([]byte(body))
	}))

	got, err := plainTransportTo(t, server).Post(nil, soap.NewMessage())
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if got != body {
		t.Errorf("Post() = %q, want %q", got, body)
	}
}

// TestEndpointURLMatchesTheLibrarys covers the URL this package rebuilds
// because the library keeps its own version unexported.
//
// The IPv6 case is the one that matters: Target.Host's own documentation
// records a bracketing bug that made a whole class of device unreachable, so
// a second place building this URL is a second place to reproduce it.
func TestEndpointURLMatchesTheLibrarys(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		host  string
		port  int
		https bool
		want  string
	}{
		{"https", "192.0.2.1", 5986, true, "https://192.0.2.1:5986/wsman"},
		{"http", "192.0.2.1", 5985, false, "http://192.0.2.1:5985/wsman"},
		{"ipv6 is bracketed", "2001:db8::1", 5986, true, "https://[2001:db8::1]:5986/wsman"},
		{"a hostname", "win01", 5986, true, "https://win01:5986/wsman"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := endpointURL(winrm.NewEndpoint(tt.host, tt.port, tt.https, false, nil, nil, nil, 0))
			if got != tt.want {
				t.Errorf("endpointURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSnippetIsBounded proves a hostile response cannot put a megabyte into
// a job record, and that a multi-line body becomes one line.
func TestSnippetIsBounded(t *testing.T) {
	t.Parallel()

	got := snippet([]byte(strings.Repeat("a", maxErrorBodyBytes*3)))
	if len(got) > maxErrorBodyBytes+3 {
		t.Errorf("snippet is %d bytes, want it bounded at %d", len(got), maxErrorBodyBytes)
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("a truncated snippet does not say it was truncated")
	}
	if got := snippet([]byte("one\n  two\n\tthree")); got != "one two three" {
		t.Errorf("snippet(...) = %q, want it collapsed to one line", got)
	}
}

// TestDescribeBodyNamesTheEmptyNonFiveOhThreeCase covers the arm the table
// above does not reach, so the hint is not accidentally given for every
// empty response.
func TestDescribeBodyNamesTheEmptyNonFiveOhThreeCase(t *testing.T) {
	t.Parallel()

	if got := describeBody(nil, http.StatusNotFound); !strings.Contains(got, "empty body") {
		t.Errorf("describeBody(nil, 404) = %q, want it to say the body was empty", got)
	}
	if got := describeBody(nil, http.StatusNotFound); strings.Contains(got, "TLS layer") {
		t.Errorf("describeBody(nil, 404) = %q, want the TLS hint reserved for 503", got)
	}
}

// plainTransportTo points a certificateTransport at a plain HTTP test
// server.
//
// The TLS half is covered by the Release Gate against a real peer; what
// these tests need is the request and response handling, and wiring TLS into
// every one of them would test net/http rather than this file.
func plainTransportTo(t *testing.T, server *httptest.Server) *certificateTransport {
	t.Helper()
	t.Cleanup(server.Close)

	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("splitting the test server address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parsing the test server port: %v", err)
	}
	return &certificateTransport{
		client: server.Client(),
		url:    endpointURL(winrm.NewEndpoint(host, port, false, false, nil, nil, nil, 0)),
	}
}

// TestTransportRefusesToSkipServerVerification pins the decision that
// removed the last InsecureSkipVerify from this package.
//
// The library's own transport honors Options.Insecure and this one refuses
// it, which is a deliberate narrowing rather than an oversight. Presenting a
// client certificate to a server you have not verified hands that
// certificate to whoever answered, which is the one thing certificate
// authentication exists to prevent.
//
// It is a refusal rather than a silent ignore because this phase was bitten
// twice by values that were accepted, carried and then dropped. A caller who
// asks for something they will not get should be told.
func TestTransportRefusesToSkipServerVerification(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := testKeyPair(t)

	err := (&certificateTransport{}).Transport(
		winrm.NewEndpoint("h", 5986, true, true, nil, certPEM, keyPEM, 0))
	if err == nil {
		t.Fatal("certificate authentication accepted a request to skip server verification")
	}
	if !strings.Contains(err.Error(), "will not skip server verification") {
		t.Errorf("error = %v, want it to name the refusal", err)
	}
	if !strings.Contains(err.Error(), "certificate authority") {
		t.Errorf("error = %v, want it to name the supported alternative", err)
	}

	// The negative control: verification on is the ordinary case and must
	// still build, or this check would have disabled the feature.
	if err := (&certificateTransport{}).Transport(
		winrm.NewEndpoint("h", 5986, true, false, nil, certPEM, keyPEM, 0)); err != nil {
		t.Errorf("a verifying endpoint was refused: %v", err)
	}
}
