// Tests for a run's TLS key log against real TLS 1.3 and TLS 1.2 servers: the
// lines written are the NSS key log lines a packet tool decrypts a session
// with, the very secrets the server derived for the same sessions, written
// only for a client built under a context carrying the log, into a file the
// log created itself, mode 0600, and never one that existed. On Windows,
// where every key log is refused, each test holds that refusal and ends.
package devicetls_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// lockedBuffer is a buffer a TLS server's handshakes write to at once.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

// Write appends p.
func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// lines returns what was written, one entry per line.
func (l *lockedBuffer) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Fields(strings.ReplaceAll(l.b.String(), " ", "_"))
}

// keyLogServer starts a TLS server negotiating at most maxVersion (zero is
// Go's default, TLS 1.3) that logs its own side of every session's secrets to
// the returned buffer, and returns device settings pinning its certificate.
func keyLogServer(t *testing.T, maxVersion uint16) (*httptest.Server, *lockedBuffer, devicetls.Settings) {
	t.Helper()
	serverLog := &lockedBuffer{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: maxVersion, KeyLogWriter: serverLog}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	settings, err := devicetls.Parse(inventory.NewProperties(map[string]inventory.PropertyValue{
		devicetls.FingerprintProperty: devicetls.Fingerprint(srv.Certificate()),
	}))
	if err != nil {
		t.Fatal(err)
	}
	return srv, serverLog, settings
}

// fetch makes n requests to srv at once with cfg, each on a connection of its
// own, so each is a TLS session of its own, and returns the first error.
func fetch(t *testing.T, cfg *tls.Config, srv *httptest.Server, n int) error {
	t.Helper()
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}, Timeout: 30 * time.Second}
			resp, err := client.Get(srv.URL)
			if err == nil {
				_ = resp.Body.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// fileLines returns path's lines, each with its spaces made underscores so a
// line compares as one word.
func fileLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 -- the test's own file
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(strings.ReplaceAll(string(raw), " ", "_"))
}

// TestKeyLog_WritesEverySessionsSecrets runs three sessions at once through
// one key log, at TLS 1.3 and at TLS 1.2, and holds the file to the NSS lines
// each version defines, every one well formed, and to exactly the lines the
// server derived for the same sessions: so the file decrypts what was sent,
// not merely something shaped like a key log.
func TestKeyLog_WritesEverySessionsSecrets(t *testing.T) {
	if refusedHere(t) {
		return
	}
	const sessions = 3
	for _, tc := range []struct {
		name   string
		max    uint16
		labels []string
	}{
		{"TLS 1.3", tls.VersionTLS13, []string{"CLIENT_HANDSHAKE_TRAFFIC_SECRET", "CLIENT_TRAFFIC_SECRET_0", "SERVER_HANDSHAKE_TRAFFIC_SECRET", "SERVER_TRAFFIC_SECRET_0"}},
		{"TLS 1.2", tls.VersionTLS12, []string{"CLIENT_RANDOM"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, serverLog, settings := keyLogServer(t, tc.max)
			path := filepath.Join(t.TempDir(), "run.keylog")
			keyLog, err := devicetls.CreateKeyLog(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := settings.ConfigContext(devicetls.WithKeyLog(context.Background(), keyLog), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := fetch(t, cfg, srv, sessions); err != nil {
				t.Fatal(err)
			}
			if err := keyLog.Close(); err != nil {
				t.Fatal(err)
			}

			got := fileLines(t, path)
			counts := map[string]int{}
			for _, line := range got {
				fields := strings.Split(line, "_")
				// The label holds underscores of its own; the last two
				// fields are the client random and the secret.
				label := strings.Join(fields[:len(fields)-2], "_")
				random, secret := fields[len(fields)-2], fields[len(fields)-1]
				if b, err := hex.DecodeString(random); err != nil || len(b) != 32 {
					t.Errorf("line %q: the client random is not 32 bytes of hex", line)
				}
				if b, err := hex.DecodeString(secret); err != nil || len(b) < 32 {
					t.Errorf("line %q: the secret is not hex of at least 32 bytes", line)
				}
				counts[label]++
			}
			for _, label := range tc.labels {
				if counts[label] != sessions {
					t.Errorf("%s written %d times, want once for each of %d sessions (all: %v)", label, counts[label], sessions, counts)
				}
			}
			if len(counts) != len(tc.labels) {
				t.Errorf("labels %v, want exactly %v", counts, tc.labels)
			}
			want := serverLog.lines()
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("the client wrote %d lines that are not the %d the server derived for the same sessions", len(got), len(want))
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("the key log's mode is %v, want 0600", info.Mode().Perm())
			}
		})
	}
}

// TestConfigContext_RefusesWhatConfigRefuses holds ConfigContext to Config's
// own refusals, with a key log on the context or not: a device that presents
// a client certificate its credential does not hold gets no configuration.
func TestConfigContext_RefusesWhatConfigRefuses(t *testing.T) {
	settings, err := devicetls.Parse(inventory.NewProperties(map[string]inventory.PropertyValue{devicetls.ClientCertificateProperty: true}))
	if err != nil {
		t.Fatal(err)
	}
	for name, ctx := range map[string]context.Context{"no key log": context.Background(), "a nil key log": devicetls.WithKeyLog(context.Background(), nil)} {
		if cfg, err := settings.ConfigContext(ctx, nil); err == nil || cfg != nil {
			t.Errorf("%s: ConfigContext with no client certificate = %v, %v, want a refusal", name, cfg, err)
		}
	}
}

// TestKeyLog_NothingIsWrittenWithoutItsContext holds every way of building a
// configuration without the run's context to writing nothing: Config,
// ConfigContext under a context carrying no key log, and one carrying a nil
// one. A key log that exists but rides no context stays empty through real
// sessions.
func TestKeyLog_NothingIsWrittenWithoutItsContext(t *testing.T) {
	if refusedHere(t) {
		return
	}
	srv, _, settings := keyLogServer(t, 0)
	path := filepath.Join(t.TempDir(), "unused.keylog")
	keyLog, err := devicetls.CreateKeyLog(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := settings.Config(nil)
	if err != nil {
		t.Fatal(err)
	}
	background, err := settings.ConfigContext(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	carryingNil, err := settings.ConfigContext(devicetls.WithKeyLog(context.Background(), nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]*tls.Config{"Config": plain, "ConfigContext, no key log": background, "ConfigContext, a nil key log": carryingNil} {
		if cfg.KeyLogWriter != nil {
			t.Errorf("%s: KeyLogWriter is set", name)
		}
		if err := fetch(t, cfg, srv, 1); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := keyLog.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("the key log no context carried holds %d bytes, want none", info.Size())
	}
}

// TestCreateKeyLog_RefusesAPathThatExists holds the key log to never writing
// over or adding to a file: an existing file, a directory, a symbolic link to
// a file and a dangling one are each refused naming the path, and none is
// changed or created through. An empty name and "-" are refused too.
func TestCreateKeyLog_RefusesAPathThatExists(t *testing.T) {
	if refusedHere(t) {
		return
	}
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.keylog")
	if err := os.WriteFile(existing, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	toFile, dangling := filepath.Join(dir, "to-file"), filepath.Join(dir, "dangling")
	if err := os.Symlink(existing, toFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"an existing file": existing, "a directory": dir, "a link to a file": toFile, "a dangling link": dangling}
	for name, path := range cases {
		keyLog, err := devicetls.CreateKeyLog(path)
		if err == nil {
			_ = keyLog.Close()
			t.Errorf("%s: created a key log at %s", name, path)
			continue
		}
		if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "already exists") {
			t.Errorf("%s: %v, want it refused as existing, naming %s", name, err, path)
		}
	}
	if raw, _ := os.ReadFile(existing); string(raw) != "keep me\n" { // #nosec G304 -- the test's own file
		t.Errorf("the existing file now holds %q", raw)
	}
	if _, err := os.Lstat(filepath.Join(dir, "nowhere")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the dangling link's target was created: %v", err)
	}
	for _, path := range []string{"", "-"} {
		if keyLog, err := devicetls.CreateKeyLog(path); err == nil {
			_ = keyLog.Close()
			t.Errorf("CreateKeyLog(%q) created a key log", path)
		}
	}
	if _, err := devicetls.CreateKeyLog(filepath.Join(dir, "missing", "run.keylog")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("a key log in a missing directory: %v", err)
	}
}

// TestKeyLog_AClosedLogEndsTheHandshake holds a key log that cannot be
// written to ending the session rather than letting one run that the file
// would not decrypt, with an error naming the file and holding no secret.
func TestKeyLog_AClosedLogEndsTheHandshake(t *testing.T) {
	if refusedHere(t) {
		return
	}
	srv, serverLog, settings := keyLogServer(t, 0)
	path := filepath.Join(t.TempDir(), "closed.keylog")
	keyLog, err := devicetls.CreateKeyLog(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.ConfigContext(devicetls.WithKeyLog(context.Background(), keyLog), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := keyLog.Close(); err != nil {
		t.Fatal(err)
	}
	if err := keyLog.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
	err = fetch(t, cfg, srv, 1)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("a session through a closed key log: %v, want it ended naming %s", err, path)
	}
	for _, line := range serverLog.lines() {
		fields := strings.Split(line, "_")
		if secret := fields[len(fields)-1]; strings.Contains(err.Error(), secret) {
			t.Errorf("the error %q holds a session secret", err)
		}
	}
	if _, err := keyLog.Write([]byte("x\n")); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("Write after Close: %v", err)
	}
}

// TestKeyLog_NamesItsFileAbsoluteAndWarnsWhatItDecrypts holds Path to the
// absolute file a relative name was made into, and Warning to naming that
// file and what it exposes.
func TestKeyLog_NamesItsFileAbsoluteAndWarnsWhatItDecrypts(t *testing.T) {
	if refusedHere(t) {
		return
	}
	dir := t.TempDir()
	t.Chdir(dir)
	keyLog, err := devicetls.CreateKeyLog("relative.keylog")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keyLog.Close() }()
	want := filepath.Join(dir, "relative.keylog")
	if keyLog.Path() != want {
		t.Errorf("Path() = %q, want %q", keyLog.Path(), want)
	}
	warning := keyLog.Warning()
	for _, part := range []string{want, "decrypts every TLS session", "gNMI, RESTCONF and http.request", "credentials in gRPC metadata and HTTP headers"} {
		if !strings.Contains(warning, part) {
			t.Errorf("Warning() = %q, missing %q", warning, part)
		}
	}
}
