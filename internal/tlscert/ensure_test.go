// The proof that Ensure keeps the same certificate across restarts, and
// replaces it exactly when what is stored cannot be served.
//
// Reuse is the behavior worth testing hardest. A controller that minted a
// fresh certificate on every start would retrain every operator to click
// through browser warnings and would break any client that pinned the
// previous one, and nothing about that failure is visible from a single
// run: it looks perfect every time and is wrong the second time.
package tlscert_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// ensureOK calls Ensure and fails the test on an error, since every test
// below cares about the certificate rather than the error path.
func ensureOK(t *testing.T, dir string, opts tlscert.Options) tlscert.ServingCert {
	t.Helper()
	cert, err := tlscert.Ensure(dir, opts)
	if err != nil {
		t.Fatalf("Ensure(%s): %v", dir, err)
	}
	return cert
}

// TestEnsureGeneratesOnceThenReusesIt is the whole point of the function.
func TestEnsureGeneratesOnceThenReusesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	first := ensureOK(t, dir, tlscert.Options{})
	if !first.Generated {
		t.Fatal("the first Ensure on an empty directory reported a reused certificate")
	}

	second := ensureOK(t, dir, tlscert.Options{})
	if second.Generated {
		t.Error("the second Ensure generated a new certificate instead of reusing the stored one")
	}
	// The bytes, not the paths: two certificates written to the same two
	// paths are still two certificates, and the paths would match either
	// way.
	if !bytes.Equal(first.Leaf.Raw, second.Leaf.Raw) {
		t.Error("the second Ensure returned a different certificate than the one on disk")
	}
	// A client that pinned the first certificate still trusts the second,
	// which is the promise reuse actually makes to an operator.
	if !second.Leaf.Equal(first.Leaf) {
		t.Error("the reused certificate is not the one a client would have pinned")
	}
}

// TestEnsureReplacesACertificateInsideItsRenewalWindow proves the renewal
// path runs at all. Without it, a persisted certificate would be reused
// until the day it expired and then fail a handshake, which reads like a
// configuration mistake rather than an expiry.
func TestEnsureReplacesACertificateInsideItsRenewalWindow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	// An hour of validity, written directly rather than through Ensure, so
	// the stored certificate is already deep inside the thirty-day renewal
	// window the call below asks for.
	stored, err := tlscert.Generate(dir, tlscert.Options{TTL: time.Hour})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	renewed := ensureOK(t, dir, tlscert.Options{})
	if !renewed.Generated {
		t.Fatal("Ensure reused a certificate that expires inside its renewal window")
	}
	if bytes.Equal(stored.Leaf.Raw, renewed.Leaf.Raw) {
		t.Error("Ensure reported a new certificate but left the old one on disk")
	}
	if !renewed.Leaf.NotAfter.After(stored.Leaf.NotAfter) {
		t.Errorf("the replacement expires at %s, no later than the certificate it replaced (%s)",
			renewed.Leaf.NotAfter, stored.Leaf.NotAfter)
	}
}

// TestEnsureReplacesACertificateMissingARequestedName is what makes adding
// a hostname to the configuration take effect.
//
// Without this check the stored certificate is still valid, still
// unexpired, and still wrong, and the browser rejects it for a name
// mismatch that looks identical to the problem serving TLS was turned on
// to fix.
func TestEnsureReplacesACertificateMissingARequestedName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	first := ensureOK(t, dir, tlscert.Options{})
	if err := first.Leaf.VerifyHostname("controller.example.test"); err == nil {
		t.Fatal("the first certificate already covers the name this test adds, so it proves nothing")
	}

	// An address as well as a name, because the two live in different SAN
	// fields and a check that only compared one of them would reuse a
	// certificate that is missing the other.
	wanted := []string{"controller.example.test", "10.9.8.7"}

	second := ensureOK(t, dir, tlscert.Options{ExtraNames: wanted})
	if !second.Generated {
		t.Fatal("Ensure reused a certificate that does not carry the requested names")
	}
	for _, name := range wanted {
		if err := second.Leaf.VerifyHostname(name); err != nil {
			t.Errorf("the replacement does not cover %q: %v", name, err)
		}
	}

	// And asking again for the same set reuses it, so a configured
	// hostname does not mean a new certificate on every restart.
	third := ensureOK(t, dir, tlscert.Options{ExtraNames: wanted})
	if third.Generated {
		t.Error("Ensure regenerated even though the stored certificate already covers the requested names")
	}
}

// TestEnsureReusesWhenOnlyAnOptionalNameChanged is a regression test for a
// defect found by running the compose stack rather than by reading the
// code.
//
// The controller adds the machine's own hostname to its certificate. Inside
// a container the hostname is the container ID, and `docker compose down`
// followed by `up` creates a container with a new one. With the hostname
// treated as required, every restart found a stored certificate that did
// not cover the new hostname and replaced it, so the fingerprint changed on
// every cycle and every operator was asked to trust a new certificate
// again. That is precisely the behavior reuse exists to prevent, arriving
// through the back door.
//
// A name an operator configured is a requirement. A name the process
// discovered about itself is not.
func TestEnsureReusesWhenOnlyAnOptionalNameChanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	first := ensureOK(t, dir, tlscert.Options{
		ExtraNames:    []string{"controller"},
		OptionalNames: []string{"020c7dfd004c"},
	})
	if !first.Generated {
		t.Fatal("the first Ensure on an empty directory reported a reused certificate")
	}

	// The same deployment, restarted into a new container: same configured
	// name, different hostname.
	second := ensureOK(t, dir, tlscert.Options{
		ExtraNames:    []string{"controller"},
		OptionalNames: []string{"2d91b429efc6"},
	})
	if second.Generated {
		t.Fatal("a new machine hostname replaced a perfectly good certificate; only a name the operator asked for may do that")
	}
	if !bytes.Equal(first.Leaf.Raw, second.Leaf.Raw) {
		t.Error("the certificate on disk changed across a restart that changed nothing an operator configured")
	}

	// The control, so this is not passing because the name check stopped
	// working altogether: a REQUIRED name that changed still replaces it.
	third := ensureOK(t, dir, tlscert.Options{
		ExtraNames:    []string{"controller", "pleiades.example.test"},
		OptionalNames: []string{"2d91b429efc6"},
	})
	if !third.Generated {
		t.Error("adding a configured name did not replace the certificate, so the name check is not working at all")
	}
}

// TestEnsureReplacesACertificateThatIsNotYetValid covers the clock case: a
// stored certificate whose validity has not started, because the clock
// moved backwards or the pair was copied in from a machine running ahead.
//
// It matters because a not-yet-valid certificate fails a handshake exactly
// like an expired one, with a message that sends an operator looking at the
// wrong thing. Replacing it is the only outcome that gets the controller
// serving again without anybody having to diagnose it.
func TestEnsureReplacesACertificateThatIsNotYetValid(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	// Planted as this package's own, because that is the situation: a
	// certificate this controller wrote before the clock jumped. A pair an
	// operator put there is never replaced no matter how it is dated, which
	// TestEnsureNeverReplacesAnOperatorsOwnPair covers.
	planted := plantPair(t, dir, time.Now().Add(48*time.Hour), time.Now().Add(90*24*time.Hour), true)

	healed := ensureOK(t, dir, tlscert.Options{})
	if !healed.Generated {
		t.Fatal("Ensure reused a certificate that is not valid yet")
	}
	if bytes.Equal(planted, healed.Leaf.Raw) {
		t.Error("Ensure reported a new certificate but left the planted one on disk")
	}
	if healed.Leaf.NotBefore.After(time.Now()) {
		t.Errorf("the replacement is not valid until %s, which is still in the future", healed.Leaf.NotBefore)
	}
}

// plantPair writes a self-signed certificate and key into dir with the
// validity window the caller names, and returns the certificate's DER.
//
// Written here rather than through the package under test because that is
// the whole point: tlscert.Generate always dates a certificate from the
// current clock, so the only way to test what happens to a badly dated one
// is to make one by hand.
//
// selfProvisioned decides whose material the planted pair claims to be, and
// it is the difference between two entirely different behaviors. True writes
// the provenance record as well, so Ensure treats the pair as one it wrote
// and may replace. False leaves the record out, which is what an operator
// copying their own certificate into this directory produces, and Ensure
// then refuses to replace it.
func plantPair(t *testing.T, dir string, notBefore, notAfter time.Time, selfProvisioned bool) []byte {
	t.Helper()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "planted"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("signing the planted certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshalling the planted key: %v", err)
	}

	writeFile(t, filepath.Join(dir, tlscert.CertFileName), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeFile(t, filepath.Join(dir, tlscert.KeyFileName), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if selfProvisioned {
		// The provenance record is a PEM bundle of the certificates this
		// package generated in a directory, newest first, so claiming
		// authorship of a planted certificate is writing exactly one block.
		writeFile(t, filepath.Join(dir, tlscert.ProvisionedFileName),
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	return der
}

// writeFile writes one planted file, failing the test rather than
// returning an error.
func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// TestEnsureRefusesADirectoryThatIsAFile covers the configuration mistake
// with the least helpful native error message: PLEIADES_TLS_AUTOCERT_DIR
// pointed at a file. Failing at startup naming the path beats failing later
// with a mkdir error nobody connects to a TLS setting.
func TestEnsureRefusesADirectoryThatIsAFile(t *testing.T) {
	occupied := filepath.Join(t.TempDir(), "not-a-directory")
	writeFile(t, occupied, []byte("this is a file"))

	if _, err := tlscert.Ensure(occupied, tlscert.Options{}); err == nil {
		t.Fatal("Ensure accepted a path that is a file rather than a directory")
	} else if !strings.Contains(err.Error(), occupied) {
		t.Errorf("the error %q does not name the path that is wrong", err)
	}
}

// TestEnsureHealsALegacyMismatchedPair covers the leftovers of the two-file
// layout this package used to publish.
//
// That layout wrote the key and then the certificate, two renames with a gap
// between them, so a process killed in the gap left this package's key beside
// this package's older certificate: a pair that cannot complete a handshake,
// made entirely of material this package wrote. It has to be recognised as
// leftovers and replaced, rather than mistaken for an operator's material and
// preserved forever.
//
// The state cannot be produced by the current design at all, because the pair
// a listener serves is one file published by one rename. It is constructed by
// hand here because a directory written by an earlier build still has to
// work.
func TestEnsureHealsALegacyMismatchedPair(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	// Two generations through the two-file writer, so both the older key and
	// the newer certificate are recorded as this package's own.
	older, err := tlscert.Generate(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	olderKey, err := os.ReadFile(older.KeyFile)
	if err != nil {
		t.Fatalf("reading the first key: %v", err)
	}
	newer, err := tlscert.Generate(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	writeFile(t, newer.KeyFile, olderKey)

	// The premise: the planted state really is unservable, so the test below
	// is not passing for some other reason.
	if _, err := tls.LoadX509KeyPair(newer.CertFile, newer.KeyFile); err == nil {
		t.Fatal("the planted certificate and key still load as a pair, so this test proves nothing")
	}

	healed := ensureOK(t, dir, tlscert.Options{})
	if !healed.Generated {
		t.Fatal("Ensure reused a certificate whose key does not match it")
	}
	if _, err := tls.LoadX509KeyPair(healed.CertFile, healed.KeyFile); err != nil {
		t.Errorf("the healed pair still does not load: %v", err)
	}
	// And the dead key file is gone rather than left as a second copy of key
	// material that matches nothing.
	if _, err := os.Stat(filepath.Join(dir, tlscert.KeyFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the retired key file is still there (stat error %v)", err)
	}
}

// TestEnsureMigratesALegacyPairWithoutReplacingIt is the upgrade an operator
// actually performs: they ran an earlier build, it left cert.pem and key.pem,
// and they restart onto this one.
//
// The certificate must not change. Clients were already asked to trust it, and
// a file layout is this package's business rather than theirs, so the same
// certificate and the same key move into the bundle and nothing is minted.
func TestEnsureMigratesALegacyPairWithoutReplacingIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	// tlscert.Generate writes exactly the layout the previous design left
	// behind: cert.pem, key.pem and the provenance record, and no bundle.
	legacy, err := tlscert.Generate(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, tlscert.BundleFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the two-file layout already has a bundle in it, so this test proves nothing (stat error %v)", err)
	}

	migrated := ensureOK(t, dir, tlscert.Options{})
	if migrated.Generated {
		t.Error("upgrading the file layout replaced the certificate, so every client was asked to trust a new one for nothing")
	}
	if !migrated.Leaf.Equal(legacy.Leaf) {
		t.Error("the certificate served after the migration is not the one the operator already had")
	}

	// The bundle is now the servable unit, and the second copy of the private
	// key is gone.
	bundle := filepath.Join(dir, tlscert.BundleFileName)
	if _, err := tls.LoadX509KeyPair(bundle, bundle); err != nil {
		t.Errorf("the migrated bundle does not load: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, tlscert.KeyFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("key.pem survived the migration, so the private key is on disk twice (stat error %v)", err)
	}
	// And cert.pem stays, because it is the file an operator copies out as a
	// trust anchor and every document says so.
	if _, err := os.Stat(filepath.Join(dir, tlscert.CertFileName)); err != nil {
		t.Errorf("cert.pem did not survive the migration: %v", err)
	}
}

// TestEnsureRefusesARenewalWindowWiderThanTheLifetime closes the one
// configuration that could never converge: every certificate it wrote
// would be inside its own renewal window the moment it was written, so
// Ensure would generate, reject, generate, and finally give up. Refusing
// it up front names the mistake instead.
func TestEnsureRefusesARenewalWindowWiderThanTheLifetime(t *testing.T) {
	_, err := tlscert.Ensure(t.TempDir(), tlscert.Options{TTL: time.Hour, RenewBefore: 2 * time.Hour})
	if err == nil {
		t.Fatal("Ensure accepted a renewal window wider than the certificate lifetime")
	}
	// The message has to name both knobs, because that is the only way a
	// caller learns which pair of values is in conflict.
	for _, want := range []string{"RenewBefore", "TTL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %s", err, want)
		}
	}
}

// TestEnsureLeavesNoTemporaryFilesBehind pins the cleanup half of the
// atomic write. A directory that slowly fills with abandoned key material
// is a worse outcome than whatever error left it there.
func TestEnsureLeavesNoTemporaryFilesBehind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	ensureOK(t, dir, tlscert.Options{})
	// A second generation, so a replacement's temporary files are covered
	// too and not just a first write's.
	ensureOK(t, dir, tlscert.Options{ExtraNames: []string{"controller.example.test"}})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	// Every entry Ensure publishes is listed rather than filtered out: a test
	// that ignored unknown entries would stop being the check it is meant to
	// be. key.pem is deliberately NOT among them. Ensure publishes the pair
	// as one bundle, so there is no second copy of the private key for it to
	// leave behind.
	want := []string{tlscert.CertFileName, tlscert.ProvisionedDirName, tlscert.BundleFileName}
	if !equalStrings(names, want) {
		t.Errorf("%s holds %v, want exactly %v", dir, names, want)
	}
}

// TestEnsureSweepsAbandonedTemporaryFiles covers what writeAtomic cannot
// clean up itself.
//
// It removes its own temporary file on every path it can reach, and reaches
// none of them if the process is killed between creating the file and
// renaming it. What is left behind is a real EC private key at 0600 that
// nothing will ever open again and nothing else will ever delete, and a
// restart loop accumulates one per crash.
func TestEnsureSweepsAbandonedTemporaryFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	ensureOK(t, dir, tlscert.Options{})

	abandoned := filepath.Join(dir, tlscert.KeyFileName+".tmp-1234567890")
	writeFile(t, abandoned, []byte("-----BEGIN EC PRIVATE KEY-----\nnot really a key\n-----END EC PRIVATE KEY-----\n"))
	// Backdated past the sweep age, because the sweep deliberately leaves
	// recent temporary files alone: one of them may belong to a process that
	// is part way through a write right now.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(abandoned, old, old); err != nil {
		t.Fatalf("backdating %s: %v", abandoned, err)
	}

	// A fresh one, which must survive, so this test proves the age rule and
	// not just "delete everything that matches a glob".
	current := filepath.Join(dir, tlscert.KeyFileName+".tmp-0987654321")
	writeFile(t, current, []byte("a write that is still in flight"))

	ensureOK(t, dir, tlscert.Options{})

	if _, err := os.Stat(abandoned); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s survived the sweep (stat error %v), so abandoned key material accumulates forever", abandoned, err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Errorf("the sweep deleted a temporary file that another process could still be writing: %v", err)
	}
}

// TestAReaderNeverSeesAPartiallyWrittenCertificate is FAILURE_PATTERNS.md
// #46 as a test rather than as a comment: "an exclusive file create still
// left a window where a concurrent reader could observe an empty file."
//
// The reader here is a second controller starting against a shared volume
// while the first one renews. It is allowed to find no file at all, and it
// is allowed to find either the old or the new certificate. What it must
// never find is a file that exists and does not parse.
func TestAReaderNeverSeesAPartiallyWrittenCertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	certPath := filepath.Join(dir, tlscert.CertFileName)

	// Buffered so the reader can record the first problem it finds and
	// return, without waiting for the writer loop below to finish.
	problems := make(chan string, 1)
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}

			raw, err := os.ReadFile(certPath)
			if errors.Is(err, fs.ErrNotExist) {
				// Before the first write, which is a legitimate thing for
				// a reader to observe.
				continue
			}
			if err != nil {
				problems <- "reading the certificate: " + err.Error()
				return
			}
			block, _ := pem.Decode(raw)
			if block == nil {
				problems <- fmt.Sprintf("observed a %d byte certificate file that is not parseable PEM", len(raw))
				return
			}
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				problems <- "observed a PEM block that is not a certificate: " + err.Error()
				return
			}
		}
	}()

	// Fifty real replacements, which is enough that a write straight to
	// the final path would be caught by the reader above on essentially
	// every run.
	for i := 0; i < 50; i++ {
		if _, err := tlscert.Generate(dir, tlscert.Options{}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
	}
	close(stop)
	<-done

	select {
	case problem := <-problems:
		t.Error(problem)
	default:
	}
}
