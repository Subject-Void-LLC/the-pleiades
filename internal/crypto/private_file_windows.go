//go:build windows

// Package crypto: ReadPrivateFile's check on Windows, which checks nothing.
package crypto

import "os"

// checkPrivate accepts every file on Windows. POSIX mode bits do not apply
// there (Go reports 0666 or 0444 from the read-only attribute alone), and a
// file's access is its ACL, which this check does not read. So on Windows a
// secret file is protected by the account's profile directory permissions
// and nothing more; the owner decided on 2026-11-13 to check nothing beyond
// what the platform gives, and docs/02 says so.
func checkPrivate(string, os.FileInfo) error { return nil }
