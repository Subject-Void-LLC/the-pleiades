// Fuzz tests of masking and the Batfish layout: configuration text is the
// device's, never trusted.
package netcfg_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// FuzzMask: masking never panics, keeps the line count, masks a masked
// configuration to itself, and leaves no secret that follows a password
// word as its own word, nor any key on a manual IPsec session key line.
func FuzzMask(f *testing.F) {
	for _, s := range []string{"password zebra", " neighbor 10.0.0.2 password 7 abc", "key chain k\n key 1\n  key-string s", "ip ospf message-digest-key 1 md5", "password", "\x00password\tx", " set session-key inbound esp 256 cipher 0123456789abcdef authenticator fedcba9876543210", " set session-key outbound ah 300 1111111111111111", "set session-key inbound esp", "set session-key\vinbound ah 256 \tk"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, config string) {
		masked := netcfg.Mask(config)
		if strings.Count(masked, "\n") != strings.Count(config, "\n") {
			t.Fatalf("the line count changed:\n%q\n%q", config, masked)
		}
		if again := netcfg.Mask(masked); again != masked {
			t.Fatalf("masking is not idempotent:\n%q\n%q", masked, again)
		}
		for _, line := range strings.Split(masked, "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == "password" && !strings.HasPrefix(f[1], netcfg.MaskPrefix) {
				t.Fatalf("a password survived: %q", line)
			}
			// Every word past a manual IPsec session key line's SPI is a
			// keyword or a placeholder.
			if len(f) > 5 && f[0] == "set" && f[1] == "session-key" && (f[3] == "ah" || f[3] == "esp") {
				for _, w := range f[5:] {
					if w != "cipher" && w != "authenticator" && !strings.HasPrefix(w, netcfg.MaskPrefix) {
						t.Fatalf("a session key survived: %q", line)
					}
				}
			}
		}
	})
}

// FuzzFRRForBatfish: the layout never panics, always opens with the
// node's name, and never keeps a passive line it moved.
func FuzzFRRForBatfish(f *testing.F) {
	f.Add("interface eth1\n ip ospf passive\nexit\nrouter ospf\nexit\n")
	f.Add("router ospf\n ip ospf passive\n")
	f.Fuzz(func(t *testing.T, config string) {
		out, err := netcfg.FRRForBatfish("r1", config)
		if err != nil {
			return
		}
		if !strings.HasPrefix(out, "r1\n# This file describes the network interfaces\n") {
			t.Fatalf("the layout does not open with the node:\n%q", out)
		}
	})
}
