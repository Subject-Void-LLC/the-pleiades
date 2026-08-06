package catalystcenter_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/catalystcenter"
)

// devicePage renders a fake page of n devices starting at index start, in
// the wire shape the real controller uses. Several numeric-looking fields
// genuinely come back as JSON strings, which this reproduces rather than
// tidies: a fixture that is cleaner than reality tests a client that does
// not exist.
func devicePage(start, n int) string {
	var b strings.Builder
	b.WriteString(`{"response":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		id := start + i
		fmt.Fprintf(&b, `{"id":"dev-%d","hostname":"sw%d","managementIpAddress":"10.0.0.%d",`+
			`"family":"Switches and Hubs","softwareType":"IOS-XE","softwareVersion":"17.12.1",`+
			`"reachabilityStatus":"Reachable","collectionStatus":"Managed","interfaceCount":"48"}`,
			id, id, id)
	}
	b.WriteString(`]}`)
	return b.String()
}

// sandbox is a fake controller. tokens counts how many times the auth
// endpoint was hit, so a test can assert on caching and refresh rather than
// only on the eventual result.
type sandbox struct {
	server   *httptest.Server
	tokens   atomic.Int64
	requests atomic.Int64
}

// newSandbox stands up a fake controller serving total devices, paging as
// the real one does. If expireAfter is positive, the first that many
// authenticated requests succeed and the next one returns 401 once,
// simulating a token aging out mid-listing.
func newSandbox(t testing.TB, total int, expireAfter int64) *sandbox {
	t.Helper()

	s := &sandbox{}
	var expired atomic.Bool

	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/dna/system/api/v1/auth/token" {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user" || pass != "pass" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			s.tokens.Add(1)
			_, _ = w.Write([]byte(`{"Token":"tok"}`))
			return
		}

		if r.Header.Get("X-Auth-Token") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		n := s.requests.Add(1)
		if expireAfter > 0 && n > expireAfter && expired.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device/count"):
			fmt.Fprintf(w, `{"response":%d}`, total)
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device"):
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if offset < 1 {
				offset = 1
			}
			start := offset - 1
			if start > total {
				start = total
			}
			remaining := total - start
			if limit < remaining {
				remaining = limit
			}
			_, _ = w.Write([]byte(devicePage(start, remaining)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

// newClient builds a client against s, closing it when the test ends.
func newClient(t testing.TB, s *sandbox) *catalystcenter.Client {
	t.Helper()

	c, err := catalystcenter.New(s.server.URL, "user", "pass")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestNew_Rejects covers the constructor's refusals. Each exists because
// the alternative surfaces much later as a confusing request error.
func TestNew_Rejects(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		username string
		wantErr  string
	}{
		{name: "no scheme", baseURL: "sandboxdnac.cisco.com", username: "u", wantErr: "scheme must be"},
		{name: "unsupported scheme", baseURL: "ftp://host", username: "u", wantErr: "scheme must be"},
		{name: "no host", baseURL: "https:///path", username: "u", wantErr: "has no host"},
		{name: "no username", baseURL: "https://host", username: "", wantErr: "username is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := catalystcenter.New(tt.baseURL, tt.username, "p")
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestNew_TrimsTrailingSlash proves a base URL written with a trailing
// slash does not produce doubled slashes in every request path.
func TestNew_TrimsTrailingSlash(t *testing.T) {
	c, err := catalystcenter.New("https://host/", "u", "p")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.BaseURL(); got != "https://host" {
		t.Errorf("BaseURL() = %q, want %q", got, "https://host")
	}
}

// TestDeviceCount proves the count endpoint and, implicitly, that
// authentication happens before the first real request.
func TestDeviceCount(t *testing.T) {
	s := newSandbox(t, 7, 0)
	got, err := newClient(t, s).DeviceCount(context.Background())
	if err != nil {
		t.Fatalf("DeviceCount: %v", err)
	}
	if got != 7 {
		t.Errorf("DeviceCount() = %d, want 7", got)
	}
}

// TestEachDevice_PagesToCompletion proves paging follows through to the end
// and stops on a short page, across sizes that divide the total evenly and
// sizes that do not. The exact-multiple case is the one that would loop
// forever if termination depended only on an empty page.
func TestEachDevice_PagesToCompletion(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		pageSize int
	}{
		{name: "single page", total: 4, pageSize: 10},
		{name: "exact multiple", total: 10, pageSize: 5},
		{name: "uneven final page", total: 7, pageSize: 3},
		{name: "page size of one", total: 5, pageSize: 1},
		{name: "no devices at all", total: 0, pageSize: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSandbox(t, tt.total, 0)

			var seen []string
			err := newClient(t, s).EachDevice(context.Background(), tt.pageSize, func(d catalystcenter.Device) error {
				seen = append(seen, d.ID)
				return nil
			})
			if err != nil {
				t.Fatalf("EachDevice: %v", err)
			}
			if len(seen) != tt.total {
				t.Fatalf("saw %d devices, want %d", len(seen), tt.total)
			}

			unique := make(map[string]bool, len(seen))
			for _, id := range seen {
				if unique[id] {
					t.Errorf("device %s was yielded twice across page boundaries", id)
				}
				unique[id] = true
			}
		})
	}
}

// TestEachDevice_StopsOnCallbackError proves a caller can abort a walk
// partway through, and that the error reaches it unwrapped enough to
// compare.
func TestEachDevice_StopsOnCallbackError(t *testing.T) {
	s := newSandbox(t, 20, 0)
	stop := fmt.Errorf("caller said stop")

	var seen int
	err := newClient(t, s).EachDevice(context.Background(), 5, func(catalystcenter.Device) error {
		seen++
		if seen == 3 {
			return stop
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "caller said stop") {
		t.Fatalf("EachDevice error = %v, want the callback's own error", err)
	}
	if seen != 3 {
		t.Errorf("callback ran %d times after asking to stop, want 3", seen)
	}
}

// TestEachDevice_RejectsBadPageSize proves a nonsensical page size fails
// immediately rather than producing an infinite loop or an empty result.
func TestEachDevice_RejectsBadPageSize(t *testing.T) {
	s := newSandbox(t, 4, 0)
	for _, size := range []int{0, -1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			err := newClient(t, s).EachDevice(context.Background(), size, func(catalystcenter.Device) error { return nil })
			if err == nil {
				t.Fatal("expected a non-positive page size to be rejected")
			}
		})
	}
}

// TestTokenIsCached proves repeated calls reuse one token rather than
// authenticating per request, which on a large fleet would double the
// request count.
func TestTokenIsCached(t *testing.T) {
	s := newSandbox(t, 4, 0)
	c := newClient(t, s)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := c.DeviceCount(ctx); err != nil {
			t.Fatalf("DeviceCount: %v", err)
		}
	}
	if got := s.tokens.Load(); got != 1 {
		t.Errorf("authenticated %d times, want 1: the token should be cached", got)
	}
}

// TestTokenRefreshesOn401 proves an expired token is transparently
// replaced. A token aging out between two pages of one listing is the
// ordinary case on a long walk, not an error worth surfacing.
func TestTokenRefreshesOn401(t *testing.T) {
	// Expire after the first authenticated request, so the second one
	// returns 401 exactly once and must be retried.
	s := newSandbox(t, 4, 1)
	c := newClient(t, s)
	ctx := context.Background()

	if _, err := c.DeviceCount(ctx); err != nil {
		t.Fatalf("first DeviceCount: %v", err)
	}
	if _, err := c.DeviceCount(ctx); err != nil {
		t.Fatalf("DeviceCount after the token expired: %v", err)
	}
	if got := s.tokens.Load(); got != 2 {
		t.Errorf("authenticated %d times, want 2: one initial and one refresh", got)
	}
}

// TestWrongCredentialsAreNotRetried proves a rejected login fails rather
// than looping. Retrying the same wrong password is how an account gets
// locked out.
func TestWrongCredentialsAreNotRetried(t *testing.T) {
	s := newSandbox(t, 4, 0)
	c, err := catalystcenter.New(s.server.URL, "user", "wrong")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.DeviceCount(context.Background()); err == nil {
		t.Fatal("expected wrong credentials to fail")
	}
	if got := s.tokens.Load(); got != 0 {
		t.Errorf("issued %d tokens for wrong credentials, want 0", got)
	}
}

// TestClose_IsIdempotentAndReusable proves Close can be called more than
// once and that a closed client still works, which is what makes it safe in
// a defer next to a client that may be reused.
func TestClose_IsIdempotentAndReusable(t *testing.T) {
	s := newSandbox(t, 4, 0)
	c := newClient(t, s)
	ctx := context.Background()

	if _, err := c.DeviceCount(ctx); err != nil {
		t.Fatalf("DeviceCount: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := c.DeviceCount(ctx); err != nil {
		t.Fatalf("DeviceCount after Close: %v", err)
	}
}

// TestServerErrorSurfaces proves a non-200, non-401 response is reported
// with its status rather than being retried or swallowed.
func TestServerErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dna/system/api/v1/auth/token" {
			_, _ = w.Write([]byte(`{"Token":"tok"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c, err := catalystcenter.New(srv.URL, "user", "pass")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	_, err = c.DeviceCount(context.Background())
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %v, want it to report the 500 status", err)
	}
}

// TestEmptyTokenIsRejected proves an auth response carrying no token is an
// error. Accepting it would send every later request with an empty header
// and fail confusingly at the first real endpoint.
func TestEmptyTokenIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Token":""}`))
	}))
	t.Cleanup(srv.Close)

	c, err := catalystcenter.New(srv.URL, "user", "pass")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.DeviceCount(context.Background()); err == nil {
		t.Fatal("expected an empty token to be rejected")
	}
}

// TestListSitesAndTags covers the two unpaged endpoints.
func TestListSitesAndTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/dna/system/api/v1/auth/token":
			_, _ = w.Write([]byte(`{"Token":"tok"}`))
		case "/dna/intent/api/v1/site":
			_, _ = w.Write([]byte(`{"response":[{"id":"s1","name":"HU_B01","siteNameHierarchy":"Global/EU/Hungary/HU_B01"}]}`))
		case "/dna/intent/api/v1/tag":
			_, _ = w.Write([]byte(`{"response":[{"id":"t1","name":"WAN","systemTag":true},{"id":"t2","name":"prod","systemTag":false}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := catalystcenter.New(srv.URL, "user", "pass")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()

	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatalf("ListSites: %v", err)
	}
	if len(sites) != 1 || sites[0].NameHierarchy != "Global/EU/Hungary/HU_B01" {
		t.Errorf("sites = %+v, want the full hierarchy preserved", sites)
	}

	tags, err := c.ListTags(ctx)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2", len(tags))
	}
	if !tags[0].SystemTag || tags[1].SystemTag {
		t.Errorf("tags = %+v, want the system/operator distinction preserved", tags)
	}
}
