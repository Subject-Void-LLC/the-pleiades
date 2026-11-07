//go:build unix

// Package loader: a program agreed at a deprecated protocol warns once.
package loader

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// TestValidateCandidates_ADeprecatedProtocolWarnsOncePerProgram proves a
// program agreed at a deprecated version loads with exactly one warning that
// names the release removing it, and one at a supported version with none.
func TestValidateCandidates_ADeprecatedProtocolWarnsOncePerProgram(t *testing.T) {
	old := candidate{path: "/c/old", desc: external.Description{Protocol: 1},
		protocol: external.Protocol{Version: 1, Status: external.ProtocolDeprecated, RemovedIn: "0.6.0"}}
	current := candidate{path: "/c/new", desc: external.Description{Protocol: 1},
		protocol: external.Protocol{Version: 1, Status: external.ProtocolSupported}}
	warnings, problems := validateCandidates([]candidate{old, current}, "0.4.0", reservedNamespaces())
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "program /c/old speaks protocol 1, which is deprecated and removed in 0.6.0") {
		t.Errorf("warnings %q, want one naming /c/old and 0.6.0", warnings)
	}
}
