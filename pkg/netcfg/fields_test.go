// Tests for secret fields in structured output.
package netcfg_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// TestMaskFields_MasksSecretFieldsAtAnyDepth: a secret field's text is
// replaced at any depth, equal secrets alike, and every other value, a
// secret field holding a number among them, is kept.
func TestMaskFields_MasksSecretFieldsAtAnyDepth(t *testing.T) {
	in := map[string]any{
		"vrf": map[string]any{"default": map[string]any{
			"neighbor": map[string]any{
				"10.0.0.2": map[string]any{"password": "bgpPw", "remote_as": int64(65001)},
				"10.0.0.3": map[string]any{"password": "bgpPw"},
			},
		}},
		"snmp":  []any{map[string]any{"community": "commRO", "access": "RO"}},
		"count": map[string]any{"community_entries": int64(3)},
	}
	out := netcfg.MaskFields(in).(map[string]any)
	text := strings.Join([]string{reflectString(out)}, "")
	for _, secret := range []string{"bgpPw", "commRO"} {
		if strings.Contains(text, secret) {
			t.Fatalf("%q survived: %v", secret, out)
		}
	}
	n := out["vrf"].(map[string]any)["default"].(map[string]any)["neighbor"].(map[string]any)
	a, b := n["10.0.0.2"].(map[string]any), n["10.0.0.3"].(map[string]any)
	if a["password"] != b["password"] || !strings.HasPrefix(a["password"].(string), netcfg.MaskPrefix) || a["remote_as"] != int64(65001) {
		t.Fatalf("masked neighbors = %v, %v", a, b)
	}
	if out["snmp"].([]any)[0].(map[string]any)["access"] != "RO" || out["count"].(map[string]any)["community_entries"] != int64(3) {
		t.Fatalf("a value that is no secret changed: %v", out)
	}
	if !reflect.DeepEqual(in["count"], map[string]any{"community_entries": int64(3)}) || in["snmp"].([]any)[0].(map[string]any)["community"] != "commRO" {
		t.Fatal("MaskFields changed its input")
	}
}

// reflectString is v's text, for a test searching it.
func reflectString(v any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, e := range t {
				b.WriteString(k + " ")
				walk(e)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case string:
			b.WriteString(t + " ")
		}
	}
	walk(v)
	return b.String()
}
