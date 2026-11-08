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

// TestNew_GivesOnePathOneIdentity: BaseURL is the synced controller's
// DeviceID, so two spellings of one base path give one BaseURL, and the
// controller is not onboarded twice. Escapes that do not mean the same
// path (a literal ; against %3B, which a Java server reads apart) stay
// apart, and an escaped slash stays escaped.
func TestNew_GivesOnePathOneIdentity(t *testing.T) {
	for _, same := range [][]string{
		{"https://dnac.example/a%2fb", "https://dnac.example/a%2Fb", "https://dnac.example/a%2Fb/"},
		{"https://dnac.example/%7Eops", "https://dnac.example/~ops", "https://dnac.example/%7eops"},
		{"https://dnac.example/%41pi%2d1", "https://dnac.example/Api-1"},
		{"https://dnac.example/a%20b", "https://dnac.example/a b"},
		{"https://dnac.example/x%2F", "https://dnac.example/x%2f"},
	} {
		var first string
		for i, base := range same {
			c, err := catalystcenter.New(base, "admin", "pw")
			if err != nil {
				t.Fatalf("New(%q): %v", base, err)
			}
			if i == 0 {
				first = c.BaseURL()
				continue
			}
			if c.BaseURL() != first {
				t.Errorf("New(%q).BaseURL() = %q, but New(%q) gave %q for the same path", base, c.BaseURL(), same[0], first)
			}
		}
	}
	for base, want := range map[string]string{
		"https://dnac.example/a%2fb": "https://dnac.example/a%2Fb",
		"https://dnac.example/%7E":   "https://dnac.example/~",
		"https://dnac.example/x%2F":  "https://dnac.example/x%2F",
		"https://dnac.example/a;b":   "https://dnac.example/a;b",
		"https://dnac.example/a%3bb": "https://dnac.example/a%3Bb",
	} {
		c, err := catalystcenter.New(base, "admin", "pw")
		if err != nil {
			t.Errorf("New(%q): %v", base, err)
			continue
		}
		if c.BaseURL() != want {
			t.Errorf("New(%q).BaseURL() = %q, want %q", base, c.BaseURL(), want)
		}
	}
}

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
