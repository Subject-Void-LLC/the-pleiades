//go:build !windows

// This file holds the link directory's ownership check on Linux and macOS:
// the directory must be this user's, and no group or other user may write in
// it.
package main

import (
	"fmt"
	"os"
	"syscall"
)

// currentUID is the user gosec-check runs as; a test replaces it to make a
// directory look like another user's.
var currentUID = os.Getuid

// ownedPrivately refuses base, whose Lstat is info, unless this user owns it
// and neither its group nor other users may write in it.
func ownedPrivately(base string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read who owns %s, so gosec-check will not put a link to the checkout in it", base)
	}
	if int(st.Uid) != currentUID() {
		return fmt.Errorf("%s belongs to user %d, not to this user (%d), so gosec-check will not put a link to the checkout in it", base, st.Uid, currentUID())
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s may be written by other users (mode %v), so gosec-check will not put a link to the checkout in it", base, info.Mode().Perm())
	}
	return nil
}
