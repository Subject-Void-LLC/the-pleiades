// Fuzz tests of what the net.frr methods parse from a router's output.
package frr

import (
	"strings"
	"testing"
)

// FuzzParseReloadTest: frr-reload's output never panics the parse, and a
// reported line is never a rule, a blank or an INFO line.
func FuzzParseReloadTest(f *testing.F) {
	f.Add("Lines To Delete\n===============\ninterface eth1\n no description x\n\nLines To Add\n============\nrouter ospf\n")
	f.Add("2026-10-22 10:00:00,000  INFO: x\n")
	f.Fuzz(func(t *testing.T, out string) {
		deleted, added := parseReloadTest(out)
		for _, l := range append(deleted, added...) {
			if strings.TrimSpace(l) == "" || strings.Trim(strings.TrimSpace(l), "=") == "" || strings.Contains(l, " INFO: ") {
				t.Fatalf("reported %q", l)
			}
		}
	})
}

// FuzzParseVersion: show version's answer never panics the parse.
func FuzzParseVersion(f *testing.F) {
	f.Add("FRRouting 10.1.1 (r1) on Linux(6.1).")
	f.Fuzz(func(t *testing.T, out string) {
		v, h, err := parseVersion(out)
		if err == nil && (v == "" || strings.ContainsAny(h, "()")) {
			t.Fatalf("read %q %q from %q", v, h, out)
		}
	})
}
