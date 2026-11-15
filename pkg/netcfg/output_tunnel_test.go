// Package netcfg: masking keeps a document's structure and leaves numbers
// alone, on the shape CVD run 11 recorded from a Catalyst 8000V (IOS XE
// 17.15.1a, 2026-11-15): a DMVPN Tunnel100 read over RESTCONF, whose GRE key
// is 100. Cisco-IOS-XE-tunnel declares that key "leaf key { type uint64
// { range 0..4294967295 } }", a number RFC 7951 writes as a JSON string, and
// netcfg's secret names keep "key" (right for crypto keys), so the leaf is
// masked in place. Before this test the value 100 was then replaced wherever
// it stood: the tunnel's name and NHRP network-id became bare placeholders
// (the JSON no longer parsed) and 10.255.100.1 became 10.255.<placeholder>.1.
package netcfg

import (
	"encoding/json"
	"strings"
	"testing"
)

// tunnelPSK is a fake IKEv2 pre-shared key, in the keyring the run wrote.
const tunnelPSK = "Dmvpn-PSK-2026-run11"

// tunnel100JSON is the run's Tunnel100 as RESTCONF read it, with the keyring
// beside it so a real secret shares the document.
const tunnel100JSON = `{
  "Cisco-IOS-XE-native:Tunnel": [
    {
      "name": 100,
      "description": "DMVPN spoke, single cloud, both hubs",
      "ip": {
        "address": {"primary": {"address": "10.255.100.11", "mask": "255.255.255.0"}},
        "Cisco-IOS-XE-nhrp:nhrp-v4": {
          "nhrp": {
            "nhs": {"ipv4": [
              {"ipv4": "10.255.100.1", "nbma": {"ipv4": [{"ipv4": "198.51.100.2"}]}},
              {"ipv4": "10.255.100.2", "nbma": {"ipv4": [{"ipv4": "198.51.100.6"}]}}
            ]},
            "network-id": 100
          }
        }
      },
      "Cisco-IOS-XE-tunnel:tunnel": {
        "source": "GigabitEthernet2",
        "key": "100",
        "protection": {"Cisco-IOS-XE-crypto:ipsec": {"profile-option": {"name": "DMVPN-IPSEC"}}}
      }
    }
  ],
  "Cisco-IOS-XE-crypto:keyring": [
    {"name": "DMVPN-KR", "peer": [{"name": "ANY", "pre-shared-key": {"key": "` + tunnelPSK + `"}}]}
  ]
}
`

// tunnel100XML is the same configuration as NETCONF reads it.
const tunnel100XML = `<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">
  <interface>
    <Tunnel>
      <name>100</name>
      <ip><address><primary><address>10.255.100.11</address><mask>255.255.255.0</mask></primary></address>
        <nhrp xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-nhrp"><network-id>100</network-id>
          <nhs><ipv4><ipv4>10.255.100.1</ipv4><nbma><ipv4><ipv4>198.51.100.2</ipv4></ipv4></nbma></ipv4></nhs></nhrp></ip>
      <tunnel xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-tunnel"><source>GigabitEthernet2</source><key>100</key></tunnel>
    </Tunnel>
  </interface>
  <crypto><ikev2 xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-crypto"><keyring><name>DMVPN-KR</name>
    <peer><name>ANY</name><pre-shared-key><key>` + tunnelPSK + `</key></pre-shared-key></peer></keyring></ikev2></crypto>
</native>
`

// TestMaskOutput_ATunnelKeyIsMaskedInPlaceAndNowhereElse: the GRE key leaf is
// masked as a whole value, every address, name and number around it is kept,
// the JSON still parses, the pre-shared key is masked, and only the
// pre-shared key is learned for the run's by-value set.
func TestMaskOutput_ATunnelKeyIsMaskedInPlaceAndNowhereElse(t *testing.T) {
	for name, out := range map[string]string{"restconf-json": tunnel100JSON, "netconf-xml": tunnel100XML} {
		masked, found, err := MaskOutput(out)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name == "restconf-json" {
			if !json.Valid([]byte(masked)) {
				t.Errorf("%s: the masked document is no longer JSON:\n%s", name, masked)
			}
			for _, kept := range []string{`"name": 100,`, `"network-id": 100`, `"key": "` + Placeholder("100") + `"`} {
				if !strings.Contains(masked, kept) {
					t.Errorf("%s: %s is not in the masked document:\n%s", name, kept, masked)
				}
			}
		} else if !strings.Contains(masked, "<name>100</name>") || !strings.Contains(masked, "<key>"+Placeholder("100")+"</key>") {
			t.Errorf("%s: the tunnel's name or its masked key is not where it stood:\n%s", name, masked)
		}
		for _, kept := range []string{"10.255.100.11", "10.255.100.1", "198.51.100.2", "GigabitEthernet2"} {
			if !strings.Contains(masked, kept) {
				t.Errorf("%s: %s was garbled:\n%s", name, kept, masked)
			}
		}
		if strings.Contains(masked, tunnelPSK) {
			t.Errorf("%s: the pre-shared key is still in the output", name)
		}
		if learn := Learnable(found); len(learn) != 1 || learn[0] != tunnelPSK {
			t.Errorf("%s: Learnable(%q) = %q, want the pre-shared key alone", name, found, learn)
		}
	}
}

// TestLearnable_NeverANumber: a number found under a secret name (a GRE key
// at its range's top, an SNMP engine boot count) is masked where it stands
// and never by value, however long: replaced wherever it appears, its digits
// would cut addresses, interface names and JSON numbers apart.
func TestLearnable_NeverANumber(t *testing.T) {
	if got := Learnable([]string{"4294967295", "12345678", "-12345678", "Key-12345678"}); len(got) != 1 || got[0] != "Key-12345678" {
		t.Errorf("Learnable = %q, want the numbers left out", got)
	}
}

// TestMaskOutput_AJSONNumberUnderASecretNameIsMaskedWhole: a secret leaf
// written as a JSON number is masked in place as a whole value, and the
// document still parses.
func TestMaskOutput_AJSONNumberUnderASecretNameIsMaskedWhole(t *testing.T) {
	masked, _, err := MaskOutput(`{"tunnel": {"key": 4294967295, "mtu": 4294967295}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(masked)) || !strings.Contains(masked, `"key": "`+Placeholder("4294967295")+`"`) || !strings.Contains(masked, `"mtu": 4294967295`) {
		t.Errorf("masked = %s, want the key alone masked, as a string, in valid JSON", masked)
	}
}

// TestMaskOutput_ALongSecretInAnotherLeafIsStillMasked: a secret a secret
// leaf holds, long enough to learn, is still masked inside any other leaf of
// the same document that repeats it, as the whole-text replacement did.
func TestMaskOutput_ALongSecretInAnotherLeafIsStillMasked(t *testing.T) {
	out := `{"pre-shared-key": {"key": "` + tunnelPSK + `"}, "description": "psk ` + tunnelPSK + ` set by hand"}`
	masked, _, err := MaskOutput(out)
	if err != nil || strings.Contains(masked, tunnelPSK) || !json.Valid([]byte(masked)) || !strings.Contains(masked, `"description": "psk `+Placeholder(tunnelPSK)+` set by hand"`) {
		t.Errorf("masked = %s, %v", masked, err)
	}
}

// TestMaskOutput_ATunnelKeyInCLITextLeavesTheAddresses: net.ios.command's
// "show running-config interface Tunnel100" reads as CLI text, masked line by
// line (the text masker reads a small number after "key" as a key id and
// keeps it), so nothing it finds reaches the addresses, while the pre-shared
// key is masked.
func TestMaskOutput_ATunnelKeyInCLITextLeavesTheAddresses(t *testing.T) {
	cli := "interface Tunnel100\n ip address 10.255.100.11 255.255.255.0\n ip nhrp network-id 100\n" +
		" ip nhrp nhs 10.255.100.1 nbma 198.51.100.2 multicast\n tunnel key 100\n" +
		"crypto ikev2 keyring DMVPN-KR\n peer ANY\n  pre-shared-key " + tunnelPSK + "\n"
	masked, found, err := MaskOutput(cli)
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"interface Tunnel100", "10.255.100.11", "network-id 100", "nhs 10.255.100.1 nbma 198.51.100.2"} {
		if !strings.Contains(masked, kept) {
			t.Errorf("%q was garbled:\n%s", kept, masked)
		}
	}
	if strings.Contains(masked, tunnelPSK) {
		t.Errorf("the pre-shared key is still in the output:\n%s", masked)
	}
	for _, v := range Learnable(found) {
		if v != tunnelPSK {
			t.Errorf("Learnable(%q) holds %q", found, v)
		}
	}
}
