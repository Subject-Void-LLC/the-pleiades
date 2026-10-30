// This file prepares a release's notes for its GitHub page. The notes are a
// repository file (docs/releases/<tag>.md) whose links are written relative to
// that file, so they read and click through on GitHub's file view. A release
// page is not a file view: a relative link there resolves against the
// repository's root URL, and every one of v0.3.0's pointed nowhere. The
// rewrite below turns each relative link into an absolute one that names the
// tag, so a link in an old release keeps showing the docs as that release
// shipped them.
//
// The rewrite handles inline links, the form the notes use. A form it does not
// handle (an image, a reference definition, raw HTML, an angle-bracket target
// or a single-quoted title) with a relative path is an error and not a link
// left pointing nowhere, so a release stops where a page would have 404ed.
package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// NotesFile is the name of the rewritten release notes written beside the
// archives, which the tag workflow publishes as the release body and does not
// upload as an asset.
const NotesFile = "RELEASE_NOTES.md"

// repoPattern is the shape of a GitHub repository, OWNER/NAME. It is checked
// because the value is spliced into a URL.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._-]+$`)

// versionPattern is the characters a release version may hold in a link and a
// file name: letters, digits, dots, plus and hyphen. A tag name may hold far
// more (a slash, a parenthesis, a space), and the version is spliced into both
// a URL and a path.
var versionPattern = regexp.MustCompile(`^[0-9A-Za-z.+-]+$`)

// linkPattern finds an inline Markdown link target: the text inside the
// parentheses of "](target)", with an optional double-quoted title after it.
// The first group is the target and the second is the title, if any.
var linkPattern = regexp.MustCompile(`\]\(([^)\s]*)(\s+"[^"]*")?\)`)

// Forms the rewrite does not handle. Each is checked on a relative path only,
// since an absolute one needs nothing.
var (
	// imagePattern finds an image, whose target would need a raw file URL and
	// not the page URL a link gets.
	imagePattern = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]*)`)
	// refDefPattern finds a reference-style link definition, [id]: target.
	refDefPattern = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s*(\S+)`)
	// htmlRefPattern finds the href or src of raw HTML.
	htmlRefPattern = regexp.MustCompile(`(?i)\b(?:href|src)\s*=\s*["']([^"']*)["']`)
	// oddLinkPattern finds an inline link written with an angle-bracket target,
	// or with a single-quoted or parenthesized title.
	oddLinkPattern = regexp.MustCompile(`\]\((?:<|[^)\s]*\s+'|[^)\s]*\s+\()`)
)

// rewriteLinks returns body with every relative inline link made absolute.
// notesPath is the repository path of the file body came from, which relative
// links are resolved against; repo is OWNER/NAME and tag is the release tag.
// exists, when not nil, says whether a repository path is a file or directory
// at the tag; a link to one that is not is an error. A link that names a scheme
// (https:, mailto:), a page anchor, or is empty is left alone. A link to a path
// outside the repository, or in a form this rewrite does not handle, is an
// error. Fenced code blocks and inline code spans are left alone, since a code
// sample is not a link.
func rewriteLinks(body, notesPath, repo, tag string, exists func(string) bool) (string, error) {
	base := path.Dir(filepath.ToSlash(notesPath))
	lines := strings.Split(body, "\n")
	var fence string // the opening fence's marker while inside a fenced block
	for i, line := range lines {
		if fence != "" {
			if closesFence(line, fence) {
				fence = ""
			}
			continue
		}
		if m := opensFence(line); m != "" {
			fence = m
			continue
		}
		rewritten, err := rewriteLine(line, base, repo, tag, exists)
		if err != nil {
			return "", fmt.Errorf("line %d: %w", i+1, err)
		}
		lines[i] = rewritten
	}
	return strings.Join(lines, "\n"), nil
}

// opensFence returns the marker (three or more backticks, or tildes) when line
// opens a fenced code block, and "" when it does not.
func opensFence(line string) string {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 || len(t) < 3 || (t[0] != '`' && t[0] != '~') {
		return ""
	}
	n := 0
	for n < len(t) && t[n] == t[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return t[:n]
}

// closesFence reports whether line closes a block opened with marker: a run of
// the same character at least as long, and nothing else on the line.
func closesFence(line, marker string) bool {
	t := strings.TrimSpace(line)
	return len(t) >= len(marker) && strings.Trim(t, marker[:1]) == ""
}

// rewriteLine rewrites the links on one line outside a fence. Text between
// backticks is code and is left as written, so the line is cut at each
// backtick and only the even-numbered pieces, the ones outside a span, are
// rewritten.
func rewriteLine(line, base, repo, tag string, exists func(string) bool) (string, error) {
	// A reference definition is the whole line, so it is judged before the cut.
	if m := refDefPattern.FindStringSubmatch(line); m != nil && relative(m[1]) {
		return "", fmt.Errorf("a reference-style link to %q is not handled; write it inline", m[1])
	}
	pieces := strings.Split(line, "`")
	for j := 0; j < len(pieces); j += 2 {
		if err := refuseUnhandled(pieces[j]); err != nil {
			return "", err
		}
		var failure error
		pieces[j] = linkPattern.ReplaceAllStringFunc(pieces[j], func(m string) string {
			sub := linkPattern.FindStringSubmatch(m)
			abs, err := absoluteTarget(sub[1], base, repo, tag, exists)
			if err != nil && failure == nil {
				failure = err
			}
			return "](" + abs + sub[2] + ")"
		})
		if failure != nil {
			return "", failure
		}
	}
	return strings.Join(pieces, "`"), nil
}

// refuseUnhandled returns an error when text holds a relative link in a form
// the rewrite does not handle: an image, raw HTML, or an inline link with an
// angle-bracket target or an unsupported title.
func refuseUnhandled(text string) error {
	if oddLinkPattern.MatchString(text) {
		return fmt.Errorf("a link with an angle-bracket target or a single-quoted or parenthesized title is not handled; use a plain target and a double-quoted title")
	}
	for _, m := range imagePattern.FindAllStringSubmatch(text, -1) {
		if relative(m[1]) {
			return fmt.Errorf("the image %q has a relative path, which a release page cannot show; use an absolute URL", m[1])
		}
	}
	for _, m := range htmlRefPattern.FindAllStringSubmatch(text, -1) {
		if relative(m[1]) {
			return fmt.Errorf("raw HTML points at %q, a relative path; use an inline Markdown link or an absolute URL", m[1])
		}
	}
	return nil
}

// relative reports whether a link target is a path that a release page would
// resolve against the wrong base: not empty, not an anchor, not protocol
// relative and not carrying a scheme.
func relative(target string) bool {
	return target != "" && !strings.HasPrefix(target, "#") && !strings.HasPrefix(target, "//") && !hasScheme(target)
}

// absoluteTarget returns the absolute form of one link target, or the target
// unchanged when it needs no rewrite. base is the directory of the notes file.
func absoluteTarget(target, base, repo, tag string, exists func(string) bool) (string, error) {
	if !relative(target) {
		return target, nil
	}
	// The path ends where a query or a fragment starts; both ride along as written.
	file, rest := target, ""
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		file, rest = target[:i], target[i:]
	}
	var resolved string
	if strings.HasPrefix(file, "/") {
		// A leading slash means the repository root, as it does on GitHub's file view.
		resolved = path.Clean(strings.TrimPrefix(file, "/"))
	} else {
		resolved = path.Join(base, file)
	}
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("link %q leaves the repository", target)
	}
	// The repository root has no blob page; its page at the tag is the tree.
	if resolved == "." {
		return "https://github.com/" + repo + "/tree/" + tag + rest, nil
	}
	if exists != nil && !exists(resolved) {
		return "", fmt.Errorf("link %q names %s, which is not in the repository at %s", target, resolved, tag)
	}
	return "https://github.com/" + repo + "/blob/" + tag + "/" + resolved + rest, nil
}

// hasScheme reports whether target begins with a URL scheme such as https: or
// mailto:, meaning it is already absolute. A colon after a slash belongs to a
// path, not a scheme.
func hasScheme(target string) bool {
	colon := strings.Index(target, ":")
	if colon <= 0 {
		return false
	}
	return !strings.ContainsAny(target[:colon], "/#?")
}

// writeNotes reads the notes file for a release, rewrites its links for the
// release page and writes the result to NotesFile in out. A missing notes file
// is an error: a release with no notes is not published. repo and version are
// already checked by the caller. notes must be a path from the repository root,
// the directory the command runs in, since the file's own location is what its
// links are resolved against and what each link's target is looked up under.
func writeNotes(notes, repo, version, out string) error {
	raw, err := os.ReadFile(notes) // #nosec G304 -- the notes path is the operator's own flag
	if err != nil {
		return fmt.Errorf("no release notes: %w", err)
	}
	exists := func(p string) bool {
		_, err := os.Stat(filepath.FromSlash(p))
		return err == nil
	}
	body, err := rewriteLinks(string(raw), notes, repo, "v"+version, exists)
	if err != nil {
		return fmt.Errorf("%s: %w", notes, err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil { // #nosec G301 -- release output is meant to be read by anyone
		return err
	}
	return os.WriteFile(filepath.Join(out, NotesFile), []byte(body), 0o644) // #nosec G306 -- public release notes
}

// runNotes is the command's notes mode: it writes the rewritten notes and
// reports it, returning 0 on success, 1 when the notes could not be read,
// rewritten or written, and 2 for a usage error.
func runNotes(notes, repo, version, out string, stdout, stderr io.Writer) int {
	// The repository and the version are spliced into every URL written, and
	// the version into a path, so a shape other than the expected one is
	// refused before anything is read.
	if !repoPattern.MatchString(repo) {
		fmt.Fprintf(stderr, "release: -notes needs -repo OWNER/NAME, got %q\n", repo)
		return 2
	}
	if !versionPattern.MatchString(version) {
		fmt.Fprintf(stderr, "release: -version %q holds characters a link cannot carry\n", version)
		return 2
	}
	if filepath.IsAbs(notes) {
		fmt.Fprintf(stderr, "release: -notes %q must be a path from the repository root, such as docs/releases/v0.3.0.md\n", notes)
		return 2
	}
	if err := writeNotes(notes, repo, version, out); err != nil {
		fmt.Fprintf(stderr, "release: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s in %s; nothing was published\n", NotesFile, out)
	return 0
}
