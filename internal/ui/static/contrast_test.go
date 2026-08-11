package static_test

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/static"
)

// This file is the accessibility gate that pays for itself.
//
// Contrast is the single most commonly shipped WCAG failure, and it is
// invisible to every other check in this repository: a palette that fails
// compiles, vets, passes gosec, and renders. Parsing the real stylesheet
// and computing real ratios turns a review-time judgment nobody reliably
// makes into a build failure nobody can miss.
//
// It also caught two genuine defects in the palette this UI replaced:
// #555 empty-state text at 2.65:1, and a 1.38:1 structural border that was
// the sole boundary of every input on the page.

// relativeLuminance implements WCAG 2.x's own definition. It is written
// out rather than pulled in as a dependency because it is eight lines and
// the formula has not changed since 2008.
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

var tokenPattern = regexp.MustCompile(`(--[a-z-]+):\s*(#[0-9a-fA-F]{3,8})\s*;`)

// themeTokens extracts one theme's resolved token set from the stylesheet.
// The dark theme inherits every token the dark block does not restate, so
// the light block is read first and then overlaid, which is exactly how a
// browser resolves them.
func themeTokens(t *testing.T, css, blockMarker string) map[string]string {
	t.Helper()

	extract := func(marker string) map[string]string {
		start := strings.Index(css, marker)
		if start < 0 {
			t.Fatalf("stylesheet has no %q block", marker)
		}
		open := strings.Index(css[start:], "{")
		end := strings.Index(css[start+open:], "}")
		if open < 0 || end < 0 {
			t.Fatalf("the %q block is unterminated", marker)
		}
		body := css[start+open : start+open+end]

		out := map[string]string{}
		for _, m := range tokenPattern.FindAllStringSubmatch(body, -1) {
			out[m[1]] = m[2]
		}
		return out
	}

	tokens := extract(":root {")
	if blockMarker != ":root {" {
		for k, v := range extract(blockMarker) {
			tokens[k] = v
		}
	}
	return tokens
}

// TestTokenContrastMeetsWCAG checks every token against the ratio its
// declared role requires, in both themes.
//
// The roles matter as much as the numbers. SC 1.4.3 asks 4.5:1 of body
// text; SC 1.4.11 asks 3:1 of anything that identifies a user interface
// component, which in this design means the border, because a 4px border
// is the only boundary an input has. A decorative rule is exempt; a
// control's sole boundary is not, and conflating the two is how a design
// that looks high-contrast ships an unusable form.
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
		{"--border", 3.0, "a 4px border is the sole boundary of every input (SC 1.4.11)"},
	}

	for _, theme := range []struct{ name, marker string }{
		{"light", ":root {"},
		{"dark", `:root[data-theme="dark"]`},
	} {
		t.Run(theme.name, func(t *testing.T) {
			tokens := themeTokens(t, css, theme.marker)
			bg, ok := tokens["--bg"]
			if !ok {
				t.Fatal("the theme declares no --bg")
			}

			for _, role := range roles {
				value, ok := tokens[role.token]
				if !ok {
					t.Errorf("%s theme declares no %s", theme.name, role.token)
					continue
				}
				got := contrastRatio(t, value, bg)
				if got < role.want {
					t.Errorf("%s theme: %s (%s) on %s is %.2f:1, want at least %.1f:1 -- %s",
						theme.name, role.token, value, bg, got, role.want, role.why)
				}
			}
		})
	}
}

// TestStatusFillContrast covers the status vocabulary, which is where
// colour-first designs usually fail.
//
// Every fill carries black text in both themes, so these ratios are
// theme-invariant -- which is itself the reason the fills are declared
// once rather than twice. Blue is the single inversion: it takes white
// text, because black on #0000FF is 2.44:1.
func TestStatusFillContrast(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	tokens := themeTokens(t, string(body), ":root {")

	onFill := tokens["--on-fill"]
	if onFill == "" {
		t.Fatal("no --on-fill declared")
	}

	for _, name := range []string{"--fill-ok", "--fill-failed", "--fill-changed", "--fill-skipped"} {
		fill, ok := tokens[name]
		if !ok {
			t.Errorf("no %s declared", name)
			continue
		}
		if got := contrastRatio(t, onFill, fill); got < 4.5 {
			t.Errorf("%s text on %s (%s) is %.2f:1, want at least 4.5:1", onFill, name, fill, got)
		}
	}

	// The inversion, asserted rather than assumed: if someone later
	// "simplifies" the blue fill onto --on-fill, this fails.
	info, onInfo := tokens["--fill-info"], tokens["--on-fill-info"]
	if info == "" || onInfo == "" {
		t.Fatal("the info fill or its text colour is missing")
	}
	if got := contrastRatio(t, onInfo, info); got < 4.5 {
		t.Errorf("%s text on %s (%s) is %.2f:1, want at least 4.5:1", onInfo, "--fill-info", info, got)
	}
	if got := contrastRatio(t, onFill, info); got >= 4.5 {
		t.Errorf("black text now passes on the blue fill (%.2f:1); the documented inversion is stale", got)
	}
}

// The regression this test was written for: pure #0000FF is perfect on
// white and unusable on near-black, so a single link colour cannot serve
// both themes. If someone unifies them, this is what says why not.
func TestLinkColourDiffersBetweenThemes(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	css := string(body)

	light := themeTokens(t, css, ":root {")
	dark := themeTokens(t, css, `:root[data-theme="dark"]`)

	if light["--link"] == dark["--link"] {
		t.Fatalf("both themes use %s for links; blue carries the lowest luminance "+
			"coefficient of any hue, so one value cannot clear 4.5:1 on both white and near-black",
			light["--link"])
	}
	// And the reason, pinned: the light link really does fail on dark.
	if got := contrastRatio(t, light["--link"], dark["--bg"]); got >= 4.5 {
		t.Errorf("the light link colour now passes on the dark background (%.2f:1); "+
			"this test's premise is stale and the themes could share one value", got)
	}
}

// The prefers-color-scheme block must restate every token the explicit
// dark block does. They are two paths to the same theme, and a token in
// one but not the other means the OS-driven dark theme differs from the
// chosen one -- a bug nobody finds, because nobody views the same page
// both ways.
func TestBothDarkPathsDeclareTheSameTokens(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	css := string(body)

	media := themeTokens(t, css, `:root:not([data-theme="light"])`)
	explicit := themeTokens(t, css, `:root[data-theme="dark"]`)

	for name, want := range explicit {
		// Only compare tokens the explicit block actually overrides;
		// everything else is inherited from :root by both paths.
		if !strings.HasPrefix(name, "--") {
			continue
		}
		if got, ok := media[name]; ok && got != want {
			t.Errorf("%s is %s under prefers-color-scheme but %s when chosen explicitly", name, got, want)
		}
	}

	for _, required := range []string{"--bg", "--fg", "--border", "--link", "--shadow"} {
		if _, ok := media[required]; !ok {
			t.Errorf("the prefers-color-scheme dark block does not restate %s", required)
		}
	}
}
