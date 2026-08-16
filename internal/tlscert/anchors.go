// Trust anchors: what a client inside this container needs in order to
// verify the listener inside this container, without ever turning
// verification off.
//
// The container healthcheck is the only caller. It dials loopback and has to
// decide whether the thing that answered is really this controller, and the
// two easy answers are both wrong. InsecureSkipVerify would make the probe
// report "ready" for anything at all that bound the port. The system root
// pool would reject a self-signed certificate that is working perfectly.
//
// The right anchor is this deployment's own certificate material, which is a
// stronger check than either: the handshake only succeeds if the listener
// presents a certificate this deployment was configured to serve AND proves
// it holds the matching private key. The one subtlety is that "the
// certificate this deployment serves" is a set rather than a single file,
// because a controller serves what it loaded at start-up while the directory
// may have moved on. See provisioned.go for why the history is kept.
package tlscert

import (
	"crypto/x509"
	"fmt"
	"path/filepath"
)

// Anchors is what a client needs in order to verify a listener: the pool to
// trust, and the name to ask that listener to prove.
type Anchors struct {
	// Roots holds every certificate this deployment may legitimately be
	// presenting, and nothing else. No public authority is in it.
	Roots *x509.CertPool

	// ServerName is the name the client asks to be shown, which is not the
	// address it dials.
	//
	// A self-probe always dials loopback, because the process it is checking
	// is in this container, while a real deployment's certificate is issued
	// for a public hostname and may carry no loopback SAN at all. Sending
	// the certificate's own first DNS name as SNI is what lets one probe
	// work for both, and it weakens nothing: the certificate still has to be
	// one of the configured ones and its key still has to be proven.
	//
	// Empty is not a fallback that gives up. It makes crypto/tls verify
	// against the address dialed instead, which is exactly right for a
	// certificate whose only names are IP addresses.
	ServerName string
}

// AnchorsForFile trusts exactly the certificate in one file.
//
// This is the operator-configured path (TLS_CERT_FILE). There is no history
// to consider: the file is whatever the operator put there, and a rotation
// they perform is picked up by the restart that also makes the server load
// it.
func AnchorsForFile(certFile string) (Anchors, error) {
	raw, err := readPEMFile(certFile)
	if err != nil {
		// Wrapped so that errors.Is(err, fs.ErrNotExist) still answers, which
		// is how a caller tells a cold start from a real fault.
		return Anchors{}, fmt.Errorf("reading the serving certificate %s: %w", certFile, err)
	}
	certs := parseCertificates(raw)
	if len(certs) == 0 {
		return Anchors{}, fmt.Errorf("%s holds no PEM certificate a client could trust", certFile)
	}
	return anchorsFrom(certs), nil
}

// AnchorsForDir trusts every certificate this package has provisioned in
// dir: the ones currently published, plus the ones they replaced.
//
// The history is what makes a self-probe survive a renewal. A controller
// serves the material it loaded at start-up, from memory, for as long as it
// runs. If a second controller sharing the directory renews, the first one is
// still serving a certificate the published files no longer describe, and a
// probe anchored on those alone would report a healthy process unhealthy
// until an orchestrator restarted it. Anchoring on "anything this deployment
// provisioned" fixes that without trusting anything this deployment did not
// write.
func AnchorsForDir(dir string) (Anchors, error) {
	// The serving bundle first, because it is the file the listener was
	// actually handed, and anchorsFrom takes the name to ask for off the
	// first certificate. cert.pem is read too rather than instead: it is a
	// copy of the same certificate that a directory written by an earlier
	// build has and a fresh one may briefly disagree with, and trusting both
	// costs nothing (a certificate is only in either file because this
	// package published it) while trusting one of them can mean a probe
	// rejecting a listener that is working.
	var certs []*x509.Certificate
	var readErr error
	for _, name := range []string{BundleFileName, CertFileName} {
		path := filepath.Join(dir, name)
		raw, err := readPEMFile(path)
		if err != nil {
			if readErr == nil {
				// A missing certificate is the cold start: the server writes
				// it before it opens its listener, so "not there yet" and
				// "not listening yet" are the same fact, and the caller
				// distinguishes them from a fault by the wrapped
				// fs.ErrNotExist.
				readErr = fmt.Errorf("reading the serving certificate %s: %w", path, err)
			}
			continue
		}
		found := parseCertificates(raw)
		if len(found) == 0 && readErr == nil {
			readErr = fmt.Errorf("%s holds no PEM certificate a client could trust", path)
			continue
		}
		certs = append(certs, found...)
	}

	// Best effort, and deliberately not an error, in BOTH of the ways it can
	// fail. A directory with no history file is one whose only certificate is
	// the published one, which is exactly what the probe already has. And a
	// history file that cannot be read costs this set the members it holds
	// and nothing else: propagating that error, which is what this used to
	// do, made a controller that provisions and serves perfectly fail its own
	// healthcheck forever over a legacy file nothing else here reads.
	// readProvisioned returns what it did gather alongside its error for
	// exactly this caller.
	history, _ := readProvisioned(dir)
	certs = append(certs, history...)

	// Counted AFTER the history is added, which is the other half of "admit
	// what a running replica may still be presenting". A replica keeps serving
	// from memory, so the published files disappearing underneath it says
	// nothing about what it is holding; refusing to build anchors at that
	// point would fail a process that is answering every request. An empty
	// directory still lands here with readErr wrapping fs.ErrNotExist, which
	// is how the caller tells a cold start from a fault.
	if len(certs) == 0 {
		return Anchors{}, readErr
	}

	return anchorsFrom(certs), nil
}

// anchorsFrom builds the pool and picks the name to verify against.
//
// The name comes from the FIRST certificate, which is always the one
// currently published, so a probe asks for the name the listener is most
// likely to present.
func anchorsFrom(certs []*x509.Certificate) Anchors {
	roots := x509.NewCertPool()
	for _, cert := range certs {
		roots.AddCert(cert)
	}
	name := ""
	if len(certs[0].DNSNames) > 0 {
		name = certs[0].DNSNames[0]
	}
	return Anchors{Roots: roots, ServerName: name}
}
