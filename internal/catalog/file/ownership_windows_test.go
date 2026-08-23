//go:build windows

package file_test

import "io/fs"

// posixOwnerIDs reports that this platform has no POSIX owner and group
// ids, which is the whole truth on Windows: os.FileInfo.Sys returns a
// *syscall.Win32FileAttributeData, and the syscall.Stat_t the !windows
// build of this helper reads does not exist here at all.
//
// Every caller already handles ok == false by skipping, so the owner and
// group assertions simply do not run on this GOOS. That is honest rather
// than convenient: CI's windows-latest leg is build-and-vet only, so what
// this file buys is a test package that type-checks there, not coverage
// it cannot have.
func posixOwnerIDs(_ fs.FileInfo) (uid, gid int, ok bool) {
	return 0, 0, false
}
