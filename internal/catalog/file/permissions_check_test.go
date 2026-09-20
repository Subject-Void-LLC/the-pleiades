// Package file_test: tests of file.permissions' check against the real run it
// predicts.
package file_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// These prove file.permissions' check against this package's real SSH
// server and real /bin/sh. The path's mode is read back from the
// filesystem after every check, and every prediction is compared with what
// the real Permissions run leaves from an identical starting state.

// permCheckParams is the params map for a check of path asking for mode.
func permCheckParams(path, mode string) map[string]any {
	return map[string]any{
		"path":                          path,
		"mode":                          mode,
		"insecure_skip_host_key_verify": true,
	}
}

// TestCheckPermissions_PredictsWhatARealRunDoes covers a mode that differs
// and one that already matches, each against the real run as its control.
func TestCheckPermissions_PredictsWhatARealRunDoes(t *testing.T) {
	cases := []struct {
		name        string
		start       os.FileMode
		want        string
		wantChanged bool
	}{
		{name: "mode differs", start: 0o600, want: "0640", wantChanged: true},
		{name: "mode already right", start: 0o640, want: "0640", wantChanged: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := startPermissionsServer(t)

			path := permissionsFile(t, tc.start)
			rc := newPermissionsContext(server)
			checked, err := file.CheckPermissions(context.Background(), rc, newPermissionsTarget(server), permCheckParams(path, tc.want))
			if err != nil {
				t.Fatalf("CheckPermissions: %v", err)
			}
			if checked.Changed != tc.wantChanged {
				t.Errorf("check Changed = %v, want %v", checked.Changed, tc.wantChanged)
			}
			if got, start := permissionsFileMode(t, path), "0"+octal(tc.start); got != start {
				t.Fatalf("the check changed the mode on disk to %s; it started as %s", got, start)
			}
			assertNoFileInverse(t, rc.stats)
			if got := rc.stats["mode"]; got != "0"+octal(tc.start) {
				t.Errorf("stat mode = %v, want the path's current %s: a check leaves the path as it found it", got, "0"+octal(tc.start))
			}
			predicted := permissionsDiffHalf(t, rc, "after")

			// The control.
			realPath := permissionsFile(t, tc.start)
			realRC := newPermissionsContext(server)
			real, err := file.Permissions(context.Background(), realRC, newPermissionsTarget(server), permCheckParams(realPath, tc.want))
			if err != nil {
				t.Fatalf("Permissions: %v", err)
			}
			if real.Changed != checked.Changed {
				t.Errorf("the check predicted Changed = %v, the real run reported %v", checked.Changed, real.Changed)
			}
			actual := permissionsDiffHalf(t, realRC, "after")
			for _, key := range []string{"exists", "kind", "mode", "owner", "group"} {
				if predicted[key] != actual[key] {
					t.Errorf("the check predicted after.%s = %v, the real run left %v", key, predicted[key], actual[key])
				}
			}
		})
	}
}

// TestCheckPermissions_RefusesWhatARealRunRefuses proves a check of a
// missing path fails exactly as the real run does.
func TestCheckPermissions_RefusesWhatARealRunRefuses(t *testing.T) {
	server := startPermissionsServer(t)
	path := filepath.Join(t.TempDir(), "missing")

	_, checkErr := file.CheckPermissions(context.Background(), newPermissionsContext(server), newPermissionsTarget(server), permCheckParams(path, "0640"))
	_, realErr := file.Permissions(context.Background(), newPermissionsContext(server), newPermissionsTarget(server), permCheckParams(path, "0640"))
	if checkErr == nil || realErr == nil {
		t.Fatalf("expected both to refuse a missing path, got check %v and real %v", checkErr, realErr)
	}
	if checkErr.Error() != realErr.Error() {
		t.Errorf("check refused with %q, the real run with %q", checkErr, realErr)
	}
}

// TestCheckPermissions_IsDeclared pins the registration the engine reads.
func TestCheckPermissions_IsDeclared(t *testing.T) {
	d, ok := collection.Lookup("file.permissions")
	if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatalf("file.permissions must declare check support with a Check function, got %+v", d.Manifest)
	}
}

// octal renders a mode's permission bits as three octal digits.
func octal(mode os.FileMode) string {
	const digits = "01234567"
	p := uint32(mode.Perm())
	return string([]byte{digits[(p>>6)&7], digits[(p>>3)&7], digits[p&7]})
}
