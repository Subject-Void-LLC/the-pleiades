// Tests that MaskChecked masks as MaskFound does and refuses a line that
// would still hold a secret after it.
package netcfg_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// TestMaskChecked_MasksAsMaskFoundDoes: a configuration whose every secret
// line holds one secret comes back exactly as MaskFound masks it, with the
// same secrets found, and none of the secrets in the text.
func TestMaskChecked_MasksAsMaskFoundDoes(t *testing.T) {
	config := strings.Join([]string{
		"hostname r1",
		"enable secret 9 $9$enableSecretValue",
		"username ops privilege 15 secret 9 $9$userSecretValue",
		"crypto isakmp key IsakmpKey123 address 10.0.0.9",
		"snmp-server community CommunityRO1 RO",
		"crypto ikev2 keyring kr",
		" peer branch",
		"  pre-shared-key local LocalPsk123",
		"ip ssh server algorithm authentication password",
		"end",
	}, "\n")
	masked, found, err := netcfg.MaskChecked(config)
	if err != nil {
		t.Fatalf("MaskChecked: %v", err)
	}
	wantMasked, wantFound := netcfg.MaskFound(config)
	if masked != wantMasked || strings.Join(found, "|") != strings.Join(wantFound, "|") {
		t.Errorf("MaskChecked differs from MaskFound:\n%s\nfound %d, want %d", masked, len(found), len(wantFound))
	}
	for _, secret := range []string{"$9$enableSecretValue", "$9$userSecretValue", "IsakmpKey123", "CommunityRO1", "LocalPsk123"} {
		if strings.Contains(masked, secret) {
			t.Errorf("a secret survived masking (%d characters)", len(secret))
		}
	}
}

// TestMaskChecked_RefusesALineWithASecondSecret: MaskFound masks the first
// secret on a line and stops, so a line holding two is refused, naming its
// number and the keyword before the second secret, never either value.
func TestMaskChecked_RefusesALineWithASecondSecret(t *testing.T) {
	config := "hostname r1\nradius-server host 10.0.0.1 key 7 FirstSecret123 pac key 7 SecondSecret456\nend"
	masked, found, err := netcfg.MaskChecked(config)
	if err == nil {
		t.Fatalf("a line with two secrets was returned: %d bytes", len(masked))
	}
	if masked != "" || found != nil {
		t.Errorf("a refusal returned text or secrets alongside its error")
	}
	if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), `"key"`) {
		t.Errorf("err = %v, want it to name line 2 and the keyword", err)
	}
	if strings.Contains(err.Error(), "FirstSecret123") || strings.Contains(err.Error(), "SecondSecret456") {
		t.Error("the refusal quoted a secret")
	}
}
