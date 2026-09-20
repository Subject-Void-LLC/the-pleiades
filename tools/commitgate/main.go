// Command commitgate refuses a commit that breaks a rule .AGENTS/AGENTS.md
// states and a machine can check.
//
// It is the commit-time half of a gate this repository already had at push
// time. `make push-gate` builds, vets, formats, scans and tests, and takes
// minutes because several packages dial real ephemeral Docker containers;
// .githooks/pre-push does not run it, it reads back the receipt that run
// left behind (see tools/gatereceipt). That is the right gate
// for "does this work" and the wrong one for "is this written the way this
// repository writes things": by the time it runs, the mistake is already
// three commits back and fixing it means rewriting history.
//
// So this runs in well under a second, reads only what the commit is about
// to record, and checks the rules that are cheap and absolute:
//
//   - No em dash, anywhere, in any line the commit adds (text.go).
//   - gofmt with no exceptions, on the staged bytes (gofile.go).
//   - A docstring at the top of every Go file the commit adds (gofile.go).
//   - An ent schema edit ships with its regenerated code, and a new entity
//     ships with a migration for both dialects (generated.go).
//   - Conventional commit subjects, and no trailer crediting a model as an
//     author (message.go).
//
// Some rules AGENTS.md states softly, or that no static check can settle,
// warn instead: the ~300-line cap on logic files, error messages that open
// with a capital or close with punctuation, and a generated tree that
// might legitimately be unchanged. A warning prints and the commit
// proceeds. The split matters: a gate that fails on a judgement call is
// one people learn to pass with --no-verify, and then it stops catching
// the things it was actually right about.
//
// # What it deliberately does not do
//
// It does not build, vet, test, or run gosec. `make push-gate` does all of
// that before a push, and duplicating it here would make every commit cost
// minutes. It cannot tell whether a doc comment is TRUE, whether a test is
// representative under RULE 0, or whether a lesson was recorded. Those are
// the rules this repository cares most about and none of them is
// checkable; a reader should not mistake a green run here for having
// followed AGENTS.md.
//
// # Usage
//
//	commitgate           # pre-commit: check the staged content
//	commitgate -msg FILE # commit-msg: check the message in FILE
//
// Both are wired by .githooks/pre-commit and .githooks/commit-msg, enabled
// once per clone by `make hooks`. Skip a single commit with
// `git commit --no-verify`.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	msgFile := flag.String("msg", "", "path to the commit message file (commit-msg mode); omit to check staged content")
	flag.Parse()

	findings, err := run(*msgFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "commitgate:", err)
		// Exit 1 rather than 0: a gate that cannot run has not passed,
		// and silently allowing the commit would make every future
		// failure of this tool invisible.
		os.Exit(1)
	}

	if report(os.Stderr, findings) {
		fmt.Fprintln(os.Stderr, "commitgate: commit refused; fix the FAIL lines above, or use --no-verify if you are certain")
		os.Exit(1)
	}
}

// run picks a mode and returns what it found.
func run(msgFile string) ([]Finding, error) {
	if msgFile != "" {
		raw, err := os.ReadFile(msgFile) // #nosec G304 -- the path comes from git itself, which passes the commit message file it just wrote; this tool runs only as a git hook
		if err != nil {
			return nil, fmt.Errorf("failed to read the commit message file: %w", err)
		}
		return checkMessage(string(raw)), nil
	}
	return checkStaged()
}

// checkStaged runs every content rule over what this commit records.
func checkStaged() ([]Finding, error) {
	entries, err := readStaged()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}

	// The cross-file rules first: a commit that regenerated nothing is
	// about the commit as a whole, and a reader should see that before a
	// list of per-line findings.
	findings := checkGenerated(entries)

	for _, entry := range entries {
		if isBinary(entry.Content) {
			continue
		}

		lines, err := addedLines(entry.Path)
		if err != nil {
			return nil, err
		}
		// The dash rules run on generated content too. A forbidden
		// character in a generated page is real, it shipped, and the fix
		// is in the source the generator read.
		findings = append(findings, checkDashes(entry.Path, lines)...)

		// The Go rules do not. Asking generated code for a hand-written
		// docstring, a smaller file or a differently worded error is
		// asking the generator to change rather than the author.
		if isGoSource(entry.Path) && !isGeneratedSource(entry.Content) {
			findings = append(findings, checkGoFile(entry.stagedFile, entry.Content)...)
		}
	}
	return findings, nil
}

// readStaged lists what this commit records and reads each path's
// content once.
func readStaged() ([]stagedEntry, error) {
	files, err := stagedFiles()
	if err != nil {
		return nil, err
	}
	entries := make([]stagedEntry, 0, len(files))
	for _, file := range files {
		content, err := stagedContent(file.Path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, stagedEntry{stagedFile: file, Content: content})
	}
	return entries, nil
}
