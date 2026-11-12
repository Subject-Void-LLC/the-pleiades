// Internal tests of the "--" mount and umount get before their operands,
// against the real tools, and of the fstab writer's own refusal of a field
// that would add a field or a line (FAILURE_PATTERNS 1174).
package fs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestRunMount_EndsOptionsBeforeTheSourceAndThePath mounts a tmpfs whose
// source and relative mountpoint are both named -h with the real mount, then
// unmounts it with the real umount, inside an unprivileged user and mount
// namespace (testsupport.InPrivateRoot) where both are permitted and the
// mount is gone when the process exits. Without the "--" util-linux mount
// and umount each read -h as "print the help" and exited 0 having done
// nothing, the harmless form of a path or source named -a, which made mount
// run mount -a. The source carries the dash as well as the mountpoint
// because the harness runs every command under POSIXLY_CORRECT, where GNU
// getopt stops at the first operand: there a dash-led word after the source
// is already an operand, while on a real device, whose getopt permutes, it
// is an option wherever it stands (measured with util-linux 2.37 and 2.38:
// mount -t tmpfs -o defaults none -h prints the help and mounts nothing).
// The fs methods refuse a dash-led field before they connect, so this is
// what holds for any other caller.
func TestRunMount_EndsOptionsBeforeTheSourceAndThePath(t *testing.T) {
	if testsupport.InPrivateRoot(t, "") {
		realDashMountScenario(t)
	}
}

// realDashMountScenario runs inside the namespace.
func realDashMountScenario(t *testing.T) {
	need.POSIXShell(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "-h"), 0o750); err != nil {
		t.Fatal(err)
	}
	// The harness's shells start in this process's working directory.
	t.Chdir(dir)
	conn := dialHarness(t)
	mountpoint := filepath.Join(dir, "-h")
	t.Cleanup(func() { _ = exec.Command("umount", "--", mountpoint).Run() }) // #nosec G204 -- a path this test made
	mounted := func() string {
		out, _ := exec.Command("findmnt", "-no", "SOURCE,FSTYPE", "--mountpoint", mountpoint).Output() // #nosec G204 -- a path this test made
		return strings.TrimSpace(string(out))
	}

	if err := runMount(context.Background(), conn, "-h", "-h", "tmpfs", "defaults"); err != nil {
		t.Fatalf("runMount on -h: %v", err)
	}
	if got := strings.Fields(mounted()); len(got) != 2 || got[0] != "-h" || got[1] != "tmpfs" {
		t.Fatalf("after runMount findmnt reports %q at %s, want the tmpfs", got, mountpoint)
	}
	if err := runUnmount(context.Background(), conn, "-h"); err != nil {
		t.Fatalf("runUnmount on -h: %v", err)
	}
	if got := mounted(); got != "" {
		t.Fatalf("after runUnmount findmnt still reports %q at %s", got, mountpoint)
	}
}

// dialHarness starts the in-process SSH harness and connects to it.
func dialHarness(t *testing.T) *remoteexec.Conn {
	t.Helper()
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	auth, err := remoteexec.AuthFrom(srv.Username, srv.Password, nil, "")
	if err != nil {
		t.Fatalf("building auth: %v", err)
	}
	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	conn, err := runner.Connect(context.Background(), nil, remoteexec.Target{Host: srv.Host, Port: srv.Port}, auth)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestFstabEntry_RefusesAFieldThatWouldAddAFieldOrALine holds the writer to
// operand.MountField on its own: a line break in the source would write a
// second fstab line mounting something on /etc at the next boot, and a space
// would shift every field after it. An empty field is refused too, since the
// line would come out a field short.
func TestFstabEntry_RefusesAFieldThatWouldAddAFieldOrALine(t *testing.T) {
	ok := fstabEntry{source: "/dev/sdb1", mountpoint: "/data", fstype: "ext4", options: "defaults"}
	if err := ok.check(); err != nil {
		t.Fatalf("a plain entry was refused: %v", err)
	}
	for name, entry := range map[string]fstabEntry{
		"a source holding a line":     {source: "/dev/sdb1 /etc ext4 defaults 0 0\n/dev/sdc1", mountpoint: "/data", fstype: "ext4", options: "defaults"},
		"a mountpoint holding a tab":  {source: "/dev/sdb1", mountpoint: "/da\tta", fstype: "ext4", options: "defaults"},
		"options holding a space":     {source: "/dev/sdb1", mountpoint: "/data", fstype: "ext4", options: "defaults noauto"},
		"a filesystem type left out":  {source: "/dev/sdb1", mountpoint: "/data", fstype: "", options: "defaults"},
		"a source fstab reads as #":   {source: "#x", mountpoint: "/data", fstype: "ext4", options: "defaults"},
		"a carriage return in a path": {source: "/dev/sdb1", mountpoint: "/data\r", fstype: "ext4", options: "defaults"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := entry.check(); err == nil {
				t.Fatalf("%+v was accepted, and its line would be %q", entry, entry.line())
			}
			if _, err := syncFstab(context.Background(), nil, "/etc/fstab", entry.mountpoint, &entry); err == nil {
				t.Fatal("syncFstab accepted the entry")
			}
			if _, err := fstabWouldChange(context.Background(), nil, "/etc/fstab", entry.mountpoint, &entry); err == nil {
				t.Fatal("fstabWouldChange accepted the entry")
			}
		})
	}
}
