//go:build windows

// This file is the link directory's ownership check on Windows, which makes
// none: only the symlink check in shortpath.go applies there.
package main

import "os"

// ownedPrivately accepts any directory on Windows.
func ownedPrivately(string, os.FileInfo) error { return nil }
