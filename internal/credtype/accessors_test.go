package credtype_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// The accessors the injector engine and the launch path read. Each one
// answers a question some later caller has to ask, so each gets a test
// naming which caller and which question.

// TestInjectorsEmpty covers the check that decides whether a credential
// injects through any of the three data targets at all.
//
// An empty document is legal rather than a defect: an ssh machine
// credential injects through a Go object, the transport's own credential
// struct, and has no env, extra_vars or file entries. The injector engine
// uses this to tell that case apart from a type somebody forgot to finish.
func TestInjectorsEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		inj  credtype.Injectors
		want bool
	}{
		{name: "nothing at all", inj: credtype.Injectors{}, want: true},
		{name: "an env entry", inj: credtype.Injectors{Env: map[string]string{"A": "x"}}},
		{name: "an extra var", inj: credtype.Injectors{ExtraVars: map[string]any{"a": "x"}}},
		{name: "a file", inj: credtype.Injectors{File: map[string]string{"template": "x"}}},
		{
			name: "empty maps rather than nil ones are still empty",
			inj: credtype.Injectors{
				Env: map[string]string{}, ExtraVars: map[string]any{}, File: map[string]string{},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.inj.Empty(); got != tt.want {
				t.Errorf("Empty() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAskAtRuntimeFields covers the accessor the relaunch refusal reads.
//
// A credential whose type declares any prompted input cannot be relaunched,
// because the answer was never stored: PLAN.md Section 29.3's never-persist
// rule means there is nothing to replay. internal/api's Relaunch already
// refuses to replay a secret survey answer for the same reason, and this is
// what lets it apply the identical rule to credentials.
func TestAskAtRuntimeFields(t *testing.T) {
	t.Parallel()

	schema := credtype.InputSchema{Fields: []credtype.InputField{
		{ID: "stored_token", Label: "Stored"},
		{ID: "vault_pass", Label: "Vault Password", Secret: true, AskAtRuntime: true},
		{ID: "become_pass", Label: "Become Password", Secret: true, AskAtRuntime: true},
	}}

	got := schema.AskAtRuntimeFields()
	if len(got) != 2 || got[0] != "become_pass" || got[1] != "vault_pass" {
		t.Errorf("AskAtRuntimeFields() = %v, want [become_pass vault_pass] sorted", got)
	}

	none := credtype.InputSchema{Fields: []credtype.InputField{{ID: "a", Label: "A"}}}
	if got := none.AskAtRuntimeFields(); len(got) != 0 {
		t.Errorf("AskAtRuntimeFields() = %v on a type with none, want empty", got)
	}
}

// TestDefaults covers what fills in the values a credential did not supply,
// immediately before injection.
func TestDefaults(t *testing.T) {
	t.Parallel()

	schema := credtype.InputSchema{Fields: []credtype.InputField{
		{ID: "region", Label: "Region", Default: "us-east-1"},
		{ID: "token", Label: "Token", Secret: true},
		{ID: "verify", Label: "Verify", Type: credtype.InputBoolean, Default: "true"},
	}}

	got := schema.Defaults()
	if len(got) != 2 {
		t.Fatalf("Defaults() = %v, want two entries", got)
	}
	if got["region"] != "us-east-1" || got["verify"] != "true" {
		t.Errorf("Defaults() = %v", got)
	}
	if _, present := got["token"]; present {
		t.Error("Defaults() carries an entry for a field with no default")
	}
}

// TestDefaultsReturnsAFreshMap guards a real hazard. The schema is shared:
// it is decoded once per credential type and read on every injection, so a
// caller merging supplied values into the returned map would corrupt the
// defaults for every later launch of every credential of that type.
func TestDefaultsReturnsAFreshMap(t *testing.T) {
	t.Parallel()

	schema := credtype.InputSchema{Fields: []credtype.InputField{
		{ID: "region", Label: "Region", Default: "us-east-1"},
	}}

	first := schema.Defaults()
	first["region"] = "mutated"
	first["injected"] = "extra"

	second := schema.Defaults()
	if second["region"] != "us-east-1" {
		t.Errorf("mutating one Defaults() result changed the next: %v", second)
	}
	if _, leaked := second["injected"]; leaked {
		t.Error("an entry added to one Defaults() result appeared in the next")
	}
}

// TestFieldAndIDs covers the two lookups the injector engine uses to build
// its render variables.
func TestFieldAndIDs(t *testing.T) {
	t.Parallel()

	schema := credtype.InputSchema{Fields: []credtype.InputField{
		{ID: "b", Label: "B"},
		{ID: "a", Label: "A"},
	}}

	// IDs preserves declaration order rather than sorting, because that
	// order is the order a form shows the fields in and AWX carries it
	// deliberately.
	ids := schema.IDs()
	if len(ids) != 2 || ids[0] != "b" || ids[1] != "a" {
		t.Errorf("IDs() = %v, want [b a] in declaration order", ids)
	}

	if f, ok := schema.Field("a"); !ok || f.Label != "A" {
		t.Errorf("Field(a) = %+v, %v", f, ok)
	}
	if _, ok := schema.Field("nonexistent"); ok {
		t.Error("Field() found a field that does not exist")
	}
}

// TestNestedExtraVarsWalkReachesEveryLeaf pins the recursion, since a walk
// that stopped at the first level would validate the outer key and silently
// skip the template inside it.
func TestNestedExtraVarsWalkReachesEveryLeaf(t *testing.T) {
	t.Parallel()

	ct := validType()
	ct.Injectors = credtype.Injectors{ExtraVars: map[string]any{
		"outer": map[string]any{
			"good": "{{ api_token }}",
			"bad":  "{{ undeclared_name }}",
		},
	}}

	if err := ct.Validate(render.New()); err == nil {
		t.Fatal("Validate() accepted a nested extra var referencing an undeclared input")
	}
}
