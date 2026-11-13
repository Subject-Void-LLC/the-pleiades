//go:build !windows

// Package crypto: the owner half of checkPrivate, on a file description the
// test builds, since giving a real file to another user needs root.
package crypto

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// statInfo is an os.FileInfo carrying a mode and a Stat_t, as a real fstat
// reports them.
type statInfo struct {
	mode os.FileMode
	st   *syscall.Stat_t
}

// Name implements os.FileInfo.
func (s statInfo) Name() string { return "master.key" }

// Size implements os.FileInfo.
func (s statInfo) Size() int64 { return 44 }

// Mode implements os.FileInfo.
func (s statInfo) Mode() fs.FileMode { return s.mode }

// ModTime implements os.FileInfo.
func (s statInfo) ModTime() time.Time { return time.Time{} }

// IsDir implements os.FileInfo.
func (s statInfo) IsDir() bool { return false }

// Sys implements os.FileInfo: the Stat_t, or nil for none.
func (s statInfo) Sys() any {
	if s.st == nil {
		return nil
	}
	return s.st
}

// TestCheckPrivate_RefusesAnotherUsersFile: a 0600 file owned by a user who
// is not this process's is refused naming chown; the same file owned by
// this user passes; and a file description with no Stat_t gets the mode
// check alone.
func TestCheckPrivate_RefusesAnotherUsersFile(t *testing.T) {
	me := uint32(os.Geteuid()) // #nosec G115 -- a test's own uid
	err := checkPrivate("/v/master.key", statInfo{mode: 0o600, st: &syscall.Stat_t{Uid: me + 1}})
	if !errors.Is(err, ErrLooseSecretFile) || !strings.Contains(err.Error(), "chown") {
		t.Errorf("another user's file: %v, want a refusal naming chown", err)
	}
	if err := checkPrivate("/v/master.key", statInfo{mode: 0o600, st: &syscall.Stat_t{Uid: me}}); err != nil {
		t.Errorf("this user's 0600 file: %v", err)
	}
	if err := checkPrivate("/v/master.key", statInfo{mode: 0o600}); err != nil {
		t.Errorf("no Stat_t: %v", err)
	}
}
