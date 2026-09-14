// This file covers the unexported parsing helpers, which the external test
// package cannot reach.
//
// They are worth reaching. Everything here turns one line of another
// machine's `stat` output into a typed value, and a wrong answer is not a
// crash: it is a file reported as the wrong kind, a permission string that
// never compares equal, or an error message carrying a page of remote
// output. The mode padding in particular is the difference between a module
// that is idempotent and one that reports "changed" on every single run,
// which is invisible until somebody notices every run says changed.
package remotefile

import "testing"

// TestParseStat_AcceptsRealStatOutput covers the happy path and every way
// the line can be malformed.
func TestParseStat_AcceptsRealStatOutput(t *testing.T) {
	// The shape the remote command actually prints: hex raw mode, octal
	// permissions, owner, group, size, mtime.
	got, err := parseStat("81a4|644|deploy|staff|1024|1700000000")
	if err != nil {
		t.Fatalf("parseStat: %v", err)
	}
	if got.Kind != KindFile {
		t.Errorf("Kind = %v, want a regular file", got.Kind)
	}
	// Padded, so it compares equal to a runbook's "0644".
	if got.Mode != "0644" {
		t.Errorf("Mode = %q, want 0644", got.Mode)
	}
	if got.Owner != "deploy" || got.Group != "staff" {
		t.Errorf("ownership = %q:%q", got.Owner, got.Group)
	}
	if got.Size != 1024 || got.Mtime != 1700000000 {
		t.Errorf("Size/Mtime = %d/%d", got.Size, got.Mtime)
	}
}

// TestParseStat_RefusesMalformedOutput proves each field is validated
// rather than silently defaulting.
//
// A zero Size accepted from unparseable output would read as an empty file,
// which is a fact somebody might act on by overwriting it.
func TestParseStat_RefusesMalformedOutput(t *testing.T) {
	for _, tc := range []struct{ name, line string }{
		{"empty", ""},
		{"too few fields", "81a4|644|deploy"},
		{"too many fields", "81a4|644|deploy|staff|1024|1700000000|extra"},
		{"a mode that is not hex", "nothex|644|deploy|staff|1024|1700000000"},
		{"a size that is not a number", "81a4|644|deploy|staff|big|1700000000"},
		{"an mtime that is not a number", "81a4|644|deploy|staff|1024|recently"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseStat(tc.line); err == nil {
				t.Errorf("parseStat(%q) returned no error", tc.line)
			}
		})
	}
}

// TestKindFromRawMode_CoversEveryFileType covers the POSIX type bits.
func TestKindFromRawMode_CoversEveryFileType(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  uint64
		want Kind
	}{
		{"a regular file", 0x81a4, KindFile},
		{"a directory", 0x41ed, KindDirectory},
		{"a symlink", 0xa1ff, KindSymlink},
		// A socket, which is none of the three and must not be reported
		// as a regular file that could then be read or overwritten.
		{"a socket", 0xc1a4, KindOther},
		{"a character device", 0x21b6, KindOther},
		{"no type bits at all", 0x01a4, KindOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := kindFromRawMode(tc.raw); got != tc.want {
				t.Errorf("kindFromRawMode(%#x) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestNormalizeMode_PadsToFourDigits is the idempotence fix.
func TestNormalizeMode_PadsToFourDigits(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"644", "0644"},
		{"0644", "0644"},
		{"7", "0007"},
		{"  755  ", "0755"},
		// Already four digits, including the setuid family, which must not
		// be truncated or re-padded.
		{"4755", "4755"},
		// Empty stays empty: "no mode reported" is not mode 0000, and
		// padding it would claim a file is unreadable by everybody.
		{"", ""},
		{"   ", ""},
	} {
		if got := normalizeMode(tc.in); got != tc.want {
			t.Errorf("normalizeMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFirstLine_KeepsAnErrorMessageShort covers the helper that stops a
// page of remote output from becoming an error string.
func TestFirstLine_KeepsAnErrorMessageShort(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"one line", "permission denied", "permission denied"},
		{"many lines", "permission denied\nstack trace\nmore", "permission denied"},
		{"leading and trailing space", "  denied  ", "denied"},
		// A command that failed silently still owes the reader something
		// to put in the error, rather than an empty quoted string.
		{"no output at all", "", "no output"},
		{"only whitespace", "   \n  ", "no output"},
		{"a leading blank line", "\n\nreal message", "real message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstLine(tc.in); got != tc.want {
				t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
