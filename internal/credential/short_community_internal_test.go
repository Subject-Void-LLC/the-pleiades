// The short-community warning held to the masking it describes.
package credential

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// TestShortCommunityWarning_NamesOnlyWhatIsMasked: every shape the warning
// names masks a short community in a line of that shape, through the
// masking every output gets with no value known (redact.Text), and the
// output the warning says is not masked (show snmp community) is not: the
// warning says what is true of the ruleset, both ways.
func TestShortCommunityWarning_NamesOnlyWhatIsMasked(t *testing.T) {
	samples := map[string]string{
		"snmp-server community": "snmp-server community public RO",
		"snmp-server host":      "snmp-server host 10.0.0.1 traps version 2c public",
		"rocommunity":           "rocommunity public 10.0.0.0/24",
		"rwcommunity":           "rwcommunity public default",
		"com2sec":               "com2sec local localhost public",
		"trapsink":              "trapsink 10.0.0.1 public",
		"trap2sink":             "trap2sink 10.0.0.1 public",
		"informsink":            "informsink 10.0.0.1 public",
		"trapcommunity":         "trapcommunity public",
		"trapsess -c":           "trapsess -v 2c -c public 10.0.0.1",
		"authCommunity":         "authCommunity log,execute,net public",
	}
	warning := ShortCommunityWarning("old-switch")
	for _, shape := range shortCommunityShapes {
		sample, ok := samples[shape]
		if !ok {
			t.Errorf("the warning names %q, and this test has no line of that shape to hold it to", shape)
			continue
		}
		if masked := redact.Text(nil, sample); strings.Contains(masked, "public") {
			t.Errorf("the warning names %q, and %q is masked as %q", shape, sample, masked)
		}
		if !strings.Contains(warning, shape) {
			t.Errorf("the warning does not name %q", shape)
		}
	}
	if len(samples) != len(shortCommunityShapes) {
		t.Errorf("this test holds %d shapes and the warning names %d", len(samples), len(shortCommunityShapes))
	}
	shown := "Community name: public\nCommunity Index: public\nCommunity SecurityName: public"
	if masked := redact.Text(nil, shown); masked != shown || !strings.Contains(warning, "show snmp community") {
		t.Errorf("the warning says show snmp community is not masked, and it is masked as %q", masked)
	}
}
