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
// relative paths, a repository-root path, a fragment, a query, a title, and
// the forms that must pass through unchanged.
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
		{"https", "[a](https://example.com/x)", "[a](https://example.com/x)"},
		{"mailto", "[a](mailto:someone@example.com)", "[a](mailto:someone@example.com)"},
		{"anchor", "[a](#top)", "[a](#top)"},
		{"protocol relative", "[a](//example.com/x)", "[a](//example.com/x)"},
		{"empty", "[a]()", "[a]()"},
		{"no link", "plain text (not a link)", "plain text (not a link)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rewriteLinks(tc.in, "docs/releases/v0.3.0.md", notesRepo, notesTag)
			if err != nil || got != tc.want {
				t.Errorf("rewriteLinks(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

// TestRewriteLinks_LeavesCodeFencesAlone checks a link written inside a fenced
// code block is shown as written, while one after the fence is rewritten.
func TestRewriteLinks_LeavesCodeFencesAlone(t *testing.T) {
	in := "```\n[a](../x.md)\n```\n[b](../x.md)"
	want := "```\n[a](../x.md)\n```\n[b](" + notesBase + "docs/x.md)"
	got, err := rewriteLinks(in, "docs/releases/v0.3.0.md", notesRepo, notesTag)
	if err != nil || got != want {
		t.Errorf("rewriteLinks = %q, %v; want %q", got, err, want)
	}
}

// TestRewriteLinks_RefusesALinkOutOfTheRepository checks a path that climbs
// past the repository root is an error naming its line, not a made-up URL.
func TestRewriteLinks_RefusesALinkOutOfTheRepository(t *testing.T) {
	_, err := rewriteLinks("fine\n[a](../../../etc/passwd)", "docs/releases/v0.3.0.md", notesRepo, notesTag)
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "leaves the repository") {
		t.Errorf("a link out of the repository = %v; want an error naming line 2", err)
	}
}

// TestRewriteLinks_TheRealV030Notes runs the file the v0.3.0 release shipped
// through the rewrite and requires that every link left in it is absolute and
// that each rewritten one names the tag, which is the defect the page had.
func TestRewriteLinks_TheRealV030Notes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "releases", "v0.3.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := rewriteLinks(string(raw), "docs/releases/v0.3.0.md", notesRepo, notesTag)
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
// rewritten file, builds nothing, and refuses a bad repository or a missing
// notes file with the right exit code.
func TestRunNotes(t *testing.T) {
	// The notes path is from the repository root, as the workflow passes it, so
	// the test makes a small root of its own and works from there.
	dir := t.TempDir()
	t.Chdir(dir)
	notes := filepath.Join("docs", "releases", "v1.2.3.md")
	if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, []byte("[a](../x.md)\n"), 0o644); err != nil {
		t.Fatal(err)
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
		{"missing notes", []string{"-version", "1.2.3", "-notes", "none.md", "-repo", notesRepo, "-out", out}, 1},
		{"absolute notes path", []string{"-version", "1.2.3", "-notes", filepath.Join(dir, notes), "-repo", notesRepo, "-out", out}, 1},
	} {
		var o, e bytes.Buffer
		if code := run(tc.args, &o, &e); code != tc.want {
			t.Errorf("%s: run = %d, want %d; stderr %q", tc.name, code, tc.want, e.String())
		}
	}
}
