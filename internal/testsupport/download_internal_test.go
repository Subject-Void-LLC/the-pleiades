// Package testsupport: the pinned-download cache, against a real HTTP server.
package testsupport

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
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
	return Pin{What: "thing", URL: srv.URL + "/file", SHA256: hex.EncodeToString(sum[:])}, &hits
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
	pin := Pin{What: "thing", URL: srv.URL + "/file", SHA256: "00"}
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
