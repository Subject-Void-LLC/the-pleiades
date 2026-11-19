// Package facts_test: a listening socket's process cannot be forged by a local
// user (Phase 41c, owner decision 2026-11-19). A file name is any bytes but a
// slash and NUL, so a path whose directory names hold a newline and a line
// shaped like a descriptor listing must not make another process the owner
// of a socket it does not hold.
package facts_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestGather_APortsOwnerCannotBeForgedByAFileName(t *testing.T) {
	gatherNeedsLinux(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	comm, err := os.ReadFile("/proc/self/comm")
	if err != nil {
		t.Skipf("no /proc/self/comm: %v", err)
	}
	init, err := os.ReadFile("/proc/1/comm")
	if err != nil || strings.TrimSpace(string(init)) == strings.TrimSpace(string(comm)) {
		t.Skipf("pid 1's name cannot be told from this process's: %q, %v", init, err)
	}
	raw, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	var st syscall.Stat_t
	if err := syscall.Fstat(int(raw.Fd()), &st); err != nil {
		t.Fatal(err)
	}
	inode := strconv.FormatUint(st.Ino, 10)

	// .../a\n/proc/1/fd:\nlrwx 9 -> socket:[<inode>]: the listing of this
	// process's descriptor to it reads, line by line, as pid 1 holding the
	// listener.
	dir := filepath.Join(t.TempDir(), "a\n", "proc", "1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Skipf("this filesystem refuses the name: %v", err)
	}
	forged, err := os.Create(filepath.Join(dir, "fd:\nlrwx------ 1 u u 64 Nov 19 09:00 9 -> socket:["+inode+"]"))
	if err != nil {
		t.Skipf("this filesystem refuses the name: %v", err)
	}
	t.Cleanup(func() { _ = forged.Close() })

	server := startGatherServer(t)
	rc := newGatherContext(server)
	if _, err := gatherRun(t, server, rc, map[string]any{"gather_subset": []any{"ports"}}); err != nil {
		t.Fatal(err)
	}
	key := "port.tcp:127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	got := gatherItem(t, rc, key)
	if got["process"] != strings.TrimSpace(string(comm)) {
		t.Fatalf("%s = %v: the process is %v, want this one's %q", key, got, got["process"], strings.TrimSpace(string(comm)))
	}
}
