// Tests of the management API a request target names and of pcapng's time
// resolution option, edge by edge.
package pcapsum

import (
	"encoding/binary"
	"testing"
	"time"
)

// TestManagementAPI names RESTCONF, gNMI, gNOI, NX-API, eAPI, IOS's HTTP
// exec and IOS XE's web interface by their paths, absolute targets
// included, and nothing that only begins like one.
func TestManagementAPI(t *testing.T) {
	for target, want := range map[string]string{
		"/ins":                             ManagementNXAPI,
		"/ins?x=1":                         ManagementNXAPI,
		"/api/aaaLogin.json":               ManagementNXAPI,
		"/command-api":                     ManagementEAPI,
		"/level/15/exec/show/version/CR":   ManagementIOSExec,
		"/level/1/exec":                    ManagementIOSExec,
		"/webui":                           ManagementWebUI,
		"/webui/":                          ManagementWebUI,
		"/webui/#/dashboard":               ManagementWebUI,
		"/insert":                          "",
		"/api/aaaLogin.jsonx":              "",
		"/command-api-docs":                "",
		"/level/x/exec":                    "",
		"/level//exec":                     "",
		"/level/15/execute":                "",
		"/webuikit":                        "",
		"/restconf":                        ManagementRESTCONF,
		"/restconf/data/ietf-interfaces":   ManagementRESTCONF,
		"/restconf?depth=1":                ManagementRESTCONF,
		"/.well-known/host-meta":           ManagementRESTCONF,
		"/.well-known/host-meta?x=1":       ManagementRESTCONF,
		"http://r1.lab/restconf/data":      ManagementRESTCONF,
		"/gnmi.gNMI/Get":                   ManagementGNMI,
		"/gnoi.system.System/Time":         ManagementGNOI,
		"/":                                "",
		"/restconfig":                      "",
		"/index.html":                      "",
		"http://r1.lab":                    "",
		"/grpc.health.v1.Health/Check":     "",
		"/.well-known/host-meta-elsewhere": "",
	} {
		if got := managementAPI(target); got != want {
			t.Errorf("%q: %q, want %q", target, got, want)
		}
	}
}

// TestInterfaceUnits reads if_tsresol past another option, stops at the
// end of options or one cut short, and gives 0 for a resolution no 64-bit
// count holds.
func TestInterfaceUnits(t *testing.T) {
	le := binary.LittleEndian
	comment := []byte{1, 0, 3, 0, 'a', 'b', 'c', 0}
	for name, tc := range map[string]struct {
		opts []byte
		want tsUnits
	}{
		"none":                 {nil, defaultUnits},
		"after a comment":      {append(append([]byte(nil), comment...), 9, 0, 1, 0, 3, 0, 0, 0), 1000},
		"end of options first": {[]byte{0, 0, 0, 0, 9, 0, 1, 0, 9, 0, 0, 0}, defaultUnits},
		"cut short":            {[]byte{9, 0, 8, 0, 3}, defaultUnits},
		"2^-63":                {[]byte{9, 0, 1, 0, 0x80 | 63, 0, 0, 0}, 1 << 63},
		"2^-64 does not fit":   {[]byte{9, 0, 1, 0, 0x80 | 64, 0, 0, 0}, 0},
		"10^-19 fits":          {[]byte{9, 0, 1, 0, 19, 0, 0, 0}, 10_000_000_000_000_000_000},
		"an empty if_tsresol":  {[]byte{9, 0, 0, 0}, defaultUnits},
	} {
		if got := interfaceUnits(le, tc.opts); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
	// A count past any time a capture was made has no time.
	if got := tsUnits(1).at(1 << 63); !got.IsZero() {
		t.Errorf("a count of 2^63 seconds is %v", got)
	}
	if got := tsUnits(0).at(5); !got.IsZero() {
		t.Errorf("no units gave %v", got)
	}
	if got := defaultUnits.at(1_500_000); !got.Equal(time.Unix(1, 500_000_000)) {
		t.Errorf("1.5 s in microseconds is %v", got)
	}
}
