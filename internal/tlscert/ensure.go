// Reuse: deciding whether the certificate already on disk is still good
// enough to serve, minting a replacement only when it is not, and doing both
// in a way that any number of controllers can run against one directory at
// the same instant.
//
// The shape of this function is the whole design, and it is worth reading
// before changing any of it. There are three steps and no loop:
//
//	LOAD what is published. If it can be served, serve it, and stop.
//	Otherwise GENERATE a replacement and publish it with one rename.
//	Then LOAD again, and serve whatever is there now.
//
// There is no lock, no claim, no staleness rule, no retry budget and no
// backoff. That is not a simplification for its own sake; it removes an
// entire family of failures at once. Every version of this function that
// serialized its writers had the same defect wearing different hats: one bad
// holder blocks everybody. A holder killed mid-write blocked the directory
// until its claim went stale, a directory that turned read-only leaked the
// claim silently, and a holder that was merely SLOW made every other
// controller exhaust its patience and refuse to start. Raising the budget or
// shortening the staleness window just moves which of those happens.
//
// A lock was the wrong primitive because the thing it guarded is cheap,
// idempotent and self-verifying. Generating a self-signed certificate costs a
// few milliseconds, and any valid pair is exactly as good as any other. So
// racers are allowed to race: N controllers may all generate and all publish,
// the last rename wins, and every one of them then READS that winner. Nothing
// is lost but a few milliseconds of a loser's CPU, and no controller can ever
// refuse to start because another one is slow or dead, because no controller
// waits for another one at all.
//
// Two files could not be published in one step, which is what made a lock
// look necessary; one file can, so the certificate and its key are one file
// (bundle.go).
//
// The last thing this function does is a LOAD, never a give-up. What it
// returns is bytes it really read back, with exactly one exception, stated at
// the line that takes it: when the re-read finds nothing this caller can use
// (the directory became unreadable, or the racer that won published a
// certificate missing a name this caller requires), the freshly minted
// material is served instead. Serving something valid is the job; the
// alternative there is refusing to start, which is the thing this whole file
// exists to stop doing.
package tlscert

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Ensure returns the serving certificate stored in dir, generating one only
// when what is there cannot be served.
//
// A controller that minted a fresh certificate on every restart would retrain
// every operator to click through browser warnings, and would break any client
// that pinned the previous one, so reuse is the normal path and generation is
// the exception. A stored certificate is replaced when it is missing,
// unreadable, mismatched, not yet valid, inside its renewal window, or missing
// a name the caller now asks for. That last case is what makes adding a
// hostname to the configuration take effect on the next restart instead of
// silently doing nothing.
//
// Two rules bound what it will do to a directory, and both exist because
// breaking them destroys something:
//
//   - It never replaces a private key it did not write. A serving bundle
//     proves its own authorship, and a bare key is proven by the certificate
//     it belongs to. A key it cannot account for, and a file it cannot read
//     at all, are refusals that name the file. Bytes that do not parse and a
//     certificate with no key beside it are neither a secret nor servable, so
//     neither one refuses. See ownership.go.
//   - It never refuses to start over a certificate it could serve. If the
//     directory cannot be written, a stored pair that is still inside its
//     validity is served with a warning rather than a startup failure.
//
// What several controllers sharing one directory get, and what they do not.
// They get: every one of them starts, every one of them serves a valid pair,
// none of them waits on another, and the directory converges on one
// certificate that all of them adopt on their next restart. They do not get an
// instantaneous guarantee that all of them present the SAME certificate: a
// racer whose re-read lands before a later racer's rename serves the one it
// read, for the life of that process. That is accepted deliberately. These
// certificates authenticate nothing (a client has to be handed the exact
// certificate to trust it either way), every one of them gets its own
// provenance record that no other writer can lose, so the container
// healthcheck accepts a replica serving any of them, and the alternative is
// the lock whose every failure mode was a controller that would not boot.
// Convergence rather than agreement is the promise, and it converges: nothing
// rewrites a bundle that is servable, so the last rename stands until it is
// genuinely due for renewal.
//
// The returned ServingCert says which of generation and reuse happened, why a
// replacement was written (Reason), and what an operator has to be told about
// the result (Warning).
func Ensure(dir string, opts Options) (ServingCert, error) {
	opts = opts.withDefaults()

	// The one configuration that could never converge, refused up front
	// rather than discovered later: a renewal window at least as wide as the
	// lifetime means every certificate this function wrote would be inside
	// its own window the moment it was written, so every start would generate
	// a certificate the next start throws away. That is the churn loop this
	// design has to be able to promise it does not have.
	if opts.RenewBefore >= opts.TTL {
		return ServingCert{}, fmt.Errorf(
			"RenewBefore (%s) is not shorter than TTL (%s), so every certificate written would already be due for renewal",
			opts.RenewBefore, opts.TTL)
	}
	// Checked here, before a directory is touched, so a mistyped name is a
	// configuration error naming the value rather than a signing failure from
	// inside crypto/x509 several steps later.
	if err := ValidateNames(opts.ExtraNames); err != nil {
		return ServingCert{}, err
	}
	if err := ValidateNames(opts.OptionalNames); err != nil {
		return ServingCert{}, err
	}

	absDir, err := prepareDir(dir)
	if err != nil {
		return ServingCert{}, err
	}

	// Before anything else touches the directory, because a temporary file
	// left behind by a process that was killed mid-write holds real private
	// key material and nothing else will ever remove it.
	sweepStaleTemporaries(absDir)

	// Step one: LOAD. One value carries both halves of the answer: reason is
	// nil exactly when the stored material can be served as it is, and
	// otherwise it is the sentence an operator reads as replaced_because, so
	// the reason a certificate was replaced is never computed twice or lost.
	stored, reason := loadPublished(absDir)
	if reason == nil {
		reason = reusable(stored, opts)
	}
	if reason == nil {
		return settle(absDir, stored), nil
	}

	// Whatever is wrong with it, if replacing it would destroy a private key
	// this package cannot account for, or a file it cannot even read, then it
	// is not this package's to replace.
	if blocker := publishBlocker(absDir); blocker != nil {
		return serveForeign(absDir, reason, blocker)
	}

	// Step two: GENERATE, and publish with one rename.
	generated, genErr := publishBundle(absDir, opts)
	if genErr != nil {
		// Not a race, because there is nothing here to race for: the
		// directory cannot be written at all. Serving a certificate that is
		// still valid beats refusing to start.
		if degraded, ok := serveDespite(absDir, reason, genErr); ok {
			return degraded, nil
		}
		return ServingCert{}, genErr
	}
	generated.Reason = reason.Error()
	retireLegacyKey(absDir)

	// Step three: LOAD again, and serve what is really there.
	//
	// This is the line that makes racing safe. Between the generate above and
	// this read, another controller may have published its own bundle; that
	// one is just as good, it is already recorded as this deployment's, and
	// reading it is how every racer ends up on the same certificate without
	// any of them having had to wait.
	final, finalErr := loadPublished(absDir)
	if finalErr == nil && final.SelfProvisioned && reusable(final, opts) == nil {
		final.Generated = true
		final.Reason = reason.Error()
		claimServed(absDir, final.Leaf)
		return withAnchorCopy(absDir, final), nil
	}
	// The re-read found nothing better than what was just minted: the
	// directory became unreadable, or the racer that won published a
	// certificate that does not carry a name THIS caller requires. Serving
	// the material in hand is right either way, and it is on disk in the
	// provenance record whether or not it is the published one.
	return generated, nil
}

// loadPublished reads whatever dir currently publishes and says whose it is,
// with no reuse policy applied.
//
// The serving bundle wins when there is one, because it is the only file this
// package publishes as a matched pair in a single step. The cert.pem/key.pem
// layout is the fallback, and it covers two cases at once: a directory
// written by an earlier build of this package, and an operator's own material
// copied into the directory this package manages.
//
// The error it returns is printed to operators as replaced_because, so it is
// a sentence rather than whatever the last call happened to fail with. The
// case that matters most is the most common one there is: a first start into
// an empty directory used to report "stat /data/tls/cert.pem: no such file or
// directory", which is a syscall, not an explanation, and made an ordinary
// cold start read like a fault.
func loadPublished(absDir string) (ServingCert, error) {
	bundlePath := filepath.Join(absDir, BundleFileName)
	raw, err := readPEMFile(bundlePath)
	switch {
	case err == nil:
		cert, parseErr := parsePair(raw, raw, bundlePath, bundlePath)
		if parseErr != nil {
			return ServingCert{}, parseErr
		}
		cert.SelfProvisioned = bundleSelfProvisioned(raw)
		if !cert.SelfProvisioned {
			cert.Warning = foreignWarning(bundlePath, bundlePath)
		}
		return withAnchorCopy(absDir, cert), nil
	case !errors.Is(err, fs.ErrNotExist):
		return ServingCert{}, err
	}

	certPath := filepath.Join(absDir, CertFileName)
	keyPath := filepath.Join(absDir, KeyFileName)
	cert, err := Load(certPath, keyPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ServingCert{}, nothingPublished(absDir, certPath, keyPath)
		}
		return ServingCert{}, err
	}
	recorded, err := readProvisioned(absDir)
	if err != nil {
		return ServingCert{}, err
	}
	cert.SelfProvisioned = recordedIn(recorded, cert.Leaf)
	if !cert.SelfProvisioned {
		cert.Warning = foreignWarning(certPath, keyPath)
	}
	return cert, nil
}

// nothingPublished turns a missing file into the sentence an operator reads
// on a start that finds no servable material.
//
// Three states reach it and they are worth telling apart: two of them describe
// half of a pair, which is what a half-finished mount leaves, and the third is
// the ordinary first start of a fresh deployment.
func nothingPublished(absDir, certPath, keyPath string) error {
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	switch {
	case certErr == nil:
		return fmt.Errorf("%s holds a certificate with no private key beside it, so there is nothing here to serve", certPath)
	case keyErr == nil:
		return fmt.Errorf("%s holds a private key with no certificate beside it, so there is nothing here to serve", keyPath)
	default:
		return fmt.Errorf("no certificate is stored in %s yet", absDir)
	}
}

// settle is what happens when the published material can simply be served:
// it is served, and the directory is tidied toward the layout this package
// wants without ever risking what is already working.
//
// Both repairs are best effort and neither can fail this call, because the
// material being returned is already good. An operator's own material is left
// exactly as it is, down to the last byte.
func settle(absDir string, stored ServingCert) ServingCert {
	if !stored.SelfProvisioned {
		return stored
	}
	if stored.KeyFile != filepath.Join(absDir, BundleFileName) {
		// A pair from the earlier two-file layout, still perfectly good. It
		// moves into a bundle so that the next start reads one atomic file,
		// carrying the same certificate rather than minting a new one.
		if adopted, ok := adoptLegacy(absDir, stored); ok {
			return adopted
		}
		return stored
	}
	claimServed(absDir, stored.Leaf)
	return withAnchorCopy(absDir, stored)
}

// claimServed writes down what this process is about to serve, so that a
// probe of this process accepts it for as long as it runs.
//
// Both writes are best effort and neither can fail a start, because the
// material is already good and this is housekeeping.
//
// Recording on the REUSE path is the half that was missing, and its absence
// was a live process being killed by its own orchestrator. A replica that
// reuses what it finds writes no record of its own, so its anchor is somebody
// else's file: let those records be lost (an operator tidying the directory,
// a restore that missed a subdirectory) and one sibling publishing one new
// certificate was enough to leave that replica serving a certificate no probe
// would accept, forever. Recording here means every running replica put its
// own answer in the set, in a file named after its own fingerprint that no
// other writer can touch.
func claimServed(absDir string, leaf *x509.Certificate) {
	_ = recordProvisioned(absDir, leaf)
	republishCertificate(absDir, leaf)
}

// publishBundle mints a certificate and publishes it as one serving bundle.
func publishBundle(absDir string, opts Options) (ServingCert, error) {
	fresh, err := mint(opts)
	if err != nil {
		return ServingCert{}, err
	}
	return publishMaterial(absDir, fresh.leaf, fresh.certPEM, fresh.keyPEM)
}
