// This file holds the directory exclusions gosec-check passes to gosec.
//
// gosec turns each -exclude-dir value into an unescaped, unanchored regular
// expression (gosec v2.30.0, helpers.go ExcludedDirsRegExp) and matches it
// against each package directory its "./..." walk finds, relative to the
// working directory, and it walks directories that `go list ./...` would
// skip. The flag used to be the bare "-exclude-dir=.claude", whose dot
// matches any character, so it matched "/claude" anywhere in a path: a copy of
// the module under /tmp/claude-<uid>/ (an agent's scratch directory) had every
// file excluded, and gosec reported the packages it could still half load as
// type errors ("undefined: Template" in internal/ent) instead of scanning them.
// It also hid, by that same accident, the Go files copied into
// .IGNORE/claude-api/ for outside reviews, which do not build as packages.
//
// Each pattern is now anchored to the start of the path or a separator, and
// matches a directory by name: .claude, where other agents' git worktrees
// live, and every directory whose name is a dot then an uppercase letter,
// which is what .gitignore's `.[A-Z]*` keeps out of the repository (.IGNORE,
// .SPECIFICATION, .AGENTS).
//
// A checkout that itself lives under a .claude directory, such as a git
// worktree in .claude/worktrees/, is scanned whole, since the paths matched are
// relative to it. gosec once could not type-check internal/ent from such a
// worktree (FAILURE_PATTERNS 1157); the cause was the length of the checkout's
// path, not the directory, and split.go now scans a package whose file list is
// too long from a short path, so make gosec runs from any checkout.
package main

// excludeAgentDirs is the -exclude-dir value that leaves out the .claude
// directory.
const excludeAgentDirs = `(^|[\\/])\.claude([\\/]|$)`

// excludeIgnoredDirs is the -exclude-dir value that leaves out every directory
// .gitignore's `.[A-Z]*` keeps out of the repository.
const excludeIgnoredDirs = `(^|[\\/])\.[A-Z]`
