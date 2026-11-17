// Release gate for brownfield TLS through the real binary, against a real
// HTTPS API on loopback presenting real self-signed certificates the gate
// changes as a device does: a pin recorded on first use, confirmed, then
// verified; a key pin across a certificate issued again for the same key; a
// list of pins held through a rotation; and the old certificate refused once
// its pin is retired.
package main_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
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
)

// brownfieldAPI is an HTTPS API on loopback whose self-signed certificate
// the gate changes between commands, counting the requests that reached it
// at all (each carries the credential) and the authenticated ones that
// reached /api/interfaces.
type brownfieldAPI struct {
	*httptest.Server
	cert  atomic.Pointer[tls.Certificate]
	asked atomic.Int32
	hits  atomic.Int32
}

// newBrownfieldAPI starts one presenting cert, accepting the basic login
// api / api-secret.
func newBrownfieldAPI(t *testing.T, cert tls.Certificate) *brownfieldAPI {
	t.Helper()
	a := &brownfieldAPI{}
	a.cert.Store(&cert)
	a.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.asked.Add(1)
		if user, pass, ok := r.BasicAuth(); !ok || user != "api" || pass != "api-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/interfaces" {
			a.hits.Add(1)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	a.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*a.cert.Load()}}, nil
	}}
	a.StartTLS()
	t.Cleanup(a.Close)
	return a
}

// present makes a present cert from its next handshake.
func (a *brownfieldAPI) present(cert tls.Certificate) {
	a.cert.Store(&cert)
	a.CloseClientConnections()
}

// brownfieldCert issues a device's own self-signed certificate for
// 127.0.0.1 with key, valid for 90 days.
func brownfieldCert(t *testing.T, key *ecdsa.PrivateKey, serial int64) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "sw1.brownfield.invalid", Organization: []string{"Brownfield"}},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(gateLifetime),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, cert
}

// brownfieldAuthority is a certificate authority the gate's binary trusts
// through SSL_CERT_FILE, as a system root, and a certificate it issued for
// 127.0.0.1 a device presents.
func brownfieldAuthority(t *testing.T) (caFile string, leafTLS tls.Certificate, leaf *x509.Certificate) {
	t.Helper()
	caKey := brownfieldKey(t)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(100), Subject: pkix.Name{CommonName: "Brownfield Gate Root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(gateLifetime),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key := brownfieldKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(101), Subject: pkix.Name{CommonName: "sw2.brownfield.invalid"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(gateLifetime),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	if leaf, err = x509.ParseCertificate(der); err != nil {
		t.Fatal(err)
	}
	caFile = filepath.Join(t.TempDir(), "roots.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return caFile, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, leaf
}

// brownfieldKey makes a P-256 key.
func brownfieldKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// pleiadesOK runs pleiades in dir and fails the test unless it succeeds.
func pleiadesOK(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runPleiades(t, dir, args...)
	if err != nil {
		t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// pleiadesRefused runs pleiades in dir and fails the test unless it fails.
func pleiadesRefused(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runPleiades(t, dir, args...)
	if err == nil {
		t.Fatalf("pleiades %s succeeded, want a refusal:\n%s", strings.Join(args, " "), out)
	}
	return out
}

// TestCLI_BrownfieldTLSFromFirstUseToRetiredPin follows one self-signed
// switch through everything brownfield TLS does, each step a real command.
func TestCLI_BrownfieldTLSFromFirstUseToRetiredPin(t *testing.T) {
	oldKey := brownfieldKey(t)
	oldTLS, oldCert := brownfieldCert(t, oldKey, 1)
	newTLS, newCert := brownfieldCert(t, brownfieldKey(t), 2)
	againTLS, _ := brownfieldCert(t, oldKey, 3)
	oldPin, newPin := devicetls.Fingerprint(oldCert), devicetls.Fingerprint(newCert)
	api := newBrownfieldAPI(t, oldTLS)

	dir := t.TempDir()
	pleiadesOK(t, dir, "init")
	pleiadesOK(t, dir, "add-host", "sw1", "--type", "generic_http", "--set", "base_url="+api.URL+"/api", "--set", "http_auth=basic")
	if out, err := runPleiadesWithStdin(t, dir, "api-secret\n", "add-credential", "sw1", "--username", "api", "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	writeFile(t, dir, "runbooks/interfaces.yaml", "# Reads sw1's interfaces over its own API. Blast radius: one GET to sw1. Changes: nothing. Undo: none.\n"+
		"id: interfaces\nhosts: sw1\ntasks:\n  - name: read the interfaces\n    http.request:\n      url: /interfaces\n")

	// Not asked for: refused, naming both forms of the certificate.
	out := pleiadesRefused(t, dir, "onboard", "sw1")
	if !strings.Contains(out, oldPin) || !strings.Contains(out, devicetls.KeyFingerprint(oldCert)) {
		t.Errorf("the refusal does not name the certificate's two fingerprints:\n%s", out)
	}

	// Recorded on first use, listed for review, kept out of inventory.yaml.
	out = pleiadesOK(t, dir, "onboard", "sw1", "--tls-first-use", "--json")
	var res struct {
		State    string   `json:"state"`
		Warnings []string `json:"warnings"`
		FirstUse struct {
			Fingerprint    string `json:"fingerprint"`
			KeyFingerprint string `json:"key_fingerprint"`
		} `json:"tls_first_use"`
		Facts map[string]any `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("onboard --json printed %q: %v", out, err)
	}
	if res.State != "active" || res.FirstUse.Fingerprint != oldPin || res.Facts["tls_certificate_fingerprint"] != oldPin || !strings.Contains(strings.Join(res.Warnings, "\n"), "no person has confirmed") {
		t.Fatalf("onboard --tls-first-use: %s", out)
	}
	if inv, _ := os.ReadFile(filepath.Join(dir, "inventory.yaml")); strings.Contains(string(inv), "tls_first_use_fingerprint") { // #nosec G304 -- this test's own project
		t.Errorf("inventory.yaml holds the first-use pin:\n%s", inv)
	}

	// A run trusts it and says it is unconfirmed.
	out = pleiadesOK(t, dir, "run", "runbooks/interfaces.yaml")
	if api.hits.Load() != 1 || !strings.Contains(out, "WARNING:") || !strings.Contains(out, "no person has confirmed") {
		t.Errorf("a run under the first-use pin (hits %d):\n%s", api.hits.Load(), out)
	}

	// A confirmation is a comparison: another pin is refused, the recorded
	// one moves into tls_fingerprint, and runs stop calling it unconfirmed.
	pleiadesRefused(t, dir, "confirm-tls-pin", "sw1", newPin)
	pleiadesOK(t, dir, "confirm-tls-pin", "sw1", oldPin)
	if inv, _ := os.ReadFile(filepath.Join(dir, "inventory.yaml")); !strings.Contains(string(inv), "tls_fingerprint: "+oldPin) { // #nosec G304 -- this test's own project
		t.Errorf("the confirmed pin is not in inventory.yaml:\n%s", inv)
	}
	out = pleiadesOK(t, dir, "run", "runbooks/interfaces.yaml")
	if strings.Contains(out, "no person has confirmed") || !strings.Contains(out, "trusts one pinned certificate") || api.hits.Load() != 2 {
		t.Errorf("a run under the confirmed pin:\n%s", out)
	}

	// A key pin accepts the certificate issued again for the same key.
	pleiadesOK(t, dir, "set-host", "sw1", "--set", "tls_fingerprint="+devicetls.KeyFingerprint(oldCert))
	api.present(againTLS)
	pleiadesOK(t, dir, "onboard", "sw1")
	if out = pleiadesOK(t, dir, "run", "runbooks/interfaces.yaml"); !strings.Contains(out, "trusts any certificate for one pinned public key") {
		t.Errorf("a run under the key pin:\n%s", out)
	}

	// A rotation holds both certificate pins; each certificate is reached.
	api.present(oldTLS)
	pleiadesOK(t, dir, "set-host", "sw1", "--set", "tls_fingerprint="+oldPin+","+newPin)
	pleiadesOK(t, dir, "onboard", "sw1")
	api.present(newTLS)
	before := api.hits.Load()
	if out = pleiadesOK(t, dir, "run", "runbooks/interfaces.yaml"); !strings.Contains(out, "trusts 2 pins") || api.hits.Load() != before+1 {
		t.Errorf("a run during the rotation:\n%s", out)
	}
	if out = pleiadesOK(t, dir, "onboard", "sw1", "--json"); !strings.Contains(out, "pin "+oldPin+" does not match") {
		t.Errorf("onboarding after the rotation does not name the old pin to retire:\n%s", out)
	}

	// The old pin retired: the old certificate is refused before the
	// request, and its credential, reach anything presenting it.
	pleiadesOK(t, dir, "set-host", "sw1", "--set", "tls_fingerprint="+newPin)
	pleiadesOK(t, dir, "onboard", "sw1")
	api.present(oldTLS)
	before = api.hits.Load()
	out = pleiadesRefused(t, dir, "run", "runbooks/interfaces.yaml")
	if !strings.Contains(out, "not the pinned "+newPin) || api.hits.Load() != before {
		t.Errorf("the old certificate after its pin was retired (hits %d -> %d):\n%s", before, api.hits.Load(), out)
	}
}

// TestCLI_FirstUseNeverPinsACertificateThatReplacedAVerifiedOne: a switch
// onboarded while the system's roots vouched for its certificate (its record
// names no trust of its own) later presents a self-signed one, as a machine
// in the middle would. onboard --tls-first-use refuses it, naming both
// certificates and what a person does instead, records nothing, and sends
// the credential to nothing.
func TestCLI_FirstUseNeverPinsACertificateThatReplacedAVerifiedOne(t *testing.T) {
	roots, verifiedTLS, verified := brownfieldAuthority(t)
	env := map[string]string{"SSL_CERT_FILE": roots, "SSL_CERT_DIR": t.TempDir()}
	api := newBrownfieldAPI(t, verifiedTLS)
	dir := t.TempDir()
	pleiadesOK(t, dir, "init")
	pleiadesOK(t, dir, "add-host", "sw2", "--type", "generic_http", "--set", "base_url="+api.URL+"/api", "--set", "http_auth=basic")
	if out, err := runPleiadesWithStdin(t, dir, "api-secret\n", "add-credential", "sw2", "--username", "api", "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithEnv(t, dir, env, "onboard", "sw2"); err != nil {
		t.Fatalf("onboarding under the authority the system trusts: %v\n%s", err, out)
	}

	replacedTLS, replaced := brownfieldCert(t, brownfieldKey(t), 7)
	api.present(replacedTLS)
	asked := api.asked.Load()
	out, err := runPleiadesWithEnv(t, dir, env, "onboard", "sw2", "--tls-first-use")
	if err == nil {
		t.Fatalf("first use pinned the certificate that replaced a verified one:\n%s", out)
	}
	for _, want := range []string{devicetls.Fingerprint(verified), devicetls.Fingerprint(replaced), "never recorded on first use", "tls_fingerprint", "tls_ca_pem"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if api.asked.Load() != asked {
		t.Errorf("the replaced certificate was sent %d requests", api.asked.Load()-asked)
	}
	for _, name := range []string{"inventory.yaml", ".inventory-state.generated.yaml"} {
		if data, _ := os.ReadFile(filepath.Join(dir, name)); strings.Contains(string(data), "tls_first_use_fingerprint") { // #nosec G304 -- this test's own project
			t.Errorf("%s holds a first-use pin:\n%s", name, data)
		}
	}
}
