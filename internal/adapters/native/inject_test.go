package native

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestNativeRefusesWhatItCannotInject is the run-time backstop, and the
// point of it is that it REFUSES rather than skipping.
//
// Silently dropping an env injector here would be FAILURE_PATTERNS.md
// #116's shape, "correctly computed and never read by anything
// downstream", which adapter.go already refuses to repeat for forks and
// limit. An operator who bound an aws credential to a native template and
// watched the run fail to authenticate would have nothing pointing at the
// reason.
func TestNativeRefusesWhatItCannotInject(t *testing.T) {
	tests := []struct {
		name     string
		injected *wire.Injected
		wantName string
	}{
		{
			name:     "an environment variable",
			injected: &wire.Injected{Env: map[string]string{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE"}},
			wantName: "AWS_ACCESS_KEY_ID",
		},
		{
			name: "a generated file",
			injected: &wire.Injected{Files: []wire.InjectedFile{
				{Path: "/run/pleiades/credentials/18.cert", Content: "body", Mode: 0o600},
			}},
			wantName: "/run/pleiades/credentials/18.cert",
		},
		{
			name: "a vault identity, which is a file and an Ansible feature at once",
			injected: &wire.Injected{Vault: []wire.InjectedVault{
				{Identifier: "prod", Path: "/run/pleiades/credentials/9.vault"},
			}},
			wantName: "/run/pleiades/credentials/9.vault",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := refuseUnsupportedInjection(tt.injected)
			if err == nil {
				t.Fatal("the native path silently accepted material it cannot inject")
			}
			if !errors.Is(err, routing.ErrUnsupportedInjection) {
				t.Errorf("error = %v, want one matching routing.ErrUnsupportedInjection", err)
			}
			// The message has to say WHICH part, or an operator has to
			// guess which half of their credential type is the problem.
			if !strings.Contains(err.Error(), tt.wantName) {
				t.Errorf("the refusal does not name %q: %v", tt.wantName, err)
			}
			// And it has to say why, once, in the shared sentence.
			if !strings.Contains(err.Error(), routing.UnsupportedInjectionMessage) {
				t.Errorf("the refusal does not explain itself: %v", err)
			}
		})
	}
}

// TestNativeAcceptsExtraVariables covers the one injector target this path
// does honour, which is pre-existing machinery rather than something built
// for it: engine.WithVariables is what the Crawl-tier CLI already uses.
func TestNativeAcceptsExtraVariables(t *testing.T) {
	injected := &wire.Injected{ExtraVars: map[string]any{"ansible_api_url": "https://api.example.test"}}

	if err := refuseUnsupportedInjection(injected); err != nil {
		t.Fatalf("the native path refused an extra-variable injection: %v", err)
	}

	merged, _, err := injectedVariables(map[string]any{"deploy_env": "prod"}, injected)
	if err != nil {
		t.Fatalf("injectedVariables() error = %v", err)
	}
	if merged["deploy_env"] != "prod" || merged["ansible_api_url"] != "https://api.example.test" {
		t.Errorf("merged variables = %v, want both the launch's own and the injected one", merged)
	}
}

// TestNativeRefusesAnExtraVariableCollision mirrors the legacy adapter's
// identical refusal, and the mirroring is the point: two execution paths
// that resolved the same collision differently would mean a run's variables
// depending on which adapter happened to serve it.
func TestNativeRefusesAnExtraVariableCollision(t *testing.T) {
	_, _, err := injectedVariables(
		map[string]any{"deploy_env": "prod"},
		&wire.Injected{ExtraVars: map[string]any{"deploy_env": "from-the-credential"}},
	)
	if err == nil {
		t.Fatal("the native path silently picked a winner between a launch variable and an injected one")
	}
	if !strings.Contains(err.Error(), "deploy_env") {
		t.Errorf("the refusal does not name the colliding variable: %v", err)
	}
}

// TestNativeMasksExactlyWhatWasDeclaredSecret covers what feeds the run's
// own output scrubbing, and the negative half is the one that matters.
//
// A task echoing an injected SECRET back must not leak it. A task echoing
// an injected NON-secret back must still be readable: masking everything
// would turn "connection to https://api.example.test refused" into
// "connection to ******** refused", which is a debugging session nobody can
// finish. Secrecy is not recoverable from the values themselves, so the
// dispatch says which are which and this reads exactly that.
func TestNativeMasksExactlyWhatWasDeclaredSecret(t *testing.T) {
	injected := &wire.Injected{
		ExtraVars: map[string]any{
			"token":  "a-real-bearer-token",
			"region": "https://api.example.test",
			"outer":  map[string]any{"inner": "a-nested-secret-value"},
			"count":  3,
		},
		Mask: []string{"a-real-bearer-token", "a-nested-secret-value"},
	}

	got := injectedSecretValues(injected)
	want := map[string]bool{"a-real-bearer-token": true, "a-nested-secret-value": true}
	if len(got) != len(want) {
		t.Fatalf("injectedSecretValues() = %v, want %v", got, want)
	}
	for _, v := range got {
		if !want[v] {
			t.Errorf("injectedSecretValues() returned %q, which was not declared secret", v)
		}
	}
}

// TestNativeWithholdsAVariableHoldingASecret is Phase 117a's S3: a bound
// credential's injected variable that holds a secret, whole, inside a
// longer string, or nested, never becomes a variable a runbook expression
// can read, while a non-secret one still does. Each withheld name is
// returned, sorted, and no value is.
func TestNativeWithholdsAVariableHoldingASecret(t *testing.T) {
	injected := &wire.Injected{
		ExtraVars: map[string]any{
			"token":       "a-real-bearer-token",
			"auth_header": "Bearer a-real-bearer-token",
			"outer":       map[string]any{"inner": []any{"a-nested-secret-value"}},
			"region":      "us-east-1",
			"count":       3.0,
		},
		Mask: []string{"a-real-bearer-token", "a-nested-secret-value"},
	}

	merged, withheld, err := injectedVariables(map[string]any{"deploy_env": "prod"}, injected)
	if err != nil {
		t.Fatalf("injectedVariables() error = %v", err)
	}
	for _, name := range []string{"token", "auth_header", "outer"} {
		if _, present := merged[name]; present {
			t.Errorf("the secret-holding variable %q reached the run's variables", name)
		}
	}
	if merged["region"] != "us-east-1" || merged["count"] != 3.0 || merged["deploy_env"] != "prod" {
		t.Errorf("merged = %v, want the non-secret injected variables and the launch's own", merged)
	}
	if strings.Join(withheld, ",") != "auth_header,outer,token" {
		t.Errorf("withheld = %v, want [auth_header outer token]", withheld)
	}
}

// TestNativeRefusesACollisionBeforeWithholdingASecret pins the order: a
// launch variable and a credential's secret variable sharing a name is
// still the collision it always was, not a silent withholding.
func TestNativeRefusesACollisionBeforeWithholdingASecret(t *testing.T) {
	_, _, err := injectedVariables(
		map[string]any{"token": "from-the-launch"},
		&wire.Injected{ExtraVars: map[string]any{"token": "a-real-bearer-token"}, Mask: []string{"a-real-bearer-token"}},
	)
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("injectedVariables() error = %v, want the collision refused by name", err)
	}
	if strings.Contains(err.Error(), "a-real-bearer-token") {
		t.Errorf("the refusal quotes the secret: %v", err)
	}
}

// TestNativeIsUnaffectedByADispatchCarryingNothing pins the nil case, which
// is every dispatch in a deployment that has created no credential type.
func TestNativeIsUnaffectedByADispatchCarryingNothing(t *testing.T) {
	if err := refuseUnsupportedInjection(nil); err != nil {
		t.Fatalf("a dispatch carrying no injection was refused: %v", err)
	}

	launched := map[string]any{"deploy_env": "prod"}
	merged, _, err := injectedVariables(launched, nil)
	if err != nil {
		t.Fatalf("injectedVariables() error = %v", err)
	}
	// The same map, not a copy: allocating one per dispatch for nothing
	// would be waste on the most common path in the system.
	if len(merged) != 1 || merged["deploy_env"] != "prod" {
		t.Errorf("merged = %v, want the launch's own variables untouched", merged)
	}

	if got := injectedSecretValues(nil); len(got) != 0 {
		t.Errorf("injectedSecretValues(nil) = %v, want none", got)
	}
}

// TestAnEmptyInjectionBlockIsNotARefusal covers the boundary between "this
// dispatch carries nothing" and "this dispatch carries something native
// cannot do". An empty block is the first, and refusing it would fail every
// dispatch from a Controller that sends the key unconditionally.
func TestAnEmptyInjectionBlockIsNotARefusal(t *testing.T) {
	if err := refuseUnsupportedInjection(&wire.Injected{}); err != nil {
		t.Fatalf("an empty injection block was refused: %v", err)
	}
}
