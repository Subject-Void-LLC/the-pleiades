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

// TestMask_AnXMLAnswerStaysWellFormedOrIsRefused: masking an XML answer that
// would leave it broken is refused rather than returned.
func TestMask_AnXMLAnswerStaysWellFormedOrIsRefused(t *testing.T) {
	c := &Client{secrets: map[string]string{"password": "mtu"}}
	if got, err := c.Mask(`<tunnel><mtu>1400</mtu><description>mtu set</description></tunnel>`); err != nil {
		t.Errorf("Mask = %q, %v; renaming both tags keeps the document whole", got, err)
	}
	// A credential that spans markup, replaced whole, would cut the elements
	// apart.
	c = &Client{secrets: map[string]string{"password": "</mtu><desc>"}}
	if got, err := c.Mask(`<tunnel><mtu>1400</mtu><desc>x</desc></tunnel>`); err == nil || got != "" {
		t.Errorf("Mask = %q, %v; want it refused and none of the answer", got, err)
	}
}
