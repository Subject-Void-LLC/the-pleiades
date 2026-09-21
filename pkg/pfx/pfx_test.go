// Package pfx_test exercises the PKCS#12 decoder against bundles it builds
// itself.
//
// Encoding with the same library that reads them is a deliberate choice
// with a stated limit: it proves this package handles what a modern encoder
// produces and says nothing about bundles from other tooling, which is
// exactly the DER-only gap the package documentation records.
package pfx_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pfx"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// bundleFixture is a real PKCS#12 bundle and the material that went into
// it, so a test can assert the round trip rather than a shape.
type bundleFixture struct {
	base64Bundle string
	passphrase   string
	leaf         *x509.Certificate
	intermediate *x509.Certificate
}

// newBundle builds a real bundle with the same library that reads it.
//
// Encoding with the library under test is a deliberate choice and its limit
// is worth stating: it proves this package handles what a modern encoder
// produces, and it cannot prove anything about bundles from other tooling.
// The DER-only caveat in the package doc is exactly that gap, and no test
// here can close it.
func newBundle(t testing.TB, passphrase string, withChain bool) bundleFixture {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the authority key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "bundle authority"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("creating the authority certificate: %v", err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parsing the authority certificate: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "bundle leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("signing the leaf certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatalf("parsing the leaf certificate: %v", err)
	}

	var chain []*x509.Certificate
	if withChain {
		chain = []*x509.Certificate{ca}
	}
	der, err := pkcs12.Modern.Encode(leafKey, leaf, chain, passphrase)
	if err != nil {
		t.Fatalf("encoding the bundle: %v", err)
	}

	fixture := bundleFixture{
		base64Bundle: base64.StdEncoding.EncodeToString(der),
		passphrase:   passphrase,
		leaf:         leaf,
	}
	if withChain {
		fixture.intermediate = ca
	}
	return fixture
}

// TestABundleUnlocksIntoAUsableKeypair is the base case, and it asserts the
// result is usable rather than merely well shaped: crypto/tls has to accept
// the pair, because presenting it is the only thing it is for.
func TestABundleUnlocksIntoAUsableKeypair(t *testing.T) {
	t.Parallel()

	fixture := newBundle(t, "a-real-passphrase", false)

	certPEM, keyPEM, err := pfx.Decode(fixture.base64Bundle, fixture.passphrase)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("crypto/tls refused the unlocked pair: %v", err)
	}
	parsed, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parsing the unlocked certificate: %v", err)
	}
	if parsed.Subject.CommonName != fixture.leaf.Subject.CommonName {
		t.Errorf("unlocked %q, want %q", parsed.Subject.CommonName, fixture.leaf.Subject.CommonName)
	}
}

// TestTheWholeChainSurvives covers the case a bare leaf would fail: a
// server that needs an intermediate to build a path to its trusted root.
func TestTheWholeChainSurvives(t *testing.T) {
	t.Parallel()

	fixture := newBundle(t, "a-real-passphrase", true)

	certPEM, _, err := pfx.Decode(fixture.base64Bundle, fixture.passphrase)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	var blocks int
	rest := certPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			t.Errorf("the certificate PEM carries a %q block", block.Type)
		}
		blocks++
	}
	if blocks != 2 {
		t.Fatalf("the certificate PEM holds %d certificates, want the leaf and its issuer", blocks)
	}

	// Leaf first, which is the order crypto/tls requires. A chain in the
	// other order parses and then fails a handshake, so the ordering is the
	// assertion rather than the count.
	first, _ := pem.Decode(certPEM)
	parsed, err := x509.ParseCertificate(first.Bytes)
	if err != nil {
		t.Fatalf("parsing the first certificate: %v", err)
	}
	if parsed.Subject.CommonName != fixture.leaf.Subject.CommonName {
		t.Errorf("the first certificate is %q, want the leaf %q",
			parsed.Subject.CommonName, fixture.leaf.Subject.CommonName)
	}
}

// TestEveryUnopenableBundleIsRefusedAsOne proves the refusals exist and
// that they do not discriminate in a way a caller could use as an oracle.
func TestEveryUnopenableBundleIsRefusedAsOne(t *testing.T) {
	t.Parallel()

	good := newBundle(t, "a-real-passphrase", false)

	// A distinctive value rather than a short one. A single character
	// appears inside ordinary English words, so it would report a leak in
	// any error message long enough to contain it, which is a test that
	// fails for a reason unrelated to what it is checking.
	const probe = "passphrase-must-never-appear-in-an-error"

	for _, tt := range []struct {
		name       string
		bundle     string
		passphrase string
	}{
		{name: "empty", bundle: "", passphrase: probe},
		{name: "not base64", bundle: "this is not base64 !!!", passphrase: probe},
		{name: "base64 of nothing useful", bundle: base64.StdEncoding.EncodeToString([]byte("hello")), passphrase: probe},
		{name: "the wrong passphrase", bundle: good.base64Bundle, passphrase: probe},
		{name: "no passphrase at all", bundle: good.base64Bundle, passphrase: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := pfx.Decode(tt.bundle, tt.passphrase)
			if err == nil {
				t.Fatal("an unopenable bundle was opened")
			}
			if !errors.Is(err, pfx.ErrBundle) {
				t.Errorf("error = %v, want it to wrap ErrBundle", err)
			}
			if tt.passphrase != "" && strings.Contains(err.Error(), tt.passphrase) {
				t.Errorf("error = %v, want it not to carry the passphrase", err)
			}
			if strings.Contains(err.Error(), good.base64Bundle) {
				t.Errorf("error = %v, want it not to carry the bundle", err)
			}
		})
	}
}

// TestAnOversizedBundleIsRefusedBeforeItIsDecoded covers the bound, which
// exists because this input comes from a credential row somebody pasted
// into and ASN.1 parsing of such bytes is where this repository has been
// bitten before.
func TestAnOversizedBundleIsRefusedBeforeItIsDecoded(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("A", base64.StdEncoding.EncodedLen(pfx.MaxBundleBytes)+4)
	_, _, err := pfx.Decode(oversized, "x")
	if err == nil {
		t.Fatal("an oversized bundle was accepted")
	}
	if !errors.Is(err, pfx.ErrBundle) {
		t.Errorf("error = %v, want it to wrap ErrBundle", err)
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %v, want it to name the limit", err)
	}

	// The negative control: a legal bundle is nowhere near the bound, so
	// this test cannot pass by refusing everything.
	good := newBundle(t, "a-real-passphrase", true)
	if _, _, err := pfx.Decode(good.base64Bundle, good.passphrase); err != nil {
		t.Errorf("a legal bundle was refused: %v", err)
	}
}

// FuzzDecode drives the decoder with arbitrary bytes.
//
// AGENTS.md requires a fuzz target ahead of a Release Gate, and a decoder
// parsing untrusted ASN.1 is the exact shape that rule was written for.
// The property is termination with a classified outcome: Decode either
// returns a usable pair or an error wrapping ErrBundle, and never panics
// and never returns half an answer.
func FuzzDecode(f *testing.F) {
	fixture := newBundle(f, "a-real-passphrase", true)
	f.Add(fixture.base64Bundle, fixture.passphrase)
	f.Add(fixture.base64Bundle, "wrong")
	f.Add("", "")
	f.Add("!!!!", "x")
	f.Add(base64.StdEncoding.EncodeToString([]byte{0x30, 0x82, 0x00, 0x01}), "x")

	f.Fuzz(func(t *testing.T, bundle, passphrase string) {
		certPEM, keyPEM, err := pfx.Decode(bundle, passphrase)
		if err != nil {
			if !errors.Is(err, pfx.ErrBundle) {
				t.Errorf("error = %v, want every refusal to wrap ErrBundle", err)
			}
			if len(certPEM) != 0 || len(keyPEM) != 0 {
				t.Error("a refusal returned material anyway")
			}
			return
		}
		// A success has to be a success in full. Half a pair would be worse
		// than a refusal, because the caller would present something
		// unusable and read the handshake failure as a server problem.
		if len(certPEM) == 0 || len(keyPEM) == 0 {
			t.Error("Decode succeeded and returned an empty certificate or key")
		}
		if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
			t.Errorf("Decode succeeded but crypto/tls refused the pair: %v", err)
		}
	})
}

// BenchmarkDecode measures one unlock.
//
// It matters because this runs once per task on the dispatch path, inside
// the per-task child, with a device fan-out waiting behind it. The figure
// to watch is whether key derivation makes an unlock comparable to the
// connection it precedes.
func BenchmarkDecode(b *testing.B) {
	fixture := newBundle(b, "a-real-passphrase", true)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := pfx.Decode(fixture.base64Bundle, fixture.passphrase); err != nil {
			b.Fatalf("Decode() error = %v", err)
		}
	}
}
