// Internal tests of parseBridge: a port the host lists for a bridge is held
// to the interface-name rule before net.linux.bridge sends it back to ip
// (FAILURE_PATTERNS 1174's sweep).
package linux

import (
	"strings"
	"testing"
)

// TestParseBridge_RefusesAPortTheHostListsThatIsNoName: the answers are
// what readBridgeScript prints: "bridge", the flags, then one port per
// line. A port of "-x" or "--if-exists" would have gone back to ip as
// ip link set -x nomaster, an option in a port's place, and into the undo;
// each is refused, naming the bridge, rather than skipped. Real names parse.
func TestParseBridge_RefusesAPortTheHostListsThatIsNoName(t *testing.T) {
	st, err := parseBridge("net.linux.bridge", "br0", "bridge\n0x1003\neth1\nveth0.10\n")
	if err != nil || !st.exists || !st.up || strings.Join(st.ports, ",") != "eth1,veth0.10" {
		t.Fatalf("parseBridge(real ports) = %+v, %v", st, err)
	}
	for _, port := range []string{"-x", "--if-exists", "eth1;reboot", strings.Repeat("a", 16)} {
		_, err := parseBridge("net.linux.bridge", "br0", "bridge\n0x1003\neth1\n"+port+"\n")
		if err == nil || !strings.Contains(err.Error(), "the bridge br0 lists a port") || !strings.Contains(err.Error(), "not an interface name") {
			t.Errorf("parseBridge with a port %q = %v, want it refused naming the bridge", port, err)
		}
	}
	for answer, want := range map[string]bridgeState{"absent\n": {}, "notbridge\n": {exists: true, notBridge: true}} {
		if st, err := parseBridge("net.linux.bridge", "br0", answer); err != nil || st.exists != want.exists || st.notBridge != want.notBridge {
			t.Errorf("parseBridge(%q) = %+v, %v", answer, st, err)
		}
	}
	if _, err := parseBridge("net.linux.bridge", "br0", "bridge\nzz\n"); err == nil {
		t.Error("flags that are not hex were read")
	}
}
