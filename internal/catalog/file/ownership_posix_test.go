//go:build !windows

package file_test

import (
	"io/fs"
	"syscall"
)

// posixOwnerIDs reports the numeric owner and group of info, and whether
// this platform reports them at all.
//
// The type assertion this wraps used to sit inline in each caller, with a
// "this platform does not report POSIX owner and group ids" skip beside
// it -- a runtime guard for a compile-time problem. syscall.Stat_t does
// not merely go unpopulated on Windows, it does not exist there at all,
// so `go vet ./...` on CI's windows-latest leg failed to type-check this
// whole test package (FAILURE_PATTERNS.md's platform-suffix family: the
// build tag is the only thing that can express "this type is not on every
// GOOS"). The ok return keeps every caller's existing skip meaningful on
// any other GOOS that reports no POSIX ids.
func posixOwnerIDs(info fs.FileInfo) (uid, gid int, ok bool) {
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(sys.Uid), int(sys.Gid), true
}
