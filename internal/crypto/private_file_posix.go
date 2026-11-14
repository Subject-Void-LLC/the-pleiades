//go:build !windows

// Package crypto: the POSIX half of ReadPrivateFile's check, on the mode bits
// and the owning user id the open file reports.
package crypto

import (
	"fmt"
	"os"
	"syscall"
)

// checkPrivate refuses info, the open file at path, when its mode grants
// group or others any access, or when its owner is not this process's
// effective user. Each refusal names the command that fixes it.
func checkPrivate(path string, info os.FileInfo) error {
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%w: %s can be read or written by group or others (mode %04o); fix it with: chmod 600 %s", ErrLooseSecretFile, path, perm, path)
	}
	// Sys is a *syscall.Stat_t on every POSIX system Go builds for; a system
	// where it is not (js/wasm) has no owner to compare, and the mode check
	// above is all it gets.
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return nil
	}
	if me := os.Geteuid(); me >= 0 && st.Uid != uint32(me) { // #nosec G115 -- a uid is never negative here, checked above
		return fmt.Errorf("%w: %s is owned by user %d, not by this user (%d); fix it with: chown %d %s, or run as its owner", ErrLooseSecretFile, path, st.Uid, me, me, path)
	}
	return nil
}
