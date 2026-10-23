// Tests that RefuseMasked refuses exactly the text Mask has been through.
package netcfg_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// TestRefuseMasked: a configuration with a real secret passes, the same
// configuration masked is refused naming what it was and the placeholder's
// prefix, and the secret itself is never in the error.
func TestRefuseMasked(t *testing.T) {
	config := "interface eth1\n ip ospf authentication-key s3cret-key\n"
	if err := netcfg.RefuseMasked("the candidate", config); err != nil {
		t.Errorf("a configuration with its real secret was refused: %v", err)
	}
	err := netcfg.RefuseMasked("the candidate", netcfg.Mask(config))
	if err == nil || !strings.HasPrefix(err.Error(), "the candidate holds a masked secret ("+netcfg.MaskPrefix) || strings.Contains(err.Error(), "s3cret-key") {
		t.Errorf("a masked configuration: %v", err)
	}
}
