// Tests of Dialect.Rejection: a command's output that holds a notice is
// not a rejection, and one that holds a rejection beside a notice is.
package netcli_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
)

// TestRejection: show crypto key mypubkey rsa's notice (IOL XE 17.15's own
// words) is not a rejection; "% Invalid input" is, after a notice too; a
// dialect with no error form rejects nothing.
func TestRejection(t *testing.T) {
	keys := "% Key pair was generated at: 21:03:11 UTC Oct 24 2026\r\nKey name: SSH\r\n Usage: General Purpose Key"
	if line, ok := netcli.IOS.Rejection(keys); ok {
		t.Errorf("a notice was taken as a rejection: %q", line)
	}
	bad := keys + "\r\n% Invalid input detected at '^' marker."
	if line, ok := netcli.IOS.Rejection(bad); !ok || line != "% Invalid input detected at '^' marker." {
		t.Errorf("Rejection = %q, %v", line, ok)
	}
	if _, ok := netcli.FromPrompt("r1#").Rejection(bad); ok {
		t.Error("a dialect with no error form rejected a line")
	}
}
