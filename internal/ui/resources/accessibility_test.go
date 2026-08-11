package resources_test

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// assertAccessibleDocument is the shared accessibility assertion set, run
// against every page every registered view renders.
//
// This is what makes this phase's central claim true rather than
// aspirational. A resource author writes a field declaration and a
// projector; they never write markup. So the markup only has to be right
// once -- and this is what holds it right, on every view, including the ones
// nobody has written yet.
//
// It covers the mechanical half of WCAG. Roughly forty per cent of the
// criteria can be checked by a machine at all, and the rest -- does focus
// order make sense, does the error text explain anything, is the reading
// order the visual order -- needs a person. Deleting the npm toolchain took
// axe-core and Lighthouse with it, so this is deliberately not sold as
// complete; docs/ carries the manual script for the other half.
func assertAccessibleDocument(t *testing.T, body string) {
	t.Helper()

	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("the page is not parseable HTML: %v", err)
	}

	var (
		h1s         int
		mains       int
		titles      int
		titleText   string
		lang        string
		ids         = map[string]int{}
		describedBy []string
		labelFor    = map[string]bool{}
		controls    []control
		firstFocus  string
		sawFocus    bool
	)

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			attrs := attrMap(n)

			if id := attrs["id"]; id != "" {
				ids[id]++
			}
			if ref := attrs["aria-describedby"]; ref != "" {
				describedBy = append(describedBy, strings.Fields(ref)...)
			}
			if !sawFocus && isFocusable(n.Data, attrs) {
				firstFocus = attrs["class"]
				sawFocus = true
			}

			switch n.Data {
			case "html":
				lang = attrs["lang"]
			case "title":
				titles++
				titleText = textOf(n)
			case "h1":
				h1s++
			case "main":
				mains++
			case "label":
				if f := attrs["for"]; f != "" {
					labelFor[f] = true
				}
			case "input", "select", "textarea":
				if attrs["type"] != "hidden" {
					controls = append(controls, control{tag: n.Data, attrs: attrs})
				}
			case "a":
				if attrs["href"] != "" && strings.TrimSpace(textOf(n)) == "" &&
					attrs["aria-label"] == "" && attrs["title"] == "" {
					t.Error("a link has no accessible name, so it is announced only as its URL")
				}
			case "button":
				if strings.TrimSpace(textOf(n)) == "" && attrs["aria-label"] == "" {
					t.Error("a button has no accessible name")
				}
			case "img":
				if _, ok := attrs["alt"]; !ok {
					t.Error("an image has no alt attribute, so a screen reader announces its filename")
				}
			}

			// A positive tabindex overrides the document's own order for
			// the whole page, not just for that element, which is why it
			// is banned outright rather than discouraged.
			if tab := attrs["tabindex"]; tab != "" && tab != "0" && tab != "-1" {
				t.Errorf("tabindex=%q rewrites the tab order of the entire page", tab)
			}

			// The CSP carries no 'unsafe-inline'; an inline style or a
			// script with a body would be silently dropped by the browser
			// rather than failing loudly here.
			if _, ok := attrs["style"]; ok {
				t.Error("an element carries an inline style attribute, which this page's " +
					"content security policy forbids")
			}
			if n.Data == "script" && attrs["src"] == "" && strings.TrimSpace(textOf(n)) != "" {
				t.Error("an inline script is present, which this page's content security policy forbids")
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if lang == "" {
		t.Error("<html> has no lang attribute, so a screen reader guesses the pronunciation of every word")
	}
	if titles != 1 || strings.TrimSpace(titleText) == "" {
		t.Errorf("the page has %d non-empty <title> elements, want exactly 1", titles)
	}
	if h1s != 1 {
		t.Errorf("the page has %d <h1> elements, want exactly 1", h1s)
	}
	if mains != 1 {
		t.Errorf("the page has %d <main> elements, want exactly 1", mains)
	}

	// The skip link must be the first thing a keyboard user reaches.
	// Without it they tab the entire sidebar before the content, on every
	// navigation, forever.
	if !strings.Contains(firstFocus, "skip-link") {
		t.Errorf("the first focusable element has class %q, want the skip link", firstFocus)
	}

	for id, count := range ids {
		if count > 1 {
			t.Errorf("id %q appears %d times; a duplicate id makes every reference to it ambiguous", id, count)
		}
	}

	// A dangling aria-describedby is worse than none: the reference is
	// announced and resolves to nothing.
	for _, ref := range describedBy {
		if ids[ref] == 0 {
			t.Errorf("aria-describedby points at %q, which is not an id on this page", ref)
		}
	}

	for _, c := range controls {
		if c.attrs["aria-label"] != "" || c.attrs["aria-labelledby"] != "" {
			continue
		}
		id := c.attrs["id"]
		if id == "" || !labelFor[id] {
			t.Errorf("<%s name=%q> has no label, so nothing announces what it is",
				c.tag, c.attrs["name"])
		}
	}
}

type control struct {
	tag   string
	attrs map[string]string
}

func attrMap(n *html.Node) map[string]string {
	out := make(map[string]string, len(n.Attr))
	for _, a := range n.Attr {
		out[a.Key] = a.Val
	}
	return out
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// isFocusable reports whether an element is in the natural tab order.
func isFocusable(tag string, attrs map[string]string) bool {
	if tab := attrs["tabindex"]; tab == "-1" {
		return false
	}
	switch tag {
	case "a":
		return attrs["href"] != ""
	case "button", "select", "textarea", "summary":
		return true
	case "input":
		return attrs["type"] != "hidden"
	default:
		return attrs["tabindex"] != ""
	}
}

// TestAccessibilityGateCatchesRealFailures is the mutation test for the
// assertion set above.
//
// A gate nobody has watched fail is a gate nobody knows works. Each case
// below is a real defect fed through the same parser the suite uses, and
// each must be caught -- otherwise every passing run above means nothing.
func TestAccessibilityGateCatchesRealFailures(t *testing.T) {
	const shell = `<html lang="en"><head><title>T</title></head><body>` +
		`<a class="skip-link" href="#main">Skip</a><main id="main">%s</main></body></html>`

	for _, tc := range []struct {
		name string
		body string
	}{
		{"no h1", ""},
		{"two h1s", "<h1>A</h1><h1>B</h1>"},
		{"unlabelled input", `<h1>A</h1><input type="text" name="x" id="x">`},
		{"duplicate id", `<h1>A</h1><p id="dup"></p><p id="dup"></p>`},
		{"dangling describedby", `<h1>A</h1><p aria-describedby="ghost">x</p>`},
		{"positive tabindex", `<h1>A</h1><div tabindex="3">x</div>`},
		{"inline style", `<h1>A</h1><p style="color:red">x</p>`},
		{"inline script", `<h1>A</h1><script>alert(1)</script>`},
		{"image with no alt", `<h1>A</h1><img src="x.png">`},
		{"nameless button", `<h1>A</h1><button></button>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &testing.T{}
			assertAccessibleDocument(probe, strings.ReplaceAll(shell, "%s", tc.body))
			if !probe.Failed() {
				t.Errorf("the accessibility gate did not catch %q, so it would not catch it "+
					"in a real view either", tc.name)
			}
		})
	}
}
