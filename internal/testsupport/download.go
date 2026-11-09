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
	"testing"
	"time"
)

// DownloadsEnv names the directory that holds downloaded test inputs. CI sets
// it to a directory it restores from and saves to its cache, keyed on the
// file that lists the pins (pins.go). When it is unset, a developer's run
// uses a directory under the user cache directory, so one machine fetches a
// pinned file once.
const DownloadsEnv = "PLEIADES_TEST_DOWNLOADS"

// maxDownload bounds what one pinned download may read, so a wrong URL that
// serves something huge cannot fill a disk.
const maxDownload = 16 << 20

// Pin is a file a test downloads: where it comes from and the SHA-256 it must
// have. The checksum is checked on every use, whether the bytes came from the
// cache or from the network.
type Pin struct {
	// What names the need in a skip or failure ("toybox"), the word
	// Require puts after "needs".
	What string
	// URL is where the file is fetched from when the cache lacks it.
	URL string
	// SHA256 is the lowercase hex digest the file must have.
	SHA256 string
}

// ErrDownloadChecksum is returned by a fetch whose bytes do not match the pin.
// It is a real signal (the publisher changed the file or a proxy replaced
// it), not an outage, so Download fails the test on it instead of skipping.
var ErrDownloadChecksum = errors.New("downloaded file does not match its pinned SHA-256")

// DownloadsDir returns the directory pinned downloads are cached in: the one
// DownloadsEnv names, else a directory under the user cache directory.
func DownloadsDir() (string, error) {
	if dir := os.Getenv(DownloadsEnv); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no cache directory for test downloads (set %s): %w", DownloadsEnv, err)
	}
	return filepath.Join(base, "pleiades-test-downloads"), nil
}

// Download returns the bytes of the pinned file, from the cache when it holds
// a copy that matches the pin and from the network otherwise (then caching
// it). A file that cannot be had, because the network is down or the
// publisher is, stops t through Require, which skips with "needs <what>" or
// fails when this run requires it, so it is never read as a pass. A file that
// arrives with the wrong checksum fails t outright.
func Download(t testing.TB, pin Pin) []byte {
	t.Helper()
	dir, err := DownloadsDir()
	if err == nil {
		var data []byte
		data, err = fetchPinned(dir, pin, &http.Client{Timeout: 2 * time.Minute})
		if err == nil {
			return data
		}
	}
	if errors.Is(err, ErrDownloadChecksum) {
		t.Fatalf("%s: %v; the published file changed", pin.What, err)
	}
	Require(t, pin.What, false, fmt.Sprintf("%s is not cached and cannot be fetched (%v); "+
		"run once with network access, or put a copy with that SHA-256 in %s", pin.URL, err, DownloadsEnv))
	return nil // unreachable: Require has stopped t
}

// fetchPinned returns the pinned file from dir when a copy there has the
// pinned checksum, and otherwise fetches it with client, checks it, and
// writes it to dir. A cached copy with the wrong checksum (a torn write, a
// disk fault, a tampered cache) is discarded and fetched again; it is never
// returned.
func fetchPinned(dir string, pin Pin, client *http.Client) ([]byte, error) {
	// The file is named by its digest, so two pins never share a name and a
	// changed pin is a different file, never a stale hit.
	cached := filepath.Join(dir, pin.SHA256)
	if data, err := os.ReadFile(cached); err == nil { // #nosec G304 -- a path under the test cache directory, named by a constant digest
		if checksumMatches(data, pin.SHA256) {
			return data, nil
		}
		// Best effort: the fetch below replaces it either way.
		_ = os.Remove(cached)
	}
	resp, err := client.Get(pin.URL)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", pin.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: HTTP %d", pin.URL, resp.StatusCode)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(resp.Body, maxDownload)); err != nil {
		return nil, fmt.Errorf("reading %s: %w", pin.URL, err)
	}
	if !checksumMatches(buf.Bytes(), pin.SHA256) {
		return nil, fmt.Errorf("%s: %w", pin.URL, ErrDownloadChecksum)
	}
	// The cache is an optimization: a directory that cannot be written
	// costs a download next time, not this test.
	_ = storeAtomically(dir, cached, buf.Bytes())
	return buf.Bytes(), nil
}

// checksumMatches reports whether data has the lowercase hex SHA-256 want.
func checksumMatches(data []byte, want string) bool {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == want
}

// storeAtomically writes data to path through a temporary file in dir and a
// rename, so a run that dies mid-write or a parallel test binary never leaves
// or reads half a file.
func storeAtomically(dir, path string, data []byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
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
