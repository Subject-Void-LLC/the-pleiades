// Writing: publishing a file so that no reader ever sees half of it, and
// clearing up after the writes that never finished.
//
// Everything this package puts on disk goes through writeAtomic, and the one
// thing it does NOT solve is worth stating up front: two files cannot be
// replaced in one step. Each rename is atomic on its own, so a reader always
// sees a whole file, and a reader arriving between two renames sees one
// writer's first file beside another writer's second.
//
// That limit is why the certificate and its key are ONE file on the path
// where it matters (bundle.go): the pair a listener has to serve is
// published in a single rename, so no reader can ever catch it half
// replaced, and no lock is needed to keep two writers from interleaving.
// Everything else this package writes is a copy rather than a pair. A
// cert.pem holding no key, and a provenance record holding no key, cannot be
// mismatched with anything, so nothing is riding on the order they land in.
package tlscert

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// temporarySweepAge is how old one of writeAtomic's temporary files has to
// be before it is treated as abandoned.
//
// An hour, against a write that takes milliseconds. The gap is that wide on
// purpose: the only cost of waiting too long is one leftover file living an
// hour longer, and the cost of being too eager is deleting the temporary
// file another process is writing right now.
const temporarySweepAge = time.Hour

// sweepStaleTemporaries removes writeAtomic's leftovers from a directory.
//
// writeAtomic removes its own temporary file on every path it can reach, but
// it cannot reach any path at all if the process is killed between creating
// the file and renaming it. What is left behind is a real EC private key, at
// 0600, that nothing will ever open again and nothing else will ever delete.
// Over a restart loop a directory accumulates them.
//
// Only files older than temporarySweepAge are touched, because another
// process may be part way through a write right now and its temporary file
// is seconds old, not hours. Every failure is ignored: this is hygiene, and
// a controller must not refuse to start because it could not tidy up.
func sweepStaleTemporaries(dir string) {
	patterns := []string{
		filepath.Join(dir, BundleFileName+".tmp-*"),
		filepath.Join(dir, CertFileName+".tmp-*"),
		filepath.Join(dir, KeyFileName+".tmp-*"),
		filepath.Join(dir, ProvisionedFileName+".tmp-*"),
		// The provenance records are one file per certificate, named after a
		// fingerprint nothing here can predict, so this pattern is over the
		// suffix rather than the name.
		filepath.Join(dir, ProvisionedDirName, "*.pem.tmp-*"),
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil || time.Since(info.ModTime()) < temporarySweepAge {
				continue
			}
			_ = os.Remove(path)
		}
	}
}

// prepareDir resolves dir to an absolute path and makes sure it exists
// with owner-only permissions.
//
// The mode is only forced on a directory this call created. A directory
// that was already there may be one the operator chose for other reasons
// (a mounted volume root, a shared data directory), and tightening
// something this process did not create could lock out whatever else lives
// there, which is a surprising thing for a certificate helper to do.
func prepareDir(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", dir, err)
	}

	_, statErr := os.Stat(absDir)
	created := errors.Is(statErr, fs.ErrNotExist)
	if err := os.MkdirAll(absDir, dirMode); err != nil {
		return "", fmt.Errorf("creating %s: %w", absDir, err)
	}
	if created {
		// MkdirAll applies the process umask, which can only remove bits,
		// so it cannot widen 0700 -- but a umask of 0077 or wider would
		// strip the owner bits this package needs. Chmod is not subject to
		// the umask, so it is what actually pins the mode.
		if err := os.Chmod(absDir, dirMode); err != nil {
			return "", fmt.Errorf("setting owner-only permissions on %s: %w", absDir, err)
		}
	}
	return absDir, nil
}

// writeAtomic writes data to dir/name so that no reader ever observes a
// partially written file, and returns the final path.
//
// FAILURE_PATTERNS.md #46 is the reason this is not an os.WriteFile call:
// an exclusive create still leaves a window in which a concurrent reader
// opens the file and finds it empty. A write to a temporary name followed
// by a rename closes it, because rename(2) either replaces the whole entry
// or does nothing, and a reader holding the old file keeps reading the old
// bytes.
//
// os.CreateTemp is what makes the mode right without a chmod: it creates
// at 0600, which is both the mode this package wants and the reason gosec
// raises no G306 here.
//
// The directory entry itself is not fsynced. What this needs is atomic
// visibility to a concurrent reader, which rename provides on its own;
// surviving a power cut mid-write would additionally need the directory
// synced, and that is not worth buying for a file this package can simply
// regenerate on the next start.
func writeAtomic(dir, name string, data []byte) (string, error) {
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Every failure below leaves the temporary file behind unless it is
	// removed here, and a directory slowly filling with abandoned key
	// material is a worse outcome than the error that caused it.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("writing %s: %w", tmpName, err)
	}
	// Sync before the rename, so the rename publishes bytes that are
	// really on the device rather than an entry pointing at a buffer.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("flushing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("closing %s: %w", tmpName, err)
	}

	final := filepath.Join(dir, name)
	if err := os.Rename(tmpName, final); err != nil {
		return "", fmt.Errorf("renaming %s to %s: %w", tmpName, final, err)
	}
	return final, nil
}
