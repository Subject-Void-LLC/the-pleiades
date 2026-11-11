// This file holds the directory exclusions gosec-check passes to gosec.
//
// gosec turns each -exclude-dir value into an unescaped, unanchored regular
// expression and matches it against absolute file paths (gosec v2.30.0,
// helpers.go ExcludedDirsRegExp), and it walks directories that `go list ./...`
// would skip. The flag used to be the bare "-exclude-dir=.claude", whose dot
// matches any character, so it matched "/claude" anywhere in a path: a copy of
// the module under /tmp/claude-<uid>/ (an agent's scratch directory) had every
// file excluded, and gosec reported the packages it could still half load as
// type errors ("undefined: Template" in internal/ent) instead of scanning them.
// It also hid, by that same accident, the Go files copied into
// .IGNORE/claude-api/ for outside reviews, which do not build as packages.
//
// Each pattern is now anchored to the start of the path or a separator, since
// gosec matches relative paths as well as absolute ones, and matches a
// directory by name: .claude, where other agents' git worktrees live, and
// every directory whose name is a dot then an uppercase letter, which is what
// .gitignore's `.[A-Z]*` keeps out of the repository (.IGNORE, .SPECIFICATION,
// .AGENTS).
//
// A checkout that itself lives under a .claude directory, such as a git
// worktree in .claude/worktrees/, is still excluded whole, and gosec v2.30.0
// cannot type-check internal/ent from inside one even with no -exclude-dir at
// all (measured 2026-11-10). Run make gosec from a checkout outside any .claude
// directory.
package main

// excludeAgentDirs leaves out the .claude directory.
const excludeAgentDirs = `-exclude-dir=(^|[\\/])\.claude([\\/]|$)`

// excludeIgnoredDirs leaves out every directory .gitignore's `.[A-Z]*` keeps
// out of the repository.
const excludeIgnoredDirs = `-exclude-dir=(^|[\\/])\.[A-Z]`
