// Package restconf: the client's value masking keeps a structured answer
// structured. A credential short enough to be masked only as a whole word
// still stands as one inside a JSON number, and replacing it there with a
// bare placeholder left the answer no longer JSON (CVD run 11's shape, with a
// credential in place of the GRE key).
package restconf

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// TestMask_AShortCredentialInAJSONAnswerLeavesItJSON: the credential is
// masked in the message that echoes it and as the whole number it equals, a
// number that only holds its digits is kept, and the answer still parses.
func TestMask_AShortCredentialInAJSONAnswerLeavesItJSON(t *testing.T) {
	c := &Client{secrets: map[string]string{"username": "admin", "password": "1234"}}
	answer := `{"Cisco-IOS-XE-native:Tunnel": [{"name": 1234, "mtu": 12345, "ip": {"address": {"primary": {"address": "10.255.1234.1"}}}}],` +
		` "ietf-restconf:errors": {"error": [{"error-message": "login 1234 refused"}]}}`
	got, err := c.Mask(answer)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("the masked answer is no longer JSON: %s", got)
	}
	for _, want := range []string{`"name": "` + netcfg.Placeholder("1234") + `"`, `"mtu": 12345`, `"error-message": "login ` + netcfg.Placeholder("1234") + ` refused"`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is not in the masked answer: %s", want, got)
		}
	}
	if strings.Contains(got, "10.255.1234.1") {
		t.Errorf("the address holding the credential as a word was kept: %s", got)
	}
}

// TestMask_AnXMLAnswerIsMaskedByItsDecodedText: a credential the device
// escaped as &apos; and &quot;, or as numeric references, is found in the
// text it decodes to and written back escaped; element names and markup are
// never replaced, so a credential that only matches markup leaves the answer
// as it was; and one in an attribute is refused with none of the answer.
func TestMask_AnXMLAnswerIsMaskedByItsDecodedText(t *testing.T) {
	const password = `pa'ss"word1`
	c := &Client{secrets: map[string]string{"username": "admin", "password": password}}
	answer := `<errors xmlns="urn:ietf:params:xml:ns:yang:ietf-restconf"><error><error-message>bad pa&apos;ss&quot;word1</error-message>` +
		`<error-info>bad pa&#39;ss&#x22;word1</error-info></error></errors>`
	got, err := c.Mask(answer)
	if err != nil {
		t.Fatal(err)
	}
	if !netcfg.WellFormedXML(got) || strings.Contains(got, "word1") || strings.Count(got, netcfg.Placeholder(password)) != 2 {
		t.Errorf("Mask = %s, want both escapings masked and the answer well-formed", got)
	}
	c = &Client{secrets: map[string]string{"password": "mtu"}}
	if got, err := c.Mask(`<tunnel><mtu>1400</mtu><description>mtu set</description></tunnel>`); err != nil || !strings.Contains(got, "<mtu>1400</mtu>") || !strings.Contains(got, netcfg.Placeholder("mtu")+" set") {
		t.Errorf("Mask = %q, %v; want the element kept and the word in the text masked", got, err)
	}
	c = &Client{secrets: map[string]string{"password": "</mtu><desc>"}}
	const markup = `<tunnel><mtu>1400</mtu><desc>x</desc></tunnel>`
	if got, err := c.Mask(markup); err != nil || got != markup {
		t.Errorf("Mask = %q, %v; want markup that only looks like the credential left as it was", got, err)
	}
	c = &Client{secrets: map[string]string{"password": password}}
	if got, err := c.Mask(`<error name="pa&apos;ss&quot;word1"/>`); err == nil || got != "" {
		t.Errorf("Mask = %q, %v; want a credential in an attribute refused", got, err)
	}
}
