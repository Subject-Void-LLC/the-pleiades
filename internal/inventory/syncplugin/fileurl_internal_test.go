// Tests of fileURLPath, on every platform: a file URL's path as each
// operating system names files.
package syncplugin

import "testing"

// TestFileURLPath: a Windows drive path loses the URL's leading slash and
// takes backslashes; every other path, and every path elsewhere, is kept.
func TestFileURLPath(t *testing.T) {
	for _, tc := range []struct{ path, goos, want string }{
		{"/C:/Users/x/up.yaml", "windows", `C:\Users\x\up.yaml`},
		{"/srv/up.yaml", "windows", `\srv\up.yaml`},
		{"/C:/Users/x/up.yaml", "linux", "/C:/Users/x/up.yaml"},
		{"/etc/hosts.yaml", "darwin", "/etc/hosts.yaml"},
	} {
		if got := fileURLPath(tc.path, tc.goos); got != tc.want {
			t.Errorf("fileURLPath(%q, %s) = %q, want %q", tc.path, tc.goos, got, tc.want)
		}
	}
}
