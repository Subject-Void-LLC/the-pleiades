// Serving something imperfect rather than refusing to start, and saying so.
//
// Two situations end up here, and they have opposite causes and the same
// answer. A private key this package cannot account for, or a file it cannot
// read at all, is never written over, because a private key exists in exactly
// one place and an operator who put theirs in this directory said something
// about their intentions even if they said it in the wrong place. Material
// this package DID write but could not replace, because the directory stopped
// being writable, is served anyway, because renewal is an optimisation and
// serving is the job.
//
// The rule both share is the one this package will not break: a controller
// must never refuse to start over a certificate it could have served. The
// only thing that changes is what the caller is told, and every path here
// leaves a message naming the file and the setting that fixes it, because a
// state that is being served but is not the intended one has to be visible
// or it becomes permanent.
package tlscert

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// foreignWarning is what a caller is told about material this package did not
// write but is serving anyway.
func foreignWarning(certPath, keyPath string) string {
	return fmt.Sprintf(
		"%s was not written by this controller, so it is served exactly as it is and is never renewed or replaced; set TLS_CERT_FILE and TLS_KEY_FILE to configure it deliberately",
		namePaths(certPath, keyPath))
}

// namePaths joins the files a message is about, and names one file once.
//
// The certificate and the key are the same file whenever the material is a
// serving bundle, and "x and x" in an error reads like a bug in the error
// rather than like the fact it is.
func namePaths(paths ...string) string {
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		seen := false
		for _, kept := range unique {
			if kept == path {
				seen = true
				break
			}
		}
		if !seen {
			unique = append(unique, path)
		}
	}
	return strings.Join(unique, " and ")
}

// serveForeign decides what happens when this package may not publish into
// its own directory: a private key it cannot account for is there, or a file
// it cannot read at all.
//
// Never a replacement. A private key exists in exactly one place, and an
// operator who copied theirs in here has said something about their
// intentions even if they said it in the wrong place. If what is there loads
// at all it is served with a warning; if it does not, this refuses to start.
// The one thing that must not happen is the material quietly becoming a
// self-signed pair.
//
// blocker is the refusal, already written by ownership.go, and it is used
// verbatim rather than summarised. It names the exact file and the exact
// action, and the message it replaced said "material this controller did not
// write" about every one of these states, which sent an operator looking for
// a secret that in most of them was not there.
func serveForeign(absDir string, reason, blocker error) (ServingCert, error) {
	if cert, err := loadAnything(absDir); err == nil {
		cert.Warning = fmt.Sprintf(
			"%s, so it is served exactly as it is and is never renewed or replaced, even though %s", blocker, reason)
		return cert, nil
	}
	// One unreadable file is often both why nothing could be loaded and why
	// nothing may be written, and printing the same sentence twice in one
	// error reads like a bug in the error rather than like the fact it is.
	if strings.Contains(blocker.Error(), reason.Error()) {
		return ServingCert{}, blocker
	}
	return ServingCert{}, fmt.Errorf(
		"%w, and what is stored in %s cannot be served as it stands either (%v)", blocker, absDir, reason)
}

// serveDespite falls back to whatever is already published when this package
// could not write a replacement, and reports whether there was anything to
// fall back to.
//
// The case it exists for is mundane and was a total outage: a directory that
// is no longer writable (a volume remounted read-only, a permission change, a
// full disk) holding a certificate that is perfectly good and has just entered
// its renewal window. Renewal is an optimisation. Serving is the job.
// Refusing to start because an optimisation failed takes a controller that
// would have worked for another thirty days and stops it.
//
// A certificate outside its validity is not a fallback, because serving it
// fails every handshake: there is genuinely nothing to serve, and the error
// the caller already has is the honest answer.
func serveDespite(absDir string, reason, cause error) (ServingCert, bool) {
	cert, err := loadAnything(absDir)
	if err != nil {
		return ServingCert{}, false
	}
	if now := time.Now(); now.Before(cert.Leaf.NotBefore) || now.After(cert.Leaf.NotAfter) {
		return ServingCert{}, false
	}
	cert.Warning = fmt.Sprintf(
		"the stored certificate needs replacing (%s) and this controller could not write a new one (%s), so it is serving the stored certificate until that is fixed",
		reason, cause)
	return cert, true
}

// loadAnything reads the published material with neither reuse policy nor
// provenance applied: the last resort, for the two paths that have already
// decided nothing is going to be written.
func loadAnything(absDir string) (ServingCert, error) {
	bundlePath := filepath.Join(absDir, BundleFileName)
	if raw, err := readPEMFile(bundlePath); err == nil {
		if cert, err := parsePair(raw, raw, bundlePath, bundlePath); err == nil {
			return withAnchorCopy(absDir, cert), nil
		}
	}
	return Load(filepath.Join(absDir, CertFileName), filepath.Join(absDir, KeyFileName))
}
