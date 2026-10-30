// This file prepares a release's notes for its GitHub page. The notes are a
// repository file (docs/releases/<tag>.md) whose links are written relative to
// that file, so they read and click through on GitHub's file view. A release
// page is not a file view: a relative link there resolves against the
// repository's root URL, and every one of v0.3.0's pointed nowhere. The
// rewrite below turns each relative link into an absolute one that names the
// tag, so a link in an old release keeps showing the docs as that release
// shipped them.
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

// linkPattern finds an inline Markdown link target: the text inside the
// parentheses of "](target)", with an optional quoted title after it. The
// first group is the target and the second is the title, if any.
var linkPattern = regexp.MustCompile(`\]\(([^)\s]*)(\s+"[^"]*")?\)`)

// rewriteLinks returns body with every relative inline link made absolute.
// notesPath is the repository path of the file body came from, which relative
// links are resolved against; repo is OWNER/NAME and tag is the release tag.
// A link that names a scheme (https:, mailto:), a page anchor, or is empty is
// left alone. A link to a path outside the repository is an error, not a URL
// that points somewhere unintended. Text inside a fenced code block is left
// alone, since a code sample is not a link.
func rewriteLinks(body, notesPath, repo, tag string) (string, error) {
	base := path.Dir(filepath.ToSlash(notesPath))
	lines := strings.Split(body, "\n")
	inFence := false
	for i, line := range lines {
		// A line that opens or closes a fence flips the state and is itself code.
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		var failure error
		lines[i] = linkPattern.ReplaceAllStringFunc(line, func(m string) string {
			sub := linkPattern.FindStringSubmatch(m)
			abs, err := absoluteTarget(sub[1], base, repo, tag)
			if err != nil && failure == nil {
				failure = fmt.Errorf("line %d: %w", i+1, err)
			}
			return "](" + abs + sub[2] + ")"
		})
		if failure != nil {
			return "", failure
		}
	}
	return strings.Join(lines, "\n"), nil
}

// absoluteTarget returns the absolute form of one link target, or the target
// unchanged when it needs no rewrite. base is the directory of the notes file.
func absoluteTarget(target, base, repo, tag string) (string, error) {
	if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "//") || hasScheme(target) {
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
	if resolved == "." {
		resolved = ""
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
// is an error: a release with no notes is not published. repo is already
// checked by the caller. notes must be a path from the repository root, since
// the file's own location is what its links are resolved against.
func writeNotes(notes, repo, version, out string) error {
	if filepath.IsAbs(notes) {
		return fmt.Errorf("-notes %q must be a path from the repository root, such as docs/releases/v0.3.0.md", notes)
	}
	raw, err := os.ReadFile(notes) // #nosec G304 -- the notes path is the operator's own flag
	if err != nil {
		return fmt.Errorf("no release notes: %w", err)
	}
	body, err := rewriteLinks(string(raw), notes, repo, "v"+version)
	if err != nil {
		return fmt.Errorf("%s: %w", notes, err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil { // #nosec G301 -- release output is meant to be read by anyone
		return err
	}
	return os.WriteFile(filepath.Join(out, NotesFile), []byte(body), 0o644) // #nosec G306 -- public release notes
}

// runNotes is the command's notes mode: it writes the rewritten notes and
// reports it, returning 0 on success, 1 when the notes could not be read or
// written, and 2 for a usage error.
func runNotes(notes, repo, version, out string, stdout, stderr io.Writer) int {
	// The repository is spliced into every URL written, so a shape other than
	// OWNER/NAME is refused before anything is read.
	if !repoPattern.MatchString(repo) {
		fmt.Fprintf(stderr, "release: -notes needs -repo OWNER/NAME, got %q\n", repo)
		return 2
	}
	if err := writeNotes(notes, repo, version, out); err != nil {
		fmt.Fprintf(stderr, "release: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s in %s; nothing was published\n", NotesFile, out)
	return 0
}
