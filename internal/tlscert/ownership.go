// Whose material is this? The one question that decides whether this package
// may write into a directory at all.
//
// The rule is narrower than "never replace material I cannot prove I wrote",
// and the narrowing is the whole of this file. That broader rule protected
// things that could not be anybody's secret and could not be served either: a
// zero-length serving.pem, a file truncated part way through a certificate, a
// certificate with no private key beside it. Every one of those made every
// controller in the directory refuse to start, forever, over a file worth
// nothing to anybody, and several of them are states this package itself
// produces. A rule that can brick a directory with a file the package wrote
// is not protecting an operator, it is protecting nothing at a price nobody
// agreed to pay.
//
// So the question is asked about the only thing whose loss cannot be undone:
// a PRIVATE KEY. A private key exists in exactly one place. An operator who
// copies theirs into the directory this package manages has done something
// reasonable and slightly wrong, and the wrong part is fixable with one
// environment variable; a key overwritten by a self-signed one is not fixable
// by anybody.
//
// Three answers, and every one of them names a file:
//
//   - No private key, or one this package can PROVE it wrote: publish. The
//     proof is possession, because a key file carries nothing that names its
//     author: either the key belongs to a certificate the provenance records
//     list (provisioned.go), or it sits in a serving bundle that carries its
//     own provenance block (bundle.go).
//   - A parseable private key this package cannot account for: refuse, and
//     say which file holds it and how to serve it deliberately.
//   - A file that exists and cannot be READ at all: refuse, and say that.
//     The contents of a file this process could not open are unknown, which
//     is not the same as harmless, and it is the one case where guessing in
//     either direction destroys something. In practice this is one
//     controller meeting another's 0600 file, so the message names the user
//     this process runs as instead of calling it somebody else's secret.
//
// Nothing else refuses, and two omissions are deliberate. Bytes that do not
// parse are not a secret: nothing can serve them and nobody can lose them. A
// CERTIFICATE this package did not write is not a secret either: it is public
// by construction and it cannot be served without the key that is not beside
// it. Neither one stops a start. A certificate is still never REWRITTEN
// unless this package can prove it wrote it, which is a separate decision
// taken at the write itself (certFileIsOurs, used by republishCertificate):
// refusing to overwrite a file and refusing to start are not the same
// promise, and only the first of them was ever worth making.
//
// The three refusal MESSAGES live in refusals.go rather than here, because
// what this file decides and what an operator is told about that decision are
// read by different people at different times.
package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// publishBlocker reports why this package must not publish a new serving
// bundle into dir, or nil when it may.
//
// The serving bundle is asked first and answers on its own, with no second
// file involved, which is what makes the answer safe when several processes
// are writing at the same instant. Only a directory with no bundle falls
// through to the two-file layout, which is what an earlier build of this
// package wrote and the shape an operator's own pair arrives in.
func publishBlocker(absDir string) error {
	bundlePath := filepath.Join(absDir, BundleFileName)
	raw, err := readPEMFile(bundlePath)
	switch {
	case err == nil:
		return keyBlocker(absDir, bundlePath, raw, bundleSelfProvisioned(raw))
	case !errors.Is(err, fs.ErrNotExist):
		return unreadableMaterial(bundlePath, err)
	}

	// cert.pem is asked one question and one only: can it be read? What it
	// HOLDS never refuses, because a certificate with no key beside it cannot
	// be served and cannot be a secret. Whether it can be read still refuses,
	// for the same reason the bundle does, and it catches the shape that is
	// worse than any refusal: a named pipe at this path makes open(2) block
	// forever, so readPEMFile stats before it opens and this turns "the
	// controller hangs with no log line" into a message naming the file.
	certPath := filepath.Join(absDir, CertFileName)
	if _, err := readPEMFile(certPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return unreadableMaterial(certPath, err)
	}

	keyPath := filepath.Join(absDir, KeyFileName)
	keyRaw, err := readPEMFile(keyPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return unreadableMaterial(keyPath, err)
	}
	return keyBlocker(absDir, keyPath, keyRaw, false)
}

// keyBlocker reports why the private key in one file must not be replaced, or
// nil when there is no reason.
//
// selfProving says the file has already answered the question itself, which
// only a serving bundle can do. Everything else has to be proven against the
// provenance records beside it.
func keyBlocker(absDir, path string, raw []byte, selfProving bool) error {
	if selfProving || !holdsPrivateKey(raw) {
		return nil
	}

	recorded, err := readProvisioned(absDir)
	if err != nil {
		// The single unanswerable case: there is a real private key here, and
		// the only evidence of who wrote it exists and cannot be read.
		return unprovableKey(path, err)
	}
	if keyWasWrittenHere(raw, recorded) {
		return nil
	}
	return foreignKeyRefusal(path)
}

// holdsPrivateKey reports whether raw holds a private key that something
// could really use.
//
// It PARSES rather than trusting the PEM header, because the distinction the
// whole rule rests on is between a secret and bytes that merely look like
// one. A block labelled PRIVATE KEY holding a truncated DER is not a key:
// nothing can serve it, nobody can lose it, and a controller that refuses to
// start over it is the defect this file was rewritten to remove.
//
// An ENCRYPTED key is treated as a key without being parsed, and that
// exception is load bearing. Go cannot decrypt one (x509.DecryptPEMBlock is
// deprecated and unsafe), so it would never parse, and calling an operator's
// encrypted key "not a key" would let this package write over the one copy of
// it that exists.
func holdsPrivateKey(raw []byte) bool {
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			return false
		}
		if !strings.Contains(block.Type, "PRIVATE KEY") {
			continue
		}
		if _, encrypted := block.Headers["DEK-Info"]; encrypted {
			return true
		}
		if strings.Contains(block.Type, "ENCRYPTED") {
			return true
		}
		if parsesAsPrivateKey(block.Bytes) {
			return true
		}
	}
}

// parsesAsPrivateKey reports whether der is a private key in any of the three
// encodings a PEM file can hold: PKCS#8, SEC 1 (EC) and PKCS#1 (RSA).
//
// All three, because crypto/tls accepts all three: recognising fewer here
// would let this package decide an operator's real key was not a key.
func parsesAsPrivateKey(der []byte) bool {
	if _, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return true
	}
	if _, err := x509.ParseECPrivateKey(der); err == nil {
		return true
	}
	if _, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return true
	}
	return false
}

// certFileIsOurs reports whether cert.pem may be rewritten: it is absent, or
// it holds a certificate this package recorded as its own.
//
// This is a narrower question than publishBlocker's and it keeps the stricter
// answer, on purpose. Refusing to start over a file and refusing to overwrite
// it are different promises: the first one costs a deployment its start-up,
// the second costs nothing at all, because cert.pem is a convenience copy and
// a stale one harms nobody. So anything unreadable, and anything holding a
// certificate with no record beside it, is left exactly as it is.
func certFileIsOurs(absDir string) (bool, error) {
	raw, err := readPEMFile(filepath.Join(absDir, CertFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, nil
	}
	published := parseCertificates(raw)
	if len(published) == 0 {
		return false, nil
	}
	recorded, err := readProvisioned(absDir)
	if err != nil {
		return false, err
	}
	return recordedIn(recorded, published[0]), nil
}

// keyWasWrittenHere reports whether a private key found in this directory is
// one this package wrote.
//
// The proof is possession: the key belongs to a certificate the provenance
// record lists, so this package generated the two of them together. There is
// no cheaper test, because a private key file carries nothing that names its
// author.
//
// keyPEM may be a whole serving bundle rather than a bare key file. The
// standard library skips the PEM blocks it is not looking for, so passing the
// bundle asks exactly the right question: does the key IN this file belong to
// a certificate this package published?
//
// A key that pairs with an OLDER recorded certificate counts, and that is
// deliberate rather than sloppy. The previous two-file layout wrote the key
// first and the certificate second, so a process killed between the two
// writes left exactly that state behind: this package's key beside this
// package's older certificate. Recognising it is what lets the leftovers be
// cleaned up instead of freezing the directory forever.
func keyWasWrittenHere(keyPEM []byte, recorded []*x509.Certificate) bool {
	for _, cert := range recorded {
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
		// tls.X509KeyPair rather than a hand-rolled public key comparison,
		// because it already understands every private key encoding a PEM
		// file can hold and it is the exact check a listener would apply.
		if _, err := tls.X509KeyPair(certPEM, keyPEM); err == nil {
			return true
		}
	}
	return false
}

// adoptLegacy moves a servable pair written by the previous two-file layout
// into a serving bundle, and reports what to serve afterwards.
//
// An operator who ran the earlier build has a working certificate that
// clients have already been asked to trust. Minting a new one because the
// file layout changed would ask them all again for nothing, so the same
// certificate and the same key are carried across unchanged; only the
// arrangement on disk moves.
//
// Everything here is best effort. A directory that cannot be written keeps
// serving the pair it already has, which is the whole point of this package's
// second rule: never refuse to start over a certificate it could serve.
func adoptLegacy(absDir string, stored ServingCert) (ServingCert, bool) {
	certPEM, err := readPEMFile(filepath.Join(absDir, CertFileName))
	if err != nil {
		return ServingCert{}, false
	}
	keyPEM, err := readPEMFile(filepath.Join(absDir, KeyFileName))
	if err != nil {
		return ServingCert{}, false
	}

	adopted, err := publishMaterial(absDir, stored.Leaf, certPEM, keyPEM)
	if err != nil {
		return ServingCert{}, false
	}
	// Not a new certificate: the same one, in a new place. Reporting it as
	// generated would tell an operator their certificate changed when it did
	// not.
	adopted.Generated = false
	retireLegacyKey(absDir)
	return adopted, true
}

// retireLegacyKey removes a key.pem this package wrote under the previous
// two-file layout, once the bundle holding the same material is published.
//
// Two copies of one private key is one copy too many, and the second copy is
// worse than redundant: after the next renewal it no longer matches the
// cert.pem beside it, so anybody who points TLS_KEY_FILE at the pair gets a
// mismatch error about files that look like they belong together.
//
// It refuses to remove a key it cannot prove this package wrote, which is the
// same rule as everywhere else in this file and matters more here than
// anywhere: this is the only line in the package that deletes key material.
// Every failure is ignored, because leaving the file behind is untidy and
// refusing to start is an outage.
func retireLegacyKey(absDir string) {
	path := filepath.Join(absDir, KeyFileName)
	keyPEM, err := readPEMFile(path)
	if err != nil {
		return
	}
	recorded, err := readProvisioned(absDir)
	if err != nil || !keyWasWrittenHere(keyPEM, recorded) {
		return
	}
	_ = os.Remove(path)
}
