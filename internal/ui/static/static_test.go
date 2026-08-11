package static_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/static"
)

// TestVendoredAssetChecksums is the supply-chain gate. The embedded bytes
// are what ship; the constants are what was reviewed. If they diverge --
// because someone dropped in a different build, or a fetch was retried
// against a moved dist-tag -- this fails rather than the binary quietly
// serving unreviewed third-party JavaScript.
func TestVendoredAssetChecksums(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"vendor/echarts.min.js", static.EChartsSHA256},
		{"vendor/htmx.min.js", static.HTMXSHA256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := static.Read(tc.name)
			if err != nil {
				t.Fatalf("reading %s: %v", tc.name, err)
			}
			sum := sha256.Sum256(body)
			if got := hex.EncodeToString(sum[:]); got != tc.want {
				t.Fatalf("%s digest = %s, want %s (see vendor/PROVENANCE.md)", tc.name, got, tc.want)
			}
		})
	}
}

// The binary redistributes Apache-2.0 code, which obliges it to carry the
// NOTICE forward. Deleting these files to save a few kilobytes is a
// licensing failure, not a size optimization, so it is a test.
func TestLicenceFilesAreEmbedded(t *testing.T) {
	for _, name := range []string{
		"vendor/LICENSE-echarts.txt",
		"vendor/NOTICE-echarts.txt",
		"vendor/LICENSE-htmx.txt",
	} {
		body, err := static.Read(name)
		if err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
			continue
		}
		if len(body) == 0 {
			t.Errorf("%s is embedded but empty", name)
		}
	}
}

func TestPath_IsContentHashed(t *testing.T) {
	got := static.Path("app.css")

	if !strings.HasPrefix(got, "app.") || !strings.HasSuffix(got, ".css") {
		t.Fatalf("Path(app.css) = %q, want app.<hash>.css", got)
	}
	if got == "app.css" {
		t.Fatal("Path(app.css) returned the unhashed name, so nothing is cache-busted")
	}
	// Stable across calls: a hash that changed per call would make every
	// page load fetch the stylesheet again.
	if second := static.Path("app.css"); second != got {
		t.Errorf("Path is not stable: %q then %q", got, second)
	}
}

func TestPath_PanicsOnAnUnknownAsset(t *testing.T) {
	// A miss is a template naming a file that does not exist, which is a
	// typo the build should not survive rather than a runtime condition
	// that silently renders an unstyled page.
	defer func() {
		if recover() == nil {
			t.Fatal("Path did not panic on an unknown asset")
		}
	}()
	static.Path("does-not-exist.css")
}

func TestHandler_ServesHashedAssetsImmutably(t *testing.T) {
	handler := static.Handler("/ui/static")

	req := httptest.NewRequest(http.MethodGet, "/ui/static/"+static.Path("app.css"), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	// Immutable is safe precisely because the URL carries the content
	// hash: a changed file is a different URL.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff is absent, so a browser may decide the type for itself")
	}
	if !strings.Contains(rec.Body.String(), "--fill-ok") {
		t.Error("the served body is not the stylesheet")
	}
}

func TestHandler_UnknownPathsAre404(t *testing.T) {
	handler := static.Handler("/ui/static")

	// Nothing here touches the filesystem, so traversal has no filesystem
	// to reach: an unknown path is a map miss.
	for _, p := range []string{
		"/ui/static/app.css",          // unhashed: not a served name
		"/ui/static/../../etc/passwd", //
		"/ui/static/",
		"/ui/static/vendor",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", p, rec.Code)
		}
	}
}

// The stylesheet is the one place colours are declared, and every rule
// below the token blocks reads them through var(). A literal hex outside
// those blocks is a colour that is wrong in the other theme with no second
// place to fix it.
func TestStylesheet_DeclaresNoColourOutsideTheTokenBlocks(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	css := string(body)

	// Token blocks are the :root rules; everything after the last one is
	// component CSS and must be colour-literal-free.
	lastTokenBlock := strings.LastIndex(css, `:root[data-theme="dark"]`)
	if lastTokenBlock < 0 {
		t.Fatal("the explicit dark token block is missing")
	}
	end := strings.Index(css[lastTokenBlock:], "}")
	if end < 0 {
		t.Fatal("the dark token block is unterminated")
	}
	components := css[lastTokenBlock+end:]

	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	if found := hex.FindAllString(components, -1); len(found) > 0 {
		t.Errorf("component CSS declares literal colours %v; use a var(--token) instead", found)
	}
}

// Three states, never a boolean. A bare boolean has no way to express
// "follow the operating system" once the user has touched the control.
func TestStylesheet_SupportsAllThreeThemeStates(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	css := string(body)

	for _, want := range []struct {
		fragment string
		why      string
	}{
		{"@media (prefers-color-scheme: dark)", "system preference must be honoured when the user has expressed none"},
		{`:root:not([data-theme="light"])`, "an explicit light choice must win over a dark OS"},
		{`:root[data-theme="dark"]`, "an explicit dark choice must win over a light OS"},
		{"@media (prefers-reduced-motion: reduce)", "motion preference must be honoured"},
		{"@media (forced-colors: active)", "Windows High Contrast overrides every colour above"},
		{"100dvh", "100vh is clipped by a mobile browser's collapsing toolbar"},
		{"@media (pointer: coarse)", "a touch target is bigger than a mouse target"},
	} {
		if !strings.Contains(css, want.fragment) {
			t.Errorf("app.css is missing %q: %s", want.fragment, want.why)
		}
	}
}

// The CSP carries no 'unsafe-inline', so the client-side files must not
// depend on being inlined, and nothing may block paste.
func TestScripts_HonourTheContentSecurityPolicy(t *testing.T) {
	for _, name := range []string{"app.js", "chart.js"} {
		body, err := static.Read(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		src := string(body)

		if strings.Contains(src, "eval(") || strings.Contains(src, "new Function(") {
			t.Errorf("%s uses eval, which script-src 'self' forbids", name)
		}
		// Paste-blocking a 900-character token is hostile, and is an
		// accessibility failure in its own right (WCAG SC 3.3.8).
		if strings.Contains(src, "onpaste") {
			t.Errorf("%s blocks paste", name)
		}
	}
}
