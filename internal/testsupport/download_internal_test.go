// Package testsupport: the pinned-download cache, against a real HTTP server.
package testsupport

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// pinnedServer serves body at /file, counts the requests it answers, and
// returns a Pin whose checksum is of want (which a test may make differ from
// body to model a changed upstream file).
func pinnedServer(t *testing.T, body, want []byte) (Pin, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(want)
	return Pin{What: need.Toybox, URL: srv.URL + "/file", SHA256: hex.EncodeToString(sum[:])}, &hits
}

// TestFetchPinned_ColdFetchesOnceThenServesFromCache shows a cold cache costs
// one request and every later use costs none.
func TestFetchPinned_ColdFetchesOnceThenServesFromCache(t *testing.T) {
	body := []byte("the pinned bytes")
	pin, hits := pinnedServer(t, body, body)
	dir := filepath.Join(t.TempDir(), "downloads") // does not exist yet: the cache creates it
	for i := 0; i < 3; i++ {
		got, err := fetchPinned(dir, pin, http.DefaultClient)
		if err != nil || string(got) != string(body) {
			t.Fatalf("use %d: got %q, %v", i, got, err)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("server answered %d requests, want 1", n)
	}
}

// TestFetchPinned_CacheWorksWithTheNetworkGone shows a warm cache needs no
// server at all, which is what keeps a run independent of the publisher.
func TestFetchPinned_CacheWorksWithTheNetworkGone(t *testing.T) {
	body := []byte("cached while the publisher was up")
	pin, _ := pinnedServer(t, body, body)
	dir := t.TempDir()
	if _, err := fetchPinned(dir, pin, http.DefaultClient); err != nil {
		t.Fatal(err)
	}
	pin.URL = "http://127.0.0.1:1/unreachable"
	got, err := fetchPinned(dir, pin, http.DefaultClient)
	if err != nil || string(got) != string(body) {
		t.Fatalf("offline with a warm cache: got %q, %v", got, err)
	}
}

// TestFetchPinned_CorruptCacheIsDiscardedAndRefetched shows a cached file
// with the wrong checksum is never returned.
func TestFetchPinned_CorruptCacheIsDiscardedAndRefetched(t *testing.T) {
	body := []byte("the real bytes")
	pin, hits := pinnedServer(t, body, body)
	dir := t.TempDir()
	cached := filepath.Join(dir, pin.SHA256)
	if err := os.WriteFile(cached, []byte("torn or tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := fetchPinned(dir, pin, http.DefaultClient)
	if err != nil || string(got) != string(body) {
		t.Fatalf("got %q, %v", got, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("a corrupt cache entry must be refetched, server answered %d requests", hits.Load())
	}
	onDisk, err := os.ReadFile(cached)
	if err != nil || string(onDisk) != string(body) {
		t.Fatalf("cache entry after the refetch: %q, %v", onDisk, err)
	}
}

// TestFetchPinned_WrongChecksumFromTheNetworkIsRefusedAndNotCached shows a
// changed upstream file is an error the caller can tell from an outage, and
// never reaches the cache.
func TestFetchPinned_WrongChecksumFromTheNetworkIsRefusedAndNotCached(t *testing.T) {
	pin, _ := pinnedServer(t, []byte("a different build"), []byte("the pinned build"))
	dir := t.TempDir()
	if _, err := fetchPinned(dir, pin, http.DefaultClient); !errors.Is(err, ErrDownloadChecksum) {
		t.Fatalf("err = %v, want ErrDownloadChecksum", err)
	}
	if _, err := os.Stat(filepath.Join(dir, pin.SHA256)); !os.IsNotExist(err) {
		t.Fatalf("a refused file reached the cache: %v", err)
	}
}

// TestFetchPinned_OutageIsNotAChecksumError shows an unreachable server and an
// HTTP error both come back as plain errors, which Download turns into a skip.
func TestFetchPinned_OutageIsNotAChecksumError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	pin := Pin{What: need.Toybox, URL: srv.URL + "/file", SHA256: strings.Repeat("0", 64)}
	if _, err := fetchPinned(t.TempDir(), pin, http.DefaultClient); err == nil || errors.Is(err, ErrDownloadChecksum) {
		t.Fatalf("HTTP 404: err = %v", err)
	}
	pin.URL = "http://127.0.0.1:1/unreachable"
	if _, err := fetchPinned(t.TempDir(), pin, http.DefaultClient); err == nil || errors.Is(err, ErrDownloadChecksum) {
		t.Fatalf("refused connection: err = %v", err)
	}
}

// TestDownloadsDir_EnvWinsThenUserCache shows CI's directory is honored.
func TestDownloadsDir_EnvWinsThenUserCache(t *testing.T) {
	t.Setenv(DownloadsEnv, "/somewhere/ci")
	if got, err := DownloadsDir(); err != nil || got != "/somewhere/ci" {
		t.Fatalf("with the variable: %q, %v", got, err)
	}
	t.Setenv(DownloadsEnv, "")
	// Each OS's own source of the user cache directory, so no run reads the
	// real login's: XDG_CACHE_HOME and HOME on Unix, LocalAppData on Windows,
	// and USERPROFILE beside HOME for os.UserHomeDir there.
	t.Setenv("XDG_CACHE_HOME", "/xdg")
	t.Setenv("HOME", "/home/x")
	t.Setenv("USERPROFILE", "/home/x")
	t.Setenv("LocalAppData", "/home/x/AppData/Local")
	if got, err := DownloadsDir(); err != nil || filepath.Base(got) != "pleiades-test-downloads" {
		t.Fatalf("without it: %q, %v", got, err)
	}
}

// TestNewTempFile_IsOutsideTheCacheDirectory shows the temporary file of a
// store is made beside the cache directory, not in it. CI saves the directory
// whole, so a temporary file left by a killed run inside it would be cached
// and would make the entry look filled.
func TestNewTempFile_IsOutsideTheCacheDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "downloads")
	if err := os.MkdirAll(dir, 0o750); err != nil { // as storeAtomically does first
		t.Fatal(err)
	}
	f, err := newTempFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if got := filepath.Dir(f.Name()); got != parent {
		t.Fatalf("temporary file made in %s, want %s (outside %s)", got, parent, dir)
	}
}

// TestFetchPinned_CacheDirectoryHoldsOnlyTheDigestNamedFile shows a fetch
// leaves the one digest-named file in the cache directory and no temporary
// file anywhere.
func TestFetchPinned_CacheDirectoryHoldsOnlyTheDigestNamedFile(t *testing.T) {
	body := []byte("the pinned bytes")
	pin, _ := pinnedServer(t, body, body)
	parent := t.TempDir()
	dir := filepath.Join(parent, "downloads")
	if _, err := fetchPinned(dir, pin, http.DefaultClient); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != pin.SHA256 {
		t.Fatalf("cache directory holds %v, want only %s", entries, pin.SHA256)
	}
	leftovers, _ := filepath.Glob(filepath.Join(parent, ".pleiades-download-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

// TestFetchPinned_RefusesAMalformedDigest shows a pin whose SHA256 is not
// 64 lowercase hex characters is its own error, whether it is a path that
// would leave the cache, uppercase, short, or empty, and that nothing is
// fetched or written for it.
func TestFetchPinned_RefusesAMalformedDigest(t *testing.T) {
	for _, digest := range []string{"", "00", "../../etc/passwd", strings.Repeat("A", 64), strings.Repeat("0", 63), strings.Repeat("0", 65), strings.Repeat("0", 63) + "/"} {
		pin, hits := pinnedServer(t, []byte("x"), []byte("x"))
		pin.SHA256 = digest
		dir := t.TempDir()
		if _, err := fetchPinned(dir, pin, http.DefaultClient); !errors.Is(err, ErrBadPin) {
			t.Errorf("digest %q: err = %v, want ErrBadPin", digest, err)
		}
		if hits.Load() != 0 {
			t.Errorf("digest %q: the server was asked anyway", digest)
		}
	}
}

// TestFetchPinned_BodyOverTheLimitIsTooLargeNotAChecksumError shows a body
// longer than the limit says so, instead of being reported as a changed
// checksum, and that a body of exactly the limit is still read in full.
func TestFetchPinned_BodyOverTheLimitIsTooLargeNotAChecksumError(t *testing.T) {
	over := make([]byte, maxDownload+1)
	pin, _ := pinnedServer(t, over, over)
	_, err := fetchPinned(t.TempDir(), pin, http.DefaultClient)
	if !errors.Is(err, ErrDownloadTooLarge) || errors.Is(err, ErrDownloadChecksum) {
		t.Fatalf("err = %v, want ErrDownloadTooLarge only", err)
	}
	exact := make([]byte, maxDownload)
	pin, _ = pinnedServer(t, exact, exact)
	if got, err := fetchPinned(t.TempDir(), pin, http.DefaultClient); err != nil || len(got) != maxDownload {
		t.Fatalf("a body of exactly the limit: %d bytes, %v", len(got), err)
	}
}

// TestFetchPinned_EmptyDirMeansNoCache shows an empty directory fetches every
// time and writes nothing.
func TestFetchPinned_EmptyDirMeansNoCache(t *testing.T) {
	body := []byte("uncached")
	pin, hits := pinnedServer(t, body, body)
	for i := 0; i < 2; i++ {
		if got, err := fetchPinned("", pin, http.DefaultClient); err != nil || string(got) != string(body) {
			t.Fatalf("use %d: %q, %v", i, got, err)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("server answered %d requests, want 2 (no cache)", hits.Load())
	}
}

// TestDownloadsDir_RelativeEnvIsRefused shows a relative path is refused with
// a message naming the variable, instead of making one cache per package.
func TestDownloadsDir_RelativeEnvIsRefused(t *testing.T) {
	t.Setenv(DownloadsEnv, "ci/downloads")
	_, err := DownloadsDir()
	if !errors.Is(err, ErrRelativeDownloadsDir) || !strings.Contains(err.Error(), DownloadsEnv) {
		t.Fatalf("err = %v, want ErrRelativeDownloadsDir naming %s", err, DownloadsEnv)
	}
}

// downloadTB is recordingTB with the Logf Download uses.
type downloadTB struct {
	recordingTB
	logs []string
}

func (d *downloadTB) Logf(format string, args ...any) {
	d.logs = append(d.logs, fmt.Sprintf(format, args...))
}

// TestDownload_ChecksumMismatchFailsTheTest shows Download itself, not just
// the fetch under it, fails the test when the published file changed, and
// neither skips nor returns the bytes.
func TestDownload_ChecksumMismatchFailsTheTest(t *testing.T) {
	t.Setenv(DownloadsEnv, t.TempDir())
	t.Setenv(need.Env, "")
	pin, _ := pinnedServer(t, []byte("a different build"), []byte("the pinned build"))
	r := &downloadTB{}
	got := Download(r, pin)
	if !strings.Contains(r.fatal, "published file changed") || !strings.Contains(r.fatal, need.Toybox) {
		t.Errorf("failure %q does not name the pin and the changed file", r.fatal)
	}
	if r.skip != "" || got != nil {
		t.Errorf("skip %q, bytes %q: a changed file must fail, not skip or return", r.skip, got)
	}
}

// TestDownload_UnavailableSkipsNamingTheNeed shows a file that cannot be
// fetched skips with "needs <what>: ..." in an ordinary run.
func TestDownload_UnavailableSkipsNamingTheNeed(t *testing.T) {
	t.Setenv(DownloadsEnv, t.TempDir())
	t.Setenv(need.Env, "")
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	pin := Pin{What: need.Toybox, URL: srv.URL + "/file", SHA256: strings.Repeat("a", 64)}
	r := &downloadTB{}
	Download(r, pin)
	if !strings.HasPrefix(r.skip, "needs toybox: ") || !strings.Contains(r.skip, DownloadsEnv) {
		t.Errorf("skip %q does not name the need and where to put a copy", r.skip)
	}
	if r.fatal != "" {
		t.Errorf("failed (%q) where an ordinary run skips", r.fatal)
	}
}

// TestDownload_UnavailableFailsWhenTheRunRequiresIt shows the same outage
// fails the test in a run whose PLEIADES_TEST_REQUIRE names the pin.
func TestDownload_UnavailableFailsWhenTheRunRequiresIt(t *testing.T) {
	t.Setenv(DownloadsEnv, t.TempDir())
	t.Setenv(need.Env, need.Toybox)
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	pin := Pin{What: need.Toybox, URL: srv.URL + "/file", SHA256: strings.Repeat("a", 64)}
	r := &downloadTB{}
	Download(r, pin)
	if !strings.Contains(r.fatal, "requires toybox") {
		t.Errorf("failure %q does not say the run requires toybox", r.fatal)
	}
	if r.skip != "" {
		t.Errorf("skipped (%q) in a run that requires it", r.skip)
	}
}

// TestDownload_RelativeEnvAndBadPinFail shows a relative cache directory and
// a malformed pin each fail the test with their own message, before any
// fetch.
func TestDownload_RelativeEnvAndBadPinFail(t *testing.T) {
	t.Setenv(need.Env, "")
	pin, hits := pinnedServer(t, []byte("x"), []byte("x"))

	t.Setenv(DownloadsEnv, "relative/dir")
	r := &downloadTB{}
	Download(r, pin)
	if !strings.Contains(r.fatal, "relative") || r.skip != "" {
		t.Errorf("relative env: fatal %q, skip %q", r.fatal, r.skip)
	}

	t.Setenv(DownloadsEnv, t.TempDir())
	pin.SHA256 = "../escape"
	r = &downloadTB{}
	Download(r, pin)
	if !strings.Contains(r.fatal, "64 lowercase hex") || r.skip != "" {
		t.Errorf("bad pin: fatal %q, skip %q", r.fatal, r.skip)
	}
	if hits.Load() != 0 {
		t.Errorf("the server was asked %d times before the pin and directory were accepted", hits.Load())
	}
}

// TestDownload_OversizedBodyFailsAsTooLarge shows the size limit reaches the
// test as its own failure, not as a changed checksum.
func TestDownload_OversizedBodyFailsAsTooLarge(t *testing.T) {
	t.Setenv(DownloadsEnv, t.TempDir())
	t.Setenv(need.Env, "")
	over := make([]byte, maxDownload+1)
	pin, _ := pinnedServer(t, over, over)
	r := &downloadTB{}
	Download(r, pin)
	if !strings.Contains(r.fatal, "larger than the limit") || strings.Contains(r.fatal, "published file changed") {
		t.Errorf("failure %q does not say the body was too large", r.fatal)
	}
}

// TestDownload_NoCacheDirectoryFetchesWithoutCaching shows a machine with no
// user cache directory still gets the file, and the log says why it was not
// cached.
func TestDownload_NoCacheDirectoryFetchesWithoutCaching(t *testing.T) {
	t.Setenv(DownloadsEnv, "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("LocalAppData", "")
	if _, err := DownloadsDir(); err == nil {
		t.Skip("this OS finds a user cache directory without HOME")
	}
	body := []byte("fetched uncached")
	pin, _ := pinnedServer(t, body, body)
	r := &downloadTB{}
	got := Download(r, pin)
	if string(got) != string(body) || r.fatal != "" || r.skip != "" {
		t.Fatalf("got %q, fatal %q, skip %q", got, r.fatal, r.skip)
	}
	if len(r.logs) != 1 || !strings.Contains(r.logs[0], "cache is unavailable") {
		t.Errorf("logs %q do not say the cache was unavailable", r.logs)
	}
}
