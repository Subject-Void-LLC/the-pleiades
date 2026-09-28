// An independent check of package iso9660's output through the Linux
// kernel's iso9660 driver, the reader a cloud-init guest actually mounts
// its NoCloud seed with. It needs a privileged container for the loop
// mount, and a control image built by xorriso proves that mount works
// before the image under test is judged.
package iso9660_test

import (
	"strings"
	"testing"
)

// TestLinuxKernelMountsTheSeed loop-mounts the seed image with the
// kernel's iso9660 driver and checks the Joliet tree shows the exact
// lower-case, hyphenated names with every file's bytes intact. It then
// mounts again with nojoliet and checks the primary tree reads back as
// the kernel's documented map=normal rendering of the derived
// identifiers: lower-cased, with ";1" and a trailing "." removed.
//
// A skip happens only when the control image, built by xorriso in the
// same container, cannot be mounted either. That means this Docker cannot
// loop-mount at all, which says nothing about the image under test; once
// the control mounts, any failure to mount the seed fails the test.
func TestLinuxKernelMountsTheSeed(t *testing.T) {
	files := seedFiles(t)
	c := startReader(t, writeImage(t, seedLabel, files), true, "xorriso")
	t.Logf("kernel: %s", strings.TrimSpace(run(t, c, "uname", "-r")))

	run(t, c, "mkdir", "-p", "/ctl/src", "/mnt/ctl", "/mnt/seed", "/out/kernel", "/out/nojoliet")
	run(t, c, "sh", "-c", "echo control > /ctl/src/control-file && "+
		"xorriso -as mkisofs -J -V control -o /ctl/control.iso /ctl/src")
	// Each mount is undone in the same shell command, so the loop device
	// is released even when a later step fails.
	code, out := runCode(t, c, "sh", "-c",
		"mount -t iso9660 -o ro,loop /ctl/control.iso /mnt/ctl && ls /mnt/ctl; rc=$?; umount /mnt/ctl; exit $rc")
	if code != 0 || !strings.Contains(out, "control-file") {
		t.Skipf("this Docker cannot loop-mount even an xorriso-built ISO 9660 image (exit %d):\n%s", code, out)
	}

	out = run(t, c, "sh", "-c", "mount -t iso9660 -o ro,loop "+seedPath+" /mnt/seed && "+
		"grep ' /mnt/seed ' /proc/mounts && ls -la /mnt/seed && cp -a /mnt/seed/. /out/kernel/; rc=$?; umount /mnt/seed; exit $rc")
	t.Logf("kernel mount, Joliet tree:\n%s", out)
	expectFiles(t, c, "/out/kernel", seedContents(files))

	out = run(t, c, "sh", "-c", "mount -t iso9660 -o ro,loop,nojoliet "+seedPath+" /mnt/seed && "+
		"ls -la /mnt/seed && cp -a /mnt/seed/. /out/nojoliet/; rc=$?; umount /mnt/seed; exit $rc")
	t.Logf("kernel mount, primary tree (nojoliet):\n%s", out)
	primary := make(map[string][]byte, len(files))
	for _, f := range files {
		id := strings.TrimSuffix(strings.TrimSuffix(seedPrimaryIDs[f.Name], ";1"), ".")
		primary[strings.ToLower(id)] = f.Data
	}
	expectFiles(t, c, "/out/nojoliet", primary)
}
