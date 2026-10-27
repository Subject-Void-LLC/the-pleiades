// Tests that --set refuses the vars property, which is a mapping that one
// key=value cannot express and that lives in group_vars and host_vars.
package main

import (
	"strings"
	"testing"
)

// TestKeyValueList_RefusesTheVarsProperty: --set vars=... names where the
// variables belong, and an ordinary key is still taken.
func TestKeyValueList_RefusesTheVarsProperty(t *testing.T) {
	var list keyValueList
	err := list.Set("vars=site")
	if err == nil || !strings.Contains(err.Error(), "group_vars") {
		t.Fatalf("err = %v, want a refusal naming group_vars", err)
	}
	if err := list.Set("rack=r4"); err != nil || list.values["rack"] != "r4" {
		t.Errorf("an ordinary key was refused or lost: %v %v", err, list.values)
	}
}
