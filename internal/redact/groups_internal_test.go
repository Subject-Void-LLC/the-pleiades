// The one fact redact restates from pkg/netcfg, held to it.
package redact

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// TestNetcfgPlaceholderPrefixIsNetcfgs: the placeholder prefix a group keeps
// is the one pkg/netcfg writes, so a change there cannot quietly turn every
// SNMP line a method masked into a second, uncomparable placeholder here.
func TestNetcfgPlaceholderPrefixIsNetcfgs(t *testing.T) {
	if netcfgPlaceholderPrefix != netcfg.MaskPrefix {
		t.Errorf("netcfgPlaceholderPrefix = %q, netcfg.MaskPrefix = %q", netcfgPlaceholderPrefix, netcfg.MaskPrefix)
	}
}
