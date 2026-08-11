package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSafeReturn_CannotBeRedirectedOffOrigin is the evidence behind this
// package's two G710 waivers.
//
// gosec's taint analysis traces the Referer header into http.Redirect and
// cannot see that safeReturn constrains it, so the finding remains reported.
// This is what closes the underlying risk rather than only silencing the
// report: every input below is a real open-redirect technique, and the
// result is asserted to be a rooted path inside this UI's own prefix in
// every case.
//
// An open redirect on a control plane is not a cosmetic issue. It is a
// phishing primitive aimed precisely at the people who trust this origin
// and hold credentials for it.
func TestSafeReturn_CannotBeRedirectedOffOrigin(t *testing.T) {
	h := &Handler{cfg: Config{Prefix: "/ui"}}

	for _, referer := range []string{
		// Absolute URLs on another origin.
		"https://evil.example/ui/inventories",
		"http://evil.example/ui",
		// Protocol-relative, which a browser resolves against the current
		// scheme and is the classic bypass of a naive "starts with /" test.
		"//evil.example/ui/inventories",
		"///evil.example/ui",
		// Userinfo, which makes the host look like ours to a reader.
		"https://ui.pleiades.example@evil.example/ui/",
		// Backslashes, which several browsers normalise to slashes.
		"\\\\evil.example/ui",
		"/\\evil.example/ui",
		// Traversal out of the prefix.
		"/ui/../../etc/passwd",
		"/ui/inventories/../../../admin",
		// Prefix look-alikes: inside the string but not the path root.
		"/uiadmin/secrets",
		"/not-ui/ui/inventories",
		"https://evil.example/#/ui/inventories",
		// Schemes that are not navigation at all.
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		// Malformed input the parser may reject outright.
		"://",
		"%",
		"",
	} {
		t.Run(referer, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/ui/theme", nil)
			if referer != "" {
				r.Header.Set("Referer", referer)
			}

			got := h.safeReturn(r)

			// The one invariant: always a rooted path inside our prefix.
			if !strings.HasPrefix(got, "/ui") {
				t.Fatalf("safeReturn(%q) = %q, which is outside the UI prefix", referer, got)
			}
			if strings.HasPrefix(got, "//") {
				t.Fatalf("safeReturn(%q) = %q, a protocol-relative URL", referer, got)
			}
			if strings.Contains(got, "..") {
				t.Fatalf("safeReturn(%q) = %q, which still contains a traversal", referer, got)
			}
			if strings.ContainsAny(got, ":\\") {
				t.Fatalf("safeReturn(%q) = %q, which carries a scheme or a backslash", referer, got)
			}
			// "/uiadmin" starts with "/ui" but is a different tree, so the
			// prefix check has to be boundary-aware rather than textual.
			if got != "/ui" && !strings.HasPrefix(got, "/ui/") {
				t.Fatalf("safeReturn(%q) = %q, which is a sibling of the prefix, not inside it", referer, got)
			}
		})
	}
}

// A genuine same-origin referer is preserved, or the control would bounce
// every user to the same landing page instead of back to their work.
func TestSafeReturn_KeepsAGenuineInternalPath(t *testing.T) {
	h := &Handler{cfg: Config{Prefix: "/ui"}}

	for _, tc := range []struct{ referer, want string }{
		{"/ui/inventories", "/ui/inventories"},
		{"/ui/inventories/core-router-01", "/ui/inventories/core-router-01"},
		{"http://localhost:8080/ui/inventories", "/ui/inventories"},
		{"https://pleiades.example/ui", "/ui"},
	} {
		t.Run(tc.referer, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/ui/theme", nil)
			r.Header.Set("Referer", tc.referer)

			if got := h.safeReturn(r); got != tc.want {
				t.Errorf("safeReturn(%q) = %q, want %q", tc.referer, got, tc.want)
			}
		})
	}
}

// resourcePath escapes the identifier, because path.Join cleans its result:
// an id of "../.." would not be a segment, it would delete the prefix in
// front of it.
func TestResourcePath_EscapesTheIdentifier(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"core-router-01", "/ui/inventories/core-router-01"},
		{"../../etc/passwd", "/ui/inventories/..%2F..%2Fetc%2Fpasswd"},
		{"a/b", "/ui/inventories/a%2Fb"},
		{"with space", "/ui/inventories/with%20space"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			got := resourcePath("/ui", "inventories", tc.id)
			if got != tc.want {
				t.Errorf("resourcePath(%q) = %q, want %q", tc.id, got, tc.want)
			}
			if !strings.HasPrefix(got, "/ui/inventories/") {
				t.Errorf("resourcePath(%q) = %q, which escaped its own prefix", tc.id, got)
			}
		})
	}
}
