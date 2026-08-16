// The serving bundle, and the ownership rules that decide who may write over
// what.
//
// Two defects are pinned here and neither was visible from a passing build.
// The first is the reason the bundle exists at all: while the pair lived in
// two files it could not be published in one step, so writers had to be
// serialized, and every version of that serialization turned one slow or
// dead controller into every other controller refusing to start. The second
// is the one the bundle does not fix on its own: the check that decides
// whether this package may write was asked only of cert.pem, so a directory
// holding an operator's private key and no certificate answered "nothing to
// destroy" and the key was destroyed.
package tlscert_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// TestBundleIsOneFileTheStandardLibraryCanServe is the whole premise, checked
// against crypto/tls rather than against this package's own reader.
//
// The bundle carries a provenance block crypto/tls has never heard of. If the
// standard library ever stopped skipping unknown PEM blocks, every listener
// built from one of these files would fail, and a test that only used this
// package's parser would not notice.
func TestBundleIsOneFileTheStandardLibraryCanServe(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	cert := ensureOK(t, dir, tlscert.Options{})

	bundle := filepath.Join(dir, tlscert.BundleFileName)
	if cert.KeyFile != bundle {
		t.Errorf("KeyFile = %q, want the serving bundle %q", cert.KeyFile, bundle)
	}

	pair, err := tls.LoadX509KeyPair(bundle, bundle)
	if err != nil {
		t.Fatalf("the standard library cannot load the serving bundle: %v", err)
	}
	if !bytes.Equal(pair.Certificate[0], cert.Leaf.Raw) {
		t.Error("the certificate in the bundle is not the one Ensure returned")
	}

	// The certificate copy beside it must hold no key at all, because it is
	// the file operators are told to copy out to clients.
	raw, err := os.ReadFile(filepath.Join(dir, tlscert.CertFileName))
	if err != nil {
		t.Fatalf("reading cert.pem: %v", err)
	}
	if strings.Contains(string(raw), "PRIVATE KEY") {
		t.Error("cert.pem holds private key material, and it is the file an operator hands to clients")
	}
	if !bytes.Equal(raw, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Leaf.Raw})) {
		t.Error("cert.pem is not the certificate being served")
	}
}

// TestEnsureReturnsWhatIsReallyOnDisk pins the last step of the design: the
// final act is a LOAD, so the bytes handed to a listener were read back
// rather than assumed.
func TestEnsureReturnsWhatIsReallyOnDisk(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	got := ensureOK(t, dir, tlscert.Options{})

	bundle := filepath.Join(dir, tlscert.BundleFileName)
	onDisk, err := tls.LoadX509KeyPair(bundle, bundle)
	if err != nil {
		t.Fatalf("loading the published bundle: %v", err)
	}
	if !bytes.Equal(got.Pair.Certificate[0], onDisk.Certificate[0]) {
		t.Error("Ensure returned certificate bytes that are not the ones it published")
	}
	if got.Pair.PrivateKey == nil {
		t.Fatal("Ensure returned no private key, so a listener would have to re-read the file")
	}
}

// TestEnsureNeverDestroysAKeyItCannotAccountFor is the defect the ownership
// check used to have, stated as the operator sees it.
//
// ownsStoredCertificate asked its question of cert.pem alone and answered
// "yes, nothing to destroy" whenever that file was absent or unreadable. A
// directory holding an operator's private key and no certificate, which is
// exactly what a half-finished secret mount looks like, therefore had the key
// written over by a generated one. A private key exists in one place.
func TestEnsureNeverDestroysAKeyItCannotAccountFor(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	keyPath := filepath.Join(dir, tlscert.KeyFileName)
	planted := operatorKeyPEM(t)
	writeFile(t, keyPath, planted)

	_, err := tlscert.Ensure(dir, tlscert.Options{})
	if err == nil {
		t.Fatal("Ensure provisioned over a directory holding a private key it cannot account for")
	}
	// The message has to name the file and the way out, because the operator
	// who mounted half a secret is the only person who can finish it.
	for _, want := range []string{keyPath, "not written by this controller", "TLS_KEY_FILE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}

	// The point of the whole test: the bytes are untouched.
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("the operator's key is gone: %v", err)
	}
	if !bytes.Equal(after, planted) {
		t.Error("the operator's private key was modified")
	}
	// And nothing was published beside it either.
	if _, err := os.Stat(filepath.Join(dir, tlscert.BundleFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a serving bundle was published into a directory this package does not own (stat error %v)", err)
	}
}

// TestEnsureRetiresOnlyAKeyItCanProveItWrote is the control for the test
// above, and it is what keeps that rule from being "refuse forever".
//
// A key this package wrote is provable: it belongs to a certificate the
// provenance record lists. Such a key is dead material once its certificate
// is replaced, and leaving it behind means the private key is on disk twice
// and the second copy stops matching the cert.pem beside it.
func TestEnsureRetiresOnlyAKeyItCanProveItWrote(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	// The two-file layout, written by this package, with the certificate then
	// removed: only the key is left, and it is provably this package's.
	legacy, err := tlscert.Generate(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := os.Remove(legacy.CertFile); err != nil {
		t.Fatalf("removing the certificate: %v", err)
	}

	provisioned := ensureOK(t, dir, tlscert.Options{})
	if !provisioned.Generated {
		t.Fatal("Ensure refused to provision over its own leftovers")
	}
	if _, err := os.Stat(filepath.Join(dir, tlscert.KeyFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a key this package wrote and no longer uses was left on disk (stat error %v)", err)
	}
}

// TestEnsureRefusesToShadowAnOperatorsPairWithABundle covers the way the
// bundle could have quietly hijacked a directory.
//
// The bundle takes priority over cert.pem and key.pem, so publishing one into
// a directory holding an operator's own pair would leave their material on
// disk, untouched and never served again, which is worse than overwriting it
// because it looks like it is working.
func TestEnsureRefusesToShadowAnOperatorsPairWithABundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	planted := plantOperatorPair(t, dir, "operator.example.test", time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))

	got, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Ensure refused to start over an operator's own certificate rather than serving it: %v", err)
	}
	if got.Generated || got.SelfProvisioned {
		t.Fatal("Ensure provisioned a certificate of its own into a directory holding an operator's pair")
	}
	if !got.Leaf.Equal(planted) {
		t.Error("the material served is not the operator's own")
	}
	if _, err := os.Stat(filepath.Join(dir, tlscert.BundleFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a serving bundle was published beside an operator's pair, so their certificate would silently stop being served (stat error %v)", err)
	}
}

// TestEnsureDisownsABundleItDidNotWrite is the other half of the bundle's
// self-proving property.
//
// An operator who assembles their own certificate and key into one file and
// drops it here has produced something this package can serve and must never
// replace. The provenance block is what tells the two apart, and this is the
// case where it is absent.
func TestEnsureDisownsABundleItDidNotWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	// A pair that is entirely an operator's: no provenance record is written
	// beside it, and its only name is a real hostname, so every reuse rule
	// this package has wants it replaced.
	planted := plantOperatorPair(t, dir, "operator.example.test", time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	foldIntoBundle(t, dir)

	got, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Ensure refused to serve a bundle it did not write: %v", err)
	}
	if got.Generated {
		t.Fatal("Ensure replaced a bundle it cannot prove it wrote")
	}
	if !got.Leaf.Equal(planted) {
		t.Error("the certificate served is not the one in the bundle on disk")
	}
	if got.Warning == "" {
		t.Error("a controller serving material it will never renew said nothing about it")
	}
	// The bytes, because that is what was at risk.
	raw, err := os.ReadFile(filepath.Join(dir, tlscert.BundleFileName))
	if err != nil {
		t.Fatalf("reading the operator's bundle: %v", err)
	}
	if !bytes.Contains(raw, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: planted.Raw})) {
		t.Error("the operator's bundle was rewritten")
	}
}

// TestEnsureRecoversFromABundleWithADamagedCertificate is the recovery the
// rule above must not block.
//
// A bundle whose certificate block is damaged still holds a private key, and
// if that key belongs to a certificate this deployment provisioned then the
// whole file is this package's own leftovers. Treating it as an operator's
// would leave a controller refusing to start over a file it wrote itself,
// which is the class of failure this whole design exists to remove.
func TestEnsureRecoversFromABundleWithADamagedCertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	first := ensureOK(t, dir, tlscert.Options{})

	bundlePath := filepath.Join(dir, tlscert.BundleFileName)
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("reading the bundle: %v", err)
	}
	// The key kept and everything before it thrown away, which is what a
	// truncated copy or a damaged certificate block leaves.
	start := bytes.Index(raw, []byte("-----BEGIN EC PRIVATE KEY-----"))
	if start < 0 {
		t.Fatal("the bundle holds no key block, so this test proves nothing")
	}
	writeFile(t, bundlePath, raw[start:])

	healed := ensureOK(t, dir, tlscert.Options{})
	if !healed.Generated {
		t.Fatal("Ensure refused to replace a damaged bundle holding a key it wrote itself")
	}
	if healed.Leaf.Equal(first.Leaf) {
		t.Error("Ensure reported a new certificate but published the damaged one")
	}
	if _, err := tls.LoadX509KeyPair(bundlePath, bundlePath); err != nil {
		t.Errorf("the healed bundle does not load: %v", err)
	}
}

// foldIntoBundle turns a cert.pem/key.pem pair in dir into one serving.pem
// holding both, with no provenance block: what an operator assembling their
// own bundle by hand produces.
func foldIntoBundle(t *testing.T, dir string) {
	t.Helper()
	certPath := filepath.Join(dir, tlscert.CertFileName)
	keyPath := filepath.Join(dir, tlscert.KeyFileName)

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("reading %s: %v", certPath, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("reading %s: %v", keyPath, err)
	}
	writeFile(t, filepath.Join(dir, tlscert.BundleFileName), append(certPEM, keyPEM...))
	for _, path := range []string{certPath, keyPath} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("removing %s: %v", path, err)
		}
	}
}

// TestEnsureServesABundleItCannotRecord is the "never refuse to boot over an
// optimisation" rule, on the path the bundle made possible.
//
// The provenance record is unreadable, so a renewal cannot be recorded and
// therefore cannot be published. The certificate on disk is still perfectly
// good. Refusing to start would take a controller that would have worked and
// stop it; serving with a warning is the answer.
//
// It is the deliberate opposite of TestEnsureRefusesAnUnreadableProvenanceRecord,
// and the difference is which question the record is being asked. In the
// two-file layout it is the only evidence of who wrote the pair, so an
// unreadable record makes that unanswerable. A bundle carries its own answer,
// so an unreadable record costs only the history.
func TestEnsureServesABundleItCannotRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	// Two hours of life against a one-minute renewal window, so the first call
	// publishes and the second one is certain to want a replacement.
	stored, err := tlscert.Ensure(dir, tlscert.Options{TTL: 2 * time.Hour, RenewBefore: time.Minute})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// The provenance records cannot be written any more, because where they
	// live is now an ordinary file: a real enough shape (a botched restore, a
	// volume mounted a level too high) and the one that reaches this path as
	// root, where a directory's write bit means nothing.
	if err := os.RemoveAll(filepath.Join(dir, tlscert.ProvisionedDirName)); err != nil {
		t.Fatalf("removing the provenance records: %v", err)
	}
	writeFile(t, filepath.Join(dir, tlscert.ProvisionedDirName), []byte("not a directory"))

	got, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Ensure refused to start rather than serving the bundle it already had: %v", err)
	}
	if got.Generated {
		t.Error("Ensure published a certificate it could not record")
	}
	if !got.Leaf.Equal(stored.Leaf) {
		t.Error("Ensure returned a certificate other than the one on disk")
	}
	for _, want := range []string{"renewal window", "could not write"} {
		if !strings.Contains(got.Warning, want) {
			t.Errorf("the warning %q does not mention %q", got.Warning, want)
		}
	}
}

// TestEnsureRepairsAStaleCertificateCopy covers the one thing a lock used to
// buy that this design has to buy another way.
//
// Several controllers publishing at once can leave cert.pem holding a
// certificate a different racer published, because it is a copy rather than
// part of the atomic unit. Nothing depends on it (the bundle is what gets
// served, and the healthcheck trusts both), but an operator who copies it out
// as a trust anchor deserves the one that works, so any later start puts it
// back in step.
func TestEnsureRepairsAStaleCertificateCopy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	cert := ensureOK(t, dir, tlscert.Options{})

	// A certificate from an earlier generation in this same directory: the
	// exact shape a lost race leaves behind, and material this package really
	// did write, so the repair is not overwriting somebody else's file.
	stale, err := tlscert.Generate(filepath.Join(t.TempDir(), "other"), tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	staleCertPEM, err := os.ReadFile(stale.CertFile)
	if err != nil {
		t.Fatalf("reading the stale certificate: %v", err)
	}
	certPath := filepath.Join(dir, tlscert.CertFileName)
	writeFile(t, certPath, staleCertPEM)

	// A certificate this package never provisioned in THIS directory is not
	// its to rewrite, so the repair leaves it alone: the ownership rule wins
	// over the tidying.
	ensureOK(t, dir, tlscert.Options{})
	if raw, err := os.ReadFile(certPath); err != nil || !bytes.Equal(raw, staleCertPEM) {
		t.Error("the repair overwrote a certificate this package did not publish in this directory")
	}

	// Now the realistic case: the copy holds a certificate from an earlier
	// generation of THIS directory, which the provenance record lists.
	older, err := tlscert.Generate(filepath.Join(t.TempDir(), "older"), tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	recordCertificate(t, dir, older.Leaf)
	writeFile(t, certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: older.Leaf.Raw}))

	ensureOK(t, dir, tlscert.Options{})
	repaired, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("reading the repaired copy: %v", err)
	}
	if !bytes.Equal(repaired, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Leaf.Raw})) {
		t.Error("cert.pem still describes a certificate this controller is not serving")
	}
}

// TestAnchorsForDirTrustsTheBundle is what keeps the container healthcheck
// working now that the bundle, not cert.pem, is the file the listener was
// handed.
func TestAnchorsForDirTrustsTheBundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	cert := ensureOK(t, dir, tlscert.Options{})

	// The copy removed entirely, which is the state between a bundle being
	// published and its copy being written, and the state of a directory
	// where writing the copy failed.
	if err := os.Remove(filepath.Join(dir, tlscert.CertFileName)); err != nil {
		t.Fatalf("removing the certificate copy: %v", err)
	}

	anchors, err := tlscert.AnchorsForDir(dir)
	if err != nil {
		t.Fatalf("AnchorsForDir with only a bundle: %v", err)
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: anchors.Roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Errorf("a probe cannot verify the certificate the listener is serving: %v", err)
	}
	if anchors.ServerName != "localhost" {
		t.Errorf("ServerName = %q, want the served certificate's first DNS name", anchors.ServerName)
	}

	// And a directory holding neither is still a cold start rather than a
	// fault, because the container healthcheck turns that difference into two
	// different exit codes.
	if _, err := tlscert.AnchorsForDir(filepath.Join(t.TempDir(), "empty")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("AnchorsForDir on an empty directory = %v, want an error matching fs.ErrNotExist", err)
	}
}

// operatorKeyPEM is a private key with no certificate anywhere, exactly as a
// half-finished secret mount leaves one.
func operatorKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshalling the key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

// recordCertificate adds one certificate to a directory's provenance record,
// which is how a test says "this package published this here" without having
// to make it happen.
func recordCertificate(t *testing.T, dir string, cert *x509.Certificate) {
	t.Helper()
	path := filepath.Join(dir, tlscert.ProvisionedFileName)
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("reading %s: %v", path, err)
	}
	writeFile(t, path, append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), existing...))
}
