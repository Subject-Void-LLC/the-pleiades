// Provenance: the record of which certificates this package generated in a
// directory, kept as one small file per certificate.
//
// It answers two questions that would otherwise need two mechanisms.
//
// The first is "may this private key be replaced?", for a key that cannot
// answer for itself. An operator who drops their own certificate and key into
// the directory this package manages must get them back, not a self-signed
// pair written over the top of a private key that exists nowhere else. A key
// file carries nothing that names its author, so the proof is possession: the
// key belongs to a certificate recorded here, which means this package
// generated the two of them together (ownership.go). A serving bundle does
// not need any of this, because it carries its own provenance block inside
// the file.
//
// The second is "what is this deployment allowed to be serving?". The
// container healthcheck verifies the local listener against a real trust
// anchor rather than skipping verification, and the anchor cannot simply be
// the currently published certificate: with two controllers sharing one
// volume, the one that renewed publishes a new certificate while the other
// keeps serving, from memory, the certificate it loaded at start-up. Its own
// probe would then fail against a file describing somebody else's identity,
// and the orchestrator would restart a perfectly healthy process. Recording
// every generation makes the anchor "any certificate this deployment
// legitimately provisioned", which covers both processes without weakening
// anything: every entry was written by this package, and the listener still
// has to prove it holds the matching private key.
//
// Which is why a record outlives its certificate's replacement and is removed
// only at its EXPIRY, and why a replica that reuses what it finds records that
// too (ensure.go's claimServed). Both of those were missing, and a set that
// could shrink underneath a running process is a set that gets healthy
// processes killed.
//
// ONE FILE PER CERTIFICATE, and this is the part that was learned the hard
// way. The record used to be a single PEM bundle that every writer read,
// prepended itself to, and wrote back. That is a read-modify-write, so two
// processes recording at the same instant lose one another's entry, and a
// sixteen-replica cold start reliably produced a controller serving a
// perfectly good certificate that no record mentioned: its own healthcheck
// rejected it, and an orchestrator would have killed it. Naming each entry
// after the certificate's own fingerprint means no writer's file is ever any
// other writer's file, so nothing can be lost, on any filesystem, with no
// coordination at all. It is the same reasoning that put the certificate and
// its key in one file: this package must have no place left where one
// writer's work can destroy another's.
package tlscert

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ProvisionedDirName holds one file per certificate this package has
// provisioned in a directory, and ProvisionedFileName is the single-file
// record an earlier build wrote.
//
// Both are exported for the same reason CertFileName is: more than one caller
// has to name these paths somewhere Go cannot reach them, and two spellings
// of either is a support question waiting to happen.
//
// The old file is READ and never written. An operator who ran the earlier
// build has their provenance in it, and throwing that away would turn their
// stored pair into "material this controller did not write", which is served
// forever and never renewed.
const (
	ProvisionedDirName  = "provisioned"
	ProvisionedFileName = "provisioned.pem"
)

// provisionedGrace is how long past a certificate's expiry its record is
// still kept.
//
// An hour, for one thing only: two machines' clocks disagree, and a record
// deleted a second after the certificate expired by THIS clock could still be
// one a replica is presenting by its own. The window costs an expired
// kilobyte and buys the whole class of clock-skew mistakes.
const provisionedGrace = time.Hour

// provisionedPreamble is the text written above the certificate in each
// record.
//
// pem.Decode skips everything before the first BEGIN line, so this is free to
// be prose, and it has to be: a record's whole job is to be believed by a
// future reader, and a bare base64 block invites somebody to delete it as
// leftover junk.
const provisionedPreamble = `# One certificate this controller provisioned for itself in this directory,
# named after its own SHA-256 fingerprint. Two things read these records.
#
#  1. A PRIVATE KEY in this directory is replaced only if it belongs to a
#     certificate recorded here, so a key an operator put here is never
#     written over. (serving.pem does not need a record: it carries its own.)
#  2. The container healthcheck trusts every certificate recorded here, so a
#     controller still serving one from before a renewal can still verify
#     its own listener. A record is removed only once the certificate in it
#     has expired, because until then some replica may still be presenting
#     it.
#
# Deleting these costs the healthcheck its history, and makes this controller
# treat a key.pem beside them as an operator's own: it would refuse to
# provision here rather than risk writing over a secret.
`

// readProvisioned returns every certificate recorded as this package's own in
// dir.
//
// The order is not meaningful and no caller may assume one: these are files
// in a directory, and the only questions asked of them are "is this
// certificate one of them" and "trust all of them".
//
// A missing record is an empty list and not an error, because that is the
// state of every directory before the first certificate is written, and of
// every directory holding only an operator's own material. A damaged entry is
// skipped, because it costs one trust anchor. The single exception, and it is
// deliberate, is a single-file record from an earlier build that exists and
// cannot be read at all: in that layout it is the only evidence of who wrote
// the pair beside it, so guessing in either direction destroys something.
//
// That exception returns the certificates it DID gather alongside the error,
// which is not the usual Go shape and is the point. Two callers ask this
// question and they need opposite things from the answer. Ownership has to
// stop, because a key it cannot account for is at stake. The trust anchors
// have to carry on, because they are a set of things to trust and one
// unreadable file costs the set one member: propagating the error there made
// a controller that provisions and serves perfectly fail its own healthcheck
// forever over a legacy file nothing else in the process even reads.
func readProvisioned(dir string) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate

	// The glob cannot match writeAtomic's temporary files, which are named
	// "<fingerprint>.pem.tmp-<random>" and therefore do not end in .pem.
	entries, err := filepath.Glob(filepath.Join(dir, ProvisionedDirName, "*.pem"))
	if err == nil {
		for _, path := range entries {
			raw, readErr := readPEMFile(path)
			if readErr != nil {
				continue
			}
			certs = append(certs, parseCertificates(raw)...)
		}
	}

	legacy := filepath.Join(dir, ProvisionedFileName)
	raw, err := readPEMFile(legacy)
	switch {
	case err == nil:
		certs = append(certs, parseCertificates(raw)...)
	case !errors.Is(err, fs.ErrNotExist):
		return certs, fmt.Errorf("reading %s: %w", legacy, err)
	}
	return certs, nil
}

// parseCertificates decodes every certificate in a PEM file, skipping
// anything that is not one.
//
// Skipping rather than failing is deliberate. These records are advisory: a
// corrupted or truncated entry should cost the deployment one trust anchor
// and one certificate's replaceability, not its ability to start.
func parseCertificates(raw []byte) []*x509.Certificate {
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			return certs
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		certs = append(certs, cert)
	}
}

// recordProvisioned writes leaf's record into dir.
//
// It is called BEFORE the certificate is published, and the order matters. A
// reader arriving between this write and the publish sees a record for a
// certificate that is not on disk yet, beside the records of the ones that
// are: every published certificate is recorded, which is the only question a
// reader asks. Publishing first and recording afterwards would invert that,
// and a process killed in between would leave a certificate this package
// could never renew.
//
// Writing the same certificate twice is a no-op rather than a duplicate,
// because the file is named after the certificate.
func recordProvisioned(dir string, leaf *x509.Certificate) error {
	recordDir := filepath.Join(dir, ProvisionedDirName)
	if err := os.MkdirAll(recordDir, dirMode); err != nil {
		return fmt.Errorf("creating %s: %w", recordDir, err)
	}

	var buf bytes.Buffer
	buf.WriteString(provisionedPreamble)
	if err := appendCertificate(&buf, leaf); err != nil {
		return err
	}

	fingerprint := fingerprintOf(leaf)
	if _, err := writeAtomic(recordDir, hex.EncodeToString(fingerprint[:])+".pem", buf.Bytes()); err != nil {
		return err
	}
	pruneProvisioned(recordDir)
	return nil
}

// pruneProvisioned removes the records no replica could still be presenting.
//
// The rule is the CERTIFICATE's own expiry, and nothing else, because it is
// the only rule that cannot kill a healthy process. A controller serves the
// material it loaded at start-up, from memory, for as long as it runs, so the
// question "may this record go?" is really "could a live process still be
// presenting this certificate?", and after it expires the answer is no for
// everybody: every handshake with it fails whether the record exists or not.
//
// It replaces a rule made of a count and a record's age, and that rule was a
// live outage waiting for a long enough uptime. Keeping the sixteen newest
// records meant the seventeenth certificate a directory ever saw evicted the
// first, and a replica that had been serving the first one since before the
// grace window then failed its own healthcheck, which an orchestrator answers
// by killing a process that is serving perfectly. Neither number could be
// tuned out of it: any count is wrong for a fleet one replica larger, and any
// age is wrong for an uptime one hour longer.
//
// What this costs is a directory that holds one record per certificate this
// deployment minted inside one certificate lifetime. That is not unbounded in
// any way that matters: a certificate is minted only when the stored one
// cannot be reused, so a converged deployment mints once a year plus once per
// configuration change plus one per replica in a contended cold start, and
// each record is about a kilobyte.
//
// Every failure is ignored. This is housekeeping, and a controller must not
// refuse to start because it could not tidy up.
func pruneProvisioned(recordDir string) {
	paths, err := filepath.Glob(filepath.Join(recordDir, "*.pem"))
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-provisionedGrace)
	for _, path := range paths {
		raw, err := readPEMFile(path)
		if err != nil {
			continue
		}
		certs := parseCertificates(raw)
		// A record holding nothing this can parse is left alone. It is
		// already costing the deployment nothing (readProvisioned skips it)
		// and deleting a file this package cannot read is not the job of a
		// function whose whole purpose is tidying up after itself.
		if len(certs) == 0 || !allExpiredBy(certs, cutoff) {
			continue
		}
		_ = os.Remove(path)
	}
}

// allExpiredBy reports whether every certificate in a record expired before
// cutoff, which is the only state in which the record is safe to remove.
func allExpiredBy(certs []*x509.Certificate, cutoff time.Time) bool {
	for _, cert := range certs {
		if cert.NotAfter.After(cutoff) {
			return false
		}
	}
	return true
}

// appendCertificate writes one PEM block for cert.
func appendCertificate(buf *bytes.Buffer, cert *x509.Certificate) error {
	if err := pem.Encode(buf, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}); err != nil {
		return fmt.Errorf("encoding a provisioned certificate record: %w", err)
	}
	return nil
}

// fingerprintOf is the SHA-256 of a certificate's DER, which is what
// "the same certificate" means everywhere else an operator will see one
// (openssl x509 -fingerprint -sha256, a browser's certificate viewer).
func fingerprintOf(cert *x509.Certificate) [sha256.Size]byte {
	return sha256.Sum256(cert.Raw)
}

// recordedIn reports whether leaf is one of the certificates in recorded.
func recordedIn(recorded []*x509.Certificate, leaf *x509.Certificate) bool {
	want := fingerprintOf(leaf)
	for _, cert := range recorded {
		if fingerprintOf(cert) == want {
			return true
		}
	}
	return false
}
