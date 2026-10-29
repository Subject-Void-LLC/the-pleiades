// Tests of MaskXML on key chains: secrecy passes from a secret-named element
// to every descendant, so RFC 8177 ietf-key-chain's keystring and
// hexadecimal-string, two levels under <key>, and OpenConfig's keychain and
// MACsec keys are masked, while each key's id, algorithm and lifetime stay
// readable.
package netcfg_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// rfc8177KeyChain is a key chain in RFC 8177's ietf-key-chain shape
// (key-chains/key-chain/key/key-string/keystring or hexadecimal-string),
// written from the RFC's tree diagram rather than captured from a device.
const rfc8177KeyChain = `<key-chains xmlns="urn:ietf:params:xml:ns:yang:ietf-key-chain">
  <key-chain>
    <name>bgp-keys</name>
    <description>peer keys</description>
    <key>
      <key-id>1</key-id>
      <lifetime><send-accept-lifetime><start-date-time>2026-01-01T00:00:00Z</start-date-time><no-end-time/></send-accept-lifetime></lifetime>
      <crypto-algorithm>hmac-sha-256</crypto-algorithm>
      <key-string><keystring>rfc8177-ascii-key</keystring></key-string>
    </key>
    <key>
      <key-id>2</key-id>
      <crypto-algorithm>aes-cmac-prf-128</crypto-algorithm>
      <key-string><hexadecimal-string>0a:1b:2c:3d:4e:5f:60:71</hexadecimal-string></key-string>
    </key>
  </key-chain>
</key-chains>`

// openConfigKeys holds OpenConfig's keychain (keys/key/config/secret-key)
// and MACsec MKA (mka-key/config/key-clear-text) shapes, written from the
// openconfig-keychain and openconfig-macsec models rather than captured.
const openConfigKeys = `<root>
  <keychains xmlns="http://openconfig.net/yang/oc-keychain"><keychain><name>KC1</name><config><name>KC1</name></config>
    <keys><key><key-id>7</key-id><config><key-id>7</key-id><secret-key>oc-keychain-secret</secret-key><crypto-algorithm>HMAC_SHA_256</crypto-algorithm></config>
      <send-lifetime><config><start-time>1700000000</start-time><end-time>1800000000</end-time></config></send-lifetime>
    </key></keys>
  </keychain></keychains>
  <macsec xmlns="http://openconfig.net/yang/macsec"><mka><key-chains><key-chain><name>MK1</name><mka-keys><mka-key><id>0102</id>
    <config><id>0102</id><key-clear-text>oc-mka-cak-0123456789</key-clear-text><cryptographic-algorithm>AES_128_CMAC</cryptographic-algorithm><valid-date-time>VALID_IMMEDIATELY</valid-date-time></config>
  </mka-key></mka-keys></key-chain></key-chains></mka></macsec>
</root>`

func TestMaskXML_MasksKeyChainKeysAtAnyDepth(t *testing.T) {
	cases := map[string]struct {
		doc            string
		secrets, kept  []string
		placeholderFor string
	}{
		"rfc 8177": {
			doc:     rfc8177KeyChain,
			secrets: []string{"rfc8177-ascii-key", "0a:1b:2c:3d:4e:5f:60:71"},
			kept: []string{"<name>bgp-keys</name>", "<key-id>1</key-id>", "<key-id>2</key-id>",
				"<crypto-algorithm>hmac-sha-256</crypto-algorithm>", "<start-date-time>2026-01-01T00:00:00Z</start-date-time>",
				"<description>peer keys</description>"},
			placeholderFor: "rfc8177-ascii-key",
		},
		"openconfig": {
			doc:     openConfigKeys,
			secrets: []string{"oc-keychain-secret", "oc-mka-cak-0123456789"},
			kept: []string{"<name>KC1</name>", "<key-id>7</key-id>", "<crypto-algorithm>HMAC_SHA_256</crypto-algorithm>",
				"<start-time>1700000000</start-time>", "<id>0102</id>", "<name>MK1</name>",
				"<cryptographic-algorithm>AES_128_CMAC</cryptographic-algorithm>"},
			placeholderFor: "oc-mka-cak-0123456789",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			masked, found, err := netcfg.MaskXML(tc.doc)
			if err != nil {
				t.Fatalf("MaskXML() error = %v", err)
			}
			for _, s := range tc.secrets {
				if strings.Contains(masked, s) {
					t.Errorf("the masked copy still holds %q:\n%s", s, masked)
				}
			}
			for _, k := range tc.kept {
				if !strings.Contains(masked, k) {
					t.Errorf("the masked copy lost %q:\n%s", k, masked)
				}
			}
			if !strings.Contains(masked, netcfg.Placeholder(tc.placeholderFor)) {
				t.Errorf("%q was not replaced by its placeholder:\n%s", tc.placeholderFor, masked)
			}
			for _, s := range tc.secrets {
				if !slices.Contains(found, s) {
					t.Errorf("found = %q, want %q among them", found, s)
				}
			}
		})
	}
}

func TestMaskXML_SNMPCommunityNameStaysMaskedDeep(t *testing.T) {
	// An SNMP community keyed by name, one level further down than the
	// community element: the name is the community string, so the identifier
	// exception never applies inside SNMP.
	doc := `<snmp><community><config><name>deep-comm-123</name></config></community></snmp>`
	masked, _, err := netcfg.MaskXML(doc)
	if err != nil {
		t.Fatalf("MaskXML() error = %v", err)
	}
	if strings.Contains(masked, "deep-comm-123") {
		t.Fatalf("an SNMP community name survived:\n%s", masked)
	}
}
