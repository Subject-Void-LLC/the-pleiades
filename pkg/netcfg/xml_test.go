// Tests of MaskXML: the secrets of IOS XE native, Junos, OpenConfig and IETF
// configurations are masked by the leaf that holds them, everything else is
// kept byte for byte, and what masking cannot vouch for is refused.
package netcfg_test

import (
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// iosXENative is an IOS XE native-model configuration in the shape a real
// get-config returns it, holding each kind of secret the model has.
const iosXENative = `<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">
  <hostname>edge-01</hostname>
  <enable><secret><type>9</type><secret>$9$abcdefghijklmn$opqrstuvwxyz0123456789ABCDEF</secret></secret></enable>
  <username><name>admin</name><privilege>15</privilege><password><encryption>0</encryption><password>cisco</password></password></username>
  <key><chain><name>OSPF-KC</name><key><id>1</id><key-string><key>chainsecret1</key></key-string></key></chain></key>
  <crypto><isakmp><key><key-address><key>isakmp-psk-77</key><addr4-container><address>192.0.2.1</address></addr4-container></key-address></key></isakmp></crypto>
  <router><bgp><id>65000</id><neighbor><id>192.0.2.2</id><remote-as>65001</remote-as><password><enctype>0</enctype><text>bgp-md5-pw</text></password></neighbor></bgp></router>
  <tacacs><server><name>T1</name><key><key>tacacs-shared</key></key></server></tacacs>
  <snmp-server><community-config><name>public-ro-9</name><permission>ro</permission></community-config></snmp-server>
  <interface><GigabitEthernet><name>1</name><description>uplink to core</description><ip><ospf><message-digest-key><id>1</id><md5><auth-key>ospf-md5-key</auth-key></md5></message-digest-key></ospf></ip></GigabitEthernet></interface>
</native>`

// iosXESecrets are iosXENative's secrets, each of which must be gone.
var iosXESecrets = []string{
	"$9$abcdefghijklmn$opqrstuvwxyz0123456789ABCDEF", ">cisco<", "chainsecret1", "isakmp-psk-77",
	"bgp-md5-pw", "tacacs-shared", "public-ro-9", "ospf-md5-key",
}

// iosXEKept are what iosXENative holds that is not a secret and must stay:
// names, addresses, the encryption types beside each secret.
var iosXEKept = []string{
	"<hostname>edge-01</hostname>", "<type>9</type>", "<name>admin</name>", "<encryption>0</encryption>",
	"<name>OSPF-KC</name>", "<id>1</id>", "<address>192.0.2.1</address>", "<enctype>0</enctype>",
	"<remote-as>65001</remote-as>", "<name>T1</name>", "<description>uplink to core</description>",
}

func TestMaskXML_MasksIOSXENativeSecretsAndKeepsTheRest(t *testing.T) {
	masked, found, err := netcfg.MaskXML(iosXENative)
	if err != nil {
		t.Fatalf("MaskXML() error = %v", err)
	}
	for _, s := range iosXESecrets {
		if strings.Contains(masked, s) {
			t.Errorf("the masked copy still holds %q:\n%s", s, masked)
		}
	}
	for _, k := range iosXEKept {
		if !strings.Contains(masked, k) {
			t.Errorf("the masked copy lost %q:\n%s", k, masked)
		}
	}
	if !slices.Contains(found, "isakmp-psk-77") || !slices.Contains(found, "cisco") {
		t.Errorf("found = %q, want each secret replaced", found)
	}
	if !strings.Contains(masked, "<auth-key>"+netcfg.Placeholder("ospf-md5-key")+"</auth-key>") {
		t.Errorf("auth-key was not replaced by its placeholder:\n%s", masked)
	}
	if got := tags(t, masked); !slices.Equal(got, tags(t, iosXENative)) {
		t.Errorf("the element structure changed:\n%q\n%q", got, tags(t, iosXENative))
	}
}

func TestMaskXML_MasksJunosAndOpenConfigSecrets(t *testing.T) {
	cases := map[string]struct{ doc, secret string }{
		"junos encrypted-password":   {`<configuration><system><root-authentication><encrypted-password>$6$salt$hashhashhash</encrypted-password></root-authentication></system></configuration>`, "$6$salt$hashhashhash"},
		"junos snmp community name":  {`<configuration><snmp><community><name>s3cr3t-comm</name><authorization>read-only</authorization></community></snmp></configuration>`, "s3cr3t-comm"},
		"junos reversible $9$":       {`<configuration><protocols><bgp><group><name>g</name><authentication-key>$9$Ab12Cd</authentication-key></group></bgp></protocols></configuration>`, "$9$Ab12Cd"},
		"crypt-shaped anywhere":      {`<a><note>$1$xyz$abc</note></a>`, "$1$xyz$abc"},
		"openconfig secret-key":      {`<system xmlns="http://openconfig.net/yang/system"><aaa><server-groups><server-group><servers><server><tacacs><config><secret-key>oc-tacacs-key</secret-key></config></tacacs></server></servers></server-group></server-groups></aaa></system>`, "oc-tacacs-key"},
		"openconfig password-hashed": {`<system><aaa><authentication><users><user><config><username>bob</username><password-hashed>hashedvalue</password-hashed></config></user></users></authentication></aaa></system>`, "hashedvalue"},
		"ietf snmp text-name":        {`<snmp xmlns="urn:ietf:params:xml:ns:yang:ietf-snmp"><community><index>c1</index><text-name>ietf-comm</text-name></community></snmp>`, "ietf-comm"},
		"cdata in a secret leaf":     {`<a><password><![CDATA[cdata-pw&]]></password></a>`, "cdata-pw"},
		"entity in a secret leaf":    {`<a><secret>amp&amp;secret</secret></a>`, "amp&amp;secret"},
		"cli text in a leaf":         {`<a><cli>hostname r1&#10;username admin secret 0 cli-secret-x&#10;</cli></a>`, "cli-secret-x"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			masked, _, err := netcfg.MaskXML(tc.doc)
			if err != nil {
				t.Fatalf("MaskXML() error = %v", err)
			}
			if strings.Contains(masked, tc.secret) {
				t.Fatalf("the secret %q survived:\n%s", tc.secret, masked)
			}
			if !strings.Contains(masked, netcfg.MaskPrefix) {
				t.Fatalf("nothing was masked:\n%s", masked)
			}
		})
	}
}

func TestMaskXML_KeepsANonSecretDocumentByteForByte(t *testing.T) {
	doc := "<?xml version=\"1.0\"?>\n<native xmlns=\"http://cisco.com/ns/yang/Cisco-IOS-XE-native\">\r\n  <hostname>a&amp;b</hostname>\n  <key-chain>OSPF-KC</key-chain><community-list>65000:100</community-list>\n</native>"
	masked, found, err := netcfg.MaskXML(doc)
	if err != nil {
		t.Fatalf("MaskXML() error = %v", err)
	}
	if masked != doc || len(found) != 0 {
		t.Fatalf("a document with no secret changed:\n%q\n%q, found %q", doc, masked, found)
	}
}

func TestMaskXML_MasksTheRunsKnownSecretsByValue(t *testing.T) {
	doc := `<a><description>login is deviceLoginPw1 here</description><short>pw</short></a>`
	masked, found, err := netcfg.MaskXML(doc, "deviceLoginPw1", "pw")
	if err != nil {
		t.Fatalf("MaskXML() error = %v", err)
	}
	if strings.Contains(masked, "deviceLoginPw1") || !slices.Contains(found, "deviceLoginPw1") {
		t.Fatalf("the known secret was not masked by value:\n%s (found %q)", masked, found)
	}
	// A value shorter than MinLearnedLength is masked by name only, as the
	// engine's masking set does, so an ordinary word is not scrubbed.
	if !strings.Contains(masked, "<short>pw</short>") {
		t.Fatalf("a short known value was masked by value:\n%s", masked)
	}
}

func TestMaskXML_WithholdsCommentsAndInstructions(t *testing.T) {
	doc := `<a><!-- enable secret hunter2 --><?app password=hunter2?><b>1</b></a>`
	masked, _, err := netcfg.MaskXML(doc)
	if err != nil {
		t.Fatalf("MaskXML() error = %v", err)
	}
	if strings.Contains(masked, "hunter2") || !strings.Contains(masked, "<b>1</b>") {
		t.Fatalf("a comment or instruction was kept:\n%s", masked)
	}
}

func TestMaskXML_FailsClosed(t *testing.T) {
	deep := strings.Repeat("<a>", netcfg.MaxXMLDepth+1) + strings.Repeat("</a>", netcfg.MaxXMLDepth+1)
	cases := map[string]string{
		"not well-formed":        `<a><password>leaked-pw</a>`,
		"truncated":              `<a><password>leaked-pw</password>`,
		"undefined entity":       `<a><b>&leaked;</b></a>`,
		"doctype":                `<!DOCTYPE a [<!ENTITY x "leaked">]><a>&x;</a>`,
		"secret attribute":       `<a password="leaked-pw"/>`,
		"crypt attribute":        `<a value="$6$leaked$x"/>`,
		"known secret attribute": `<a note="xx-leakedKnown-xx"/>`,
		"two secrets in a line":  `<a><cli>radius-server host 10.0.0.1 key 7 leakedOne123 pac key 7 leakedTwo456</cli></a>`,
		"too deep":               deep,
		"not utf-8":              "<a><b>\xff leaked</b></a>",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			masked, found, err := netcfg.MaskXML(doc, "leakedKnown")
			if err == nil {
				t.Fatalf("MaskXML() = %q, want a refusal", masked)
			}
			if masked != "" || found != nil {
				t.Fatalf("a refusal returned text %q or secrets %q", masked, found)
			}
			if strings.Contains(err.Error(), "leaked") {
				t.Fatalf("the refusal quotes the document: %v", err)
			}
		})
	}
}

func TestMaskXML_IsIdempotent(t *testing.T) {
	once, _, err := netcfg.MaskXML(iosXENative)
	if err != nil {
		t.Fatalf("MaskXML() error = %v", err)
	}
	twice, _, err := netcfg.MaskXML(once)
	if err != nil {
		t.Fatalf("MaskXML(masked) error = %v", err)
	}
	if twice != once {
		t.Fatalf("masking a masked copy changed it:\n%s\n%s", once, twice)
	}
}

// FuzzMaskXML: masking never panics; a masked copy reads as XML with the
// same elements in the same order, masks to itself, and holds no password
// leaf whose text is not a placeholder. A refusal returns no text.
func FuzzMaskXML(f *testing.F) {
	for _, s := range []string{iosXENative, `<a><password>x</password></a>`, `<a><!--c--><b>&amp;</b></a>`, `<a><cli>password 0 x</cli></a>`, `<a password="x"/>`, `<a><![CDATA[secret y]]></a>`, `text<a/>`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, doc string) {
		masked, _, err := netcfg.MaskXML(doc)
		if err != nil {
			if masked != "" {
				t.Fatalf("a refusal returned text: %q", masked)
			}
			return
		}
		if got, want := tagsOrNil(masked), tagsOrNil(doc); !slices.Equal(got, want) {
			t.Fatalf("the element structure changed:\n%q\n%q", want, got)
		}
		again, _, err := netcfg.MaskXML(masked)
		if err != nil || again != masked {
			t.Fatalf("masking a masked copy changed it (err %v):\n%q\n%q", err, masked, again)
		}
		d := xml.NewDecoder(strings.NewReader(masked))
		inPassword := false
		for {
			tok, err := d.Token()
			if err != nil {
				return
			}
			switch e := tok.(type) {
			case xml.StartElement:
				inPassword = strings.EqualFold(e.Name.Local, "password")
			case xml.EndElement:
				inPassword = false
			case xml.CharData:
				if v := strings.TrimSpace(string(e)); inPassword && v != "" && !strings.HasPrefix(v, netcfg.MaskPrefix) {
					t.Fatalf("a password survived: %q in\n%q", v, masked)
				}
			}
		}
	})
}

// tagsOrNil is tags for a fuzz target, which has no document it may fail
// on: nil when doc does not read.
func tagsOrNil(doc string) []string {
	d := xml.NewDecoder(strings.NewReader(doc))
	var out []string
	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		switch e := tok.(type) {
		case xml.StartElement:
			out = append(out, "<"+e.Name.Local)
		case xml.EndElement:
			out = append(out, "/"+e.Name.Local)
		}
	}
}

// tags lists doc's element names in order, start and end, for comparing
// structure.
func tags(t *testing.T, doc string) []string {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(doc))
	var out []string
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("reading %q: %v", doc, err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			out = append(out, "<"+e.Name.Local)
		case xml.EndElement:
			out = append(out, "/"+e.Name.Local)
		}
	}
}
