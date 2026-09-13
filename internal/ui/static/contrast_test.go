package static_test

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/static"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file is the accessibility gate that pays for itself.
//
// Contrast is the most commonly shipped WCAG failure and it is invisible to
// every other check here: a palette that fails compiles, vets, passes
// gosec, and renders. Parsing the real stylesheet and computing real ratios
// turns a review-time judgment nobody reliably makes into a build failure
// nobody can miss.
//
// It has already earned its place three times. It caught #555 empty-state
// text at 2.65:1 and a 1.38:1 structural border in the palette this UI
// replaced, and it caught six failures in the Las Ventanas palette before a
// line of it shipped -- including Facebook Blue as light-mode link text at
// 3.99:1, which is exactly the kind of brand colour that looks obviously
// fine and is not.
//
// The matrix matters as much as the maths. Two skins times two themes times
// accessibility mode on or off is eight complete palettes, and a value that
// is safe in seven of them is still a barrier in the eighth.

// relativeLuminance implements WCAG 2.x's own definition, written out
// rather than taken as a dependency because it is eight lines and the
// formula has not changed since 2008.
func relativeLuminance(hex string) (float64, error) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, fmt.Errorf("%q is not a 3- or 6-digit hex colour", hex)
	}

	channel := func(offset int) (float64, error) {
		v, err := strconv.ParseUint(hex[offset:offset+2], 16, 8)
		if err != nil {
			return 0, err
		}
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92, nil
		}
		return math.Pow((c+0.055)/1.055, 2.4), nil
	}

	r, err := channel(0)
	if err != nil {
		return 0, err
	}
	g, err := channel(2)
	if err != nil {
		return 0, err
	}
	b, err := channel(4)
	if err != nil {
		return 0, err
	}
	return 0.2126*r + 0.7152*g + 0.0722*b, nil
}

func contrastRatio(t *testing.T, fg, bg string) float64 {
	t.Helper()
	lf, err := relativeLuminance(fg)
	if err != nil {
		t.Fatalf("parsing %q: %v", fg, err)
	}
	lb, err := relativeLuminance(bg)
	if err != nil {
		t.Fatalf("parsing %q: %v", bg, err)
	}
	hi, lo := math.Max(lf, lb), math.Min(lf, lb)
	return (hi + 0.05) / (lo + 0.05)
}

var (
	tokenPattern = regexp.MustCompile(`(--[a-z-]+):\s*(#[0-9a-fA-F]{3,8})\s*;`)
	// Top-level rule blocks only. Blocks nested inside @media are skipped
	// by requiring the selector to start at the beginning of a line.
	blockPattern = regexp.MustCompile(`(?m)^(:root[^{]*)\{([^}]*)\}`)
)

// combination is one fully resolved palette: a skin, a theme, and whether
// accessibility mode is on.
type combination struct {
	Skin  view.Skin
	Theme view.Theme
	A11y  bool
}

func (c combination) String() string {
	name := string(c.Skin) + "/" + string(c.Theme)
	if c.A11y {
		name += "/a11y"
	}
	return name
}

// matches reports whether a selector applies to this combination.
//
// It understands only the selector shapes this stylesheet actually uses,
// which is deliberate: a general CSS matcher would be a second CSS engine
// to maintain and to get subtly wrong. The stylesheet keeps its skin
// selectors mutually exclusive precisely so this can stay simple, and a
// selector shape this does not recognise fails the test rather than being
// silently ignored.
func (c combination) matches(t *testing.T, selector string) bool {
	t.Helper()

	for _, part := range strings.Split(selector, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if c.matchesOne(t, part) {
			return true
		}
	}
	return false
}

func (c combination) matchesOne(t *testing.T, selector string) bool {
	t.Helper()

	rest := strings.TrimPrefix(selector, ":root")
	rest = strings.TrimSpace(rest)

	// Every clause in the selector must hold.
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, `[data-skin="`):
			want := clauseValue(rest)
			if view.Skin(want) != c.Skin {
				return false
			}
			rest = afterClause(rest)
		case strings.HasPrefix(rest, `[data-theme="`):
			want := clauseValue(rest)
			if view.Theme(want) != c.Theme {
				return false
			}
			rest = afterClause(rest)
		case strings.HasPrefix(rest, `[data-a11y="true"]`):
			if !c.A11y {
				return false
			}
			rest = strings.TrimPrefix(rest, `[data-a11y="true"]`)
		case strings.HasPrefix(rest, ":not("):
			end := strings.Index(rest, ")")
			if end < 0 {
				t.Fatalf("unterminated :not() in selector %q", selector)
			}
			inner := rest[len(":not("):end]
			// A negation holds when the inner clause does NOT match.
			if c.matchesOne(t, ":root"+inner) {
				return false
			}
			rest = strings.TrimSpace(rest[end+1:])
		default:
			t.Fatalf("selector %q uses a shape this test does not understand (%q); "+
				"either simplify the selector or teach matchesOne about it", selector, rest)
		}
	}
	return true
}

func clauseValue(s string) string {
	open := strings.Index(s, `="`)
	close := strings.Index(s[open+2:], `"`)
	return s[open+2 : open+2+close]
}

func afterClause(s string) string {
	return strings.TrimSpace(s[strings.Index(s, "]")+1:])
}

// resolve computes the token values a combination actually renders with,
// by applying every matching top-level block in source order. Because the
// stylesheet's skin selectors are mutually exclusive, source order is the
// whole cascade and no specificity arithmetic is needed.
func resolve(t *testing.T, css string, c combination) map[string]string {
	t.Helper()

	tokens := map[string]string{}
	applied := 0
	for _, block := range blockPattern.FindAllStringSubmatch(css, -1) {
		selector, body := strings.TrimSpace(block[1]), block[2]

		// Only token blocks participate. A :root rule that declares no
		// custom property is a behaviour rule (accessibility mode stilling
		// transitions, for instance), and asking the selector matcher
		// about it would mean teaching it descendant combinators it has no
		// reason to understand.
		declarations := tokenPattern.FindAllStringSubmatch(body, -1)
		if len(declarations) == 0 {
			continue
		}
		if !c.matches(t, selector) {
			continue
		}
		applied++
		for _, m := range declarations {
			tokens[m[1]] = m[2]
		}
	}
	if applied == 0 {
		t.Fatalf("no stylesheet block matched %s", c)
	}
	return tokens
}

// everyCombination is the full matrix. Only explicit theme choices are
// enumerated; the prefers-color-scheme blocks are covered separately by
// TestBothDarkPathsAgree, which asserts they say the same thing as the
// explicit ones.
func everyCombination() []combination {
	var out []combination
	for _, skin := range []view.Skin{view.SkinBrutalist, view.SkinLasVentanas, view.SkinHoneycrisp} {
		for _, theme := range []view.Theme{view.ThemeLight, view.ThemeDark} {
			for _, a11y := range []bool{false, true} {
				out = append(out, combination{Skin: skin, Theme: theme, A11y: a11y})
			}
		}
	}
	return out
}

// TestTokenContrastMeetsWCAG checks every token against the ratio its
// declared role requires, in all eight palettes.
//
// The roles matter as much as the numbers. SC 1.4.3 asks 4.5:1 of body
// text; SC 1.4.11 asks 3:1 of anything identifying a user interface
// component. --border and --border-interactive are split precisely because
// those are different jobs: a decorative hairline is exempt, and a
// control's only boundary is not. Conflating them is how a design that
// looks high-contrast ships an unusable form.
func TestTokenContrastMeetsWCAG(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	css := string(body)

	roles := []struct {
		token string
		want  float64
		why   string
	}{
		{"--fg", 4.5, "body text (SC 1.4.3)"},
		{"--fg-muted", 4.5, "muted text is still text, and is where under-contrast usually hides"},
		{"--link", 4.5, "links are body text, and the focus ring is drawn in this colour"},
		{"--border-interactive", 3.0, "a control's sole boundary (SC 1.4.11)"},
	}

	for _, c := range everyCombination() {
		t.Run(c.String(), func(t *testing.T) {
			tokens := resolve(t, css, c)
			bg, ok := tokens["--bg"]
			if !ok {
				t.Fatal("this combination resolves no --bg")
			}

			for _, role := range roles {
				value, ok := tokens[role.token]
				if !ok {
					t.Errorf("%s resolves no %s", c, role.token)
					continue
				}
				if got := contrastRatio(t, value, bg); got < role.want {
					t.Errorf("%s: %s (%s) on %s is %.2f:1, want at least %.1f:1 -- %s",
						c, role.token, value, bg, got, role.want, role.why)
				}
			}
		})
	}
}

// TestStatusFillContrast covers the status vocabulary, which is where
// colour-first designs usually fail.
//
// The fills carry the same text colour in every palette, which is itself
// the point: a status badge must not change meaning or legibility when
// somebody switches skin. Blue is the single inversion, and it is asserted
// rather than assumed so that "simplifying" it back onto --on-fill fails.
func TestStatusFillContrast(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	css := string(body)

	for _, c := range everyCombination() {
		t.Run(c.String(), func(t *testing.T) {
			tokens := resolve(t, css, c)

			onFill := tokens["--on-fill"]
			if onFill == "" {
				t.Fatal("no --on-fill resolved")
			}
			for _, name := range []string{"--fill-ok", "--fill-failed", "--fill-changed", "--fill-skipped"} {
				fill, ok := tokens[name]
				if !ok {
					t.Errorf("no %s resolved", name)
					continue
				}
				if got := contrastRatio(t, onFill, fill); got < 4.5 {
					t.Errorf("%s: %s on %s (%s) is %.2f:1, want at least 4.5:1",
						c, onFill, name, fill, got)
				}
			}

			info, onInfo := tokens["--fill-info"], tokens["--on-fill-info"]
			if info == "" || onInfo == "" {
				t.Fatal("the info fill or its text colour is missing")
			}
			if got := contrastRatio(t, onInfo, info); got < 4.5 {
				t.Errorf("%s: %s on --fill-info (%s) is %.2f:1, want at least 4.5:1",
					c, onInfo, info, got)
			}
		})
	}
}

// The regression this was written for: pure #0000FF is perfect on white and
// unusable on near-black, and Facebook Blue is the mirror image -- fine on
// tinted black at 4.52:1 and a barrier on cream at 3.99:1. A single link
// colour cannot serve both themes in either skin.
func TestLinkColourDiffersBetweenThemes(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	css := string(body)

	for _, skin := range []view.Skin{view.SkinBrutalist, view.SkinLasVentanas} {
		t.Run(string(skin), func(t *testing.T) {
			light := resolve(t, css, combination{Skin: skin, Theme: view.ThemeLight})
			dark := resolve(t, css, combination{Skin: skin, Theme: view.ThemeDark})

			if light["--link"] == dark["--link"] {
				t.Fatalf("both themes use %s for links; no single value clears 4.5:1 on "+
					"both this skin's light and dark grounds", light["--link"])
			}
			if got := contrastRatio(t, light["--link"], dark["--bg"]); got >= 4.5 {
				t.Errorf("the light link colour now passes on the dark ground (%.2f:1); "+
					"this test's premise is stale and the two could share one value", got)
			}
		})
	}
}

// The prefers-color-scheme blocks and the explicit dark blocks are two
// paths to the same appearance. A token in one but not the other means the
// OS-driven dark theme differs from the chosen one -- a bug nobody finds,
// because nobody views the same page both ways.
func TestBothDarkPathsAgree(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	css := string(body)

	// Inside @media blocks, which the top-level pattern skips.
	media := regexp.MustCompile(`(?s)@media \(prefers-color-scheme: dark\) \{(.*?)\n\}`)
	var mediaTokens []map[string]string
	for _, m := range media.FindAllStringSubmatch(css, -1) {
		inner := map[string]string{}
		for _, tok := range tokenPattern.FindAllStringSubmatch(m[1], -1) {
			inner[tok[1]] = tok[2]
		}
		if len(inner) > 0 {
			mediaTokens = append(mediaTokens, inner)
		}
	}
	if len(mediaTokens) < 3 {
		t.Fatalf("found %d prefers-color-scheme blocks, want one per skin plus a11y", len(mediaTokens))
	}

	// Every value declared under the media query must appear identically
	// in some explicit dark block.
	explicit := map[string]map[string]bool{}
	for _, block := range blockPattern.FindAllStringSubmatch(css, -1) {
		if !strings.Contains(block[1], `data-theme="dark"`) {
			continue
		}
		for _, tok := range tokenPattern.FindAllStringSubmatch(block[2], -1) {
			if explicit[tok[1]] == nil {
				explicit[tok[1]] = map[string]bool{}
			}
			explicit[tok[1]][tok[2]] = true
		}
	}

	for _, block := range mediaTokens {
		for name, value := range block {
			if !explicit[name][value] {
				t.Errorf("%s: %s is declared under prefers-color-scheme but no explicit "+
					"dark block declares that value, so choosing dark differs from the OS choosing it",
					name, value)
			}
		}
	}
}

// TestBannerContrast checks every environment and classification banner.
//
// These are the one set of colours that are not tokens, because six are
// published IC/DoD banner-marking colours whose values are prescribed and
// all nine must render identically in every skin and theme. Being exempt
// from the token discipline makes them more important to check, not less:
// there is no shared token whose correction would fix them all at once.
//
// A classification marking that cannot be read is a marking that is not
// displayed, whatever the pixels say.
func TestBannerContrast(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}

	rule := regexp.MustCompile(`(?s)\.(banner-[a-z-]+)\s*\{[^}]*background:\s*(#[0-9a-fA-F]{6})[^}]*color:\s*(#[0-9a-fA-F]{6})`)
	matches := rule.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatal("no banner levels found in app.css")
	}

	seen := map[string]bool{}
	for _, m := range matches {
		name, background, foreground := m[1], m[2], m[3]
		seen[name] = true
		if got := contrastRatio(t, foreground, background); got < 4.5 {
			t.Errorf("%s: %s on %s is %.2f:1, want at least 4.5:1", name, foreground, background, got)
		}
	}

	for _, level := range view.BannerLevelNames() {
		if !seen["banner-"+level] {
			t.Errorf("banner level %q has no styling in app.css", level)
		}
	}
}
