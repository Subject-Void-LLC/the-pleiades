package launch_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file covers the kind vocabulary's two smallest and most
// load-bearing statements: how an absent kind resolves, and what a
// Template answers when asked its own.
//
// Neither had a test. ResolveKind's own doc comment explains why that is
// worse than it looks: the default-kind rule "used to live only in
// internal/adapters/routing, which left the Controller's own fan-out with
// no statement of the rule at all, and a second statement would
// eventually disagree with the first." It now lives here, once. A rule
// that exists in exactly one place and is verified in none is one edit
// away from silently becoming a different rule.

// TestResolveKind_AnAbsentKindIsTheDefaultRatherThanAnError proves the
// additive-field property every record carrying a kind depends on: a job
// or a dispatch written before the field existed carries none, and must
// still reach the executor it was always going to reach.
func TestResolveKind_AnAbsentKindIsTheDefaultRatherThanAnError(t *testing.T) {
	tests := []struct {
		name string
		kind string
		want string
	}{
		{name: "a record written before the field existed", kind: "", want: launch.DefaultKind},
		{name: "a column that stored whitespace", kind: "   ", want: launch.DefaultKind},
		{name: "a tab and newline from a hand-edited document", kind: "\t\n", want: launch.DefaultKind},
		{name: "an explicit kind is kept", kind: "playbook", want: "playbook"},
		{name: "an explicit kind is trimmed, not rejected", kind: "  playbook  ", want: "playbook"},
		{name: "an unregistered kind still resolves, because resolving is not validating", kind: "terraform", want: "terraform"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := launch.ResolveKind(tt.kind); got != tt.want {
				t.Errorf("ResolveKind(%q) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

// TestResolveKind_TheDefaultIsARegisteredKind proves the default is not
// merely a string: whatever DefaultKind names has to be something the
// registry can actually look up, or every record carrying no kind would
// resolve to a kind nothing can run.
func TestResolveKind_TheDefaultIsARegisteredKind(t *testing.T) {
	if _, ok := launch.Lookup(launch.DefaultKind); !ok {
		t.Fatalf("DefaultKind %q is not a registered kind, so every record with no kind resolves to something unrunnable", launch.DefaultKind)
	}
}

// TestTemplate_KindAnswersItsOwnStoredKind proves Template satisfies the
// Launchable side of the contract with the kind it was stored with,
// rather than with a resolved or defaulted one.
//
// The distinction is deliberate and worth pinning: ResolveKind's
// defaulting belongs at the boundary where a record is read, so a
// Template that genuinely carries no kind reports none here and is
// refused by Descriptor rather than quietly running as a runbook.
func TestTemplate_KindAnswersItsOwnStoredKind(t *testing.T) {
	if got := (launch.Template{KindName: "playbook"}).Kind(); got != "playbook" {
		t.Errorf("Kind() = %q, want %q", got, "playbook")
	}

	empty := launch.Template{}
	if got := empty.Kind(); got != "" {
		t.Errorf("Kind() on a template with no stored kind = %q, want the empty string: defaulting belongs at the read boundary, not here", got)
	}
	if _, err := empty.Descriptor(); err == nil {
		t.Error("a template carrying no kind resolved a descriptor, so it would run as something nobody chose")
	}
}

// TestMustRegister_PanicsRatherThanShadowingARegistration proves the
// refusal a built-in kind's own init() relies on: two registrations of
// one kind must take the process down at start rather than leave which
// descriptor wins depending on import order.
func TestMustRegister_PanicsRatherThanShadowingARegistration(t *testing.T) {
	existing, ok := launch.Lookup(launch.DefaultKind)
	if !ok {
		t.Fatalf("%q is not registered, so this test has no duplicate to attempt", launch.DefaultKind)
	}

	defer func() {
		if recover() == nil {
			t.Error("MustRegister on an already-registered kind did not panic")
		}
		// The original registration must survive the attempt: a refused
		// duplicate that damaged the registry on its way out would be
		// worse than the shadowing it exists to prevent.
		if after, stillThere := launch.Lookup(launch.DefaultKind); !stillThere || after.Label != existing.Label {
			t.Errorf("the original %q registration did not survive the refused duplicate", launch.DefaultKind)
		}
	}()

	launch.MustRegister(existing)
}
