// Package iosparse_test: ShowInterfaces on the "Encapsulation X, medium is
// Y" line, where the port deliberately differs from genieparser.
package iosparse_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
)

// TestShowInterfaces_MediumLine holds ShowInterfaces to an Encapsulation
// line that carries a medium. genieparser reads the medium from the wrong
// match and raises KeyError on such a line, so none of its fixtures has
// one. Each case takes a real genieparser fixture, rewrites its one
// Encapsulation line into the medium form genieparser's own comments give
// ("Encapsulation ARPA, medium is broadcast" and "Encapsulation 802.1Q
// Virtual LAN, Vlan ID 20, medium is p2p", spaced as IOS XE spaces the
// VLAN ID), and wants the fixture's expected result plus the medium on
// that interface. The second fixture has three interfaces, so the case
// also holds that the other two still parse.
func TestShowInterfaces_MediumLine(t *testing.T) {
	cases := []struct {
		// name names the case.
		name string
		// fixture is the iosxe equal fixture the case starts from.
		fixture string
		// intf is the interface whose Encapsulation line is rewritten.
		intf string
		// from is that line as the fixture has it, and to is the line
		// with a medium that replaces it.
		from, to string
		// medium is the medium the result must record.
		medium string
	}{
		{
			name:    "arpa, medium is broadcast",
			fixture: "golden_interface_output_5",
			intf:    "FastEthernet1",
			from:    "  Encapsulation ARPA, loopback not set\n",
			to:      "  Encapsulation ARPA, medium is broadcast\n",
			medium:  "broadcast",
		},
		{
			name:    "dot1q vlan id, medium is p2p",
			fixture: "golden_interface_output_4",
			intf:    "GigabitEthernet0/0/0.105",
			from:    "  Encapsulation 802.1Q Virtual LAN, Vlan ID  105.\n",
			to:      "  Encapsulation 802.1Q Virtual LAN, Vlan ID  105, medium is p2p\n",
			medium:  "p2p",
		},
	}
	dir := filepath.Join("testdata", "ShowInterfaces", "iosxe", "equal")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, args := readFixture(t, dir, c.fixture)
			// The line must be there exactly once, or the case tests
			// nothing.
			if n := strings.Count(out, c.from); n != 1 {
				t.Fatalf("%s holds %q %d times, want once", c.fixture, c.from, n)
			}
			out = strings.Replace(out, c.from, c.to, 1)
			data, err := os.ReadFile(filepath.Join(dir, c.fixture+"_expected.json"))
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			entry, ok := want[c.intf].(map[string]any)
			if !ok {
				t.Fatalf("%s's expected result has no %s", c.fixture, c.intf)
			}
			// The medium is the only change the rewritten line makes.
			entry["medium"] = c.medium
			command := showInterfacesCommand(args)
			_, parsed, err := iosparse.Parse(command, out)
			if err != nil {
				t.Fatalf("Parse(%q): %v", command, err)
			}
			if d := firstDiff("", normalize(t, parsed), want); d != "" {
				t.Fatal(d)
			}
		})
	}
}
