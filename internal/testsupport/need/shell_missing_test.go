// Tests for POSIXShellMissing: the reason a TestMain hands facttest when the
// tests POSIXShell guards skip, and "" when they run.
package need

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPOSIXShellMissing_NamesTheNeedWhenThereIsNoShell(t *testing.T) {
	t.Parallel()
	why := shellMissing(filepath.Join(t.TempDir(), "sh"))
	if !strings.HasPrefix(why, "needs "+PosixShell+": ") || !strings.Contains(why, ShellPath) {
		t.Errorf("shellMissing(no such file) = %q, want the need named as Require names it", why)
	}
}

func TestPOSIXShellMissing_IsEmptyWhereTheShellIs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got := shellMissing(dir); got != "" {
		t.Errorf("shellMissing(an existing path) = %q, want \"\"", got)
	}
	if got, want := POSIXShellMissing(), shellMissing(ShellPath); got != want {
		t.Errorf("POSIXShellMissing() = %q, want shellMissing(%s)'s %q", got, ShellPath, want)
	}
}
