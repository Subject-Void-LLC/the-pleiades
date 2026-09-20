// Package loader: tests of the refusal on a platform with no loader.
package loader

import (
	"strings"
	"testing"
)

// TestUnsupported covers the refusal a platform without the loader gives,
// on every platform, since the Windows-only test runs only on Windows: it
// names the reason everywhere, and only Windows is pointed at WSL 2.
func TestUnsupported(t *testing.T) {
	for goos, wantWSL := range map[string]bool{"windows": true, "plan9": false} {
		err := unsupportedMessage(goos)
		if !strings.Contains(err, "not supported on "+goos) || !strings.Contains(err, "confined") {
			t.Errorf("%s: refusal %q does not name the reason", goos, err)
		}
		if strings.Contains(err, "WSL 2") != wantWSL {
			t.Errorf("%s: refusal %q, want WSL 2 mentioned = %v", goos, err, wantWSL)
		}
	}
}
