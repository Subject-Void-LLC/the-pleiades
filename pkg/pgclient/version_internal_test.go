// Tests for parseServerVersion, the read of server_version_num's text that
// ServerVersion runs on the server's answer.
package pgclient

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
)

// TestParseServerVersion reads a real version number, and refuses one that
// is not a number by quoting the server's text bounded, with a control byte
// shown escaped rather than sent.
func TestParseServerVersion(t *testing.T) {
	num, major, err := parseServerVersion("150019")
	if err != nil || num != 150019 || major != 15 {
		t.Fatalf("parseServerVersion(150019) = %d, %d, %v; want 150019, 15, nil", num, major, err)
	}

	long := "\x1b]0;owned\x07" + strings.Repeat("9x", 2000)
	_, _, err = parseServerVersion(long)
	if err == nil {
		t.Fatal("a version that is not a number was accepted")
	}
	msg := err.Error()
	const prefix = `the server's version number "`
	if !strings.HasPrefix(msg, prefix) {
		t.Errorf("error %q does not quote the server's text", msg)
	}
	if strings.ContainsAny(msg, "\x1b\x07") {
		t.Errorf("error %q sends a raw control byte", msg)
	}
	if !strings.Contains(msg, `\x1b]0;owned\a`) {
		t.Errorf("error %q does not show the escape quoted", msg)
	}
	if max := len(prefix) + farside.MaxBytes + 5 + len(" is not a number"); len(msg) > max {
		t.Errorf("error is %d bytes, want at most %d", len(msg), max)
	}
	if _, _, err := parseServerVersion(""); err == nil || !strings.Contains(err.Error(), `number "" is not`) {
		t.Errorf("an empty version: got %v, want it quoted as empty", err)
	}
}
