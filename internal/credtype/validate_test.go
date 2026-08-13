package credtype_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// validType returns a minimal type that passes validation, for a test to
// break one field of at a time.
func validType() credtype.CredentialType {
	return credtype.CredentialType{
		Name:      "Custom API",
		Kind:      credtype.KindCloud,
		Namespace: "custom_api",
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "api_token", Label: "Token", Secret: true},
				{ID: "api_url", Label: "URL"},
			},
			Required: []string{"api_token"},
		},
		Injectors: credtype.Injectors{
			Env: map[string]string{"API_TOKEN": "{{ api_token }}"},
		},
	}
}

// TestCredentialTypeValidateRefusals covers everything a malformed type
// can be. Each case names what it protects, because several look arbitrary
// until the reason is written down.
func TestCredentialTypeValidateRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*credtype.CredentialType)
	}{
		{
			name:   "no name",
			mutate: func(ct *credtype.CredentialType) { ct.Name = "" },
		},
		{
			name:   "a kind outside the closed vocabulary",
			mutate: func(ct *credtype.CredentialType) { ct.Kind = "made-up" },
		},
		{
			name: "no namespace",
			// The namespace is the identifier an AWX import keys on to
			// decide whether a type already exists. Without one, every
			// import creates duplicates.
			mutate: func(ct *credtype.CredentialType) { ct.Namespace = "" },
		},
		{
			name:   "a namespace with a hyphen",
			mutate: func(ct *credtype.CredentialType) { ct.Namespace = "custom-api" },
		},
		{
			name:   "a namespace starting with a digit",
			mutate: func(ct *credtype.CredentialType) { ct.Namespace = "1custom" },
		},
		{
			name: "an input id that is not a valid template variable",
			// An id is both a variable name in an injector template and
			// commonly part of an environment variable name. A hyphen
			// parses in neither.
			mutate: func(ct *credtype.CredentialType) { ct.Inputs.Fields[0].ID = "api-token" },
		},
		{
			name:   "an input id starting with a digit",
			mutate: func(ct *credtype.CredentialType) { ct.Inputs.Fields[0].ID = "1token" },
		},
		{
			name:   "two inputs with the same id",
			mutate: func(ct *credtype.CredentialType) { ct.Inputs.Fields[1].ID = "api_token" },
		},
		{
			name:   "an input with no label",
			mutate: func(ct *credtype.CredentialType) { ct.Inputs.Fields[0].Label = "" },
		},
		{
			name:   "an input with an unknown type",
			mutate: func(ct *credtype.CredentialType) { ct.Inputs.Fields[1].Type = "multiselect" },
		},
		{
			name: "a secret input carrying a default",
			// A default on a secret field is a credential stored in the
			// type record, readable by anybody who may edit the type and
			// copied into every credential made from it.
			// launch.Survey.Validate refuses the identical thing for a
			// password question.
			mutate: func(ct *credtype.CredentialType) { ct.Inputs.Fields[0].Default = "prefilled-secret" },
		},
		{
			name:   "required naming an input that does not exist",
			mutate: func(ct *credtype.CredentialType) { ct.Inputs.Required = []string{"nonexistent"} },
		},
		{
			name: "an empty choice",
			mutate: func(ct *credtype.CredentialType) {
				ct.Inputs.Fields[1].Choices = []string{"a", ""}
			},
		},
		{
			name: "a duplicated choice",
			mutate: func(ct *credtype.CredentialType) {
				ct.Inputs.Fields[1].Choices = []string{"a", "a"}
			},
		},
		{
			name: "a default outside its own choices",
			mutate: func(ct *credtype.CredentialType) {
				ct.Inputs.Fields[1].Choices = []string{"a", "b"}
				ct.Inputs.Fields[1].Default = "c"
			},
		},
		{
			name: "an injector referencing an input the type does not declare",
			// The renderer would refuse this at render time, which is
			// correct and late. A launch is the wrong moment to discover a
			// typo in a credential type.
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.Env["API_TOKEN"] = "{{ api_tokn }}"
			},
		},
		{
			name: "an injector template that does not parse",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.Env["API_TOKEN"] = "{{ api_token"
			},
		},
		{
			name: "an injector using a statement block the renderer does not implement",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.Env["API_TOKEN"] = "{% for x in y %}{{ x }}{% endfor %}"
			},
		},
		{
			name: "an environment variable name with a hyphen",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.Env = map[string]string{"API-TOKEN": "{{ api_token }}"}
			},
		},
		{
			name: "an environment variable name starting with a digit",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.Env = map[string]string{"1TOKEN": "{{ api_token }}"}
			},
		},
		{
			name: "a nested extra var that is null",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.ExtraVars = map[string]any{"a": nil}
			},
		},
		{
			name: "an extra var that is a list",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.ExtraVars = map[string]any{"a": []any{"{{ api_token }}"}}
			},
		},
		{
			name: "an extra var with an empty name",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.ExtraVars = map[string]any{"": "{{ api_token }}"}
			},
		},
		{
			name: "the file injector mixing its single and multi spellings",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.File = map[string]string{
					"template":      "{{ api_token }}",
					"template.cert": "{{ api_token }}",
				}
			},
		},
		{
			name: "a file injector key that is neither spelling",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.File = map[string]string{"cert": "{{ api_token }}"}
			},
		},
		{
			name: "a file label with a character that cannot go in a filename",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.File = map[string]string{"template./etc/passwd": "{{ api_token }}"}
			},
		},
	}

	eng := render.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ct := validType()
			tt.mutate(&ct)

			if err := ct.Validate(eng); err == nil {
				t.Fatal("Validate() accepted the type, want a refusal")
			} else if !errors.Is(err, credtype.ErrInvalidType) {
				t.Errorf("Validate() error = %v, want one matching ErrInvalidType", err)
			}
		})
	}
}

// TestInjectorsRefuseReservedEnvironmentVariables is the headline item of
// this phase's Schema and Injection Hardening audit, so it gets its own
// test rather than a row in the table above.
//
// PLAN.md Section 29.4 accepts the ephemeral container as the trust
// boundary, which is what permits secrets in the environment at all. The
// customer's playbook runs INSIDE that boundary, so an injector that can
// set LD_PRELOAD is arbitrary code execution inside the very process the
// credential was meant to authenticate. The container being single use does
// not help: the code runs before the container is destroyed.
func TestInjectorsRefuseReservedEnvironmentVariables(t *testing.T) {
	t.Parallel()

	reserved := []string{
		"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT",
		"PYTHONPATH", "PYTHONHOME", "PYTHONSTARTUP",
		"BASH_ENV", "ENV", "IFS", "PATH", "HOME",
		"ANSIBLE_CONFIG", "ANSIBLE_FORCE_COLOR", "ANSIBLE_NOCOLOR",
		// A name that passes the character check, so this row actually
		// reaches the prefix rule. Bash's own historical spelling carries
		// a "%%" suffix, which the character check refuses first; that
		// shape is covered separately below, because a refusal for the
		// wrong reason is a rule that is not being tested.
		"BASH_FUNC_evil",
	}

	eng := render.New()

	for _, name := range reserved {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ct := validType()
			ct.Injectors.Env = map[string]string{name: "{{ api_token }}"}

			err := ct.Validate(eng)
			if err == nil {
				t.Fatalf("Validate() allowed an injector to set %s", name)
			}
			if !errors.Is(err, credtype.ErrInvalidType) {
				t.Errorf("Validate() error = %v, want one matching ErrInvalidType", err)
			}
			// The message has to say why, because the author of a
			// credential type that needs one of these has a real problem
			// and "not allowed" does not help them solve it.
			if !strings.Contains(err.Error(), "because") {
				t.Errorf("the refusal does not say why: %v", err)
			}
		})
	}
}

// TestInjectorsRefuseTheShellshockSpelling covers bash's own historical
// exported-function name, which carries characters the general name check
// already refuses.
//
// It is asserted separately from the reserved-prefix table because the two
// rules catch it at different points, and a test that could not tell them
// apart would pass while the prefix rule sat unexercised. That is exactly
// what happened when this file was first written.
func TestInjectorsRefuseTheShellshockSpelling(t *testing.T) {
	t.Parallel()

	ct := validType()
	ct.Injectors.Env = map[string]string{"BASH_FUNC_evil%%": "{{ api_token }}"}

	err := ct.Validate(render.New())
	if err == nil {
		t.Fatal("Validate() allowed an exported shell function name")
	}
	if !errors.Is(err, credtype.ErrInvalidType) {
		t.Errorf("Validate() error = %v, want one matching ErrInvalidType", err)
	}
}

// TestValidInjectorShapes is the positive counterpart: every shape a real
// credential type legitimately uses must be accepted. A validator that
// rejects a type a customer actually has blocks the migration this phase
// exists to enable.
func TestValidInjectorShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		inj  credtype.Injectors
	}{
		{
			name: "no injectors at all, which an ssh machine credential has",
			inj:  credtype.Injectors{},
		},
		{
			name: "several environment variables",
			inj: credtype.Injectors{Env: map[string]string{
				"API_TOKEN": "{{ api_token }}",
				"API_URL":   "{{ api_url }}",
			}},
		},
		{
			name: "a flat extra var",
			inj:  credtype.Injectors{ExtraVars: map[string]any{"token": "{{ api_token }}"}},
		},
		{
			name: "a nested extra var, which the release gate requires",
			inj: credtype.Injectors{ExtraVars: map[string]any{
				"outer": map[string]any{"inner": "{{ api_token }}"},
			}},
		},
		{
			name: "a deeply nested extra var",
			inj: credtype.Injectors{ExtraVars: map[string]any{
				"a": map[string]any{"b": map[string]any{"c": "{{ api_token }}"}},
			}},
		},
		{
			name: "a literal scalar extra var needing no rendering",
			inj: credtype.Injectors{ExtraVars: map[string]any{
				"verify": true, "port": float64(443),
			}},
		},
		{
			name: "the single-file spelling",
			inj:  credtype.Injectors{File: map[string]string{"template": "token={{ api_token }}\n"}},
		},
		{
			name: "the multi-file spelling, which the release gate requires two of",
			inj: credtype.Injectors{File: map[string]string{
				"template.cert": "{{ api_token }}",
				"template.key":  "{{ api_url }}",
			}},
		},
		{
			name: "a file injector addressing itself back through the reserved namespace",
			inj: credtype.Injectors{
				File: map[string]string{"template": "{{ api_token }}"},
				Env:  map[string]string{"CONFIG_PATH": "{{ tower.filename }}"},
			},
		},
		{
			name: "the native spelling of the reserved namespace",
			inj: credtype.Injectors{
				File: map[string]string{"template": "{{ api_token }}"},
				Env:  map[string]string{"CONFIG_PATH": "{{ pleiades.filename }}"},
			},
		},
		{
			name: "a multi-file injector addressing one of its files",
			inj: credtype.Injectors{
				File: map[string]string{"template.cert": "{{ api_token }}"},
				Env:  map[string]string{"CERT_PATH": "{{ tower.filename.cert }}"},
			},
		},
		{
			name: "an optional input guarded by the default filter",
			inj:  credtype.Injectors{Env: map[string]string{"REGION": "{{ api_url | default('us-east-1') }}"}},
		},
	}

	eng := render.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ct := validType()
			ct.Injectors = tt.inj

			if err := ct.Validate(eng); err != nil {
				t.Fatalf("Validate() refused a legitimate shape: %v", err)
			}
		})
	}
}

// TestValidateRequiresARenderEngine pins the nil case. Validation without
// an engine cannot compile a template, so it would silently skip the check
// that moves undefined-variable failures from launch time to save time.
// Silently skipping a validation is worse than not having it, because the
// caller believes it ran.
func TestValidateRequiresARenderEngine(t *testing.T) {
	t.Parallel()

	if err := validType().Validate(nil); err == nil {
		t.Fatal("Validate(nil) succeeded, want a refusal")
	}
}

// TestCheckValues covers what a credential holds against its own type.
func TestCheckValues(t *testing.T) {
	t.Parallel()

	schema := credtype.InputSchema{
		Fields: []credtype.InputField{
			{ID: "api_token", Label: "Token", Secret: true},
			{ID: "region", Label: "Region", Choices: []string{"us", "eu"}, Default: "us"},
			{ID: "prompted", Label: "Prompted", Secret: true, AskAtRuntime: true},
		},
		Required: []string{"api_token", "region", "prompted"},
	}

	tests := []struct {
		name    string
		values  map[string]string
		wantErr bool
	}{
		{
			name:   "every required value supplied",
			values: map[string]string{"api_token": "t", "region": "eu", "prompted": "p"},
		},
		{
			name: "a required field falling back to its default",
			// region is required and not supplied, but declares a default,
			// so the credential is complete.
			values: map[string]string{"api_token": "t", "prompted": "p"},
		},
		{
			name: "a required ask-at-runtime field not supplied",
			// It is answered at launch by definition, so a stored
			// credential omitting it is complete rather than broken.
			values: map[string]string{"api_token": "t"},
		},
		{
			name:    "a required field with neither a value nor a default",
			values:  map[string]string{"region": "eu"},
			wantErr: true,
		},
		{
			name:    "a value for an input the type does not declare",
			values:  map[string]string{"api_token": "t", "nonexistent": "x"},
			wantErr: true,
		},
		{
			name:    "a value outside its own choices",
			values:  map[string]string{"api_token": "t", "region": "antarctica"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := schema.CheckValues(tt.values)
			if tt.wantErr && err == nil {
				t.Fatal("CheckValues() accepted invalid values")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("CheckValues() refused valid values: %v", err)
			}
		})
	}
}

// TestCheckValuesNeverQuotesAValue is a disclosure check.
//
// Every error here can reach an API response and a log line, and half of
// these values are secrets. An error that quoted the offending value would
// put a rejected password in a log, and a rejected password is still a
// password somebody typed.
func TestCheckValuesNeverQuotesAValue(t *testing.T) {
	t.Parallel()

	const secret = "CANARY-secret-value"

	schema := credtype.InputSchema{
		Fields: []credtype.InputField{
			{ID: "region", Label: "Region", Secret: true, Choices: []string{"us", "eu"}},
		},
	}

	err := schema.CheckValues(map[string]string{"region": secret})
	if err == nil {
		t.Fatal("CheckValues() accepted a value outside its choices")
	}
	if strings.Contains(err.Error(), "CANARY") {
		t.Errorf("the error quotes the rejected value: %v", err)
	}
	// It must still name the field, or the author cannot act on it.
	if !strings.Contains(err.Error(), "region") {
		t.Errorf("the error does not name the field: %v", err)
	}
}

// TestKindVocabularyIsClosed pins the binding rule's foundation. The rule
// "one credential per type, vault exempted" keys on kind, so an open
// vocabulary would make it unenforceable.
func TestKindVocabularyIsClosed(t *testing.T) {
	t.Parallel()

	kinds := credtype.Kinds()
	if len(kinds) != 12 {
		t.Errorf("Kinds() has %d entries, want AWX's 12: %v", len(kinds), kinds)
	}
	for _, k := range kinds {
		if !k.Valid() {
			t.Errorf("Kinds() returned %q, which Valid() rejects", k)
		}
	}
	if credtype.Kind("made-up").Valid() {
		t.Error("an unknown kind reported itself valid")
	}
}

// TestFileLabels covers the accessor the injector engine uses to decide how
// many files to generate and what to call them.
func TestFileLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file map[string]string
		want []string
	}{
		{
			name: "no files",
			file: nil,
			want: nil,
		},
		{
			name: "the single-file spelling reports one empty label",
			// Empty rather than absent: it distinguishes a type generating
			// one unnamed file from a type generating none.
			file: map[string]string{"template": "x"},
			want: []string{""},
		},
		{
			name: "the multi-file spelling reports its labels, sorted",
			file: map[string]string{"template.key": "x", "template.cert": "y"},
			want: []string{"cert", "key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := credtype.Injectors{File: tt.file}.FileLabels()
			if len(got) != len(tt.want) {
				t.Fatalf("FileLabels() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("FileLabels() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}
