// Tests that a Catalyst Center reached under a path prefix (a reverse proxy
// in front of the controller) is called under exactly that prefix: escaped
// as the operator wrote it, and never one a server would resolve away.
package catalystcenter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/catalystcenter"
)

// TestNew_KeepsTheBasePathAsWritten puts one awkward segment in the base
// URL's path and reads the device count through it. The controller must be
// sent the segment as url.PathEscape wrote it and decode it back. New used
// to keep only the decoded path, so a %2F in the prefix reached the proxy as
// a second segment; a ".." in it, which a server resolves away, is refused.
func TestNew_KeepsTheBasePathAsWritten(t *testing.T) {
	var mu sync.Mutex
	var uri, decoded string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/dna/system/api/v1/auth/token"):
			_, _ = w.Write([]byte(`{"Token":"tok"}`))
		case strings.HasSuffix(r.URL.Path, "/dna/intent/api/v1/network-device/count"):
			mu.Lock()
			uri, decoded = r.RequestURI, r.URL.Path
			mu.Unlock()
			_, _ = w.Write([]byte(`{"response":3}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	for _, tc := range []struct {
		segment string
		refused bool
	}{
		{"a b", false},
		{"a/b", false},
		{"a?b", false},
		{"a#b", false},
		{"a;b", false},
		{"..", true},
		{".", true},
		{"%2E%2E", false},
		{"%2F", false},
	} {
		seg := url.PathEscape(tc.segment)
		c, err := catalystcenter.New(srv.URL+"/"+seg+"/", "admin", "pw")
		if tc.refused {
			if err == nil {
				t.Errorf("New with base path segment %q = %s, want a refusal", tc.segment, c.BaseURL())
			}
			continue
		}
		if err != nil {
			t.Errorf("New with base path segment %q: %v", tc.segment, err)
			continue
		}
		mu.Lock()
		uri, decoded = "", ""
		mu.Unlock()
		if n, err := c.DeviceCount(context.Background()); err != nil || n != 3 {
			t.Errorf("segment %q: DeviceCount = %d, %v", tc.segment, n, err)
		}
		_ = c.Close()
		mu.Lock()
		gotURI, gotPath := uri, decoded
		mu.Unlock()
		if want := "/" + seg + "/dna/intent/api/v1/network-device/count"; gotURI != want {
			t.Errorf("segment %q was sent as %q, want %q", tc.segment, gotURI, want)
		}
		if want := "/" + tc.segment + "/dna/intent/api/v1/network-device/count"; gotPath != want {
			t.Errorf("segment %q was decoded by the server as %q, want %q", tc.segment, gotPath, want)
		}
	}
}
