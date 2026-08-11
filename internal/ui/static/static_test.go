package static_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os/exec"
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
	// Comments are stripped first. The token blocks document their own
	// substitutions ("#1877F2 -> #1464CC"), and a documented colour is a
	// note about a decision rather than a declaration that renders.
	comments := regexp.MustCompile(`(?s)/\*.*?\*/`)
	css := comments.ReplaceAllString(string(body), "")

	// Strip every :root token block and every banner rule; what remains is
	// component CSS, which must read colours through var() alone. A literal
	// there is a colour that is wrong in one of the eight palettes with no
	// single place to fix it.
	tokenBlocks := regexp.MustCompile(`(?s):root[^{]*\{[^}]*\}`)
	components := tokenBlocks.ReplaceAllString(css, "")

	// The banner levels are the one carve-out, and it is narrow. Six of the
	// nine are published IC/DoD banner-marking colours whose values are
	// prescribed, and all nine must render identically in every skin and
	// theme -- making them tokens would be making them theme-dependent,
	// which is the bug. TestBannerContrast checks them separately, so the
	// carve-out costs no coverage.
	bannerRules := regexp.MustCompile(`(?s)\.banner-[a-z-]+\s*\{[^}]*\}`)
	components = bannerRules.ReplaceAllString(components, "")

	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	if found := hex.FindAllString(components, -1); len(found) > 0 {
		t.Errorf("component CSS declares literal colours %v; use a var(--token) instead", found)
	}
}

// Every banner level must declare both halves of its pair. A rule that set
// a background and inherited its text colour would render the marking in
// whatever the current theme's foreground happens to be, which is exactly
// how a yellow TS//SCI banner ends up with unreadable cream text at night.
func TestBannerLevelsDeclareBothColours(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}

	rules := regexp.MustCompile(`(?s)(\.banner-[a-z-]+)\s*\{([^}]*)\}`).
		FindAllStringSubmatch(string(body), -1)
	if len(rules) == 0 {
		t.Fatal("app.css declares no banner levels")
	}

	for _, rule := range rules {
		name, decls := rule[1], rule[2]
		if !strings.Contains(decls, "background:") {
			t.Errorf("%s declares no background", name)
		}
		if !strings.Contains(decls, "color:") {
			t.Errorf("%s declares no text colour, so it would inherit the theme's", name)
		}
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
		{`[data-skin="las-ventanas"]`, "the second skin must exist"},
		{`[data-a11y="true"]`, "the explicit accessibility override must exist"},
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

// TestEmbeddedAssetsAreTrackedByGit is the gate for a failure that already
// happened here, and one that no amount of local testing would have caught.
//
// .gitignore carried an unanchored "vendor/" line, which matches a
// directory of that name at any depth, so it excluded this package's entire
// vendor directory -- the third-party JavaScript the controller embeds and
// redistributes. `git add` prints nothing when it skips an ignored path, so
// the commit that claimed to add those assets added none of them, and the
// committed tree failed to build with "pattern vendor: no matching files
// found". Every local build stayed green, because the files were sitting on
// disk the whole time.
//
// The general rule this enforces: a file the binary embeds is a file the
// repository must actually contain. Anything else is a build that only works
// on the machine it was written on.
func TestEmbeddedAssetsAreTrackedByGit(t *testing.T) {
	assets := static.Assets()
	if len(assets) == 0 {
		t.Fatal("no embedded assets found, which means this gate is checking nothing")
	}

	// --error-unmatch turns "not tracked" into a non-zero exit rather than
	// empty output, so a path that is ignored, deleted or never added fails
	// here instead of passing quietly.
	args := append([]string{"ls-files", "--error-unmatch", "--"}, assets...)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("embedded assets are not all tracked by git, so the committed "+
			"tree does not build:\n%s\ncheck .gitignore for a pattern matching "+
			"internal/ui/static: %v", out, err)
	}

	tracked := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		tracked[strings.TrimPrefix(strings.TrimSpace(line), "internal/ui/static/")] = true
	}
	for _, name := range assets {
		if !tracked[name] {
			t.Errorf("embedded asset %q is not tracked by git", name)
		}
	}
}
