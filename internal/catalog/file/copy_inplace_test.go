// Package file_test: file.copy over a file this login may write but may
// not give away, against a real sshd whose login is not root.
package file_test

import (
	"context"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// rootOwnedCopyDir is a directory the login owns, holding a file root
// owns that everyone may write.
const rootOwnedCopyDir = "/config/copy-in-place"

// TestCopy_ARootFileItMayWriteIsWrittenInPlace: new content for a file
// root owns, keeping its attributes, cannot be renamed over it (a new
// file belongs to the login), so it is copied into the file, which stays
// root's, and the run says so. Asking for a mode as well needs a new file
// root owns, which this login cannot make: that is refused with the file
// untouched, naming the owner the task can set instead.
func TestCopy_ARootFileItMayWriteIsWrittenInPlace(t *testing.T) {
	if testing.Short() {
		t.Skip("starts an sshd container")
	}
	server, container := startFileSSHD(t)
	asRoot(t, container, "mkdir -p "+rootOwnedCopyDir+" && chown 1000:1000 "+rootOwnedCopyDir+
		" && printf 'old\\n' > "+rootOwnedCopyDir+"/f && chown root:root "+rootOwnedCopyDir+"/f && chmod 0666 "+rootOwnedCopyDir+"/f")
	target := copyServer(server)
	dest := rootOwnedCopyDir + "/f"

	params := copyParams(dest, "new\n")
	params["mode"] = "0644"
	_, err := file.Copy(context.Background(), newCopyContext(target), newCopyTarget(target), params)
	if err == nil || !strings.Contains(err.Error(), "set owner: "+busyboxSSHUser) {
		t.Fatalf("a mode on a root file = %v, want a refusal naming owner: %s", err, busyboxSSHUser)
	}
	if got := runOn(t, server, "cat "+dest+"; stat -c %U:%G:%a "+dest); got != "old\nroot:root:666" {
		t.Errorf("after the refusal the file is %q, want it untouched", got)
	}

	rc := newCopyContext(target)
	res, err := file.Copy(context.Background(), rc, newCopyTarget(target), copyParams(dest, "new\n"))
	if err != nil || !res.Changed {
		t.Fatalf("Copy = %+v, %v; want a change", res, err)
	}
	if got := runOn(t, server, "cat "+dest+"; stat -c %U:%G:%a "+dest); got != "new\nroot:root:666" {
		t.Errorf("the file is %q, want the new content still root's at 0666", got)
	}
	warnings, _ := rc.stats[sdk.StatWarnings].([]string)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "written in place") || !strings.Contains(warnings[0], "root:root") {
		t.Errorf("warnings = %q, want one saying the file was written in place", warnings)
	}
}

// asRoot runs command in the container as root, for a test's own setup.
func asRoot(t *testing.T, container testcontainers.Container, command string) {
	t.Helper()
	if code, _, err := container.Exec(context.Background(), []string{"sh", "-c", command}); err != nil || code != 0 {
		t.Fatalf("%q as root: exit %d, %v", command, code, err)
	}
}
