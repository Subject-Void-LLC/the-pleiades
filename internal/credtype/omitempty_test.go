package credtype_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// The omit-when-empty rule, the render namespace's seeding, and the
// injection-time readiness check. All three arrived together because they
// are one story: what a template sees when an input was left blank.

// omitEmptyType is a type whose optional input gates two variables, which
// is AWX's aws shape.
func omitEmptyType() credtype.CredentialType {
	return credtype.CredentialType{
		Name: "Gated", Kind: credtype.KindCloud, Namespace: "gated",
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "key", Label: "Key"},
				{ID: "token", Label: "Token", Secret: true},
			},
			Required: []string{"key"},
		},
		Injectors: credtype.Injectors{
			Env: map[string]string{
				"ALWAYS": "{{ key }}",
				"GATED":  "{{ token }}",
			},
			OmitEmpty: []string{"GATED"},
		},
	}
}

// TestOmitEmptyLeavesAGatedVariableUnsetRatherThanEmpty is the behaviour
// the field exists for, in both directions.
func TestOmitEmptyLeavesAGatedVariableUnsetRatherThanEmpty(t *testing.T) {
	t.Parallel()

	in, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatalf("NewInjector() error = %v", err)
	}

	supplied, err := in.Inject([]credtype.Credential{{
		ID: 1, Name: "gated", Type: omitEmptyType(),
		Inputs: map[string]string{"key": "k", "token": "t"},
	}}, nil)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if supplied.Env["GATED"] != "t" {
		t.Errorf("GATED = %q, want the supplied token", supplied.Env["GATED"])
	}

	blank, err := in.Inject([]credtype.Credential{{
		ID: 1, Name: "gated", Type: omitEmptyType(),
		Inputs: map[string]string{"key": "k"},
	}}, nil)
	if err != nil {
		t.Fatalf("Inject() with a blank optional error = %v", err)
	}
	if v, set := blank.Env["GATED"]; set {
		t.Errorf("GATED = %q, want it unset entirely", v)
	}
	// The ungated one is still set, which is what stops this from being a
	// blanket "drop empty values" rule. AWX sets a blank optional's
	// variable to the empty string everywhere it is not explicitly gated.
	if v, set := blank.Env["ALWAYS"]; !set || v != "k" {
		t.Errorf("ALWAYS = %q (set=%v), want the supplied key", v, set)
	}
}

// TestOmitEmptyMustNameAVariableTheTypeSets covers the save-time refusal.
// A name matching nothing would save cleanly, inject an empty value, and
// fail authentication at run time with nothing pointing back at the type.
func TestOmitEmptyMustNameAVariableTheTypeSets(t *testing.T) {
	t.Parallel()

	eng := render.New()

	tests := []struct {
		name  string
		omit  []string
		wants string
	}{
		{"a variable the env injector does not set", []string{"NOT_SET"}, "does not set"},
		{"the same variable twice", []string{"GATED", "GATED"}, "twice"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ct := omitEmptyType()
			ct.Injectors.OmitEmpty = tt.omit

			err := ct.Validate(eng)
			if !errors.Is(err, credtype.ErrInvalidType) {
				t.Fatalf("Validate() = %v, want one matching ErrInvalidType", err)
			}
			if !strings.Contains(err.Error(), tt.wants) {
				t.Errorf("message %q does not say %q", err, tt.wants)
			}
		})
	}
}

// TestPreviewReportsTheShapeARunWouldProduce checks that the preview
// applies the same gate, so an author's test and their run agree about
// which variables exist.
func TestPreviewReportsTheShapeARunWouldProduce(t *testing.T) {
	t.Parallel()

	ct := omitEmptyType()
	eng := render.New()

	full, err := ct.Injectors.Preview(ct.Inputs, eng, map[string]string{"key": "k", "token": "t"})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if len(full.Env) != 2 {
		t.Errorf("Env = %v, want both variables", full.Env)
	}

	gated, err := ct.Injectors.Preview(ct.Inputs, eng, map[string]string{"key": "k"})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	for _, name := range gated.Env {
		if name == "GATED" {
			t.Error("the preview reports a variable a run would leave unset")
		}
	}
}

// TestRenderVarsSeedsEveryDeclaredInput is the fix for
// FAILURE_PATTERNS.md #121, asserted at the level it broke: a blank
// optional must render, not fail the whole injection.
func TestRenderVarsSeedsEveryDeclaredInput(t *testing.T) {
	t.Parallel()

	cred := credtype.Credential{
		ID: 1, Name: "sparse", Type: omitEmptyType(),
		Inputs: map[string]string{"key": "k"},
	}

	vars := cred.RenderVars()
	if got, ok := vars["token"]; !ok || got != "" {
		t.Errorf("token = %v (present=%v), want the empty string and present", got, ok)
	}
	// An undeclared name is still absent, which is what keeps
	// strict-undefined meaningful: Injectors.Validate refused it at save,
	// so it can only be a typo, and a typo must still fail.
	if _, ok := vars["never_declared"]; ok {
		t.Error("an undeclared name is in the render namespace, so strict-undefined no longer catches a typo")
	}
}

// TestRenderVarsCarriesAnUndeclaredStoredValue covers the other branch:
// a value the schema does not declare is passed through rather than
// dropped, so the map is a faithful view of what the credential holds.
func TestRenderVarsCarriesAnUndeclaredStoredValue(t *testing.T) {
	t.Parallel()

	cred := credtype.Credential{
		ID: 1, Name: "extra", Type: omitEmptyType(),
		Inputs: map[string]string{"key": "k", "left_over": "v"},
	}

	if got := cred.RenderVars()["left_over"]; got != "v" {
		t.Errorf("left_over = %v, want it carried through", got)
	}
}

// TestBooleanAndKeyValuesRenderAsAWXSpellsThem covers renderValue's two
// normalisations, both of which exist because a migrated playbook
// observes the result.
func TestBooleanAndKeyValuesRenderAsAWXSpellsThem(t *testing.T) {
	t.Parallel()

	ct := credtype.CredentialType{
		Name: "Shapes", Kind: credtype.KindCloud, Namespace: "shapes",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "flag", Label: "Flag", Type: credtype.InputBoolean},
			{ID: "key", Label: "Key", Format: credtype.FormatSSHPrivateKey, Secret: true},
			{ID: "text", Label: "Text"},
		}},
	}

	tests := []struct {
		name, input, want string
		field             string
	}{
		{"a true boolean", "true", "True", "flag"},
		{"a false boolean", "false", "False", "flag"},
		{"an unset boolean", "", "False", "flag"},
		{"a boolean stored in another spelling", "1", "True", "flag"},
		{"a boolean stored as nonsense fails closed", "maybe", "False", "flag"},
		{"a key without its trailing newline", "-----BEGIN-----", "-----BEGIN-----\n", "key"},
		{"a key that already ends correctly", "-----BEGIN-----\n", "-----BEGIN-----\n", "key"},
		{"an empty key gains nothing", "", "", "key"},
		{"ordinary text is untouched", "  spaced  ", "  spaced  ", "text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cred := credtype.Credential{
				ID: 1, Name: "shapes", Type: ct,
				Inputs: map[string]string{tt.field: tt.input},
			}
			if got := cred.RenderVars()[tt.field]; got != tt.want {
				t.Errorf("%s = %q, want %q", tt.field, got, tt.want)
			}
		})
	}
}

// TestInjectionRefusesARequiredInputNobodyAnswered is the check that
// replaced the protection strict-undefined used to provide by accident.
//
// The message has to name the input. The undefined-variable error it
// replaced named a template, which sent an operator looking at the
// credential type rather than at the prompt they skipped.
func TestInjectionRefusesARequiredInputNobodyAnswered(t *testing.T) {
	t.Parallel()

	ct := omitEmptyType()
	// Required and prompted, so CheckValues exempts it at save time and
	// only the injection-time check can catch it.
	ct.Inputs.Fields[0].AskAtRuntime = true

	in, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatalf("NewInjector() error = %v", err)
	}

	_, err = in.Inject([]credtype.Credential{{
		ID: 1, Name: "unanswered", Type: ct, Inputs: map[string]string{},
	}}, nil)
	if err == nil {
		t.Fatal("Inject() accepted a credential whose required input was never answered")
	}
	if !strings.Contains(err.Error(), "key") {
		t.Errorf("message %q does not name the input that was left blank", err)
	}

	// The same credential with the prompt answered injects normally, so
	// the check refuses the real case rather than the whole feature.
	if _, err := in.Inject([]credtype.Credential{{
		ID: 1, Name: "answered", Type: ct, Inputs: map[string]string{},
	}}, credtype.PromptedInputs{1: {"key": "typed-at-launch"}}); err != nil {
		t.Errorf("Inject() with the prompt answered = %v, want nil", err)
	}
}
