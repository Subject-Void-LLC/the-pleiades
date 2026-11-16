// Tests for recording the certificate a device presented, on real
// handshakes over loopback: a verified one is recorded once however often
// it is seen, a refused one never, and nothing is recorded or wrapped for a
// configuration built outside a context that asks.
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

// getWith fetches srv's root with cfg, returning the request's error.
func getWith(t *testing.T, cfg *tls.Config, srv *httptest.Server) error {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}, Timeout: 30 * time.Second}
	resp, err := client.Get(srv.URL)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// TestPresented_RecordsEachVerifiedCertificateOnce: a pinned configuration
// and one verifying against a pinned authority each record the certificate
// of every handshake they verify, each distinct certificate once; a refused
// handshake records nothing; and a configuration built with no Presented on
// its context records nothing and keeps Go's own verification as it was.
func TestPresented_RecordsEachVerifiedCertificateOnce(t *testing.T) {
	a := mint(t, newKey(t), 1, "127.0.0.1", time.Now().Add(time.Hour))
	b := mint(t, newKey(t), 2, "127.0.0.1", time.Now().Add(time.Hour))
	srvA, srvB := serveMinted(t, a), serveMinted(t, b)

	ctx, seen := devicetls.WithPresented(context.Background())
	cfg, err := pinning(t, []any{devicetls.Fingerprint(a.cert), devicetls.Fingerprint(b.cert)}).ConfigContext(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, srv := range []*httptest.Server{srvA, srvA, srvB} {
		if err := getWith(t, cfg, srv); err != nil {
			t.Fatal(err)
		}
	}
	certs, more := seen.Certificates()
	if len(certs) != 2 || more || !certs[0].Equal(a.cert) || !certs[1].Equal(b.cert) {
		t.Errorf("seen %d certificates (more %v), want a then b once each", len(certs), more)
	}

	refusedCtx, refusedSeen := devicetls.WithPresented(context.Background())
	refusing, err := pinning(t, devicetls.Fingerprint(a.cert)).ConfigContext(refusedCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := getWith(t, refusing, srvB); err == nil {
		t.Fatal("a certificate no pin matches was trusted")
	}
	if certs, _ := refusedSeen.Certificates(); len(certs) != 0 {
		t.Errorf("a refused handshake recorded %d certificates", len(certs))
	}

	// A pinned authority: Go verifies the chain, then the record is made.
	authority := mint(t, newKey(t), 3, "127.0.0.1", time.Now().Add(time.Hour))
	caSrv := serveMinted(t, authority)
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: authority.der}))
	caSettings, err := parse(map[string]inventory.PropertyValue{devicetls.CAPEMProperty: caPEM})
	if err != nil {
		t.Fatal(err)
	}
	caCtx, caSeen := devicetls.WithPresented(context.Background())
	caCfg, err := caSettings.ConfigContext(caCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := getWith(t, caCfg, caSrv); err != nil {
		t.Fatalf("the pinned authority's own certificate was refused: %v", err)
	}
	if certs, _ := caSeen.Certificates(); len(certs) != 1 || !certs[0].Equal(authority.cert) {
		t.Errorf("a chain-verified handshake recorded %d certificates", len(certs))
	}

	plain, err := caSettings.ConfigContext(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain.VerifyConnection != nil {
		t.Error("a configuration built with no Presented on its context was given a verification hook")
	}
}

// TestPresented_KeepsABoundedNumber: a server presenting a new certificate
// on every connection fills the record to its bound and says more were
// seen, rather than growing it.
func TestPresented_KeepsABoundedNumber(t *testing.T) {
	_, seen := devicetls.WithPresented(context.Background())
	for i := 0; i < 10; i++ {
		devicetls.Observe(seen, mint(t, newKey(t), int64(i+1), "x", time.Now().Add(time.Hour)).cert)
	}
	if certs, more := seen.Certificates(); len(certs) != 8 || !more {
		t.Errorf("kept %d certificates (more %v), want the bound of 8 and more", len(certs), more)
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
