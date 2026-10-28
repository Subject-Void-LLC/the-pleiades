// Tests that pleiades doc, asked about one of its own commands, points to that
// command's --help instead of calling it an unknown method.
package main

import (
	"strings"
	"testing"
)

func TestDoc_ACommandNamePointsToItsHelp(t *testing.T) {
	err := runDoc([]string{"add-secret"})
	if err == nil || !strings.Contains(err.Error(), "'pleiades add-secret --help'") {
		t.Fatalf("runDoc(add-secret) = %v, want it to point to the command's --help", err)
	}
	if err := runDoc([]string{"nope.nope"}); err == nil || !strings.Contains(err.Error(), "unknown fqcn") {
		t.Errorf("an unknown method: %v", err)
	}
}
