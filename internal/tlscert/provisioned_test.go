// The provenance record's own behavior: what it keeps, what it drops, what
// happens to a deployment when it is missing or damaged, and the one property
// it was rewritten to have.
//
// This file matters more than its size suggests. Two separate guarantees are
// read off these records: whether a certificate may be replaced, and which
// certificates a container healthcheck will accept from its own listener. A
// bug that silently emptied them would turn every renewal into "this is an
// operator's certificate, never touch it", and a bug that let them grow
// without limit would put an unbounded read on the probe's path.
//
// The property that is new, and that the single-file version could not have,
// is that concurrent writers cannot lose one another's entries. That is
// covered by TestProvenanceLosesNothingUnderConcurrentWriters, and it is not
// a theoretical nicety: a sixteen-replica cold start reliably produced a
// controller whose own healthcheck rejected the certificate it was serving,
// because a sibling's read-modify-write had dropped its record.
package tlscert_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// TestProvenanceKeepsEveryCertificateAReplicaCouldServe pins the rule that
// decides when a record may go, and it is the rule a live outage rewrote.
//
// A controller serves the material it loaded at start-up, from memory, for as
// long as it runs, so a record may only be removed once no process could
// still be presenting that certificate. The rule this replaced kept the
// sixteen most recent records and evicted the rest once they were an hour
// old, which meant the seventeenth certificate a directory ever saw evicted
// the first, and a replica that had been serving the first one since before
// that hour failed its own healthcheck and was killed by its orchestrator
// while it was serving perfectly.
func TestProvenanceKeepsEveryCertificateAReplicaCouldServe(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	// Twenty-four generations against the old cap of sixteen, so a rule made
	// of a count would certainly have acted by now.
	const generations = 24
	var written []*x509.Certificate
	for i := 0; i < generations; i++ {
		cert, err := tlscert.Generate(dir, tlscert.Options{})
		if err != nil {
			t.Fatalf("Generate %d: %v", i, err)
		}
		written = append(written, cert.Leaf)
	}

	// Backdated well past the old grace period, which is the state in which
	// the old rule started deleting, and then one more generation, which is
	// what used to trigger it.
	backdateRecords(t, dir, 2*time.Hour)
	fresh, err := tlscert.Generate(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Every one of them is still trusted, asked the way the container
	// healthcheck asks it. None of these certificates has expired, so any of
	// them could be what a replica started an hour ago is presenting right
	// now.
	for i, cert := range append(written, fresh.Leaf) {
		if !recorded(t, dir, cert) {
			t.Errorf("generation %d is no longer trusted, so a controller still serving it would fail its own healthcheck and be restarted", i)
		}
	}
	if kept := recordCount(t, dir); kept != generations+1 {
		t.Errorf("%d records kept out of %d, so a write evicted a certificate a live process could still be presenting", kept, generations+1)
	}

	// And a directory the records cover is still one this package will
	// replace, which is the other thing they decide.
	renewed, err := tlscert.Ensure(dir, tlscert.Options{ExtraNames: []string{"controller.example.test"}})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !renewed.Generated {
		t.Error("a directory this package provisioned was treated as an operator's own")
	}
	if !renewed.SelfProvisioned {
		t.Error("a certificate this package just wrote is not reported as self-provisioned")
	}
}

// TestProvenanceRemovesARecordNobodyCouldBeServing is the other half: the
// records do not grow forever, they just wait for the only event that makes
// removal safe.
//
// An expired certificate fails every handshake whether or not a record for it
// exists, so once it has expired past the clock-skew grace nobody can be
// legitimately presenting it and the record is dead weight.
func TestProvenanceRemovesARecordNobodyCouldBeServing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	live, err := tlscert.Generate(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// A record for a certificate that expired days ago, written by hand
	// because no option on this package can mint one: Options.TTL is clamped
	// to a positive lifetime precisely so a caller cannot ask for this.
	dead := plantExpiredRecord(t, dir)

	// Any write at all runs the prune.
	if _, err := tlscert.Generate(dir, tlscert.Options{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if recorded(t, dir, dead) {
		t.Error("a record for a certificate that expired days ago is still trusted, so the records grow without limit")
	}
	if !recorded(t, dir, live.Leaf) {
		t.Error("a certificate that is still valid was pruned beside the expired one")
	}
}

// TestProvenanceLosesNothingUnderConcurrentWriters is the defect the one-file
// record had and this one cannot.
//
// Sixteen writers recording at the same instant is a cold start on a shared
// volume. With a single file each of them read, prepended itself to and wrote
// back, some of those entries were simply gone afterwards, and the process
// serving the certificate behind a lost entry failed its own healthcheck.
// With one file per certificate there is no shared file to lose.
func TestProvenanceLosesNothingUnderConcurrentWriters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	const writers = 16

	certs := make([]tlscert.ServingCert, writers)
	errs := make([]error, writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			// Generate rather than Ensure, because this is a test of the
			// record alone: every call here certainly writes one, so the
			// expected set is known exactly.
			certs[i], errs[i] = tlscert.Generate(dir, tlscert.Options{})
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}

	// Every writer's record survived: none of them is a file any other writer
	// touched. Asked the way the container healthcheck asks it, because a
	// probe rejecting a working listener is the failure a lost record causes.
	if kept := recordCount(t, dir); kept != writers {
		t.Errorf("%d records for %d writers, so a concurrent writer lost another's entry", kept, writers)
	}
	for i, cert := range certs {
		if !recorded(t, dir, cert.Leaf) {
			t.Errorf("a probe would reject a controller serving writer %d's certificate", i)
		}
	}
}

// TestProvenanceReadsARecordFromTheOlderLayout is the upgrade case.
//
// An earlier build kept every record in one provisioned.pem. Ignoring it
// would turn an operator's stored pair into material this controller "did not
// write", which is served forever and never renewed, so the old file is still
// read even though it is never written again.
func TestProvenanceReadsARecordFromTheOlderLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	legacy, err := tlscert.Generate(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// The record moved back into the single file the older build wrote, with
	// the per-certificate records removed: exactly what upgrading finds.
	if err := os.WriteFile(filepath.Join(dir, tlscert.ProvisionedFileName),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: legacy.Leaf.Raw}), 0o600); err != nil {
		t.Fatalf("writing the old-style record: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, tlscert.ProvisionedDirName)); err != nil {
		t.Fatalf("removing the new-style records: %v", err)
	}

	migrated, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Ensure over a record from the older layout: %v", err)
	}
	if migrated.Generated {
		t.Error("a certificate recorded in the older layout was treated as an operator's own and replaced")
	}
	if !migrated.SelfProvisioned {
		t.Error("a certificate recorded in the older layout is not reported as this package's own")
	}
}

// TestProvenanceSurvivesADamagedEntry is the tolerance the record needs.
//
// These records are advisory. A truncated or unrecognised block in one should
// cost the deployment one trust anchor, not its ability to start, so anything
// that is not a parseable certificate is skipped rather than treated as a
// failure.
func TestProvenanceSurvivesADamagedEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	cert, err := tlscert.Generate(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// A record that is not a certificate at all, and one that says it is and
	// holds nothing usable, beside the real one.
	recordDir := filepath.Join(dir, tlscert.ProvisionedDirName)
	damaged := []byte("# a comment nothing should choke on\n")
	damaged = append(damaged, pem.EncodeToMemory(&pem.Block{Type: "PARAMETERS", Bytes: []byte{1, 2, 3}})...)
	damaged = append(damaged, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not DER")})...)
	writeFile(t, filepath.Join(recordDir, "0000000000000000.pem"), damaged)

	anchors, err := tlscert.AnchorsForDir(dir)
	if err != nil {
		t.Fatalf("AnchorsForDir over a damaged record: %v", err)
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: anchors.Roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Errorf("the published certificate no longer verifies against its own anchors: %v", err)
	}

	// And the certificate is still this package's to renew, so one bad byte in
	// an advisory file does not freeze a deployment's certificate forever.
	renewed, err := tlscert.Ensure(dir, tlscert.Options{ExtraNames: []string{"controller.example.test"}})
	if err != nil {
		t.Fatalf("Ensure over a damaged record: %v", err)
	}
	if !renewed.Generated {
		t.Error("a damaged provenance entry made this package disown a certificate it wrote")
	}
}

// TestAnchorsForDirWorksWithNoHistoryYet covers the first start, where the
// only certificate that exists is the published one.
func TestAnchorsForDirWorksWithNoHistoryYet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	cert := ensureOK(t, dir, tlscert.Options{})
	if err := os.RemoveAll(filepath.Join(dir, tlscert.ProvisionedDirName)); err != nil {
		t.Fatalf("removing the records: %v", err)
	}

	anchors, err := tlscert.AnchorsForDir(dir)
	if err != nil {
		t.Fatalf("AnchorsForDir with no records: %v", err)
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: anchors.Roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Errorf("a probe cannot verify the only certificate there is: %v", err)
	}
}

// TestEnsureNeverReplacesAKeyItCannotProveAnythingAbout is the other
// direction of the tolerance above, and the line between them is deliberate.
//
// A damaged ENTRY costs one anchor. A record from the older layout that
// cannot be read AT ALL, in a directory holding a private key, means this
// package cannot tell its own key from an operator's, and guessing that
// question destroys something whichever way it is guessed. So it never
// replaces the pair, and it names the file that made the question
// unanswerable.
//
// It still SERVES the pair, which is the half that changed. The material
// loads and a handshake with it works, and this package's second rule is that
// it never refuses to start over a certificate it could serve. Refusing here
// turned an unreadable legacy file into a controller that would not boot.
func TestEnsureNeverReplacesAKeyItCannotProveAnythingAbout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	// Two hours of life against the default thirty-day renewal window, so
	// every reuse rule this package has wants the pair replaced.
	stored, err := tlscert.Generate(dir, tlscert.Options{TTL: 2 * time.Hour})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// The two-file layout with its provenance unreadable: the per-certificate
	// records removed, and the old single-file record replaced by a directory
	// nothing can read as PEM.
	if err := os.RemoveAll(filepath.Join(dir, tlscert.ProvisionedDirName)); err != nil {
		t.Fatalf("removing the records: %v", err)
	}
	path := filepath.Join(dir, tlscert.ProvisionedFileName)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("replacing the record with a directory: %v", err)
	}

	got, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Ensure refused to start rather than serving the pair it already had: %v", err)
	}
	if got.Generated {
		t.Fatal("Ensure replaced a private key while the only evidence of who wrote it was unreadable")
	}
	if !got.Leaf.Equal(stored.Leaf) {
		t.Error("Ensure served a certificate other than the one on disk")
	}
	if !strings.Contains(got.Warning, tlscert.ProvisionedFileName) {
		t.Errorf("the warning %q does not name the file that made the question unanswerable", got.Warning)
	}

	// And when there is nothing servable there either, the same unanswerable
	// question is a refusal that names the same file.
	if err := os.Remove(filepath.Join(dir, tlscert.CertFileName)); err != nil {
		t.Fatalf("removing the certificate: %v", err)
	}
	if _, err := tlscert.Ensure(dir, tlscert.Options{}); err == nil {
		t.Error("Ensure carried on with a provenance record it could not read, so it was guessing whose key this is")
	} else {
		// Both files, because a human fixing this needs to know which key is
		// being protected AND which record made the question unanswerable.
		assertRefusalIsActionable(t, err, filepath.Join(dir, tlscert.KeyFileName), tlscert.ProvisionedFileName)
	}
}

// recordCount is how many provenance records dir holds.
//
// The files are counted rather than read, and membership is asked of
// AnchorsForDir below, because that is the question the container healthcheck
// actually asks: reading the records here would let this file agree with
// itself about a format the probe reads differently.
func recordCount(t *testing.T, dir string) int {
	t.Helper()

	entries, err := filepath.Glob(filepath.Join(dir, tlscert.ProvisionedDirName, "*.pem"))
	if err != nil {
		t.Fatalf("listing the provenance records: %v", err)
	}
	return len(entries)
}

// recorded reports whether a probe of dir would accept a listener serving
// leaf, which is what "this certificate is recorded" means to the only two
// callers that care.
func recorded(t *testing.T, dir string, leaf *x509.Certificate) bool {
	t.Helper()

	anchors, err := tlscert.AnchorsForDir(dir)
	if err != nil {
		t.Fatalf("AnchorsForDir: %v", err)
	}
	_, err = leaf.Verify(x509.VerifyOptions{Roots: anchors.Roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err == nil
}

// backdateRecords ages every provenance record in dir, which is the only way
// to reach the cap: a record younger than the grace period is never pruned,
// on purpose, because a live process may be serving it.
func backdateRecords(t *testing.T, dir string, by time.Duration) {
	t.Helper()

	entries, err := filepath.Glob(filepath.Join(dir, tlscert.ProvisionedDirName, "*.pem"))
	if err != nil {
		t.Fatalf("listing the provenance records: %v", err)
	}
	old := time.Now().Add(-by)
	for _, path := range entries {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("backdating %s: %v", path, err)
		}
	}
}

// plantExpiredRecord writes a provenance record for a certificate that
// expired days ago, and returns that certificate.
//
// It builds the record by hand because no option this package exposes can
// mint an expired certificate: Options.TTL is clamped to a positive lifetime
// exactly so that a caller cannot ask for one. The file name is the
// certificate's own SHA-256 fingerprint in hex, which is the whole naming
// rule the records use, so a record written here is indistinguishable from
// one this package wrote.
func plantExpiredRecord(t *testing.T, dir string) *x509.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(99),
		Subject:               pkix.Name{CommonName: "pleiades-local"},
		NotBefore:             time.Now().Add(-30 * 24 * time.Hour),
		NotAfter:              time.Now().Add(-7 * 24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("signing the expired certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the expired certificate: %v", err)
	}

	fingerprint := sha256.Sum256(der)
	path := filepath.Join(dir, tlscert.ProvisionedDirName, hex.EncodeToString(fingerprint[:])+".pem")
	writeFile(t, path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return parsed
}

// containsCertificate reports whether certs holds want.
func containsCertificate(certs []*x509.Certificate, want *x509.Certificate) bool {
	for _, cert := range certs {
		if cert.Equal(want) {
			return true
		}
	}
	return false
}
