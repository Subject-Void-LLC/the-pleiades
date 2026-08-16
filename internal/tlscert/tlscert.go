// Package tlscert generates, persists and reuses the self-signed serving
// certificate a process presents when nobody handed it a real one.
//
// It exists because four things in this repository need a certificate the
// controller can present, and four private copies of "generate a
// self-signed cert" is exactly the shape that drifts: the controller
// itself when an operator configured none, the development server
// (tools/uidev), the end-to-end harness (tests/e2e), and the make target
// that writes one by hand (tools/devcert). One of them would forget the IP
// SAN, or the extended key usage, and the failure arrives as an
// unexplained handshake error in whichever tool was not being looked at.
//
// This logic used to live in internal/testsupport, which imports testing.
// That was fine while only tests and developer tools called it, and stopped
// being fine the moment the shipped controller needed it: importing a
// package that imports testing links the testing package into the shipped
// binary, which registers test flags on the default flag set and grows the
// image for no one's benefit. So the logic lives here, in an ordinary
// production package, and internal/testsupport now calls it rather than
// holding a second copy. internal/archtest fails the build if a production
// package ever imports internal/testsupport again.
//
// What a certificate from this package is, and is not. It ENCRYPTS: a
// caller's password and session cookie cross the network sealed. It does
// not AUTHENTICATE: nothing a client already trusts vouches for it, so a
// browser warns, and a client that clicks through the warning has no way to
// tell this server from an interposed one. That is a real limitation and
// callers are expected to say so out loud rather than to imply the traffic
// is as protected as a real certificate would make it.
package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"time"
)

// CertFileName, KeyFileName and BundleFileName are what this package writes
// inside the directory it is given.
//
// Exported because more than one caller has to name these paths in a place
// Go cannot reach: a make target, a shell command in a document, an
// operator's `curl --cacert`. Two spellings of "cert.pem" is a support
// question waiting to happen.
//
// The three are not equals, and the difference is the whole of this
// package's concurrency story:
//
//   - BundleFileName is the SERVING material, a certificate and its own
//     private key in one file. It is what Ensure loads, what a listener is
//     handed, and the only file here whose contents are guaranteed to be a
//     matched pair. See bundle.go for why one file rather than two.
//   - CertFileName holds that same certificate with no key beside it. It is
//     published so a client can be handed the trust anchor without being
//     handed the server's private key (`docker compose cp
//     controller:/data/tls/cert.pem`, `curl --cacert`), and so that a
//     directory written by an earlier build is still readable.
//   - KeyFileName is written only by Generate, which produces the two-file
//     layout an operator's own TLS_CERT_FILE and TLS_KEY_FILE pair arrives
//     in and which the developer tools want. Ensure never writes it. It
//     reads one left by an earlier build, moves that pair into a bundle, and
//     retires the file only once it can prove this package wrote it.
const (
	CertFileName   = "cert.pem"
	KeyFileName    = "key.pem"
	BundleFileName = "serving.pem"
)

// DefaultTTL bounds how long a generated certificate stays valid when
// Options.TTL is zero.
//
// A year, chosen against two real limits rather than as a round number. It
// has to be long enough that a controller which is restarted only for
// upgrades does not expire between them, because this package renews at
// startup and nowhere else, and an expired certificate fails a handshake in
// a way that reads like a configuration mistake. It has to be short enough
// that a browser still accepts it: Apple's platforms refuse a server
// certificate whose validity period exceeds 398 days, and while that rule
// is written for publicly trusted certificates, a local trust decision is
// not worth betting on the exception. A year sits under that cap with room
// for the backdating below.
const DefaultTTL = 365 * 24 * time.Hour

// DefaultRenewBefore is how long before expiry Ensure stops reusing a
// stored certificate and writes a new one, when Options.RenewBefore is
// zero.
//
// Thirty days, which is the same shape every ACME client uses and for the
// same reason: renewal only happens at startup, so the window has to be
// wide enough that a normal restart cadence lands inside it at least once
// before the certificate actually expires. A window of zero would mean the
// only chance to renew is the one restart that happens to fall in the last
// second of validity.
const DefaultRenewBefore = 30 * 24 * time.Hour

// dirMode and fileMode are the permissions this package writes with.
//
// 0600 files inside a 0700 directory, following the precedent
// docs/10-running-in-production.md already documents for the master
// encryption key. The private key is a secret in the ordinary sense:
// anyone holding it can present this server's identity to a client that
// trusted it. The certificate is public, and is written 0600 anyway,
// because a caller that needs another user to read it should have to say
// so where the reason can be written down (tools/devcert does exactly
// that).
const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

// Options configures what a generated certificate covers and how long it
// lives. The zero value is valid and means "the loopback names, the
// default lifetime, the default renewal window".
type Options struct {
	// ExtraNames are subject alternative names to add beyond the loopback
	// set every certificate from this package already carries. An entry
	// that parses as an IP address becomes an IP SAN and everything else
	// becomes a DNS SAN, because a certificate is matched against the name
	// the client DIALED and the two are separate fields: listing
	// "127.0.0.1" as a DNS name produces a certificate that looks correct
	// in every dump and fails every handshake with "certificate is not
	// valid for any names".
	//
	// Blank and duplicate entries are dropped rather than rejected, so a
	// caller can hand this the result of splitting an environment variable
	// without pre-cleaning it.
	//
	// These are REQUIRED names: Ensure replaces a stored certificate that
	// does not already carry one of them. That is what makes adding a
	// hostname to the configuration take effect on the next restart.
	ExtraNames []string

	// OptionalNames are added to a certificate when one is generated and
	// are NOT required of a stored one.
	//
	// The distinction exists because of a defect found by running the
	// compose stack, not by reading the code. The controller adds the
	// machine's own hostname to its certificate, which is right: an
	// operator reaching it by that name needs it there. Inside a container
	// the hostname is the container ID, and `docker compose down` followed
	// by `up` creates a container with a NEW id. With the hostname treated
	// as required, every restart found a stored certificate that did not
	// cover the new hostname and replaced it, which is exactly the
	// regenerate-every-time behavior reuse exists to prevent, arriving
	// through the back door.
	//
	// So the rule is about who asked. A name an operator configured is a
	// requirement and is worth replacing a certificate for. A name this
	// process discovered about itself is a convenience, and a convenience
	// must not invalidate a certificate somebody already trusted.
	OptionalNames []string

	// TTL is how long the certificate stays valid. Zero means DefaultTTL.
	TTL time.Duration

	// RenewBefore is how long before expiry Ensure replaces a stored
	// certificate instead of reusing it. Zero means DefaultRenewBefore.
	RenewBefore time.Duration
}

// withDefaults returns a copy with the zero fields filled in, so every
// function in this package reads one already-resolved value rather than
// repeating the same "if zero then default" line.
func (o Options) withDefaults() Options {
	if o.TTL <= 0 {
		o.TTL = DefaultTTL
	}
	if o.RenewBefore <= 0 {
		o.RenewBefore = DefaultRenewBefore
	}
	return o
}

// ServingCert is a serving certificate on disk, plus what a client needs
// in order to trust it and what a caller needs in order to describe it.
type ServingCert struct {
	// CertFile and KeyFile are absolute paths, suitable for the
	// controller's own TLS_CERT_FILE and TLS_KEY_FILE.
	//
	// They are allowed to name the SAME file, and on Ensure's path they
	// usually do not: CertFile is cert.pem and KeyFile is the serving
	// bundle, which holds the certificate as well as the key. That still
	// satisfies every caller, including tls.LoadX509KeyPair, because the
	// standard library reads certificates out of the first file and a
	// private key out of the second and skips whatever else it finds in
	// either.
	//
	// A caller that is going to PRINT one of these, or hand it to something
	// that expects a certificate and nothing else, has to ask AnchorFile
	// rather than reading CertFile directly. The two fields naming one file
	// is a real state (an operator's own bundle, a directory whose cert.pem
	// could not be written) and in that state CertFile holds a private key.
	CertFile string
	KeyFile  string

	// Pair is the certificate and private key already parsed into the form
	// crypto/tls serves from.
	//
	// It exists so that a server can be handed the exact material that was
	// just verified, instead of being handed two paths and re-reading them.
	// That is not a micro-optimisation, it is the fix for a real crash: two
	// controllers sharing a directory could have one of them verify a pair,
	// log that it was listening, and then have ListenAndServeTLS re-read the
	// two files after the other controller replaced them, failing with
	// "tls: private key does not match public key" and exiting. Verifying
	// and serving the same bytes closes that window completely.
	Pair tls.Certificate

	// Leaf is the parsed certificate that is actually on disk. It is
	// parsed back out of the written bytes rather than kept from the
	// template, so a caller logging NotAfter is reporting what a client
	// will really be shown.
	Leaf *x509.Certificate

	// Roots holds exactly this certificate and nothing else. A client
	// given this pool trusts this one server and no public authority,
	// which is a stronger position than the system pool, not a weaker
	// one.
	Roots *x509.CertPool

	// Generated records whether this call wrote a new certificate (true)
	// or reused one that was already on disk (false). Callers log it,
	// because "generated" and "reused" are the two facts an operator needs
	// to tell a fresh volume from a working one.
	Generated bool

	// SelfProvisioned records whether this package wrote the pair, as
	// distinct from finding material an operator put in the directory.
	//
	// It is the fact that decides whether the pair may ever be replaced,
	// and callers log it, because an operator whose own certificate is
	// being served out of the auto-provisioning directory needs to know
	// that it will never be renewed from there.
	SelfProvisioned bool

	// Reason says why a stored certificate was replaced, when Generated is
	// true.
	//
	// This field exists because the reason used to be computed precisely
	// ("does not cover controller.example.test", "expires at T, inside its
	// renewal window") and then dropped on the floor by the caller, which
	// left "why does it regenerate on every restart?" unanswerable from the
	// logs. The check that knows the answer is the only thing that knows it.
	Reason string

	// Warning is a problem an operator must be told about that did not stop
	// this certificate from being served.
	//
	// Serving something imperfect beats refusing to start, and both of the
	// cases that produce a warning are exactly that trade: material this
	// package did not write and therefore will never replace, and a stored
	// certificate that needed renewing in a directory that could not be
	// written.
	Warning string
}

// AnchorFile returns the file holding this certificate with no private key in
// it, and whether there is such a file.
//
// It exists because of a real leak shape rather than for tidiness. The
// startup log used to print CertFile under the name cert_file, and on every
// reuse CertFile was the serving bundle, which is the one file here that
// holds the PRIVATE KEY. An operator following a field named cert_file into a
// script (a `curl --cacert`, a ConfigMap, a copy to a colleague) would have
// handed this server's private key to something that only ever wanted the
// certificate.
//
// False means there is no key-free copy to point at, so a caller must print
// nothing rather than print the file it has.
func (c ServingCert) AnchorFile() (string, bool) {
	return c.CertFile, c.CertFile != "" && c.CertFile != c.KeyFile
}

// TLSClientConfig returns the client configuration for reaching a server
// presenting this certificate.
//
// This exists so that no caller has to write a tls.Config by hand, which
// is where InsecureSkipVerify gets typed. Skipping verification would turn
// every test that uses it into a test that proves nothing about the
// certificate, and it would trade a clean scan for a gosec G402 finding.
func (c ServingCert) TLSClientConfig() *tls.Config {
	return &tls.Config{
		RootCAs: c.Roots,
		// The same floor cmd/controller sets on the serving side, so a
		// client from this helper and the server it dials cannot disagree
		// about which protocol versions are acceptable.
		MinVersion: tls.VersionTLS12,
	}
}
