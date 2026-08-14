// Package file resolves an external secret from a file on the Controller's
// own filesystem.
//
// It is the one real external secret source Phase 22 ships, and it is not a
// placeholder for a real integration. A Kubernetes projected volume, a
// HashiCorp Vault Agent sidecar and the External Secrets Operator all
// deliver a secret to a process as a file, so a deployment using any of the
// three is already served by this source without a client library, a second
// authentication mechanism, or a network dependency on the dispatch path.
//
// internal/credtype's own lookup.go carries the fuller reasoning, including
// why an environment-variable source was rejected.
package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// Name is the source name a reference selects this Lookup with.
const Name = "file"

// EnvDirectory is the environment variable naming the one directory this
// source reads from.
//
// The directory is configuration and the secrets are not, which is the
// split PLAN.md Section 17.5 draws: a path in a Controller environment
// variable is fine, and the secret itself in one is not.
const EnvDirectory = "CONTROLLER_EXTERNAL_SECRETS_DIR"

// maxSecretBytes bounds one secret file.
//
// A bound exists because the reference comes from a database row and the
// file it names is whatever is on disk: without one, a reference pointing
// at a large file reads the whole thing into memory on the dispatch path,
// once per device in a fan-out. The value is far above any real secret,
// including a PEM key with a full certificate chain.
const maxSecretBytes = 1 << 20

// ErrNotConfigured reports a deployment that has not set EnvDirectory.
//
// It is distinguished from a missing file deliberately: "this Controller
// has no external secrets directory" and "that secret is not in it" are
// different operator actions, and one error for both sends people to the
// wrong one.
var ErrNotConfigured = errors.New("file: no external secrets directory is configured")

// Lookup reads secrets from one directory.
type Lookup struct {
	dir string
}

// New builds a Lookup over dir.
//
// An empty dir is not an error here. It produces a Lookup whose Resolve
// reports ErrNotConfigured, which is what a Controller with no external
// secrets directory should do: start normally, and fail only the specific
// credential that actually names an external reference. Refusing to
// construct would make an unrelated misconfiguration a startup failure.
func New(dir string) *Lookup {
	return &Lookup{dir: filepath.Clean(dir)}
}

// FromEnvironment builds a Lookup from EnvDirectory.
func FromEnvironment() *Lookup {
	return New(os.Getenv(EnvDirectory))
}

// Name returns the source name.
func (l *Lookup) Name() string { return Name }

// Resolve reads the secret the reference names.
//
// The reference is a single path element: a bare name, not a path. That is
// stricter than joining and cleaning, and deliberately so. Cleaning a
// caller-supplied path and then checking the result is inside the directory
// is correct but it is the check people get subtly wrong, most often by
// forgetting that a symlink inside the directory can point outside it.
// Refusing every separator, every dot segment and every empty name means
// there is no traversal to check for.
//
// The residual, stated rather than left implicit: a symlink placed inside
// the directory by somebody who can already write to it still resolves
// through. Whoever can write into the Controller's secrets directory can
// put any bytes they like in a file there anyway, so a symlink grants
// nothing a plain file would not.
func (l *Lookup) Resolve(_ context.Context, reference string) (string, error) {
	if l.dir == "" || l.dir == "." {
		return "", fmt.Errorf("%w: set %s to use the %q source", ErrNotConfigured, EnvDirectory, Name)
	}
	if err := checkReference(reference); err != nil {
		return "", err
	}

	// #nosec G304 -- reference is a single path element with no separator,
	// no dot segment and a bounded character set (checkReference), joined
	// to a directory this Controller's own operator configured. There is no
	// caller-controlled path here, only a caller-controlled filename inside
	// one fixed directory, which is the whole point of this source.
	f, err := os.Open(filepath.Join(l.dir, reference))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Names the reference, which is a pointer rather than a secret,
			// and never the directory, which names the deployment's own
			// filesystem layout.
			return "", fmt.Errorf("%w: no secret named %q", credtype.ErrLookupReference, reference)
		}
		return "", fmt.Errorf("%w: secret %q could not be read", credtype.ErrLookupReference, reference)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("%w: secret %q could not be read", credtype.ErrLookupReference, reference)
	}
	// A directory, a device node or a named pipe. A pipe in particular
	// would block the dispatch path indefinitely, so this is a refusal
	// rather than a tidiness check.
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %q is not a regular file", credtype.ErrLookupReference, reference)
	}

	// Read through a LimitReader rather than allocating info.Size() bytes:
	// the size and the read are two calls with a gap between them, and a
	// file that grew in that gap would be silently truncated, which presents
	// as an authentication failure against the target device rather than as
	// a bug here. One byte over the bound is what distinguishes "exactly at
	// the limit" from "too large".
	body, err := io.ReadAll(io.LimitReader(f, maxSecretBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: secret %q could not be read", credtype.ErrLookupReference, reference)
	}
	if len(body) > maxSecretBytes {
		return "", fmt.Errorf("%w: secret %q is larger than %d bytes", credtype.ErrLookupReference, reference, maxSecretBytes)
	}

	// Exactly one trailing newline is stripped, because every tool that
	// writes a secret to a file adds one and no secret this platform
	// injects ends in a newline that matters. Stripping all of them would
	// silently alter a PEM body, which legitimately ends with one.
	return strings.TrimSuffix(string(body), "\n"), nil
}

// checkReference refuses anything that is not a single, plain filename.
func checkReference(reference string) error {
	switch {
	case reference == "":
		return fmt.Errorf("%w: an external secret reference cannot be empty", credtype.ErrLookupReference)
	case strings.ContainsRune(reference, os.PathSeparator), strings.ContainsRune(reference, '/'):
		return fmt.Errorf(
			"%w: %q contains a path separator, and the %q source reads a plain filename inside one configured directory",
			credtype.ErrLookupReference, reference, Name)
	case reference == "." || reference == "..":
		return fmt.Errorf("%w: %q is a directory reference, not a secret", credtype.ErrLookupReference, reference)
	case strings.ContainsRune(reference, 0):
		return fmt.Errorf("%w: an external secret reference cannot contain a null byte", credtype.ErrLookupReference)
	}
	return nil
}
