// Package yangfacts: the yang.* facts a gNMI read gives, always partial and
// with no digest a YANG library's could be compared with.
package yangfacts

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// TestGNMIValues_ArePartialAndCarryNoDigest builds the family from gNMI's
// modules, and holds it to the three keys gNMI records.
func TestGNMIValues_ArePartialAndCarryNoDigest(t *testing.T) {
	values, warnings, err := GNMIValues([]yanglib.Module{{Name: "openconfig-interfaces"}, {Name: "srl_nokia-interfaces", Revision: "2026-07-31"}}, nil)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("GNMIValues = %v, %v", warnings, err)
	}
	if values[FactCompleteness] != string(yanglib.Partial) || values[FactModuleCount] != 2 {
		t.Errorf("values = %v", values)
	}
	if got, _ := values[FactModules].([]string); !slices.Equal(got, []string{"openconfig-interfaces", "srl_nokia-interfaces@2026-07-31"}) {
		t.Errorf("yang.modules = %v", got)
	}
	for _, key := range []string{FactModuleDigest, FactDigestRule, FactContentID} {
		if _, ok := values[key]; ok {
			t.Errorf("%s recorded from a gNMI read", key)
		}
	}
	for key := range values {
		if !slices.Contains(GNMIKeys, key) {
			t.Errorf("%s is not one of GNMIKeys", key)
		}
	}
}

// TestGNMIValues_SayWhatWasLeftOut warns for the models naming no module,
// and for a list too long to keep, which is then kept by count alone.
func TestGNMIValues_SayWhatWasLeftOut(t *testing.T) {
	var many []yanglib.Module
	for i := 0; i < 4000; i++ {
		many = append(many, yanglib.Module{Name: fmt.Sprintf("vendor-model-with-a-long-name-%05d", i), Revision: "2026-01-01"})
	}
	values, warnings, err := GNMIValues(many, []string{"urn:bad name", "x/y", "a", "b", "c", "d"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := values[FactModules]; ok || values[FactModuleCount] != 4000 {
		t.Errorf("values = count %v, list present %v", values[FactModuleCount], ok)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "recorded by count only") || !strings.Contains(joined, "6 of the device's gNMI models") || strings.Contains(joined, `"d"`) {
		t.Errorf("warnings = %q, want the bound and five samples of six", joined)
	}
}

// TestGNMIReturns_DocumentEveryKey documents each key GNMIKeys names.
func TestGNMIReturns_DocumentEveryKey(t *testing.T) {
	returns := GNMIReturns("always")
	if len(returns) != len(GNMIKeys) {
		t.Fatalf("%d returns for %d keys", len(returns), len(GNMIKeys))
	}
	for i, r := range returns {
		if r.Name != GNMIKeys[i] || r.Description == "" || !strings.HasPrefix(r.Returned, "always") {
			t.Errorf("return %d = %+v", i, r)
		}
	}
}
