// Tests of what the net.frr methods read from the router's own words:
// frr-reload's difference, show version, and vtysh's refusals.
package frr

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
)

// TestParseReloadTest reads both sections of frr-reload.py --test, keeping
// context lines and dropping its logging and rules.
func TestParseReloadTest(t *testing.T) {
	out := `2026-10-22 10:00:00,000  INFO: Called via "Namespace(test=True)"

Lines To Delete
===============
interface eth1
 no description old

Lines To Add
============
interface eth1
 description new
exit
router ospf
 ospf router-id 10.255.0.1
`
	deleted, added := parseReloadTest(out)
	if !slices.Equal(deleted, []string{"interface eth1", " no description old"}) {
		t.Errorf("deleted = %q", deleted)
	}
	if !slices.Equal(added, []string{"interface eth1", " description new", "router ospf", " ospf router-id 10.255.0.1"}) {
		t.Errorf("added = %q", added)
	}
	if d, a := parseReloadTest("\nLines To Delete\n===============\n\nLines To Add\n============\n"); len(d)+len(a) != 0 {
		t.Errorf("an empty difference read as %q %q", d, a)
	}
}

// TestParseVersion reads FRR's version and hostname and refuses anything
// that does not answer as FRR does.
func TestParseVersion(t *testing.T) {
	v, h, err := parseVersion("FRRouting 10.1.1_git (r1) on Linux(6.18).\nCopyright 1996-2005\n")
	if err != nil || v != "10.1.1_git" || h != "r1" {
		t.Errorf("got %q %q %v", v, h, err)
	}
	for _, bad := range []string{"", "Hello, World", "FRRouting 10.1.1 on Linux"} {
		if _, _, err := parseVersion(bad); err == nil {
			t.Errorf("%q was read as FRR", bad)
		}
	}
}

// TestReloadError names frr-reload's own error, masked, not its logging.
func TestReloadError(t *testing.T) {
	out := "2026-10-23 02:24:40,920  INFO: Called via \"Namespace(...)\"\nTraceback (most recent call last):\n  File \"x\"\nPermissionError: [Errno 13] Permission denied: '/var/run/frr/reload-U81GG8.txt'\n neighbor 10.0.0.2 password zebra\n"
	got := reloadError(out)
	if !strings.Contains(got, "PermissionError") || strings.Contains(got, "INFO") || strings.Contains(got, "zebra") {
		t.Errorf("reloadError = %q", got)
	}
	if reloadError("x INFO: y\n") != "it printed nothing but logging" {
		t.Error("logging only")
	}
	// A long traceback line is held to farside's bound, keeping its end, and a
	// control byte in it is shown escaped.
	long := reloadError("Traceback\n" + strings.Repeat("y", 3000) + "\x1b[2J ValueError: the real reason\n")
	if !strings.Contains(long, "ValueError: the real reason") || strings.ContainsRune(long, 0x1b) || len(long) > farside.MaxBytes+5 {
		t.Errorf("reloadError of a long traceback = %d bytes: %.120q", len(long), long)
	}
}

// TestRefusal reads vtysh -m -f's answer to a candidate it cannot load: the
// whole candidate on stdout and the refused line, marked "% ", on stderr.
// The error holds the refusal, not the candidate's first line, with the
// secret on the refused line masked, on one line and quoted.
func TestRefusal(t *testing.T) {
	stdout := "frr version 10.1.1\nfrr defaults traditional\nhostname r1\n!\nrouter bgp 65001\n neighbor 10.0.0.2 remote-as 65002\n"
	stderr := "line 7: % Unknown command[4]:  neighbor 10.0.0.2 password zebra ttl\n"
	got := refusal(stdout + "\n" + stderr)
	if !strings.HasPrefix(got, `"line 7: % Unknown command[4]: neighbor 10.0.0.2 password `) || strings.Contains(got, "zebra") || strings.Contains(got, "frr version") {
		t.Errorf("refusal = %s, want the masked refused line alone", got)
	}
	if got := refusal("mktemp: failed to create file via template '/tmp/pleiades-frr.XXXXXX'\nRead-only file system\n"); got != `"mktemp: failed to create file via template '/tmp/pleiades-frr.XXXXXX' Read-only file system"` {
		t.Errorf("an unmarked answer is not read whole: %s", got)
	}
	if got := refusal(" \n"); got != "no output" {
		t.Errorf("an empty answer = %s", got)
	}
}
