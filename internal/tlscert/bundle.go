// The serving bundle: one file holding a certificate, its private key, and
// the note that says this package wrote them.
//
// It exists because of arithmetic, not taste. rename(2) replaces one
// directory entry atomically and there is no call that replaces two. While
// the certificate and its key lived in two files, publishing a pair was two
// renames, a reader arriving between them saw one writer's key beside
// another writer's certificate, and the only way to prevent that was to let
// a single writer into the directory at a time. A lock is a very heavy price
// for an arithmetic problem: it adds a holder that can be killed, can stall,
// or can lose the ability to release, and every one of those turns into
// another controller refusing to start. Putting both halves in one file
// makes publishing exactly one rename, which needs no lock at all.
//
// The provenance block is in here for the same reason the key is. "May this
// package replace what is published?" is a question about the material that
// is actually published, and an answer kept in a separate file can be lost
// by a concurrent writer of that file, at which point a certificate this
// package wrote looks like an operator's and is never renewed again. Keeping
// the answer inside the atomic unit makes it impossible to separate from the
// thing it describes.
//
// The block is a fingerprint rather than a flag, so it cannot be transplanted:
// a bundle assembled by pasting this header above somebody else's certificate
// does not claim that certificate, because the fingerprint would not match.
// It is not a signature and is not meant to resist an attacker who can write
// to this directory; anyone who can do that can do far worse. It resists the
// accident this package actually has to survive, which is material getting
// mixed up in a directory several processes write.
package tlscert

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"path/filepath"
)

// provenanceBlockType is the PEM block that says this package generated the
// certificate in the same file.
//
// A block rather than a comment because pem.Decode is the one parser every
// reader of this file already runs, and because crypto/tls skips a PEM block
// it does not recognise: the bundle is still a valid input to
// tls.X509KeyPair with this in it, which is what lets one file be both the
// certificate and the key argument.
const provenanceBlockType = "PLEIADES SELF-PROVISIONED CERTIFICATE"

// bundlePreamble is the prose written above the blocks.
//
// pem.Decode skips everything before the first BEGIN line, so this is free to
// be prose, and it has to be: the file holds a private key, and somebody who
// finds it has to be able to tell in one look what it is and what deleting it
// would do.
const bundlePreamble = `# The serving certificate and private key this controller provisioned for
# itself, in one file so that publishing them is a single atomic rename and
# no reader can ever see one writer's key beside another writer's
# certificate.
#
# THIS FILE HOLDS A PRIVATE KEY. To hand a client the trust anchor, copy
# cert.pem beside it instead, which is the same certificate with no key in
# it.
#
# Deleting this file makes this controller provision a new certificate on its
# next start, and every client that trusted the old one has to be told about
# the new one.
`

// encodeBundle renders one serving bundle: the provenance block, the
// certificate, then the key.
//
// The order is for a human. Anything reading this decodes the whole file
// anyway, and the first thing a person needs to know about a file holding a
// private key is where it came from.
func encodeBundle(certPEM, keyPEM []byte, leaf *x509.Certificate) []byte {
	fingerprint := fingerprintOf(leaf)

	var buf bytes.Buffer
	buf.WriteString(bundlePreamble)
	// pem.EncodeToMemory returns nil only for a block with a malformed
	// header, and this one has no headers at all, so there is no failure to
	// handle here.
	buf.Write(pem.EncodeToMemory(&pem.Block{Type: provenanceBlockType, Bytes: fingerprint[:]}))
	buf.Write(certPEM)
	buf.Write(keyPEM)
	return buf.Bytes()
}

// bundleSelfProvisioned reports whether raw is a serving bundle this package
// wrote.
//
// Both halves have to hold: the provenance block is present, and the
// fingerprint in it is the fingerprint of the certificate in the same file.
// Anything else, including a file that is a perfectly good certificate and
// key with no provenance block, is an operator's, and this package never
// replaces an operator's material.
func bundleSelfProvisioned(raw []byte) bool {
	var recorded []byte
	var leaf *x509.Certificate

	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case block.Type == provenanceBlockType && recorded == nil:
			recorded = block.Bytes
		case block.Type == "CERTIFICATE" && leaf == nil:
			// A block that does not parse is skipped rather than fatal: the
			// answer to "did this package write it" is then no, which is the
			// safe direction to be wrong in.
			if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
				leaf = cert
			}
		}
	}

	if leaf == nil || len(recorded) != sha256.Size {
		return false
	}
	want := fingerprintOf(leaf)
	return bytes.Equal(recorded, want[:])
}

// publishMaterial writes a certificate and its key into dir as one serving
// bundle, and returns what a listener should serve.
//
// The order of the three writes is the part worth reading, and it was got
// wrong once in a way only a sixteen-way race found. The history is recorded
// first, so a failure to record aborts before anything is published and the
// history always covers what is published. The bundle is next, and that
// rename is the moment this directory starts serving something new. cert.pem
// is LAST, and it is a copy rather than a publication.
//
// Writing cert.pem earlier put a certificate in the directory with no key and
// no bundle beside it, which is exactly the shape an operator's half-mounted
// material has, so a second controller arriving in that instant concluded the
// directory belonged to somebody else and refused to start. Nothing this
// package publishes may look like an operator's material at any instant, not
// just at rest.
//
// The returned material is built from the bytes in hand rather than by reading
// the bundle back. Ensure re-reads on purpose, because what it serves must be
// what is on disk; this function is the writing half and has no business
// hiding a failed read inside a successful write.
func publishMaterial(absDir string, leaf *x509.Certificate, certPEM, keyPEM []byte) (ServingCert, error) {
	if err := recordProvisioned(absDir, leaf); err != nil {
		return ServingCert{}, err
	}
	bundlePath, err := writeAtomic(absDir, BundleFileName, encodeBundle(certPEM, keyPEM, leaf))
	if err != nil {
		return ServingCert{}, err
	}
	republishCertificate(absDir, leaf)

	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		// Unreachable through either caller, both of which hand over a pair
		// that has already been parsed once. It is checked anyway because the
		// alternative to checking is returning material a listener cannot
		// serve, and the cost is one parse on a path that runs at start-up.
		return ServingCert{}, fmt.Errorf("the certificate and key just written to %s are not a usable pair: %w", bundlePath, err)
	}
	pair.Leaf = leaf

	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	// CertFile is settled by reading cert.pem back rather than by naming it,
	// because republishCertificate above is best effort and refuses to write
	// over a file this package cannot prove it wrote. Naming a cert.pem that
	// holds somebody else's bytes, or none at all, would print a path an
	// operator then copies out as this server's trust anchor.
	return withAnchorCopy(absDir, ServingCert{
		KeyFile:         bundlePath,
		Pair:            pair,
		Leaf:            leaf,
		Roots:           roots,
		Generated:       true,
		SelfProvisioned: true,
	}), nil
}

// republishCertificate keeps cert.pem holding the certificate this process
// is actually serving.
//
// It exists because cert.pem is a copy and copies drift. Several controllers
// starting at once each publish their own bundle and their own cert.pem, the
// last of each wins, and the two winners can be different processes, so a
// contended cold start can leave cert.pem describing a certificate nobody
// serves. Nothing important depends on it (AnchorsForDir reads the bundle as
// well, and the key is never in this file), but an operator who copies it out
// as a trust anchor deserves the one that works.
//
// Every failure is ignored, on purpose. This is a convenience copy: a
// directory that has gone read-only must not turn a controller that could
// serve into one that refuses to.
func republishCertificate(absDir string, leaf *x509.Certificate) {
	want := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	path := filepath.Join(absDir, CertFileName)

	if have, err := readPEMFile(path); err == nil && bytes.Equal(have, want) {
		return
	}
	// Never over an operator's certificate. The bundle being this package's
	// own says nothing about a cert.pem that arrived after it.
	if ours, err := certFileIsOurs(absDir); err != nil || !ours {
		return
	}
	_, _ = writeAtomic(absDir, CertFileName, want)
}

// anchorCopy reports the path of the key-free copy of leaf in dir, and
// whether cert.pem really holds that certificate.
//
// It exists because a caller PRINTS the path it gets back, and an operator
// follows what is printed. ServingCert.CertFile used to be the serving bundle
// on every reuse, and the serving bundle holds the private key, so a script
// that copied "the certificate" out of a deployment copied the key out with
// it. A path is reported as a certificate only when reading the file back
// proves it is one, and holds this one.
func anchorCopy(absDir string, leaf *x509.Certificate) (string, bool) {
	path := filepath.Join(absDir, CertFileName)
	raw, err := readPEMFile(path)
	if err != nil {
		return path, false
	}
	for _, cert := range parseCertificates(raw) {
		if cert.Equal(leaf) {
			return path, true
		}
	}
	return path, false
}

// withAnchorCopy points a result's CertFile at the key-free copy of its own
// certificate, or at the file the material really came from when there is no
// such copy.
//
// CertFile and KeyFile naming the same file is honest and a caller can see
// it: ServingCert.AnchorFile is how a caller asks whether the two differ
// before printing one of them as a certificate.
func withAnchorCopy(absDir string, cert ServingCert) ServingCert {
	if path, ok := anchorCopy(absDir, cert.Leaf); ok {
		cert.CertFile = path
		return cert
	}
	cert.CertFile = cert.KeyFile
	return cert
}
