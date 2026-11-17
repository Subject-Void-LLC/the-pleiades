// Tests that a session to a device trusted only by a pin of its self-signed
// certificate is what its capture shows: over TLS 1.2 the certificate the
// pin names crosses in the clear inside the handshake, then everything else
// is encrypted; over TLS 1.3 the certificate is encrypted too, and the run's
// key log (devicetls.CreateKeyLog, as pleiades run --tls-keylog writes it)
// is what lets a reader see it; and a handshake the pin refuses carries no
// application data at all, the request and its credential never sent.
//
// The sessions are real: devicetls's own pinned client configuration over
// a real TCP connection on loopback to a real TLS server, recorded byte for
// byte and written as the capture of that session (tcpcapture_test.go).
//
// With PLEIADES_TLS_CAPTURE_DIR set to a directory, the captures, the key
// log and a manifest saying what each should show are written there, for a
// reader such as S.O.N.I.C. to validate later:
//
//	PLEIADES_TLS_CAPTURE_DIR=$PWD/.IGNORE/tls-capture go test -count=1 -run TestCapture_ ./pkg/pcapsum/
package pcapsum_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pcapsum"
)

// captureDirEnv names the directory the captures are written to, when set.
const captureDirEnv = "PLEIADES_TLS_CAPTURE_DIR"

// captureToken is the bearer token each request carries: a stand-in for a
// device's credential, which a capture must never show in the clear.
const captureToken = "brownfield-capture-token-7c1e"

// capturedSession is one recorded session and what it was made with.
type capturedSession struct {
	chunks []chunk
	err    error
	hits   int32
}

// pinnedSession runs one GET of /api/interfaces carrying captureToken from
// a client configured by devicetls with the pins in tlsProps to a server
// presenting cert, at most maxVersion, and returns the session's bytes as the
// client's connection saw them. ctx may carry a key log.
func pinnedSession(ctx context.Context, t *testing.T, cert tls.Certificate, maxVersion uint16, tlsProps map[string]inventory.PropertyValue) capturedSession {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `{"interfaces":["ethernet-1/1"]}`)
	}))
	srv.EnableHTTP2 = false
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: maxVersion, Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	defer srv.Close()

	settings, err := devicetls.Parse(inventory.NewProperties(tlsProps))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.ConfigContext(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.NextProtos = []string{"http/1.1"}
	var rec *recorder
	transport := &http.Transport{
		TLSClientConfig:   cfg,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			rec = &recorder{Conn: c}
			return rec, nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/interfaces", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+captureToken)
	resp, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	transport.CloseIdleConnections()
	if rec == nil {
		t.Fatal("the client never dialed")
	}
	return capturedSession{chunks: rec.recorded(), err: err, hits: hits.Load()}
}

// selfSignedDevice is a device's own certificate for 127.0.0.1, its key
// never leaving the test.
func selfSignedDevice(t *testing.T, cn string) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn, Organization: []string{"Brownfield lab"}},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, parsed
}

// clientRecords returns the TLS record content types the client sent, in
// order, read from its own bytes.
func clientRecords(chunks []chunk) []byte {
	var stream []byte
	for _, c := range chunks {
		if c.up {
			stream = append(stream, c.data...)
		}
	}
	var types []byte
	for len(stream) >= 5 {
		n := int(stream[3])<<8 | int(stream[4])
		types = append(types, stream[0])
		if len(stream) < 5+n {
			break
		}
		stream = stream[5+n:]
	}
	return types
}

// manifestEntry says what one written capture should show a reader.
type manifestEntry struct {
	File           string `json:"file"`
	KeyLog         string `json:"key_log,omitempty"`
	TLSVersion     string `json:"tls_version"`
	Pin            string `json:"pin"`
	Fingerprint    string `json:"certificate_fingerprint"`
	KeyFingerprint string `json:"certificate_key_fingerprint"`
	Subject        string `json:"certificate_subject"`
	Expect         string `json:"expect"`
}

// TestCapture_APinnedSelfSignedSession records the three sessions and holds
// each capture to what it must show, through this package's own reader.
func TestCapture_APinnedSelfSignedSession(t *testing.T) {
	cert, parsed := selfSignedDevice(t, "srl1.brownfield.invalid")
	_, stranger := selfSignedDevice(t, "stranger.brownfield.invalid")
	pin := devicetls.Fingerprint(parsed)
	pinned := map[string]inventory.PropertyValue{devicetls.FingerprintProperty: pin}

	// TLS 1.2: the pinned certificate crosses in the clear, then only
	// application data, the token never in the clear.
	tls12 := pinnedSession(context.Background(), t, cert, tls.VersionTLS12, pinned)
	if tls12.err != nil || tls12.hits != 1 {
		t.Fatalf("the pinned TLS 1.2 session: %v, %d requests", tls12.err, tls12.hits)
	}
	data12 := sessionCapture(tls12.chunks, 50123, 443)
	if !bytes.Contains(data12, parsed.Raw) {
		t.Error("the TLS 1.2 capture does not hold the certificate the pin names")
	}
	if bytes.Contains(data12, []byte(captureToken)) {
		t.Fatal("the token is in the TLS 1.2 capture in the clear")
	}
	if s := read(t, data12); onlyFlow(t, s).Protocol != pcapsum.TLS || s.Encrypted == 0 || s.Cleartext != 0 || s.CleartextCredentials != 0 {
		t.Errorf("the TLS 1.2 capture reads as %+v", s)
	}

	// TLS 1.3 with the run's key log: the certificate is encrypted, and the
	// key log, created as pleiades run --tls-keylog creates it, holds the
	// secrets that decrypt it.
	keyLogPath := filepath.Join(t.TempDir(), "pinned-tls13.keylog")
	keyLog, err := devicetls.CreateKeyLog(keyLogPath)
	if err != nil {
		t.Fatal(err)
	}
	tls13 := pinnedSession(devicetls.WithKeyLog(context.Background(), keyLog), t, cert, tls.VersionTLS13, pinned)
	if err := keyLog.Close(); err != nil {
		t.Fatal(err)
	}
	if tls13.err != nil || tls13.hits != 1 {
		t.Fatalf("the pinned TLS 1.3 session: %v, %d requests", tls13.err, tls13.hits)
	}
	data13 := sessionCapture(tls13.chunks, 50124, 443)
	if bytes.Contains(data13, parsed.Raw) || bytes.Contains(data13, []byte(captureToken)) {
		t.Error("the TLS 1.3 capture holds the certificate or the token in the clear")
	}
	secrets, err := os.ReadFile(keyLogPath) // #nosec G304 -- this test's own file
	if err != nil || !bytes.Contains(secrets, []byte("SERVER_HANDSHAKE_TRAFFIC_SECRET")) {
		t.Errorf("the key log holds no handshake secret (%v)", err)
	}
	if s := read(t, data13); onlyFlow(t, s).Protocol != pcapsum.TLS || s.Encrypted == 0 || s.Cleartext != 0 {
		t.Errorf("the TLS 1.3 capture reads as %+v", s)
	}

	// A pin the certificate does not match: the handshake is refused, the
	// client sends no application data record, and the request never leaves.
	refused := pinnedSession(context.Background(), t, cert, tls.VersionTLS12, map[string]inventory.PropertyValue{devicetls.FingerprintProperty: devicetls.Fingerprint(stranger)})
	var untrusted *devicetls.UntrustedError
	if !errors.As(refused.err, &untrusted) || untrusted.Fingerprint != pin || refused.hits != 0 {
		t.Fatalf("the refused session: %v, %d requests", refused.err, refused.hits)
	}
	dataRefused := sessionCapture(refused.chunks, 50125, 443)
	if types := clientRecords(refused.chunks); bytes.IndexByte(types, 0x17) >= 0 || bytes.IndexByte(types, 0x14) >= 0 {
		t.Errorf("the refused client sent records %x, holding application data or a change of cipher spec", types)
	}
	if s := read(t, dataRefused); s.Encrypted != 0 || bytes.Contains(dataRefused, []byte(captureToken)) || bytes.Contains(dataRefused, []byte("/api/interfaces")) {
		t.Errorf("the refused capture carries application data: %+v", s)
	}

	dir := os.Getenv(captureDirEnv)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	entries := []manifestEntry{
		{File: "pinned-tls12.pcap", TLSVersion: "1.2", Expect: "the certificate the pin names, in the clear in the server's Certificate message, then only application data; the bearer token never in the clear"},
		{File: "pinned-tls13.pcap", KeyLog: "pinned-tls13.keylog", TLSVersion: "1.3", Expect: "with the key log, the certificate the pin names in the decrypted handshake and the GET of /api/interfaces carrying an Authorization header; without it, nothing past the ServerHello"},
		{File: "refused-pin.pcap", TLSVersion: "1.2", Expect: "the handshake refused by the client's pin: a ClientHello, the server's flight with the certificate, and the client's alert; no application data record and no change of cipher spec from the client"},
	}
	for i, data := range [][]byte{data12, data13, dataRefused} {
		if err := os.WriteFile(filepath.Join(dir, entries[i].File), data, 0o600); err != nil {
			t.Fatal(err)
		}
		entries[i].Pin, entries[i].Fingerprint, entries[i].KeyFingerprint, entries[i].Subject = pin, pin, devicetls.KeyFingerprint(parsed), parsed.Subject.String()
	}
	entries[2].Pin = devicetls.Fingerprint(stranger)
	if err := os.WriteFile(filepath.Join(dir, entries[1].KeyLog), secrets, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"written":  time.Now().UTC().Format(time.RFC3339),
		"note":     "the token is a throwaway made for the capture; the key log decrypts pinned-tls13.pcap and nothing else",
		"token":    strings.Repeat("*", len(captureToken)),
		"captures": entries,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote the captures to %s", dir)
}
