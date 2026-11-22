// This file makes the short path gosec-check scans a long package from: a
// symlink to the checkout, with the same name on every run.
//
// The name must not change between runs. The Go build cache keys each
// compile on the package's directory unless -trimpath is set (cmd/go,
// internal/work/exec.go, buildActionID: "dir %s"), and gosec's `go list
// -export` compiles every package it loads and every package those import.
// A first fix that ran gosec through a symlink with a fresh name on each run
// therefore compiled the module from nothing every time, many compilers at
// once, and hung a gate for 27 minutes with swap full (FAILURE_PATTERNS
// 1337). Measured on 2026-11-21 from a new worktree: 123 s the first time,
// 38 s the second, at one unchanged path.
//
// So the link is named after the checkout and kept. It lives in a directory
// of its own under the user's cache directory (os.UserCacheDir), made private
// and refused when it is a symlink or writable by anyone else, so no other
// user can put a link of their own where gosec-check looks. Two checkouts get
// two names; two runs of one checkout share one link, and the first to make
// it wins, since both would make the same one. A link whose checkout is gone
// is removed by the next run that needs the directory.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// shortLinkDir is the directory under the user's cache directory that holds
// the links, kept short because its length is part of every path gosec loads
// through it.
const shortLinkDir = "pgs"

// shortLinkHex is how many hex digits of the checkout path's SHA-256 name its
// link: 48 bits, so two checkouts on one machine never share a name in
// practice, and a clash is refused rather than resolved.
const shortLinkHex = 12

// defaultShortBase returns the directory gosec-check keeps its links in.
func defaultShortBase() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("finding the user cache directory for a short path to the checkout: %w", err)
	}
	return filepath.Join(cache, shortLinkDir), nil
}

// shortLinkPath returns the link's path for the checkout at root inside base,
// without making anything.
func shortLinkPath(base, root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return filepath.Join(base, hex.EncodeToString(sum[:])[:shortLinkHex])
}

// stableShortRoot returns a path to the checkout at root through a symlink in
// base, making base and the link when they are not there yet. The link is
// left in place for the next run. It refuses a base that is not a private
// directory, and a link of that name that leads somewhere else.
func stableShortRoot(base, root string) (string, error) {
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("making %s: %w", base, err)
	}
	if err := privateDir(base); err != nil {
		return "", err
	}
	pruneDanglingLinks(base)
	link := shortLinkPath(base, root)
	if err := os.Symlink(root, link); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("making a symlink %s to the checkout: %w", link, err)
	}
	// Whether this run made the link or found it, read what it leads to: a
	// link another checkout made under the same name is refused.
	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", link, err)
	}
	if filepath.Clean(target) != filepath.Clean(root) {
		return "", fmt.Errorf("%s leads to %s, not to this checkout at %s; remove it and run again", link, target, root)
	}
	return link, nil
}

// privateDir refuses base when it is a symlink rather than a directory, or,
// outside Windows, when a group or other users may write in it.
func privateDir(base string) error {
	info, err := os.Lstat(base)
	if err != nil {
		return fmt.Errorf("reading %s: %w", base, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory (a symlink is refused), so gosec-check will not put a link to the checkout in it", base)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s may be written by other users (mode %v), so gosec-check will not put a link to the checkout in it", base, info.Mode().Perm())
	}
	return nil
}

// pruneDanglingLinks removes each link in base that leads nowhere, which is
// what a removed checkout leaves. Only symlinks named as shortLinkPath names
// them are touched, and a failure to remove one only leaves it.
func pruneDanglingLinks(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 || !isLinkName(e.Name()) {
			continue
		}
		path := filepath.Join(base, e.Name())
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(path)
		}
	}
}

// isLinkName reports whether name is shortLinkHex lowercase hex digits, the
// only names stableShortRoot makes.
func isLinkName(name string) bool {
	if len(name) != shortLinkHex {
		return false
	}
	for _, r := range name {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
