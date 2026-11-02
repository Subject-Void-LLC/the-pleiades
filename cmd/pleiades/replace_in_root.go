// Package main: replaceInRoot writes a file inside an os.Root by renaming a
// fresh temporary file over it, for a command whose output is meant to replace
// what an earlier run wrote (pleiades import awx-credential-types --out).
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// replaceInRoot writes data to name inside root at perm, replacing whatever
// is there.
//
// It never writes through a symbolic link: the data goes to a new temporary
// file (created with O_EXCL, so a planted file or link at that name is
// refused rather than followed) and is then renamed over name. A rename
// replaces a symlink at name instead of following it (FAILURE_PATTERNS 792),
// and a reader sees the old file or the new one, never half of each. The
// root keeps every name inside its directory.
func replaceInRoot(root *os.Root, name string, data []byte, perm os.FileMode) error {
	// A dot-prefixed name beside the target, unique to this process.
	tmp := "." + strings.TrimPrefix(name, ".") + ".pleiades-" + strconv.Itoa(os.Getpid()) + ".tmp"
	// A leftover from a killed run of this same process id: remove it so
	// the exclusive create below can succeed. Remove deletes a link, never
	// its target.
	_ = root.Remove(tmp)
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("creating a temporary file for %s: %w", name, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = root.Remove(tmp)
		return fmt.Errorf("writing %s: %w", name, err)
	}
	// Sync before the rename so a crash cannot leave name pointing at an
	// empty file.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = root.Remove(tmp)
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("replacing %s: %w", name, err)
	}
	return nil
}
