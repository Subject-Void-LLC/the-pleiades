// Package main tests the release notes rewrite: which links become absolute,
// which are left alone, what is refused, and that the real v0.3.0 notes come
// out with no link a release page cannot follow.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// notesRepo and notesTag are the repository and tag the rewrite tests use.
const (
	notesRepo = "Subject-Void-LLC/the-pleiades"
	notesTag  = "v0.3.0"
	notesBase = "https://github.com/" + notesRepo + "/blob/" + notesTag + "/"
)

// TestRewriteLinks checks each spelling of a link: parent, current and bare
// relative paths, a repository-root path, a fragment, a query, a title, the
// repository root itself, and the forms that must pass through unchanged,
// including a link inside an inline code span.
func TestRewriteLinks(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"parent", "[a](../01-start-here.md)", "[a](" + notesBase + "docs/01-start-here.md)"},
		{"current", "[a](./other.md)", "[a](" + notesBase + "docs/releases/other.md)"},
		{"bare", "[a](other.md)", "[a](" + notesBase + "docs/releases/other.md)"},
		{"two up", "[a](../../README.md)", "[a](" + notesBase + "README.md)"},
		{"root path", "[a](/docs/x.md)", "[a](" + notesBase + "docs/x.md)"},
		{"fragment", "[a](../x.md#part)", "[a](" + notesBase + "docs/x.md#part)"},
		{"query", "[a](../x.md?plain=1)", "[a](" + notesBase + "docs/x.md?plain=1)"},
		{"title kept", `[a](../x.md "the title")`, `[a](` + notesBase + `docs/x.md "the title")`},
		{"two on a line", "[a](../x.md) and [b](../y.md)", "[a](" + notesBase + "docs/x.md) and [b](" + notesBase + "docs/y.md)"},
		{"repository root", "[a](../..)", "[a](https://github.com/" + notesRepo + "/tree/" + notesTag + ")"},
		{"code in the text", "[`a`](../x.md)", "[`a`](" + notesBase + "docs/x.md)"},
		{"inline code span", "`[a](../x.md)` and [b](../x.md)", "`[a](../x.md)` and [b](" + notesBase + "docs/x.md)"},
		{"https", "[a](https://example.com/x)", "[a](https://example.com/x)"},
		{"absolute image", "![a](https://example.com/x.png)", "![a](https://example.com/x.png)"},
		{"mailto", "[a](mailto:someone@example.com)", "[a](mailto:someone@example.com)"},
		{"anchor", "[a](#top)", "[a](#top)"},
		{"protocol relative", "[a](//example.com/x)", "[a](//example.com/x)"},
		{"empty", "[a]()", "[a]()"},
		{"no link", "plain text (not a link)", "plain text (not a link)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rewriteLinks(tc.in, "docs/releases/v0.3.0.md", notesRepo, notesTag, nil)
			if err != nil || got != tc.want {
				t.Errorf("rewriteLinks(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

// TestRewriteLinks_LeavesCodeFencesAlone checks a link written inside a fenced
// code block is shown as written, while one after the fence is rewritten, for
// a backtick fence, a tilde fence, and a longer fence around a shorter sample.
func TestRewriteLinks_LeavesCodeFencesAlone(t *testing.T) {
	after := "[b](../x.md)"
	rewrittenAfter := "[b](" + notesBase + "docs/x.md)"
	for name, fenced := range map[string]string{
		"backticks":     "```\n[a](../x.md)\n```",
		"tildes":        "~~~\n[a](../x.md)\n~~~",
		"longer fence":  "````\n```\n[a](../x.md)\n```\n[c](../x.md)\n````",
		"with language": "```go\n[a](../x.md)\n```",
	} {
		got, err := rewriteLinks(fenced+"\n"+after, "docs/releases/v0.3.0.md", notesRepo, notesTag, nil)
		if want := fenced + "\n" + rewrittenAfter; err != nil || got != want {
			t.Errorf("%s: rewriteLinks = %q, %v; want %q", name, got, err, want)
		}
	}
}

// TestRewriteLinks_RefusesWhatItCannotRewrite checks each form the rewrite does
// not handle is an error naming its line when its path is relative, and is
// accepted when the path is absolute, so a release stops where a page would
// have shown a dead link.
func TestRewriteLinks_RefusesWhatItCannotRewrite(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"out of the repository", "[a](../../../etc/passwd)"},
		{"angle brackets", "[a](<../x.md>)"},
		{"single-quoted title", "[a](../x.md 'T')"},
		{"parenthesized title", "[a](../x.md (T))"},
		{"image", "![a](../img.png)"},
		{"reference definition", "[id]: ../x.md"},
		{"html href", `<a href="../x.md">a</a>`},
		{"html src", `<img src='../img.png'>`},
	} {
		_, err := rewriteLinks("fine\n"+tc.in, "docs/releases/v0.3.0.md", notesRepo, notesTag, nil)
		if err == nil || !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%s: %q gave %v; want an error naming line 2", tc.name, tc.in, err)
		}
	}
	for _, in := range []string{
		`<a href="https://example.com/x">a</a>`,
		"[id]: https://example.com/x",
	} {
		if got, err := rewriteLinks(in, "docs/releases/v0.3.0.md", notesRepo, notesTag, nil); err != nil || got != in {
			t.Errorf("an absolute %q = %q, %v; want it unchanged", in, got, err)
		}
	}
}

// TestRewriteLinks_ChecksTheTargetExists checks a link to a file the checkout
// at the tag does not hold is an error naming the path, so a stale link stops
// the release and does not publish a 404.
func TestRewriteLinks_ChecksTheTargetExists(t *testing.T) {
	has := func(p string) bool { return p == "docs/x.md" }
	if _, err := rewriteLinks("[a](../x.md)", "docs/releases/v0.3.0.md", notesRepo, notesTag, has); err != nil {
		t.Errorf("a link to a file that exists = %v", err)
	}
	_, err := rewriteLinks("[a](../gone.md)", "docs/releases/v0.3.0.md", notesRepo, notesTag, has)
	if err == nil || !strings.Contains(err.Error(), "docs/gone.md") {
		t.Errorf("a link to a missing file = %v; want an error naming docs/gone.md", err)
	}
}

// TestRewriteLinks_TheRealV030Notes runs the file the v0.3.0 release shipped
// through the rewrite, requiring that every link in it names a file that exists
// (looked up from the repository root, two directories up), that every link
// left is absolute, and that each rewritten one names the tag: the defect the
// page had.
func TestRewriteLinks_TheRealV030Notes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "releases", "v0.3.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	exists := func(p string) bool {
		_, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(p)))
		return err == nil
	}
	got, err := rewriteLinks(string(raw), "docs/releases/v0.3.0.md", notesRepo, notesTag, exists)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == got {
		t.Fatal("the notes came out unchanged, but they hold relative links")
	}
	for _, m := range linkPattern.FindAllStringSubmatch(got, -1) {
		target := m[1]
		if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		if !strings.HasPrefix(target, "https://") {
			t.Errorf("link %q is not absolute after the rewrite", target)
		}
		if strings.HasPrefix(target, "https://github.com/"+notesRepo) && !strings.HasPrefix(target, notesBase) {
			t.Errorf("link %q does not name the tag %s", target, notesTag)
		}
	}
	// No link may still climb out of the file's directory.
	if regexp.MustCompile(`\]\(\.\./`).MatchString(got) {
		t.Error("a ../ link survived the rewrite")
	}
}

// TestRunNotes checks notes mode end to end through run: it writes the
// rewritten file, builds nothing, and refuses a bad repository, a bad version,
// an absolute notes path, a missing notes file and a link to a missing file with
// the right exit code.
func TestRunNotes(t *testing.T) {
	// The notes path is from the repository root, as the workflow passes it, so
	// the test makes a small root of its own and works from there.
	dir := t.TempDir()
	t.Chdir(dir)
	notes := filepath.Join("docs", "releases", "v1.2.3.md")
	for p, body := range map[string]string{
		notes:                         "[a](../x.md)\n",
		filepath.Join("docs", "x.md"): "x\n",
		filepath.Join("docs", "releases", "v1.2.4.md"): "[a](../gone.md)\n",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := "dist"

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-version", "1.2.3", "-notes", notes, "-repo", notesRepo, "-out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(out, NotesFile))
	if err != nil {
		t.Fatal(err)
	}
	if want := "[a](https://github.com/" + notesRepo + "/blob/v1.2.3/docs/x.md)\n"; string(got) != want {
		t.Errorf("notes = %q; want %q", got, want)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 1 {
		t.Errorf("notes mode wrote %d files, want only %s", len(entries), NotesFile)
	}

	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"no repo", []string{"-version", "1.2.3", "-notes", notes, "-out", out}, 2},
		{"bad repo", []string{"-version", "1.2.3", "-notes", notes, "-repo", "no-slash", "-out", out}, 2},
		{"repo with a path", []string{"-version", "1.2.3", "-notes", notes, "-repo", "a/b/../c", "-out", out}, 2},
		{"no version", []string{"-notes", notes, "-repo", notesRepo, "-out", out}, 2},
		{"absolute notes path", []string{"-version", "1.2.3", "-notes", filepath.Join(dir, notes), "-repo", notesRepo, "-out", out}, 2},
		{"missing notes", []string{"-version", "1.2.3", "-notes", "none.md", "-repo", notesRepo, "-out", out}, 1},
		{"link to a missing file", []string{"-version", "1.2.4", "-notes", filepath.Join("docs", "releases", "v1.2.4.md"), "-repo", notesRepo, "-out", out}, 1},
	} {
		var o, e bytes.Buffer
		if code := run(tc.args, &o, &e); code != tc.want {
			t.Errorf("%s: run = %d, want %d; stderr %q", tc.name, code, tc.want, e.String())
		}
	}
}

// TestRunNotes_RefusesAVersionALinkCannotCarry checks a version holding
// characters that would end a link or climb a path is a usage error before
// anything is read, whatever the tag name allowed.
func TestRunNotes_RefusesAVersionALinkCannotCarry(t *testing.T) {
	for _, v := range []string{"1.2.3-x)[t](http://evil)", "1.2.3-x/../..", "1.2.3 x", "1.2.3#x"} {
		var o, e bytes.Buffer
		if code := runNotes("docs/releases/v1.2.3.md", notesRepo, v, t.TempDir(), &o, &e); code != 2 {
			t.Errorf("version %q: runNotes = %d, want 2; stderr %q", v, code, e.String())
		}
	}
}
