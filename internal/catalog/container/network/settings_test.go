// Pure tests of a network's declared settings: what a task may declare, how
// it compares with a live network, and what an undo carries back.
package network

import (
	"net/netip"
	"strings"
	"testing"

	moby "github.com/moby/moby/api/types/network"
)

// TestParseSettings_RefusesWhatAnEngineWouldReadOtherwise: each refusal
// names the parameter at fault.
func TestParseSettings_RefusesWhatAnEngineWouldReadOtherwise(t *testing.T) {
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"an unknown driver":           {map[string]any{"driver": "overlay"}, "driver"},
		"a subnet with host bits":     {map[string]any{"subnet": "10.0.0.1/24"}, "subnet"},
		"a subnet that is not one":    {map[string]any{"subnet": "ten"}, "subnet"},
		"an IPv6 subnet without IPv6": {map[string]any{"subnet": "fd00::/64"}, "enable_ipv6"},
		"a gateway with no subnet":    {map[string]any{"gateway": "10.0.0.1"}, "gateway"},
		"a gateway outside":           {map[string]any{"subnet": "10.0.0.0/24", "gateway": "10.0.1.1"}, "gateway"},
		"a range outside":             {map[string]any{"subnet": "10.0.0.0/24", "ip_range": "10.0.1.0/28"}, "ip_range"},
		"a range wider than subnet":   {map[string]any{"subnet": "10.0.0.0/24", "ip_range": "10.0.0.0/16"}, "ip_range"},
		"an MTU too small":            {map[string]any{"mtu": 10}, "mtu"},
		"an MTU on macvlan":           {map[string]any{"driver": "macvlan", "mtu": 1500}, "mtu"},
		"an MTU as text":              {map[string]any{"mtu": "1500"}, "mtu"},
		"a parent on a bridge":        {map[string]any{"parent": "eth0"}, "parent"},
		"a parent that is not one":    {map[string]any{"driver": "macvlan", "parent": "eth0;reboot"}, "parent"},
		"internal as text":            {map[string]any{"internal": "yes"}, "internal"},
		"a label that is a number":    {map[string]any{"labels": map[string]any{"n": 1}}, "labels"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSettings(tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("parseSettings(%v) = %v, want it refused naming %s", tc.params, err, tc.want)
			}
		})
	}
}

// TestParseSettings_TheCreateRequest: a full declaration becomes the
// request an engine is sent, and an empty one is a bridge with IPv6 off.
func TestParseSettings_TheCreateRequest(t *testing.T) {
	s, err := parseSettings(map[string]any{"subnet": "10.9.0.0/24", "gateway": "10.9.0.1", "ip_range": "10.9.0.128/25", "mtu": 1400, "internal": true, "labels": map[string]any{"a": "b"}})
	if err != nil {
		t.Fatal(err)
	}
	req := s.create()
	if req.Driver != "bridge" || !req.Internal || req.EnableIPv6 == nil || *req.EnableIPv6 || req.Options[optionMTU] != "1400" || req.Labels["a"] != "b" {
		t.Errorf("create = %+v", req)
	}
	if req.IPAM == nil || len(req.IPAM.Config) != 1 || req.IPAM.Config[0].IPRange.String() != "10.9.0.128/25" || req.IPAM.Config[0].Gateway.String() != "10.9.0.1" {
		t.Errorf("IPAM = %+v", req.IPAM)
	}
	s, err = parseSettings(map[string]any{"driver": "macvlan", "parent": "eth0.10"})
	if err != nil || s.create().Options[optionParent] != "eth0.10" || s.create().IPAM != nil {
		t.Errorf("a macvlan = %+v, %v", s.create(), err)
	}
}

// live is a bridge network as Docker 29 reports one.
func live() moby.Inspect {
	var n moby.Inspect
	n.Name, n.Driver = "appnet", "bridge"
	n.IPAM.Config = []moby.IPAMConfig{{Subnet: netip.MustParsePrefix("10.9.0.0/24"), Gateway: netip.MustParseAddr("10.9.0.1")}}
	n.Options = map[string]string{"com.docker.network.enable_ipv4": "true", "com.docker.network.enable_ipv6": "false", optionMTU: "1400"}
	n.Labels = map[string]string{"a": "b", "extra": "x"}
	return n
}

// TestSettings_Differences: only declared settings are compared, and each
// difference is named.
func TestSettings_Differences(t *testing.T) {
	same, _ := parseSettings(map[string]any{"subnet": "10.9.0.0/24", "mtu": 1400, "labels": map[string]any{"a": "b"}})
	if d := same.differences(live()); len(d) != 0 {
		t.Errorf("a matching declaration differs: %v", d)
	}
	bare, _ := parseSettings(map[string]any{})
	if d := bare.differences(live()); len(d) != 0 {
		t.Errorf("a declaration leaving everything to the engine differs: %v", d)
	}
	other, err := parseSettings(map[string]any{"subnet": "10.9.0.0/24", "gateway": "10.9.0.254", "mtu": 9000, "enable_ipv6": true, "labels": map[string]any{"a": "c"}})
	if err != nil {
		t.Fatal(err)
	}
	ipvlan, err := parseSettings(map[string]any{"driver": "ipvlan"})
	if err != nil {
		t.Fatal(err)
	}
	d := strings.Join(append(other.differences(live()), ipvlan.differences(live())...), "\n")
	for _, want := range []string{"driver: bridge", "enable_ipv6: false", "gateway: 10.9.0.1", "mtu: \"1400\"", "labels: a is \"b\""} {
		if !strings.Contains(d, want) {
			t.Errorf("differences lack %q:\n%s", want, d)
		}
	}
	missing, _ := parseSettings(map[string]any{"subnet": "10.8.0.0/24"})
	if d := missing.differences(live()); len(d) != 1 || !strings.Contains(d[0], "[10.9.0.0/24]") {
		t.Errorf("another subnet = %v", d)
	}
}

// TestRecreateParams: an undo carries what a create can set and says when
// that is not everything.
func TestRecreateParams(t *testing.T) {
	p, partial := recreateParams(live())
	if partial || p["subnet"] != "10.9.0.0/24" || p["gateway"] != "10.9.0.1" || p["mtu"] != 1400 || p["driver"] != "bridge" {
		t.Errorf("recreateParams = %v, partial %t", p, partial)
	}
	if _, err := parseSettings(p); err != nil {
		t.Errorf("the undo's own parameters are refused: %v", err)
	}
	n := live()
	n.Options["com.docker.network.bridge.name"] = "br-app"
	if _, partial := recreateParams(n); !partial {
		t.Error("an option no parameter sets is not partial")
	}
	n = live()
	n.IPAM.Config = append(n.IPAM.Config, moby.IPAMConfig{Subnet: netip.MustParsePrefix("fd00::/64")})
	if _, partial := recreateParams(n); !partial {
		t.Error("a second subnet is not partial")
	}
}

// TestTextList: a runbook's list and an in-process undo's both read.
func TestTextList(t *testing.T) {
	for _, raw := range []any{[]any{"a"}, []string{"a"}} {
		if got, err := textList(map[string]any{"k": raw}, "k"); err != nil || len(got) != 1 || got[0] != "a" {
			t.Errorf("textList(%#v) = %v, %v", raw, got, err)
		}
	}
	if _, err := textList(map[string]any{"k": []any{1}}, "k"); err == nil {
		t.Error("a number was read as text")
	}
}
