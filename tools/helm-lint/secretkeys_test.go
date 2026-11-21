// Tests for the Secret key rule, on synthetic renders.
package main

import (
	"strings"
	"testing"
)

// fromChartSecret is name read from the chart's Secret under its own name.
func fromChartSecret(name string) envVar {
	return envVar{Name: name, ValueFrom: &envVarSource{SecretKeyRef: &secretKeyRef{Name: "r-the-pleiades", Key: name}}}
}

// TestCheckSecretKeys covers a key the controller reads that its Secret
// lacks, a key a profile sets that the controller never reads, a reference
// to a Secret the operator manages, and a render with no controller.
func TestCheckSecretKeys(t *testing.T) {
	const previous = "PLEIADES_FACTS_DIGEST_KEY_PREVIOUS"
	cases := []struct {
		name      string
		objects   []manifest
		reads     []string
		wantFound string
	}{
		{"a key the Secret carries and the controller reads", tokenRender([]envVar{fromChartSecret(previous)}, previous), []string{previous}, ""},
		{"a key read from a Secret that lacks it", tokenRender([]envVar{fromChartSecret(previous)}), nil, "which does not carry it"},
		{"a key the profile sets that nothing reads", tokenRender(nil, previous), []string{previous}, "does not read " + previous},
		{"a key read under another name", tokenRender([]envVar{{Name: "OTHER", ValueFrom: &envVarSource{SecretKeyRef: &secretKeyRef{Name: "r-the-pleiades", Key: previous}}}}, previous), []string{previous}, "does not read " + previous},
		{"a Secret the operator manages", tokenRender([]envVar{{Name: previous, ValueFrom: &envVarSource{SecretKeyRef: &secretKeyRef{Name: "theirs", Key: previous}}}}), nil, ""},
		{"no controller at all", nil, []string{previous}, "checked nothing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkSecretKeys("p", tc.objects, tc.reads)
			if tc.wantFound == "" {
				if len(got) != 0 {
					t.Fatalf("a correct render produced findings: %v", got)
				}
				return
			}
			if len(got) == 0 || !strings.Contains(got[0].message, tc.wantFound) {
				t.Fatalf("findings = %v, want one saying %q", got, tc.wantFound)
			}
		})
	}
}
