// Tests for the data directory settings' rules.
package oscheck

import (
	"strings"
	"testing"
)

// TestCheckDataDir: each rule takes a plain absolute directory and nothing
// else, names the setting in a refusal, and takes "" as no setting.
func TestCheckDataDir(t *testing.T) {
	for _, good := range []string{"", "/data", "/mnt/check-1/data.v2"} {
		if err := CheckPOSIXDataDir("X_DATA", good); err != nil {
			t.Errorf("POSIX %q: %v", good, err)
		}
	}
	for _, bad := range []string{"data", "/data/", `/da"ta`, "/data;rm -rf /", "/da ta", "/data/$HOME", "/a//b"} {
		if err := CheckPOSIXDataDir("X_DATA", bad); err == nil || !strings.Contains(err.Error(), `X_DATA="`) {
			t.Errorf("POSIX %q: %v", bad, err)
		}
	}
	for _, good := range []string{"", `D:\pleiades`, `E:\check\data_1`} {
		if err := CheckWindowsDataDir("X_DATA", good); err != nil {
			t.Errorf("Windows %q: %v", good, err)
		}
	}
	for _, bad := range []string{`D:`, `D:\`, `D:\a b`, `D:\a"b`, "/data", `D:\pleiades\`, `D:\a\\b`} {
		if err := CheckWindowsDataDir("X_DATA", bad); err == nil || !strings.Contains(err.Error(), `X_DATA="`) {
			t.Errorf("Windows %q: %v", bad, err)
		}
	}
}
