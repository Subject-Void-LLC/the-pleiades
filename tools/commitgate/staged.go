// Package main: reading what a commit is actually about to record.
//
// Every check here reads the INDEX, never the working tree. The two
// differ whenever a file is partly staged (git add -p, or an edit made
// after git add), and checking the working tree in that case would pass
// a commit whose recorded content breaks a rule, or fail one whose
// recorded content is clean. Both are worse than not checking at all,
// because both teach a reader that the gate's answer is about something
// other than the commit.
package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// emptyTreeHash is git's own hash for an empty tree object. It is the
// same value in every repository, and diffing the index against it is
// how the checks below see a repository's very first commit, which has
// no HEAD to diff against.
const emptyTreeHash = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// changeKind is how a staged path changed, narrowed to the distinction
// the checks actually make.
type changeKind int

const (
	// changeAdded means this commit creates the file. Some rules apply
	// only here, because they ask for something an author can still do
	// cheaply while writing a file and expensively afterwards.
	changeAdded changeKind = iota

	// changeModified means the file already existed. A rename counts as
	// modified: its content is what matters, and treating a moved file as
	// new would demand a fresh file docstring from a file that has one.
	changeModified
)

// stagedFile is one path this commit records, with how it changed.
type stagedFile struct {
	// Path is repo-relative, as git reports it.
	Path string

	// Kind is how the path changed.
	Kind changeKind
}

// addedLine is one line this commit introduces, with where it lands.
type addedLine struct {
	// Number is the 1-indexed line number in the file as this commit
	// records it, so a reported location opens the right line in an
	// editor rather than the right line of a diff.
	Number int

	// Text is the line without its leading diff marker.
	Text string
}

// git runs a git command from the current directory and returns its
// standard output.
//
// It returns stderr inside the error, because a git plumbing failure
// with the message stripped off ("exit status 128") is indistinguishable
// from every other git plumbing failure.
func git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...) // #nosec G204 -- every caller passes literal arguments plus paths git itself just reported; nothing here is attacker-controlled input
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// baseRef returns what the index should be diffed against: HEAD when
// there is one, and the empty tree in a repository whose first commit
// this is.
func baseRef() string {
	if _, err := git("rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		return emptyTreeHash
	}
	return "HEAD"
}

// stagedFiles lists every path this commit adds, modifies, copies or
// renames.
//
// Deletions are excluded: there is no content left to check, and every
// rule here is about content. The -z form is not a detail to simplify
// away, since a path may contain a newline and the line-oriented form
// quotes such a path instead of reporting it.
func stagedFiles() ([]stagedFile, error) {
	out, err := git("diff", "--cached", "--name-status", "-z", "--diff-filter=ACMR", baseRef())
	if err != nil {
		return nil, err
	}

	// -z output is a flat NUL-separated stream, not one record per entry:
	// a rename or copy spends THREE fields (status, old path, new path)
	// while every other status spends two. So this walks the stream with
	// an index rather than ranging over pairs.
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	var files []stagedFile
	for i := 0; i < len(fields); i++ {
		status := fields[i]
		if status == "" {
			continue
		}
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			// Skip the source path and report the destination.
			if i+2 >= len(fields) {
				return nil, fmt.Errorf("truncated rename record in git diff output near %q", status)
			}
			files = append(files, stagedFile{Path: fields[i+2], Kind: changeModified})
			i += 2
			continue
		}
		if i+1 >= len(fields) {
			return nil, fmt.Errorf("truncated record in git diff output near %q", status)
		}
		kind := changeModified
		if status == "A" {
			kind = changeAdded
		}
		files = append(files, stagedFile{Path: fields[i+1], Kind: kind})
		i++
	}
	return files, nil
}

// stagedContent returns a path's content exactly as this commit records
// it.
func stagedContent(path string) ([]byte, error) {
	return git("show", ":"+path)
}

// addedLines returns the lines this commit introduces into path, each
// carrying its line number in the recorded file.
//
// It parses a zero-context diff rather than comparing the two blobs,
// because an unchanged line that happens to break a rule is not this
// commit's to answer for. A gate that reported it would fire on every
// commit that touched the file until someone fixed history, which is how
// a gate becomes something people pass with --no-verify.
func addedLines(path string) ([]addedLine, error) {
	out, err := git("diff", "--cached", "-U0", baseRef(), "--", path)
	if err != nil {
		return nil, err
	}
	return parseAddedLines(string(out)), nil
}

// parseAddedLines walks a zero-context unified diff and returns its
// added lines with their line numbers in the new file.
//
// Split out from addedLines so the parsing is testable against literal
// diff text, with no repository to set up first.
func parseAddedLines(diff string) []addedLine {
	var added []addedLine
	next := 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			next = parseHunkStart(line)
		case strings.HasPrefix(line, "+++"):
			// The file header, which begins with "+" and is not content.
			continue
		case strings.HasPrefix(line, "+"):
			if next > 0 {
				added = append(added, addedLine{Number: next, Text: line[1:]})
				next++
			}
		}
	}
	return added
}

// parseHunkStart reads the new-file starting line out of a hunk header
// of the form "@@ -12,0 +13,4 @@", returning zero if it cannot.
func parseHunkStart(header string) int {
	plus := strings.Index(header, "+")
	if plus < 0 {
		return 0
	}
	rest := header[plus+1:]
	end := strings.IndexAny(rest, ", @")
	if end < 0 {
		return 0
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return n
}

// stagedEntry is one staged path together with the content this commit
// records for it.
//
// The content is read once, here, rather than by each rule that wants
// it. Two rules reading the same path separately could disagree if the
// working tree changed between them, and a gate whose answer depends on
// timing is worse than no gate.
type stagedEntry struct {
	stagedFile

	// Content is the path's bytes exactly as the index holds them.
	Content []byte
}

// generatedMarker is the comment Go's own convention requires at the top
// of a generated file, and which ent, this repository's generators and
// every other well behaved tool emit.
//
// Matching the marker rather than the path is what keeps this correct as
// the tree moves. internal/ent holds four hand-written files beside its
// generated ones (generate.go, migrate/apply.go, migrate/parity_test.go
// and migrate/gen/main.go), so any path prefix wide enough to catch the
// generated code also silently excuses those from every rule.
var generatedMarker = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// isGeneratedSource reports whether content opens with the generated-code
// marker.
//
// Go's convention places the marker before the package clause, so only
// the head of the file is scanned. Reading further would start matching
// a comment that merely quotes the convention, which this repository's
// own tooling does.
func isGeneratedSource(content []byte) bool {
	lines := strings.SplitN(string(content), "\n", generatedMarkerScanLines+1)
	if len(lines) > generatedMarkerScanLines {
		lines = lines[:generatedMarkerScanLines]
	}
	for _, line := range lines {
		if generatedMarker.MatchString(strings.TrimRight(line, "\r")) {
			return true
		}
	}
	return false
}

// generatedMarkerScanLines bounds how far into a file the marker is
// looked for. A build constraint, a copyright header and a blank line
// can all legally precede it; a screen of them cannot.
const generatedMarkerScanLines = 10
