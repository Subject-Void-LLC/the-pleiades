// Tests of what the net.frr methods read from the router's own words:
// frr-reload's difference and show version.
package frr

import (
	"slices"
	"testing"
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
