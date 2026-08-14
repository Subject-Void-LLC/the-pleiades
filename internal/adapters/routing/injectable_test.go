package routing_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/runbook"
)

// TestAdapterNamesMatchTheirKinds is what keeps a restated constant honest.
//
// internal/adapters/routing declares the adapter names itself rather than
// importing them from the kind packages, because a routing table that
// depended on the specific kinds a deployment registers would defeat its
// own design: adding a kind is meant to change this package's input, not
// its code.
//
// The price of that is two definitions of one string. This is the test that
// makes the price safe. Without it, a kind renaming its adapter would leave
// the bind-time injector check comparing against a value nothing produces,
// so it would silently permit every binding it exists to refuse.
func TestAdapterNamesMatchTheirKinds(t *testing.T) {
	t.Parallel()

	if routing.AdapterNative != runbook.Adapter {
		t.Errorf("routing.AdapterNative = %q, and the runbook kind names %q", routing.AdapterNative, runbook.Adapter)
	}
	if routing.AdapterLegacy != playbook.Adapter {
		t.Errorf("routing.AdapterLegacy = %q, and the playbook kind names %q", routing.AdapterLegacy, playbook.Adapter)
	}
	if routing.AdapterNative == routing.AdapterLegacy {
		t.Error("the two adapters share a name, so the injector check cannot tell them apart")
	}
}

// TestCheckInjectable covers the one rule, from both sides.
func TestCheckInjectable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		adapter    string
		envNames   []string
		fileLabels []string
		wantErr    bool
		// wantNamed are strings the refusal must contain, so an operator
		// learns which part of their credential type is the difficulty.
		wantNamed []string
	}{
		{
			name:     "the legacy path accepts an environment injector",
			adapter:  routing.AdapterLegacy,
			envNames: []string{"AWS_ACCESS_KEY_ID"},
		},
		{
			name:       "the legacy path accepts a file injector",
			adapter:    routing.AdapterLegacy,
			fileLabels: []string{"cert"},
		},
		{
			name:    "the native path accepts a type injecting neither",
			adapter: routing.AdapterNative,
		},
		{
			name:      "the native path refuses an environment injector, naming it",
			adapter:   routing.AdapterNative,
			envNames:  []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"},
			wantErr:   true,
			wantNamed: []string{"env AWS_ACCESS_KEY_ID", "env AWS_SECRET_ACCESS_KEY"},
		},
		{
			name:       "the native path refuses a multi-file injector, naming each label",
			adapter:    routing.AdapterNative,
			fileLabels: []string{"cert", "key"},
			wantErr:    true,
			wantNamed:  []string{"file template.cert", "file template.key"},
		},
		{
			name:       "the single-file spelling is named as template rather than as an empty label",
			adapter:    routing.AdapterNative,
			fileLabels: []string{""},
			wantErr:    true,
			wantNamed:  []string{"file template"},
		},
		{
			name:    "an adapter this build does not know is not this check's business",
			adapter: "some-future-adapter",
			// Deliberately permissive: a rule about the native path has
			// nothing to say about a path it has never heard of, and
			// guessing would refuse a binding for a reason that does not
			// apply.
			envNames: []string{"ANYTHING"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := routing.CheckInjectable(tt.adapter, "Amazon Web Services", tt.envNames, tt.fileLabels)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("CheckInjectable() refused a legal binding: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("CheckInjectable() accepted a binding the execution path cannot honour")
			}
			if !errors.Is(err, routing.ErrUnsupportedInjection) {
				t.Errorf("error = %v, want one matching ErrUnsupportedInjection", err)
			}
			// The type's name, so the operator knows which credential.
			if !strings.Contains(err.Error(), "Amazon Web Services") {
				t.Errorf("the refusal does not name the credential type: %v", err)
			}
			for _, want := range tt.wantNamed {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}
