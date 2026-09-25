// Tests for the device API rules: a joined path never leaves the base
// URL's origin, and a credential is attached only as its mode says.
package httpapi_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
)

func mustBase(t testing.TB, raw string) *url.URL {
	t.Helper()
	u, err := httpapi.ValidateBaseURL(raw, httpapi.AuthBasic, false)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestJoin covers the paths a device URL may and may not be.
func TestJoin(t *testing.T) {
	base := mustBase(t, "https://api.example.com/v2/")
	for _, tc := range []struct {
		ref, want string
	}{
		{"/items", "https://api.example.com/v2/items"},
		{"/items?page=2&x=a%20b", "https://api.example.com/v2/items?page=2&x=a%20b"},
		{"/a/b/", "https://api.example.com/v2/a/b/"},
		{"/", "https://api.example.com/v2/"},
		{"items", ""},
		{"//evil.example/x", ""},
		{`/\evil.example`, ""},
		{"/a/../../b", ""},
		{"/a/%2e%2e/b", ""},
		{"/./a", ""},
		{"/a#frag", ""},
		{"/a\nb", ""},
		{"https://evil.example/x", ""},
	} {
		got, err := httpapi.Join(base, tc.ref)
		switch {
		case tc.want == "" && err == nil:
			t.Errorf("Join(%q) = %s, want a refusal", tc.ref, got)
		case tc.want != "" && err != nil:
			t.Errorf("Join(%q): %v", tc.ref, err)
		case tc.want != "" && got.String() != tc.want:
			t.Errorf("Join(%q) = %s, want %s", tc.ref, got, tc.want)
		}
	}
}

// FuzzJoin: whatever ref is, a joined URL has the base's origin and sits
// under the base's path.
func FuzzJoin(f *testing.F) {
	for _, s := range []string{"/items", "//x", "/%2e%2e/", "/a?b#c", "/\\x", "/..;/"} {
		f.Add(s)
	}
	base := mustBase(f, "https://api.example.com:8443/v2")
	f.Fuzz(func(t *testing.T, ref string) {
		got, err := httpapi.Join(base, ref)
		if err != nil {
			return
		}
		if !httpapi.SameOrigin(base, got) || got.User != nil {
			t.Fatalf("Join(%q) = %s left the origin", ref, got)
		}
		if !strings.HasPrefix(got.Path, "/v2/") {
			t.Fatalf("Join(%q) = %s left the base path", ref, got)
		}
		reparsed, err := url.Parse(got.String())
		if err != nil || !httpapi.SameOrigin(base, reparsed) {
			t.Fatalf("Join(%q) = %s reparses to another origin", ref, got)
		}
	})
}

// TestSameOrigin compares scheme, host and port, with default ports.
func TestSameOrigin(t *testing.T) {
	a, _ := url.Parse("https://API.example.com/x")
	for raw, want := range map[string]bool{
		"https://api.example.com:443/y": true,
		"http://api.example.com/y":      false,
		"https://api.example.com:8443":  false,
		"https://evil.example.com":      false,
	} {
		b, _ := url.Parse(raw)
		if got := httpapi.SameOrigin(a, b); got != want {
			t.Errorf("SameOrigin(%s) = %v", raw, got)
		}
	}
}

// TestAuthorize attaches each mode's credential and refuses a mode whose
// credential is missing.
func TestAuthorize(t *testing.T) {
	req := func() *http.Request { r, _ := http.NewRequest(http.MethodGet, "https://a.example", nil); return r }
	r := req()
	if err := httpapi.Authorize(r, httpapi.AuthBasic, map[string]string{"username": "u", "password": "p"}); err != nil {
		t.Fatal(err)
	}
	if u, p, ok := r.BasicAuth(); !ok || u != "u" || p != "p" {
		t.Errorf("basic: %q %q %v", u, p, ok)
	}
	r = req()
	if err := httpapi.Authorize(r, httpapi.AuthBearer, map[string]string{"password": "tok"}); err != nil || r.Header.Get("Authorization") != "Bearer tok" {
		t.Errorf("bearer: %v %q", err, r.Header.Get("Authorization"))
	}
	r = req()
	if err := httpapi.Authorize(r, httpapi.AuthNone, map[string]string{"password": "tok"}); err != nil || r.Header.Get("Authorization") != "" {
		t.Errorf("none sent %q", r.Header.Get("Authorization"))
	}
	for _, mode := range []string{httpapi.AuthBasic, httpapi.AuthBearer, "digest"} {
		if err := httpapi.Authorize(req(), mode, nil); err == nil {
			t.Errorf("%s with no credential was accepted", mode)
		}
	}
}

// TestValidateBaseURL covers the base URL rules on their own.
func TestValidateBaseURL(t *testing.T) {
	for _, tc := range []struct {
		raw, auth string
		ok        bool
	}{
		{"https://a.example/v2", httpapi.AuthBasic, true},
		{"http://a.example", httpapi.AuthNone, true},
		{"http://a.example", httpapi.AuthBearer, false},
		{"http://a.example", httpapi.AuthBasic, false},
		{"", httpapi.AuthNone, false},
		{"https://a.example/\x7f", httpapi.AuthNone, false},
		{"https://a.example/%zz", httpapi.AuthNone, false},
		{"ftp://a.example", httpapi.AuthNone, false},
		{"https://", httpapi.AuthNone, false},
		{"https://u@a.example", httpapi.AuthNone, false},
		{"https://a.example/#frag", httpapi.AuthNone, false},
		{"https://a.example/?", httpapi.AuthNone, false},
		{"https://a.example", "digest", false},
	} {
		if _, err := httpapi.ValidateBaseURL(tc.raw, tc.auth, false); (err == nil) != tc.ok {
			t.Errorf("ValidateBaseURL(%q, %q): %v, want ok=%v", tc.raw, tc.auth, err, tc.ok)
		}
	}
	// The explicit allow: a credential over http:// only with the flag,
	// and the flag refused where it allows nothing.
	for _, tc := range []struct {
		raw, auth string
		ok        bool
	}{
		{"http://a.example", httpapi.AuthBasic, true},
		{"http://a.example", httpapi.AuthBearer, true},
		{"http://a.example", httpapi.AuthNone, false},
		{"https://a.example", httpapi.AuthBasic, false},
	} {
		if _, err := httpapi.ValidateBaseURL(tc.raw, tc.auth, true); (err == nil) != tc.ok {
			t.Errorf("ValidateBaseURL(%q, %q, allowed): %v, want ok=%v", tc.raw, tc.auth, err, tc.ok)
		}
	}
	plain, _ := url.Parse("http://a.example")
	if !httpapi.SendsPlaintextCredential(plain, httpapi.AuthBasic) || httpapi.SendsPlaintextCredential(plain, httpapi.AuthNone) {
		t.Error("SendsPlaintextCredential is wrong")
	}
	if w := httpapi.PlaintextWarning("api1"); !strings.Contains(w, "rotate the credential") || !strings.Contains(w, httpapi.AllowPlaintextCredentialsProperty) {
		t.Errorf("the warning %q does not say to rotate, or name its flag", w)
	}
	a, _ := url.Parse("http://a.example")
	b, _ := url.Parse("http://a.example:80/x")
	if !httpapi.SameOrigin(a, b) {
		t.Error("http's default port was not applied")
	}
}
