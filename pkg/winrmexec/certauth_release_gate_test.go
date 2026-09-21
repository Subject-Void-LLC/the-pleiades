// Package winrmexec_test holds the half of the WinRM client certificate
// Release Gate that runs anywhere.
//
// What it proves, what it deliberately cannot, and why the gate is split in
// two at all is set out in full below the import block.
package winrmexec_test

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
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// This file is the Release Gate for WinRM client certificate
// authentication, and it is deliberately in two halves living in two
// places. This is the half that runs anywhere.
//
// # What this half proves, and what it cannot
//
// It proves the security property the feature is actually about: that this
// platform presents a client certificate to a peer that demands one, that a
// real TLS stack verifies it against a real authority, and that withholding
// it fails. Everything in the path is real, not a stand-in for itself. The
// transport is this package's own certificateTransport, selected by the
// shipped newClient through the shipped Options, and the server is a real
// Go TLS listener configured with RequireAndVerifyClientCert.
//
// It named masterzen/winrm's ClientAuthRequest until that transport was
// replaced (see certtransport.go for why), and the sentence outlived the
// code by one commit. Worth keeping the correction visible: a gate whose
// header names the wrong transport invites somebody to conclude the
// replacement is ungated, or to restore the library's version believing
// this test covers it.
//
// It does NOT prove that Windows maps the certificate to an account and
// runs the command, because the peer here is a TLS server rather than a
// WinRM service. That is what the other half is for
// (cmd/pleiades/winrm_certificate_release_gate_test.go), which needs a real
// Windows host and skips without one.
//
// Stating the split rather than writing one gate is the honest option.
// There is no Windows container a Linux box can run, and faking a WinRM
// service would mean faking the thing under test, which is the same
// argument cmd/pleiades/winrm_static_ip_release_gate_test.go already makes
// for itself.
//
// # The negative control is the deliverable
//
// Without the refusal half, a passing test cannot tell certificate
// authentication from a server that was accepting anything put in front of
// it. LESSONS_LEARNED #95 is the rule; this is it applied.

// gateAuthority is a throwaway certificate authority and the material it
// issues, built fresh per test.
//
// Generated rather than embedded because an embedded certificate expires,
// and a gate that starts failing on a date nobody chose is worse than one
// that costs a few milliseconds of key generation.
type gateAuthority struct {
	caPEM           []byte
	serverCert      tls.Certificate
	clientCertPEM   []byte
	clientKeyPEM    []byte
	clientSerial    *big.Int
	strangerCertPEM []byte
	strangerKeyPEM  []byte
}

// newGateAuthority builds a certificate authority, a server certificate for
// 127.0.0.1, a client certificate this authority signed, and a second
// client certificate signed by a DIFFERENT authority.
//
// The stranger exists so the gate can tell "the server demanded a
// certificate" apart from "the server verified the certificate". Those are
// different properties and only one of them is interesting.
func newGateAuthority(t *testing.T) gateAuthority {
	t.Helper()

	caKey, caCert, caDER := issueAuthority(t, "pleiades release gate CA")
	strangerKey, strangerCA, _ := issueAuthority(t, "some other CA")

	serverCertPEM, serverKeyPEM, _ := issueLeaf(t, caCert, caKey, "127.0.0.1",
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, net.ParseIP("127.0.0.1"))
	serverPair, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("building the server keypair: %v", err)
	}

	clientCertPEM, clientKeyPEM, clientSerial := issueLeaf(t, caCert, caKey, "pleiades-runner",
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil)
	strangerCertPEM, strangerKeyPEM, _ := issueLeaf(t, strangerCA, strangerKey, "an-impostor",
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil)

	return gateAuthority{
		caPEM:           pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		serverCert:      serverPair,
		clientCertPEM:   clientCertPEM,
		clientKeyPEM:    clientKeyPEM,
		clientSerial:    clientSerial,
		strangerCertPEM: strangerCertPEM,
		strangerKeyPEM:  strangerKeyPEM,
	}
}

// issueAuthority creates a self-signed certificate authority.
func issueAuthority(t *testing.T, name string) (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the authority key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating the authority certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the authority certificate: %v", err)
	}
	return key, parsed, der
}

// issueLeaf signs one end-entity certificate with the given authority.
func issueLeaf(
	t *testing.T,
	ca *x509.Certificate,
	caKey *ecdsa.PrivateKey,
	commonName string,
	usage []x509.ExtKeyUsage,
	ip net.IP,
) (certPEM, keyPEM []byte, serial *big.Int) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the leaf key: %v", err)
	}
	serial = big.NewInt(time.Now().UnixNano())
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usage,
	}
	if ip != nil {
		template.IPAddresses = []net.IP{ip}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("signing the leaf certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling the leaf key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		serial
}

// mutualTLSListener is a real TLS server that demands and verifies a client
// certificate, and records what it was shown.
type mutualTLSListener struct {
	server *httptest.Server
	mu     sync.Mutex
	seen   []*x509.Certificate
}

// startMutualTLSListener starts a server requiring a verified client
// certificate signed by the given authority.
func startMutualTLSListener(t *testing.T, ca gateAuthority) *mutualTLSListener {
	t.Helper()

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.caPEM) {
		t.Fatal("the authority did not parse into a pool, so nothing below would mean anything")
	}

	listener := &mutualTLSListener{}
	listener.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		listener.mu.Lock()
		if r.TLS != nil {
			listener.seen = append(listener.seen, r.TLS.PeerCertificates...)
		}
		listener.mu.Unlock()
		// The handshake is what this gate is about. A WS-Man fault is a
		// truthful answer from something that is not a WinRM service, and
		// the client reporting it as a failure is expected.
		w.WriteHeader(http.StatusInternalServerError)
	}))
	listener.server.TLS = &tls.Config{
		Certificates: []tls.Certificate{ca.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	}
	listener.server.StartTLS()
	t.Cleanup(listener.server.Close)
	return listener
}

// port returns the ephemeral port the listener bound.
func (l *mutualTLSListener) port(t *testing.T) int {
	t.Helper()

	_, portText, err := net.SplitHostPort(l.server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("reading the listener address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parsing the listener port: %v", err)
	}
	return port
}

// presented returns the certificates the server was shown.
func (l *mutualTLSListener) presented() []*x509.Certificate {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*x509.Certificate(nil), l.seen...)
}

// TestReleaseGate_TheCertificateIsPresentedAndVerified is the gate.
//
// Three acts, and the second and third are what make the first mean
// anything: the certificate is presented and accepted, withholding it is
// refused, and a certificate from an authority the server does not trust is
// refused as well.
func TestReleaseGate_TheCertificateIsPresentedAndVerified(t *testing.T) {
	ca := newGateAuthority(t)
	listener := startMutualTLSListener(t, ca)
	target := winrmexec.Target{Host: "127.0.0.1", Port: listener.port(t)}

	// Act one: the certificate is presented, and the server verifies it.
	//
	// Run returns an error because the peer is a TLS server rather than a
	// WinRM service, which is expected and is not what is being asserted.
	// What is asserted is what the SERVER saw, which is the only place the
	// presentation is observable.
	_, _ = winrmexec.Run(context.Background(), target,
		winrmexec.Auth{CertificatePEM: ca.clientCertPEM, PrivateKeyPEM: ca.clientKeyPEM},
		winrmexec.ShellPowerShell, "hostname",
		winrmexec.Options{CACert: ca.caPEM, Timeout: 10 * time.Second})

	presented := listener.presented()
	if len(presented) == 0 {
		t.Fatal("the server was shown no client certificate, so this platform presented none")
	}
	if presented[0].SerialNumber.Cmp(ca.clientSerial) != 0 {
		t.Errorf("the server was shown serial %v, want the client certificate's %v",
			presented[0].SerialNumber, ca.clientSerial)
	}
	if presented[0].Subject.CommonName != "pleiades-runner" {
		t.Errorf("the server was shown %q, want the client certificate", presented[0].Subject.CommonName)
	}

	// Act two: withholding the certificate is refused.
	//
	// The same package, the same server, password authentication instead.
	// Without this act, act one could not tell a verifying server from one
	// accepting anything.
	refusing := startMutualTLSListener(t, ca)
	refusingTarget := winrmexec.Target{Host: "127.0.0.1", Port: refusing.port(t)}
	_, err := winrmexec.Run(context.Background(), refusingTarget,
		winrmexec.Auth{Username: "administrator", Password: "hunter2"},
		winrmexec.ShellPowerShell, "hostname",
		winrmexec.Options{HTTPS: true, CACert: ca.caPEM, Timeout: 10 * time.Second})
	if err == nil {
		t.Fatal("a session with no client certificate succeeded against a server that requires one")
	}
	if got := refusing.presented(); len(got) != 0 {
		t.Errorf("the server recorded %d certificates from a client that presented none", len(got))
	}

	// Act three: a certificate from an authority the server does not trust
	// does not produce a session either.
	//
	// The mechanism is worth recording, because it is not the one the act
	// looks like it is testing and a later reader would otherwise draw the
	// wrong conclusion from it. Verified by running it: the server advertises
	// its acceptable authorities in the CertificateRequest, the Go client
	// finds that none of its certificates is covered by that list and sends
	// an empty certificate rather than an untrusted one, and the server then
	// refuses for lack of a certificate ("remote error: tls: certificate
	// required"). So the rejection happens on the client side first and the
	// server side second.
	//
	// The act is kept because the property it asserts is the one that
	// matters and holds either way: possessing a certificate is not enough,
	// it has to be one this peer's authority issued. What it does not do is
	// exercise the server's signature verification, since nothing reaches
	// it.
	stranger := startMutualTLSListener(t, ca)
	strangerTarget := winrmexec.Target{Host: "127.0.0.1", Port: stranger.port(t)}
	_, err = winrmexec.Run(context.Background(), strangerTarget,
		winrmexec.Auth{CertificatePEM: ca.strangerCertPEM, PrivateKeyPEM: ca.strangerKeyPEM},
		winrmexec.ShellPowerShell, "hostname",
		winrmexec.Options{CACert: ca.caPEM, Timeout: 10 * time.Second})
	if err == nil {
		t.Fatal("a certificate from an untrusted authority was accepted")
	}
	if got := stranger.presented(); len(got) != 0 {
		t.Errorf("an untrusted certificate reached the handler: %d recorded", len(got))
	}
}

// TestReleaseGate_ABundlePresentsTheSameCertificateAsThePEMPath is the
// assertion that ties the PKCS#12 decoder back to the path it feeds.
//
// The decoder was built last on purpose, as an input adapter into a
// certificate path that already worked. This is what makes that claim
// checkable rather than merely stated: the SAME certificate, supplied once
// as a PEM pair and once as a sealed bundle unlocked by its passphrase,
// has to reach the far end identically. If the two disagree, the adapter is
// not an adapter.
func TestReleaseGate_ABundlePresentsTheSameCertificateAsThePEMPath(t *testing.T) {
	ca := newGateAuthority(t)
	listener := startMutualTLSListener(t, ca)
	target := winrmexec.Target{Host: "127.0.0.1", Port: listener.port(t)}

	// Seal exactly the material the PEM path uses, so any difference at the
	// far end is the decoder's doing and nothing else's.
	const passphrase = "a-real-bundle-passphrase"
	bundle := sealBundle(t, ca.clientCertPEM, ca.clientKeyPEM, passphrase)

	// Through AuthFromSecrets, because that is where a Collection method
	// gets its credential and therefore where the unlock really happens.
	auth, err := winrmexec.AuthFromSecrets(map[string]string{
		wire.SecretPFXBase64:  bundle,
		wire.SecretPassphrase: passphrase,
	})
	if err != nil {
		t.Fatalf("AuthFromSecrets() on a sealed bundle error = %v", err)
	}

	_, _ = winrmexec.Run(context.Background(), target, auth,
		winrmexec.ShellPowerShell, "hostname",
		winrmexec.Options{CACert: ca.caPEM, Timeout: 10 * time.Second})

	presented := listener.presented()
	if len(presented) == 0 {
		t.Fatal("the server was shown no certificate, so the unlocked bundle presented none")
	}
	if presented[0].SerialNumber.Cmp(ca.clientSerial) != 0 {
		t.Errorf("the bundle presented serial %v, want the same certificate the PEM path presents, %v",
			presented[0].SerialNumber, ca.clientSerial)
	}

	// The negative control: the wrong passphrase must not open it. Without
	// this, the test above would pass against a decoder that ignored the
	// passphrase entirely.
	if _, err := winrmexec.AuthFromSecrets(map[string]string{
		wire.SecretPFXBase64:  bundle,
		wire.SecretPassphrase: "not-the-passphrase",
	}); err == nil {
		t.Error("a bundle opened with the wrong passphrase")
	}
}

// sealBundle re-encodes a PEM certificate and key as a base64 PKCS#12
// bundle, so a test can supply one identity both ways.
func sealBundle(t *testing.T, certPEM, keyPEM []byte, passphrase string) string {
	t.Helper()

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		t.Fatal("the certificate PEM did not decode")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("parsing the certificate: %v", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("the key PEM did not decode")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatalf("parsing the key: %v", err)
	}

	der, err := pkcs12.Modern.Encode(key, cert, nil, passphrase)
	if err != nil {
		t.Fatalf("sealing the bundle: %v", err)
	}
	return base64.StdEncoding.EncodeToString(der)
}
