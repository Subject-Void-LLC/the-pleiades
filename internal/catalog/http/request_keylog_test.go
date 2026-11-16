// This file tests that http.request's device mode writes its TLS session's
// secrets to the run's key log (pleiades run --tls-keylog, carried on the
// method's context by pkg/devicetls), at TLS 1.3 and TLS 1.2, and that a full
// URL, which is not a device session and is not built by pkg/devicetls,
// writes nothing to it, as the run's warning says.
package http_test

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/http"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// TestRequestDevice_WritesTheRunsTLSKeyLog calls a device's API through a run
// context carrying a key log, and holds the file to the NSS lines of the
// session's version: the four TLS 1.3 traffic secrets, or TLS 1.2's
// CLIENT_RANDOM.
func TestRequestDevice_WritesTheRunsTLSKeyLog(t *testing.T) {
	for _, tc := range []struct {
		name   string
		max    uint16
		labels []string
	}{
		{"TLS 1.3", tls.VersionTLS13, []string{"CLIENT_HANDSHAKE_TRAFFIC_SECRET", "SERVER_HANDSHAKE_TRAFFIC_SECRET", "CLIENT_TRAFFIC_SECRET_0", "SERVER_TRAFFIC_SECRET_0"}},
		{"TLS 1.2", tls.VersionTLS12, []string{"CLIENT_RANDOM"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := tlsDeviceServer(t, tls.VersionTLS12, tc.max)
			dev, err := requestDeviceWith(t, srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "run.keylog")
			keyLog, err := devicetls.CreateKeyLog(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := devicetls.WithKeyLog(context.Background(), keyLog)
			if _, err := http.Request(ctx, newRequestContext(), dev, map[string]any{"url": "/items"}); err != nil {
				t.Fatal(err)
			}
			if err := keyLog.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path) // #nosec G304 -- the test's own file
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				label, _, _ := strings.Cut(line, " ")
				counts[label]++
			}
			for _, label := range tc.labels {
				if counts[label] != 1 {
					t.Errorf("the key log holds %s %d times, want once for the one session (all: %v)", label, counts[label], counts)
				}
			}
			if len(counts) != len(tc.labels) {
				t.Errorf("the key log's labels are %v, want exactly %v", counts, tc.labels)
			}
		})
	}
}

// TestRequest_AFullURLWritesNoKeyLog calls a full URL, which is not the
// device's own API and is not built by pkg/devicetls, under a context
// carrying a key log: the session is made and nothing reaches the file.
func TestRequest_AFullURLWritesNoKeyLog(t *testing.T) {
	srv := tlsDeviceServer(t, tls.VersionTLS12, 0)
	path := filepath.Join(t.TempDir(), "run.keylog")
	keyLog, err := devicetls.CreateKeyLog(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := devicetls.WithKeyLog(context.Background(), keyLog)
	if _, err := http.Request(ctx, newRequestContext(), nil, map[string]any{"url": srv.URL + "/items", "validate_certs": false}); err != nil {
		t.Fatal(err)
	}
	if srv.hits.Load() != 1 {
		t.Fatalf("the server answered %d requests, want 1", srv.hits.Load())
	}
	if err := keyLog.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Errorf("a full URL's session wrote to the key log: %v", err)
	}
}
