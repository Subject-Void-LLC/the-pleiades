package runbook_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/runbook"
)

// This file covers the runbook kind's definition rule.
//
// A runbook id is not a cosmetic field. It reaches a NATS subject by
// concatenation, which this repository has had to fix twice already
// (FAILURE_PATTERNS.md #18 and #81, where a `>` in an id streamed every
// job's logs), and it reaches a filesystem path through filepath.Join
// (#78). Both were fixed at their own boundaries. This is the boundary a
// saved template crosses, and it refuses the same class of value rather
// than trusting that every downstream guard is still in place.

func validate(t *testing.T, reference string) error {
	t.Helper()

	d, ok := launch.Lookup(runbook.Kind)
	if !ok {
		t.Fatalf("the %q kind is not registered, so nothing can launch one", runbook.Kind)
	}
	if d.ValidateDefinition == nil {
		t.Fatal("the runbook kind declares no definition rule, so any id would be accepted")
	}
	return d.ValidateDefinition(reference)
}

func TestRunbookID_RefusesWhatWouldReachAWildcardOrAPath(t *testing.T) {
	refused := map[string]string{
		"empty":            "",
		"blank":            "   ",
		"nats wildcard":    "patch>",
		"nats token wild":  "patch*edge",
		"subject dot":      "patch.edge",
		"path separator":   "../secrets",
		"windows path":     `..\secrets`,
		"null byte":        "patch\x00",
		"space":            "patch edge",
		"quote":            `patch"edge`,
		"unicode wildcard": "patch✱",
	}

	for name, reference := range refused {
		t.Run(name, func(t *testing.T) {
			if err := validate(t, reference); err == nil {
				t.Errorf("the runbook kind accepted %q", reference)
			}
		})
	}

	// Length is bounded too: an id becomes part of a subject name, and a
	// subject with no ceiling is a way to make the broker do arbitrary work
	// per dispatch.
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	if err := validate(t, string(long)); err == nil {
		t.Error("the runbook kind accepted a 300-character id")
	}
}

func TestRunbookID_AcceptsAnOrdinaryID(t *testing.T) {
	for _, reference := range []string{"patch-edge", "patch_edge", "PatchEdge2", "a"} {
		t.Run(reference, func(t *testing.T) {
			if err := validate(t, reference); err != nil {
				t.Errorf("the runbook kind refused %q: %v", reference, err)
			}
		})
	}
}

func TestRunbookKind_IsRegisteredWithTheNativeAdapter(t *testing.T) {
	d, ok := launch.Lookup(runbook.Kind)
	if !ok {
		t.Fatalf("the %q kind is not registered", runbook.Kind)
	}
	if d.Adapter != runbook.Adapter {
		t.Errorf("the runbook kind runs on adapter %q, want %q", d.Adapter, runbook.Adapter)
	}
	if d.BadgeClass == "" {
		t.Error("the runbook kind has no badge class, so a list could not show which kind a template is")
	}
	if len(d.Fields) == 0 {
		t.Error("the runbook kind declares no fields, so nothing could be set on a launch")
	}
}
