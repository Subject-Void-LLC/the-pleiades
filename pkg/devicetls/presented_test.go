// Tests for recording the certificate a device presented, on real
// handshakes over loopback: a completed one is recorded once however often
// it is seen; a refused one never; one from a server replaying a trusted
// certificate without its key, which passes the certificate check and fails
// the key's proof, never; and nothing is recorded for a client built outside
// a context that asks.
package devicetls_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// getWith fetches srv's root with cfg through a transport recording on seen
// (nil records nothing), returning the request's error.
func getWith(t *testing.T, cfg *tls.Config, seen *devicetls.Presented, srv *httptest.Server) error {
	t.Helper()
	client := &http.Client{Transport: seen.Transport(&http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}), Timeout: 30 * time.Second}
	resp, err := client.Get(srv.URL)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// serveWithoutTheKey starts a TLS server presenting m's certificate but
// holding another key, as a server replaying a certificate it captured
// does: the client's certificate check passes, and the key's proof (TLS 1.3's
// CertificateVerify, TLS 1.2's signed key exchange) fails. maxVersion picks
// the protocol.
func serveWithoutTheKey(t *testing.T, m minted, maxVersion uint16) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: maxVersion, Certificates: []tls.Certificate{{Certificate: [][]byte{m.der}, PrivateKey: newKey(t)}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// TestPresented_RecordsEachCompletedHandshakeOnce: a pinned configuration
// and one verifying against a pinned authority each record the certificate
// of every handshake that completed, each distinct certificate once; a
// refused handshake records nothing; and a client with no Presented records
// nothing and is given no wrapper.
func TestPresented_RecordsEachCompletedHandshakeOnce(t *testing.T) {
	a := mint(t, newKey(t), 1, "127.0.0.1", time.Now().Add(time.Hour))
	b := mint(t, newKey(t), 2, "127.0.0.1", time.Now().Add(time.Hour))
	srvA, srvB := serveMinted(t, a), serveMinted(t, b)

	ctx, seen := devicetls.WithPresented(context.Background())
	if devicetls.PresentedFrom(ctx) != seen || devicetls.PresentedFrom(context.Background()) != nil {
		t.Fatal("PresentedFrom does not return what WithPresented put on the context")
	}
	cfg, err := pinning(t, []any{devicetls.Fingerprint(a.cert), devicetls.Fingerprint(b.cert)}).ConfigContext(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, srv := range []*httptest.Server{srvA, srvA, srvB} {
		if err := getWith(t, cfg, seen, srv); err != nil {
			t.Fatal(err)
		}
	}
	certs, more := seen.Certificates()
	if len(certs) != 2 || more || !certs[0].Equal(a.cert) || !certs[1].Equal(b.cert) {
		t.Errorf("seen %d certificates (more %v), want a then b once each", len(certs), more)
	}

	_, refusedSeen := devicetls.WithPresented(context.Background())
	if err := getWith(t, pinnedConfig(t, devicetls.Fingerprint(a.cert)), refusedSeen, srvB); err == nil {
		t.Fatal("a certificate no pin matches was trusted")
	}
	if certs, _ := refusedSeen.Certificates(); len(certs) != 0 {
		t.Errorf("a refused handshake recorded %d certificates", len(certs))
	}

	// A pinned authority: Go verifies the chain, and the completed handshake
	// is recorded.
	authority := mint(t, newKey(t), 3, "127.0.0.1", time.Now().Add(time.Hour))
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: authority.der}))
	caSettings, err := parse(map[string]inventory.PropertyValue{devicetls.CAPEMProperty: caPEM})
	if err != nil {
		t.Fatal(err)
	}
	caCfg, err := caSettings.Config(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, caSeen := devicetls.WithPresented(context.Background())
	if err := getWith(t, caCfg, caSeen, serveMinted(t, authority)); err != nil {
		t.Fatalf("the pinned authority's own certificate was refused: %v", err)
	}
	if certs, _ := caSeen.Certificates(); len(certs) != 1 || !certs[0].Equal(authority.cert) {
		t.Errorf("a chain-verified handshake recorded %d certificates", len(certs))
	}

	var none *devicetls.Presented
	inner := &http.Transport{}
	if none.Transport(inner) != http.RoundTripper(inner) {
		t.Error("a nil Presented wrapped the transport")
	}
	if certs, more := none.Certificates(); certs != nil || more {
		t.Error("a nil Presented holds certificates")
	}
	none.Record(tls.ConnectionState{HandshakeComplete: true, PeerCertificates: []*x509.Certificate{a.cert}})
}

// TestPresented_ACertificateReplayedWithoutItsKeyIsNeverRecorded: a server
// presenting a certificate the client trusts while holding another key
// passes the client's certificate check, and Go calls VerifyConnection
// there, before the key's proof fails; nothing is recorded from it, over
// TLS 1.3 or 1.2, and the real device's handshake that follows leaves its
// one certificate, never two.
func TestPresented_ACertificateReplayedWithoutItsKeyIsNeverRecorded(t *testing.T) {
	for name, version := range map[string]uint16{"1.3": tls.VersionTLS13, "1.2": tls.VersionTLS12} {
		t.Run(name, func(t *testing.T) {
			replayed := mint(t, newKey(t), 1, "127.0.0.1", time.Now().Add(time.Hour))
			genuine := mint(t, newKey(t), 2, "127.0.0.1", time.Now().Add(time.Hour))
			cfg := pinnedConfig(t, []any{devicetls.Fingerprint(replayed.cert), devicetls.Fingerprint(genuine.cert)})
			_, seen := devicetls.WithPresented(context.Background())
			if err := getWith(t, cfg, seen, serveWithoutTheKey(t, replayed, version)); err == nil {
				t.Fatal("a server without the certificate's key completed the handshake")
			}
			if certs, _ := seen.Certificates(); len(certs) != 0 {
				t.Fatalf("a handshake whose server never proved the key recorded %d certificates", len(certs))
			}
			if err := getWith(t, cfg, seen, serveMinted(t, genuine)); err != nil {
				t.Fatal(err)
			}
			if certs, _ := seen.Certificates(); len(certs) != 1 || !certs[0].Equal(genuine.cert) {
				t.Errorf("after the real device's handshake %d certificates are recorded, want its one", len(certs))
			}
		})
	}
}

// pinnedConfig is the client configuration of settings pinning pins.
func pinnedConfig(t *testing.T, pins inventory.PropertyValue) *tls.Config {
	t.Helper()
	cfg, err := pinning(t, pins).Config(nil)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestPresented_KeepsABoundedNumber: a server presenting a new certificate
// on every connection fills the record to its bound and says more were
// seen, rather than growing it; a handshake that did not complete adds
// nothing.
func TestPresented_KeepsABoundedNumber(t *testing.T) {
	_, seen := devicetls.WithPresented(context.Background())
	seen.Record(tls.ConnectionState{PeerCertificates: []*x509.Certificate{mint(t, newKey(t), 99, "x", time.Now().Add(time.Hour)).cert}})
	for i := 0; i < 10; i++ {
		seen.Record(tls.ConnectionState{HandshakeComplete: true, PeerCertificates: []*x509.Certificate{mint(t, newKey(t), int64(i+1), "x", time.Now().Add(time.Hour)).cert}})
	}
	certs, more := seen.Certificates()
	if len(certs) != 8 || !more {
		t.Errorf("kept %d certificates (more %v), want the bound of 8 and more", len(certs), more)
	}
	if certs[0].SerialNumber.Int64() == 99 {
		t.Error("a handshake that never completed was recorded")
	}
}

// TestDescribe_KeepsBothPinFormsAndBoundsTheNames: a description holds both
// of a pin's forms and the last moment of validity, and a device's own
// names lose every control and format character and are cut at their bound.
func TestDescribe_KeepsBothPinFormsAndBoundsTheNames(t *testing.T) {
	key := newKey(t)
	// An escape sequence and a right-to-left override (U+202E, written as its
	// UTF-8 bytes so this file holds no invisible character) in the subject.
	hostile := "dev\x1b[31m\xe2\x80\xae.lab.invalid"
	cert := namedCert(t, key, hostile, time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	d := devicetls.Describe(cert)
	if d.Fingerprint != devicetls.Fingerprint(cert) || d.KeyFingerprint != devicetls.KeyFingerprint(cert) || d.NotAfterText() != "2030-01-02T03:04:05Z" {
		t.Errorf("description %+v", d)
	}
	if strings.Contains(d.Subject, "\x1b") || strings.Contains(d.Subject, "\xe2\x80\xae") || !strings.Contains(d.Subject, "dev[31m.lab.invalid") {
		t.Errorf("subject %q kept a control or format character", d.Subject)
	}
	if got := devicetls.Describe(namedCert(t, key, strings.Repeat("\xc3\xa9", 400), time.Now().Add(time.Hour))).Subject; len(got) > 512 || !strings.HasSuffix(got, "\xc3\xa9") {
		t.Errorf("a long subject was kept as %d bytes, or cut inside a character", len(got))
	}
}

// namedCert issues a self-signed certificate whose subject's common name is
// cn, naming no host, as a device's own certificate may.
func namedCert(t *testing.T, key *ecdsa.PrivateKey, cn string, notAfter time.Time) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
