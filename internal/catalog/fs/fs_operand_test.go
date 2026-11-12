// Tests that fs.mount and fs.unmount refuse, before they connect, a field
// mount, umount, findmnt or fstab would read as something else, and that a
// findmnt that fails to answer is not read as "not mounted"
// (FAILURE_PATTERNS 1174). TestRunMount_EndsOptionsBeforeTheSourceAndThePath
// (fs_operand_internal_test.go) runs the real mount and umount.
package fs_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fs"
)

// TestMethods_RefuseAFieldMountOrFstabWouldMisread: path -a made util-linux
// mount run mount -a, which mounts every fstab entry, and a source holding a
// line break would have added a line to fstab. Each is refused with nothing
// sent: no mount, no umount, and fstab as it was. The harness's session
// budget is zero, so a method that connected would fail on the session
// rather than on the rule, and the error says which.
func TestMethods_RefuseAFieldMountOrFstabWouldMisread(t *testing.T) {
	good := map[string]any{"src": "/dev/sdb1", "fstype": "ext4", "opts": "defaults"}
	for _, tc := range []struct {
		name, key, value string
	}{
		{"a mountpoint mount reads as -a", "path", "-a"},
		{"a source mount reads as an option", "src", "--bind"},
		{"a filesystem type beginning with a dash", "fstype", "-a"},
		{"options beginning with a dash", "opts", "-a"},
		{"a source holding a whole fstab line", "src", "/dev/sdb1 /etc ext4 defaults 0 0\n/dev/sdc1"},
		{"a mountpoint holding a space", "path", "/mnt/a b"},
		{"options holding a tab", "opts", "defaults\tnoauto"},
		{"a source fstab reads as a comment", "src", "#/dev/sdb1"},
		{"a mountpoint holding an fstab escape", "path", `/mnt/a\040b`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessBudgeted(t, notMounted, "# fstab\n", 0)
			params := h.params("/data", nil)
			for k, v := range good {
				params[k] = v
			}
			params[tc.key] = tc.value
			calls := []struct {
				fqcn string
				fn   func() error
			}{
				{"fs.mount", func() error { _, err := fs.Mount(context.Background(), h.rc, h.device, params); return err }},
				{"fs.mount check", func() error { _, err := fs.CheckMount(context.Background(), h.rc, h.device, params); return err }},
			}
			if tc.key == "path" {
				calls = append(calls,
					struct {
						fqcn string
						fn   func() error
					}{"fs.unmount", func() error { _, err := fs.Unmount(context.Background(), h.rc, h.device, params); return err }},
					struct {
						fqcn string
						fn   func() error
					}{"fs.unmount check", func() error { _, err := fs.CheckUnmount(context.Background(), h.rc, h.device, params); return err }},
				)
			}
			for _, call := range calls {
				err := call.fn()
				if err == nil || !strings.Contains(err.Error(), tc.key+" is refused") {
					t.Fatalf("%s with %s %q = %v, want it refused by the mount field rule before connecting", call.fqcn, tc.key, tc.value, err)
				}
				if strings.Contains(err.Error(), tc.value) {
					t.Errorf("%s's refusal carries the value: %v", call.fqcn, err)
				}
			}
			if sent := h.invocations(t); len(sent) != 0 {
				t.Errorf("commands were sent: %v", sent)
			}
			if got := h.fstabContent(t); got != "# fstab\n" {
				t.Errorf("fstab changed to %q", got)
			}
		})
	}
}

// TestMethods_TakeTheSourcesMountReally: the forms a mount source really
// takes (a device path, UUID=, LABEL=, an NFS export, an SMB share, a
// bind-mount source findmnt writes with brackets) pass the rule and reach
// mount after its "--".
func TestMethods_TakeTheSourcesMountReally(t *testing.T) {
	for _, src := range []string{"/dev/sdb1", "UUID=0b1c-2d3e", "LABEL=data", "nas:/export/data", "//server/share", "/dev/sda1[/srv]", "tmpfs"} {
		t.Run(src, func(t *testing.T) {
			h := newHarness(t, notMounted, "# fstab\n")
			_, err := fs.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{"src": src, "fstype": "ext4", "persist": false}))
			if err != nil {
				t.Fatalf("fs.mount src %q: %v", src, err)
			}
			if sent := h.invocations(t); len(sent) != 1 || sent[0] != "mount -t ext4 -o defaults -- "+src+" /data" {
				t.Errorf("sent %v, want one mount of %s after --", sent, src)
			}
		})
	}
}

// TestQueryMount_OnlySilenceReadsAsNotMounted: findmnt exits 1 both when it
// finds nothing and for an error it explains on stderr, and 127 when it is
// not installed. Only the silent 1 is "not mounted"; before, every one of
// these went on to mount.
func TestQueryMount_OnlySilenceReadsAsNotMounted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  mountState
		wantOK bool
	}{
		{"nothing found, nothing said", notMounted, true},
		{"an error it explains", mountState{findmntExit: 1, findmntStderr: "findmnt: unknown column: OPTIONS"}, false},
		{"not installed", mountState{findmntExit: 127, findmntStderr: "sh: findmnt: not found"}, false},
		{"another status", mountState{findmntExit: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.state, "# fstab\n")
			_, err := fs.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{"src": "/dev/sdb1", "fstype": "ext4", "persist": false}))
			sent := h.invocations(t)
			if tc.wantOK {
				if err != nil || len(sent) != 1 {
					t.Fatalf("fs.mount = %v, sent %v; want one mount", err, sent)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "findmnt --mountpoint /data exited") {
				t.Fatalf("fs.mount = %v, want findmnt's failure to answer reported", err)
			}
			if len(sent) != 0 {
				t.Errorf("mount was sent after findmnt failed to answer: %v", sent)
			}
		})
	}
}
