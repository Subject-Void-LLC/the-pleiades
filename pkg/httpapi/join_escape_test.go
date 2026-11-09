// Tests that a joined path reaches a real server exactly as its caller
// escaped it: escaped once, never decoded on the way, and never holding a
// dot segment a server would resolve.
package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
)

// sent is what a test server saw of the last request: the request target as
// it arrived on the wire, and the path the server decoded from it.
type sent struct {
	mu        sync.Mutex
	uri, path string
}

// last returns the request target and decoded path last recorded.
func (s *sent) last() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.uri, s.path
}

// recorder starts a TLS test server that records each request it is sent.
func recorder(t *testing.T) (*httptest.Server, *sent) {
	t.Helper()
	got := &sent{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.mu.Lock()
		defer got.mu.Unlock()
		got.uri, got.path = r.RequestURI, r.URL.Path
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// TestJoin_SendsTheCallersEscaping sends one escaped segment of each awkward
// kind through Join to a real server. The server must receive the segment
// exactly as url.PathEscape wrote it, and decode it back to the name. A %2F
// that Join decoded into a / used to reach the server as a second segment,
// and a %3B as a literal ; that a Java server reads as a matrix parameter.
func TestJoin_SendsTheCallersEscaping(t *testing.T) {
	srv, got := recorder(t)
	base := mustBase(t, srv.URL+"/api/")
	for _, tc := range []struct {
		name    string
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
		seg := url.PathEscape(tc.name)
		full, err := httpapi.Join(base, "/labs/"+seg+"/nodes?tail=5")
		if tc.refused {
			if err == nil {
				t.Errorf("Join with segment %q = %s, want a refusal", tc.name, full)
			}
			continue
		}
		if err != nil {
			t.Errorf("Join with segment %q: %v", tc.name, err)
			continue
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, full.String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		uri, decoded := got.last()
		if want := "/api/labs/" + seg + "/nodes?tail=5"; uri != want {
			t.Errorf("name %q was sent as %q, want %q", tc.name, uri, want)
		}
		if want := "/api/labs/" + tc.name + "/nodes"; decoded != want {
			t.Errorf("name %q was decoded by the server as %q, want %q", tc.name, decoded, want)
		}
	}
}

// TestJoin_KeepsEachEscapeAsWrittenBesideARawByte: a path a person wrote,
// as http.request's device mode takes one, may mix escapes with raw bytes.
// Each escape reaches the server exactly as written, and each raw byte a
// path may not hold is escaped. Join used to keep the caller's form only
// when the whole path was validly escaped, so one raw space sent
// "/items/a%2Fb c" as /items/a/b%20c, a second segment.
func TestJoin_KeepsEachEscapeAsWrittenBesideARawByte(t *testing.T) {
	srv, got := recorder(t)
	base := mustBase(t, srv.URL+"/api")
	for ref, want := range map[string][2]string{
		"/items/a%2Fb c":     {"/api/items/a%2Fb%20c", "/api/items/a/b c"},
		"/items/%7e%41":      {"/api/items/%7e%41", "/api/items/~A"},
		"/items/a%2fb\"<>":   {"/api/items/a%2fb%22%3C%3E", "/api/items/a/b\"<>"},
		"/items/caf\xc3\xa9": {"/api/items/caf%C3%A9", "/api/items/caf\xc3\xa9"},
	} {
		full, err := httpapi.Join(base, ref)
		if err != nil {
			t.Errorf("Join(%q): %v", ref, err)
			continue
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, full.String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		uri, decoded := got.last()
		if uri != want[0] || decoded != want[1] {
			t.Errorf("Join(%q) reached the server as %q, decoded %q, want %q, %q", ref, uri, decoded, want[0], want[1])
		}
	}
}

// TestJoin_RefusesWhatTheDecodedPathHolds: a control character is refused
// written as an escape too, since the server acts on the decoded path.
func TestJoin_RefusesWhatTheDecodedPathHolds(t *testing.T) {
	base := mustBase(t, "https://api.example.com/v2/")
	for _, ref := range []string{"/a%00b", "/a%0Ab", "/a%0d%0aHost:x", "/a%7F"} {
		if got, err := httpapi.Join(base, ref); err == nil {
			t.Errorf("Join(%q) = %s, want a refusal", ref, got)
		}
	}
	// A ; that only follows other text is a path parameter, not a dot
	// segment, and is sent.
	if _, err := httpapi.Join(base, "/a/b;v=1/..x"); err != nil {
		t.Errorf("Join of a path parameter: %v", err)
	}
}

// TestJoin_RefusesADotSegmentInAnyEncoding: the dot-segment check runs on the
// decoded path, so a dot segment is refused however it is written, including
// one that only appears once an escaped slash is decoded, and one followed
// by a ";" path parameter, which a Java server strips before resolving it.
func TestJoin_RefusesADotSegmentInAnyEncoding(t *testing.T) {
	base := mustBase(t, "https://api.example.com/v2/")
	for _, ref := range []string{
		"/labs/..", "/labs/.", "/labs/../x", "/labs/%2E%2E/x", "/labs/%2e%2E", "/labs/%2E/x",
		"/labs/a%2F..%2Fb", "/labs/a%2f.%2fb",
		"/..;/x", "/labs/..;jsessionid=1/x", "/labs/.;x", "/labs/..%3B/x", "/labs/%2E%2E;/x",
	} {
		if got, err := httpapi.Join(base, ref); err == nil {
			t.Errorf("Join(%q) = %s, want a refusal", ref, got)
		}
	}
}

// TestJoin_KeepsAnEscapedBasePath: a base whose own path holds an escaped
// slash keeps it, and the joined path and its encoding agree.
func TestJoin_KeepsAnEscapedBasePath(t *testing.T) {
	base := mustBase(t, "https://api.example.com/tenant%2Fone/v2")
	got, err := httpapi.Join(base, "/items/a%2Fb")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://api.example.com/tenant%2Fone/v2/items/a%2Fb"; got.String() != want {
		t.Errorf("Join = %s, want %s", got, want)
	}
	if want := "/tenant/one/v2/items/a/b"; got.Path != want {
		t.Errorf("Join's decoded path = %q, want %q", got.Path, want)
	}
}
