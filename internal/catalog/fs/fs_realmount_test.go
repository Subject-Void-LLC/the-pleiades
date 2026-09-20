// Package fs_test: the fs checks against real mounts, inside an
// unprivileged user and mount namespace.
package fs_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestChecks_AgainstARealMount is the control the fake findmnt cannot be:
// fs.mount's and fs.unmount's checks, then their real runs, with real
// mount(8), umount(8), findmnt and the kernel. Mounting needs root, so the
// test re-runs itself as root in an unprivileged user and mount namespace
// (testsupport.InPrivateRoot), where a tmpfs mount is permitted and is
// gone when the process exits; the in-process SSH harness's shells are its
// children and share that namespace. Every key a check's after half
// states is the one findmnt reports after the real run, and the check
// itself leaves the mount table and fstab as they were.
func TestChecks_AgainstARealMount(t *testing.T) {
	if testsupport.InPrivateRoot(t, "") {
		realMountScenario(t)
	}
}

// realMountScenario runs inside the namespace.
func realMountScenario(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	device := &target{
		Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameLinux}},
		host: srv.Host, port: srv.Port,
	}
	dir := t.TempDir()
	mountpoint := filepath.Join(dir, "data")
	if err := os.Mkdir(mountpoint, 0o750); err != nil {
		t.Fatal(err)
	}
	fstab := filepath.Join(dir, "fstab")
	if err := os.WriteFile(fstab, []byte("# fstab\n"), 0o644); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("umount", mountpoint).Run() }) // #nosec G204 -- a path this test made

	mounted := func() string {
		out, _ := exec.Command("findmnt", "-no", "SOURCE,FSTYPE", "--mountpoint", mountpoint).Output() // #nosec G204 -- a path this test made
		return strings.TrimSpace(string(out))
	}
	fstabText := func() string {
		data, err := os.ReadFile(fstab) // #nosec G304 -- a path this test made
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	params := map[string]any{
		"insecure_skip_host_key_verify": true,
		"path":                          mountpoint,
		"src":                           "tmpfs",
		"fstype":                        "tmpfs",
		"fstab":                         fstab,
	}

	step := func(fqcn string, wantChange bool) {
		t.Helper()
		d, ok := collection.Lookup(fqcn)
		if !ok || d.Check == nil {
			t.Fatalf("%s does not declare a check", fqcn)
		}
		mountsBefore, fstabBefore := mounted(), fstabText()
		crc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
		checked, err := d.Check(context.Background(), crc, device, params)
		if err != nil {
			t.Fatalf("%s check: %v", fqcn, err)
		}
		if mounted() != mountsBefore || fstabText() != fstabBefore {
			t.Fatalf("%s's check changed the mount (%q to %q) or fstab", fqcn, mountsBefore, mounted())
		}
		rrc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
		ran, err := d.Invoke(context.Background(), rrc, device, params)
		if err != nil {
			t.Fatalf("%s real run: %v", fqcn, err)
		}
		if checked.Changed != wantChange || ran.Changed != wantChange {
			t.Errorf("%s: the check predicted Changed = %v and the real run reported %v, want %v", fqcn, checked.Changed, ran.Changed, wantChange)
		}
		_, predicted := checkDiff(t, crc)
		_, actual := checkDiff(t, rrc)
		for key, want := range predicted {
			if actual[key] != want {
				t.Errorf("%s: predicted %s = %v, the real run left %v", fqcn, key, want, actual[key])
			}
		}
	}

	step("fs.mount", true)
	if got := mounted(); !strings.HasPrefix(got, "tmpfs") {
		t.Fatalf("the real mount left findmnt reporting %q, so the comparison above proves nothing", got)
	}
	step("fs.mount", false)
	step("fs.unmount", true)
	if got := mounted(); got != "" {
		t.Fatalf("the real unmount left %q mounted", got)
	}
	step("fs.unmount", false)
}
