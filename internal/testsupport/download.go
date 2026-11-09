// Package testsupport: a pinned download a test needs, kept in a cache so a
// run does not depend on the project that publishes it being up.
package testsupport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// DownloadsEnv names the directory that holds downloaded test inputs. CI sets
// it to a directory it restores from and saves to its cache, keyed on the
// file that lists the pins (pins.go). It must be an absolute path, since go
// test runs each package in its own directory. When it is unset, a
// developer's run uses a directory under the user cache directory, so one
// machine fetches a pinned file once.
const DownloadsEnv = "PLEIADES_TEST_DOWNLOADS"

// maxDownload bounds what one pinned download may read, so a wrong URL that
// serves something huge cannot fill a disk.
const maxDownload = 16 << 20

// Pin is a file a test downloads: where it comes from and the SHA-256 it must
// have. The checksum is checked on every use, whether the bytes came from the
// cache or from the network.
type Pin struct {
	// What names the need in a skip or failure (need.Toybox), the word
	// Require puts after "needs". It must be one of need.Names.
	What string
	// URL is where the file is fetched from when the cache lacks it.
	URL string
	// SHA256 is the lowercase hex digest the file must have: 64 characters
	// of 0-9 and a-f. It also names the cached file, which is why nothing
	// else is accepted (a path separator or a dot-dot would leave the cache
	// directory).
	SHA256 string
}

// sha256Hex matches a lowercase hex SHA-256 digest and nothing else.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

var (
	// ErrDownloadChecksum is returned by a fetch whose bytes do not match the
	// pin. It is a real signal (the publisher changed the file or a proxy
	// replaced it), not an outage, so Download fails the test on it instead
	// of skipping.
	ErrDownloadChecksum = errors.New("downloaded file does not match its pinned SHA-256")
	// ErrDownloadTooLarge is returned by a fetch whose body is longer than
	// the limit. Like a checksum mismatch it means the pin no longer
	// describes what the URL serves, so Download fails the test.
	ErrDownloadTooLarge = errors.New("downloaded file is larger than the limit")
	// ErrBadPin is returned for a pin whose SHA256 is not 64 lowercase hex
	// characters. It is a mistake in pins.go, so Download fails the test.
	ErrBadPin = errors.New("pin's SHA256 is not 64 lowercase hex characters")
	// ErrRelativeDownloadsDir is returned when DownloadsEnv names a relative
	// path, which would resolve against whichever package directory the test
	// binary runs in, so each package would keep its own cache.
	ErrRelativeDownloadsDir = errors.New("is a relative path; use an absolute one")
)

// DownloadsDir returns the directory pinned downloads are cached in: the one
// DownloadsEnv names (which must be absolute), else a directory under the
// user cache directory. When this machine has no user cache directory it
// returns an error that is not ErrRelativeDownloadsDir, and Download fetches
// without caching.
func DownloadsDir() (string, error) {
	if dir := os.Getenv(DownloadsEnv); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("%s=%q %w", DownloadsEnv, dir, ErrRelativeDownloadsDir)
		}
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no user cache directory for test downloads (or set %s): %w", DownloadsEnv, err)
	}
	return filepath.Join(base, "pleiades-test-downloads"), nil
}

// Download returns the bytes of the pinned file, from the cache when it holds
// a copy that matches the pin and from the network otherwise (then caching
// it). A file that cannot be had, because the network is down or the
// publisher is, stops t through Require, which skips with "needs <what>" or
// fails when this run requires it, so it is never read as a pass. A file
// that arrives with the wrong checksum or size, a malformed pin and a
// relative DownloadsEnv fail t outright. When the machine has no cache
// directory at all, the file is fetched without caching.
func Download(t testing.TB, pin Pin) []byte {
	t.Helper()
	dir, err := DownloadsDir()
	switch {
	case errors.Is(err, ErrRelativeDownloadsDir):
		t.Fatalf("%v", err)
		return nil // unreachable with a real testing.T, whose Fatalf never returns
	case err != nil:
		t.Logf("%s: the test download cache is unavailable (%v); fetching without caching", pin.What, err)
		dir = ""
	}
	data, err := fetchPinned(dir, pin, &http.Client{Timeout: 2 * time.Minute})
	switch {
	case err == nil:
		return data
	case errors.Is(err, ErrDownloadChecksum):
		t.Fatalf("%s: %v; the published file changed", pin.What, err)
	case errors.Is(err, ErrDownloadTooLarge), errors.Is(err, ErrBadPin):
		t.Fatalf("%s: %v", pin.What, err)
	default:
		Require(t, pin.What, false, fmt.Sprintf("%s is not cached and cannot be fetched (%v); "+
			"run once with network access, or put a copy with that SHA-256 in %s", pin.URL, err, DownloadsEnv))
	}
	return nil // unreachable with a real testing.T: Fatalf and Require have stopped it
}

// fetchPinned returns the pinned file from dir when a copy there has the
// pinned checksum, and otherwise fetches it with client, checks it, and
// writes it to dir. A cached copy with the wrong checksum (a torn write, a
// disk fault, a tampered cache) is discarded and fetched again; it is never
// returned. An empty dir means no cache: always fetch, never store.
func fetchPinned(dir string, pin Pin, client *http.Client) ([]byte, error) {
	if !sha256Hex.MatchString(pin.SHA256) {
		return nil, fmt.Errorf("%s: %w (got %q)", pin.What, ErrBadPin, pin.SHA256)
	}
	// The file is named by its digest, so two pins never share a name and a
	// changed pin is a different file, never a stale hit.
	cached := ""
	if dir != "" {
		cached = filepath.Join(dir, pin.SHA256)
		if data, err := os.ReadFile(cached); err == nil { // #nosec G304 -- a path under the test cache directory, named by a validated digest
			if checksumMatches(data, pin.SHA256) {
				return data, nil
			}
			// Best effort: the fetch below replaces it either way.
			_ = os.Remove(cached)
		}
	}
	resp, err := client.Get(pin.URL)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", pin.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: HTTP %d", pin.URL, resp.StatusCode)
	}
	// Read one byte past the limit so a body over it is told from one that
	// is exactly the limit.
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(resp.Body, maxDownload+1)); err != nil {
		return nil, fmt.Errorf("reading %s: %w", pin.URL, err)
	}
	if buf.Len() > maxDownload {
		return nil, fmt.Errorf("%s: %w (%d MiB)", pin.URL, ErrDownloadTooLarge, maxDownload>>20)
	}
	if !checksumMatches(buf.Bytes(), pin.SHA256) {
		return nil, fmt.Errorf("%s: %w", pin.URL, ErrDownloadChecksum)
	}
	if cached != "" {
		// The cache is an optimization: a directory that cannot be written
		// costs a download next time, not this test.
		_ = storeAtomically(dir, cached, buf.Bytes())
	}
	return buf.Bytes(), nil
}

// checksumMatches reports whether data has the lowercase hex SHA-256 want.
func checksumMatches(data []byte, want string) bool {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == want
}

// storeAtomically writes data to path in dir through a temporary file and a
// rename, so a run that dies mid-write or a parallel test binary never leaves
// or reads half a file. The temporary file lives in dir's parent, on the same
// filesystem so the rename is atomic, and never inside dir: CI caches dir
// whole, so a leftover temporary file there would be saved as part of the
// entry, and dir holds nothing but digest-named files.
func storeAtomically(dir, path string, data []byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := newTempFile(dir)
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// newTempFile creates the temporary file a store writes before it renames it
// into dir. It is made in dir's parent, never in dir (see storeAtomically).
func newTempFile(dir string) (*os.File, error) {
	return os.CreateTemp(filepath.Dir(dir), ".pleiades-download-*")
}
