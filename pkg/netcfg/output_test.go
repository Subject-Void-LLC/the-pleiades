// Package netcfg: tests of MaskOutput against the shapes IOS XE prints its
// configuration in (CLI text, "| format netconf-xml", "| format
// restconf-json"), with fake secrets in the places a Catalyst 8000V (17.15.1a)
// put real ones.
package netcfg

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	fakeEnableHash = "$9$bYdpvIoh3.LxcE$FDAtrfHEgep2vQ4uXHB3BGYLaEWljmR7K2cWDBZ0isE"
	fakeUserHash   = "$9$RLTXhZqoBxma0o$aaJlIdkbiUybkdQk8mLLEoFHRzzzzzzzzzzzzzzzzzz"
	fakePSK        = "Gx-psk-0123456789ab"
)

// formatNetconfXML is the shape of "show running-config | format netconf-xml".
const formatNetconfXML = `<config xmlns="http://tail-f.com/ns/config/1.0">
  <native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">
    <hostname>C8K</hostname>
    <enable>
      <secret>
        <type>9</type>
        <secret>` + fakeEnableHash + `</secret>
      </secret>
    </enable>
    <username>
      <name>netops</name>
      <privilege>15</privilege>
      <secret>
        <encryption>9</encryption>
        <secret>` + fakeUserHash + `</secret>
      </secret>
    </username>
    <crypto>
      <ikev2 xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-crypto">
        <keyring>
          <name>KR</name>
          <peer>
            <name>ALL</name>
            <pre-shared-key>
              <key>` + fakePSK + `</key>
            </pre-shared-key>
          </peer>
        </keyring>
      </ikev2>
    </crypto>
  </native>
</config>
`

// formatRestconfJSON is the shape of "show running-config | format restconf-json".
const formatRestconfJSON = `{
  "Cisco-IOS-XE-native:native": {
    "hostname": "C8K",
    "enable": {
      "secret": {
        "type": "9",
        "secret": "` + fakeEnableHash + `"
      }
    },
    "username": [
      {
        "name": "netops",
        "privilege": 15,
        "secret": {
          "encryption": "9",
          "secret": "` + fakeUserHash + `"
        }
      }
    ],
    "crypto": {
      "Cisco-IOS-XE-crypto:ikev2": {
        "keyring": [
          {"name": "KR", "peer": [{"name": "ALL", "pre-shared-key": {"key": "` + fakePSK + `"}}]}
        ]
      }
    }
  }
}
`

func TestMaskOutput_MasksEverySecretInEachForm(t *testing.T) {
	cli := "hostname C8K\nenable secret 9 " + fakeEnableHash + "\nusername netops privilege 15 secret 9 " + fakeUserHash + "\n" +
		"crypto ikev2 keyring KR\n peer ALL\n  pre-shared-key " + fakePSK + "\n"
	for name, out := range map[string]string{"cli": cli, "netconf-xml": formatNetconfXML, "restconf-json": formatRestconfJSON} {
		masked, found, err := MaskOutput(out)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, secret := range []string{fakeEnableHash, fakeUserHash, fakePSK} {
			if strings.Contains(masked, secret) {
				t.Errorf("%s: %q is still in the output", name, secret)
			}
		}
		if len(found) < 3 {
			t.Errorf("%s: found %d secrets, want the three for the run's masking set", name, len(found))
		}
		for _, kept := range []string{"C8K", "netops"} {
			if !strings.Contains(masked, kept) {
				t.Errorf("%s: %q was masked, and it is no secret", name, kept)
			}
		}
	}
}

func TestMaskOutput_KeepsTheLayoutAndTheMetadata(t *testing.T) {
	masked, _, err := MaskOutput(formatRestconfJSON)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal([]byte(masked), &doc); err != nil {
		t.Fatalf("the masked JSON does not parse: %v", err)
	}
	if strings.Count(masked, "\n") != strings.Count(formatRestconfJSON, "\n") || !strings.Contains(masked, `"type": "9"`) || !strings.Contains(masked, `"encryption": "9"`) {
		t.Errorf("the layout or an encryption type changed:\n%s", masked)
	}
	xml, _, err := MaskOutput(formatNetconfXML)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, "<type>9</type>") || !strings.Contains(xml, "<name>KR</name>") {
		t.Errorf("an encryption type or a keyring name was masked:\n%s", xml)
	}
}

func TestMaskOutput_ACryptValueIsMaskedWhereverItStands(t *testing.T) {
	out := "Last hash seen: " + fakeUserHash + " (from a log line no word names)\nprice $1$ is not one\n"
	masked, _, err := MaskOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(masked, fakeUserHash) {
		t.Errorf("a crypt-shaped value with no secret word before it was kept:\n%s", masked)
	}
	if !strings.Contains(masked, "price $1$ is not one") {
		t.Errorf("text that only starts like a crypt value was masked:\n%s", masked)
	}
}

func TestMaskOutput_ASecretUnderMetadataInJSONIsStillFound(t *testing.T) {
	out := `{"mode": {"password": "` + fakePSK + `"}}`
	masked, _, err := MaskOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(masked, fakePSK) {
		t.Errorf("a password under a metadata-named field was kept: %s", masked)
	}
}

func TestMaskOutput_FailsClosedOnStructuredTextItCannotRead(t *testing.T) {
	for name, out := range map[string]string{
		"broken XML":  "<config><secret>" + fakePSK + "</secret>",
		"broken JSON": `{"secret": "` + fakePSK + `"`,
	} {
		masked, found, err := MaskOutput(out)
		if err == nil || masked != "" || found != nil {
			t.Errorf("%s: masked=%q found=%v err=%v; want an error and none of the text", name, masked, found, err)
		}
		if err != nil && strings.Contains(err.Error(), fakePSK) {
			t.Errorf("%s: the error quotes the secret: %v", name, err)
		}
	}
}

// TestMaskFound_ReadsCLITextOnly is the control: the CLI text masker, which
// net.ios.command and net.cli.command used for their output before
// MaskOutput, leaves the hashes of both structured forms in place.
func TestMaskFound_ReadsCLITextOnly(t *testing.T) {
	for name, out := range map[string]string{"netconf-xml": formatNetconfXML, "restconf-json": formatRestconfJSON} {
		if masked, _ := MaskFound(out); !strings.Contains(masked, fakeEnableHash) {
			t.Errorf("%s: MaskFound masked the hash, so this control no longer shows the gap MaskOutput closes", name)
		}
	}
}
