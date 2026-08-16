// Loading: turning bytes on disk into a certificate a listener can present,
// and judging whether the one already there is still worth reusing.
//
// The split between the two halves here is this package's whole design in
// miniature. Load applies NO policy: it is the path for a certificate an
// operator configured, which this package has no business judging and no
// ability to replace, so the only question is whether it can be served, and
// everything else is a log line the caller writes. ValidityProblem is that
// log line. The unexported reusable below it applies every reuse rule,
// because it is the path for a certificate this package wrote and may write
// again.
package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"
	"time"
)

// Load reads a certificate and key with no policy applied at all: no renewal
// window, no required names, not even a validity check.
//
// It is the operator-configured path. TLS_CERT_FILE names a certificate this
// package has no business judging and no ability to replace, so the only
// question is whether it can be served, and the answer to everything else is
// a log line the caller writes. ValidityProblem is what produces that line.
func Load(certPath, keyPath string) (ServingCert, error) {
	certPEM, err := readPEMFile(certPath)
	if err != nil {
		return ServingCert{}, err
	}
	keyPEM, err := readPEMFile(keyPath)
	if err != nil {
		return ServingCert{}, err
	}
	return parsePair(certPEM, keyPEM, certPath, keyPath)
}

// parsePair turns certificate and key bytes already in memory into the form
// crypto/tls serves from.
//
// It is split out of Load for one caller: a serving bundle is a single file
// holding both, so it is read once and passed here twice, and reading it
// twice to satisfy a two-path signature would open a window where the two
// reads see different files.
func parsePair(certPEM, keyPEM []byte, certPath, keyPath string) (ServingCert, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		// tls.X509KeyPair is what catches a certificate sitting beside a key
		// that does not belong to it. A serving bundle cannot produce that
		// state, since both halves are published in one rename; a pair from
		// the older two-file layout can, and this is where it is caught.
		return ServingCert{}, fmt.Errorf("%s and %s are not a usable certificate and key: %w", certPath, keyPath, err)
	}
	if len(pair.Certificate) == 0 {
		return ServingCert{}, fmt.Errorf("%s parsed as a key pair holding no certificate", certPath)
	}

	// Parsed from the DER the pair actually loaded, so nothing below is
	// asserted about a file and then served from another.
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return ServingCert{}, fmt.Errorf("parsing %s: %w", certPath, err)
	}
	// Attached so that crypto/tls does not have to re-parse the leaf on
	// every handshake, and so a caller reading Pair.Leaf sees the same
	// certificate this returns.
	pair.Leaf = leaf

	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return ServingCert{
		CertFile: certPath,
		KeyFile:  keyPath,
		Pair:     pair,
		Leaf:     leaf,
		Roots:    roots,
	}, nil
}

// ValidityProblem describes why a certificate cannot be trusted right now,
// or returns an empty string when it can.
//
// It exists for the certificate this package must not touch: an operator's
// own, named by TLS_CERT_FILE. An expired one is still served, because
// substituting a self-signed certificate would change this server's identity
// behind the operator's back and every client that pinned the real one would
// break. What must not happen is serving it silently: an expired certificate
// fails every handshake, and "the site stopped working and nothing was
// logged" is a much longer outage than "the site stopped working and the
// controller said the certificate expired on Tuesday".
func ValidityProblem(leaf *x509.Certificate) string {
	now := time.Now()
	switch {
	case now.Before(leaf.NotBefore):
		return fmt.Sprintf("it is not valid until %s, so every handshake fails until then; check this machine's clock",
			leaf.NotBefore.UTC().Format(time.RFC3339))
	case now.After(leaf.NotAfter):
		return fmt.Sprintf("it expired at %s, so every handshake with it fails",
			leaf.NotAfter.UTC().Format(time.RFC3339))
	default:
		return ""
	}
}

// reusable reports why a stored certificate cannot simply be served as it
// is, or nil when it can.
//
// An error from this function is never fatal by itself. Every caller treats
// it as a reason to consider writing a new one, so the message it carries is
// the reason a certificate was replaced, not a failure, and Ensure puts it in
// ServingCert.Reason for exactly that.
//
// It is also the only thing standing between renewal and a churn loop. A
// certificate is replaced when it is unusable or inside its renewal window
// and at no other time, so a certificate this package has just written is
// reusable by the next call, and a directory converges on one certificate
// instead of being rewritten on every start. Ensure refuses a renewal window
// as wide as the lifetime for the same reason: that is the one configuration
// under which a fresh certificate would fail this check immediately.
func reusable(stored ServingCert, opts Options) error {
	leaf := stored.Leaf

	now := time.Now()
	if now.Before(leaf.NotBefore) {
		// A clock that moved backwards, or a certificate copied in from a
		// machine whose clock is ahead. Either way it fails a handshake
		// exactly like an expired one, so it is replaced rather than served.
		return fmt.Errorf("the stored certificate is not valid until %s", leaf.NotBefore.UTC().Format(time.RFC3339))
	}
	if renewAt := leaf.NotAfter.Add(-opts.RenewBefore); !now.Before(renewAt) {
		return fmt.Errorf("the stored certificate expires at %s, which is inside its %s renewal window",
			leaf.NotAfter.UTC().Format(time.RFC3339), opts.RenewBefore)
	}
	if missing := missingNames(leaf, opts); len(missing) > 0 {
		return fmt.Errorf("the stored certificate does not cover %s", strings.Join(missing, ", "))
	}
	return nil
}

// missingNames lists the REQUIRED subject alternative names the stored
// certificate does not carry.
//
// This is what makes a configuration change take effect. An operator who
// adds their real hostname and restarts expects to be able to reach the
// controller by that name; without this check the stored certificate is
// still valid, still unexpired, and still wrong, and the browser rejects it
// for a name mismatch that looks identical to the problem TLS was turned on
// to fix.
//
// Required, not every name the certificate would be generated with, and the
// difference is load bearing: see Options.OptionalNames for the
// container-hostname churn that reading resolveNames here caused.
func missingNames(leaf *x509.Certificate, opts Options) []string {
	wantDNS, wantIP := requiredNames(opts)

	have := make(map[string]bool, len(leaf.DNSNames))
	for _, name := range leaf.DNSNames {
		// Lowercased on both sides because DNS names are case insensitive,
		// so a certificate carrying "Controller" really does cover
		// "controller" and must not be regenerated on every start.
		have[strings.ToLower(name)] = true
	}

	var missing []string
	for _, name := range wantDNS {
		if !have[strings.ToLower(name)] {
			missing = append(missing, name)
		}
	}
	for _, ip := range wantIP {
		if !hasIP(leaf.IPAddresses, ip) {
			missing = append(missing, ip.String())
		}
	}
	return missing
}

// hasIP reports whether addrs holds want.
//
// net.IP.Equal rather than a string or byte comparison, because the same
// address has more than one representation: 127.0.0.1 stored as a 4-byte
// value and as its 16-byte IPv4-mapped form are equal addresses and unequal
// slices, and comparing the wrong way regenerates a perfectly good
// certificate on every single start.
func hasIP(addrs []net.IP, want net.IP) bool {
	for _, addr := range addrs {
		if addr.Equal(want) {
			return true
		}
	}
	return false
}
