package credtype_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// newTestInjector builds an Injector with an isolated masking set, so one
// test's registered secrets cannot affect another's assertions about what
// is tracked.
func newTestInjector(t *testing.T) (*credtype.Injector, *redact.Literals) {
	t.Helper()

	masker, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() error = %v", err)
	}
	literals := masker.Literals()

	in, err := credtype.NewInjector(render.New(), credtype.WithLiterals(literals))
	if err != nil {
		t.Fatalf("NewInjector() error = %v", err)
	}
	return in, literals
}

// restAPIType is the corpus type, restated as a value so a test can vary
// one field of it without editing the committed fixture. corpus_test.go is
// what proves the fixture decodes into these same structs.
func restAPIType() credtype.CredentialType {
	return credtype.CredentialType{
		Name:      "Custom REST API Token",
		Kind:      credtype.KindCloud,
		Namespace: "custom_api_token",
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "api_token", Type: credtype.InputString, Label: "API Bearer Token", Secret: true},
				{ID: "api_url", Type: credtype.InputString, Label: "API Base URL"},
			},
			Required: []string{"api_token", "api_url"},
		},
		Injectors: credtype.Injectors{
			Env: map[string]string{
				"REST_API_TOKEN": "{{ api_token }}",
				"REST_API_URL":   "{{ api_url }}",
			},
			ExtraVars: map[string]any{"ansible_api_token": "{{ api_token }}"},
		},
	}
}

// TestInjectRendersTheCorpusTypeExactly is this stage's tightest
// correctness statement: the real credential type from the parity corpus,
// injected, produces exactly the environment and extra variables a
// customer's playbook reads.
//
// Exactly, not "contains": an injector that produced a superset would still
// pass a contains-shaped assertion while setting a variable nobody declared.
func TestInjectRendersTheCorpusTypeExactly(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	art, err := in.Inject([]credtype.Credential{{
		ID:     18,
		Name:   "prod api",
		Type:   restAPIType(),
		Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "https://api.example.test"},
	}}, nil)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	wantEnv := map[string]string{
		"REST_API_TOKEN": "a-real-bearer-token",
		"REST_API_URL":   "https://api.example.test",
	}
	if len(art.Env) != len(wantEnv) {
		t.Fatalf("Env = %v, want %v", art.Env, wantEnv)
	}
	for name, want := range wantEnv {
		if art.Env[name] != want {
			t.Errorf("Env[%s] = %q, want %q", name, art.Env[name], want)
		}
	}

	if len(art.ExtraVars) != 1 || art.ExtraVars["ansible_api_token"] != "a-real-bearer-token" {
		t.Errorf("ExtraVars = %v, want exactly ansible_api_token", art.ExtraVars)
	}
	if len(art.Files) != 0 {
		t.Errorf("Files = %v, want none", art.Files)
	}
	if _, ok := art.Machine(); ok {
		t.Error("a cloud credential produced a machine identity")
	}
}

// TestInjectRegistersEveryRenderedSecret covers the Decorator, which is the
// one thing in this package that must not be forgettable.
func TestInjectRegistersEveryRenderedSecret(t *testing.T) {
	t.Parallel()

	in, literals := newTestInjector(t)

	ct := restAPIType()
	// A derived value: the sensitive half is a substring, and a line
	// carrying the whole header must be masked too.
	ct.Injectors.Env["REST_API_AUTH"] = "Bearer {{ api_token }}"

	if _, err := in.Inject([]credtype.Credential{{
		ID:     18,
		Name:   "prod api",
		Type:   ct,
		Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "https://api.example.test"},
	}}, nil); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	tracked := literals.Snapshot()
	for _, want := range []string{"a-real-bearer-token", "Bearer a-real-bearer-token"} {
		found := false
		for _, got := range tracked {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the masking set does not track %q; it holds %v", want, tracked)
		}
	}

	// The non-secret input is deliberately absent. Registering it would
	// scrub an ordinary URL out of every later log line in the process.
	for _, got := range tracked {
		if strings.Contains(got, "api.example.test") {
			t.Errorf("the masking set tracks the non-secret value %q", got)
		}
	}
}

// TestInjectResolvesTheReservedNamespaceToTheRealPath is what proves
// {{ tower.filename }} and the file the adapter creates cannot disagree:
// both come from FilePath, and this asserts the rendered value equals it.
func TestInjectResolvesTheReservedNamespaceToTheRealPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		injectors credtype.Injectors
		wantFiles map[string]string // path -> content
		wantEnv   map[string]string
	}{
		{
			name: "the single-file spelling",
			injectors: credtype.Injectors{
				File: map[string]string{"template": "token={{ api_token }}\n"},
				Env:  map[string]string{"CONFIG": "{{ tower.filename }}"},
			},
			wantFiles: map[string]string{
				credtype.FilePath(18, ""): "token=a-real-bearer-token\n",
			},
			wantEnv: map[string]string{"CONFIG": credtype.FilePath(18, "")},
		},
		{
			name: "the multi-file spelling",
			injectors: credtype.Injectors{
				File: map[string]string{
					"template.cert": "cert-{{ api_url }}",
					"template.key":  "key-{{ api_token }}",
				},
				Env: map[string]string{
					"CERT": "{{ tower.filename.cert }}",
					"KEY":  "{{ pleiades.filename.key }}",
				},
			},
			wantFiles: map[string]string{
				credtype.FilePath(18, "cert"): "cert-https://api.example.test",
				credtype.FilePath(18, "key"):  "key-a-real-bearer-token",
			},
			wantEnv: map[string]string{
				"CERT": credtype.FilePath(18, "cert"),
				"KEY":  credtype.FilePath(18, "key"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in, _ := newTestInjector(t)
			ct := restAPIType()
			ct.Injectors = tt.injectors

			art, err := in.Inject([]credtype.Credential{{
				ID:     18,
				Name:   "prod api",
				Type:   ct,
				Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "https://api.example.test"},
			}}, nil)
			if err != nil {
				t.Fatalf("Inject() error = %v", err)
			}

			if len(art.Files) != len(tt.wantFiles) {
				t.Fatalf("Files = %+v, want %d", art.Files, len(tt.wantFiles))
			}
			for _, f := range art.Files {
				want, declared := tt.wantFiles[f.Path]
				if !declared {
					t.Fatalf("a file was generated at an undeclared path %q", f.Path)
				}
				if f.Content != want {
					t.Errorf("the file at %s = %q, want %q", f.Path, f.Content, want)
				}
				if f.Mode != credtype.FileMode {
					t.Errorf("the file at %s has mode %#o, want %#o", f.Path, f.Mode, credtype.FileMode)
				}
			}

			for name, want := range tt.wantEnv {
				if art.Env[name] != want {
					t.Errorf("Env[%s] = %q, want the real generated path %q", name, art.Env[name], want)
				}
			}
		})
	}
}

// TestFilePathCannotEscapeItsDirectory pins the property FilePath's own doc
// comment leans on the label pattern for.
func TestFilePathCannotEscapeItsDirectory(t *testing.T) {
	t.Parallel()

	for _, label := range []string{"", "cert", "key", "a_b_9"} {
		got := credtype.FilePath(7, label)
		if !strings.HasPrefix(got, "/run/pleiades/credentials/7") {
			t.Errorf("FilePath(7, %q) = %q, which is not under the credential directory", label, got)
		}
		if strings.Contains(got, "..") || strings.Count(got, "/") != 4 {
			t.Errorf("FilePath(7, %q) = %q, which leaves the one directory", label, got)
		}
	}
	if credtype.FilePath(7, "") == credtype.FilePath(7, "cert") {
		t.Error("the single-file and multi-file spellings collide on one path")
	}
}

// TestInjectMachineCredential covers the kind that resolves to a Go value
// rather than to any of the three data targets.
func TestInjectMachineCredential(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	machineType := credtype.CredentialType{
		Name:      "Machine",
		Kind:      credtype.KindSSH,
		Namespace: "ssh",
		Managed:   true,
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "username", Label: "Username"},
			{ID: "password", Label: "Password", Secret: true},
			{ID: "ssh_key_data", Label: "SSH Private Key", Secret: true, Multiline: true, Format: credtype.FormatSSHPrivateKey},
			{ID: "ssh_key_unlock", Label: "Private Key Passphrase", Secret: true},
		}},
	}

	t.Run("the four transport inputs map onto the flattened keys", func(t *testing.T) {
		art, err := in.Inject([]credtype.Credential{{
			ID:   3,
			Name: "lab machine",
			Type: machineType,
			Inputs: map[string]string{
				"username":       "operator",
				"password":       "a-real-password",
				"ssh_key_data":   "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n",
				"ssh_key_unlock": "a-real-passphrase",
			},
		}}, nil)
		if err != nil {
			t.Fatalf("Inject() error = %v", err)
		}

		machine, ok := art.Machine()
		if !ok {
			t.Fatal("a machine credential produced no machine identity")
		}
		want := map[string]string{
			credtype.MachineUsername:   "operator",
			credtype.MachinePassword:   "a-real-password",
			credtype.MachinePrivateKey: "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n",
			credtype.MachinePassphrase: "a-real-passphrase",
		}
		if len(machine) != len(want) {
			t.Fatalf("Machine() = %v, want %v", machine, want)
		}
		for k, v := range want {
			if machine[k] != v {
				t.Errorf("Machine()[%s] = %q, want %q", k, machine[k], v)
			}
		}
	})

	t.Run("an unset input is absent rather than empty", func(t *testing.T) {
		// Absence is how a Collection method distinguishes "this device has
		// no key" from "this device has a key that happens to be empty",
		// which is internal/credential.Flatten's own documented contract.
		art, err := in.Inject([]credtype.Credential{{
			ID:     3,
			Name:   "password only",
			Type:   machineType,
			Inputs: map[string]string{"username": "operator", "password": "a-real-password"},
		}}, nil)
		if err != nil {
			t.Fatalf("Inject() error = %v", err)
		}
		machine, _ := art.Machine()
		if _, present := machine[credtype.MachinePrivateKey]; present {
			t.Errorf("Machine() carries an empty %s key", credtype.MachinePrivateKey)
		}
		if len(machine) != 2 {
			t.Errorf("Machine() = %v, want only the two inputs that were set", machine)
		}
	})

	t.Run("a machine credential holding nothing produces no identity", func(t *testing.T) {
		art, err := in.Inject([]credtype.Credential{{
			ID: 3, Name: "empty", Type: machineType, Inputs: map[string]string{},
		}}, nil)
		if err != nil {
			t.Fatalf("Inject() error = %v", err)
		}
		if _, ok := art.Machine(); ok {
			t.Error("Machine() reported an identity for a credential holding no values")
		}
	})
}

// vaultType is the managed vault credential type's shape.
func vaultType() credtype.CredentialType {
	return credtype.CredentialType{
		Name:      "Vault",
		Kind:      credtype.KindVault,
		Namespace: "vault",
		Managed:   true,
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "vault_password", Label: "Vault Password", Secret: true},
				{ID: "vault_id", Label: "Vault Identifier", Format: credtype.FormatVaultID},
			},
			Required: []string{"vault_password"},
		},
	}
}

// TestInjectVaultCredential covers the other kind that resolves to a Go
// value: a password file plus the identity naming it on the command line.
func TestInjectVaultCredential(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	art, err := in.Inject([]credtype.Credential{{
		ID:     9,
		Name:   "prod vault",
		Type:   vaultType(),
		Inputs: map[string]string{"vault_password": "a-real-vault-password", "vault_id": "prod"},
	}}, nil)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	vaults := art.Vault()
	if len(vaults) != 1 {
		t.Fatalf("Vault() = %+v, want one identity", vaults)
	}
	if vaults[0].Identifier != "prod" {
		t.Errorf("Vault()[0].Identifier = %q, want %q", vaults[0].Identifier, "prod")
	}
	if len(art.Files) != 1 || art.Files[0].Path != vaults[0].Path {
		t.Fatalf("Files = %+v, want exactly the file Vault() names at %s", art.Files, vaults[0].Path)
	}
	// One trailing newline, no more: ansible-playbook strips exactly one,
	// so a second would authenticate with a password nobody typed.
	if art.Files[0].Content != "a-real-vault-password\n" {
		t.Errorf("the vault password file = %q, want the password and one newline", art.Files[0].Content)
	}
}

// TestInjectRefusesAVaultCredentialWithNoPassword covers the branch that
// keeps an empty password file from being generated, which ansible-playbook
// would accept and then fail to decrypt with a message about the vault
// rather than about the credential.
func TestInjectRefusesAVaultCredentialWithNoPassword(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	ct := vaultType()
	ct.Inputs.Required = nil // so schema validation does not catch it first

	_, err := in.Inject([]credtype.Credential{{
		ID: 9, Name: "empty vault", Type: ct, Inputs: map[string]string{"vault_id": "prod"},
	}}, nil)
	if err == nil {
		t.Fatal("Inject() accepted a vault credential carrying no password")
	}
	if !errors.Is(err, credtype.ErrInjection) {
		t.Errorf("error = %v, want one matching ErrInjection", err)
	}
}

// TestInjectAppliesPromptedValues covers the launch-time inputs, which are
// a parameter rather than a stored value by design.
func TestInjectAppliesPromptedValues(t *testing.T) {
	t.Parallel()

	ct := restAPIType()
	ct.Inputs.Fields[0].AskAtRuntime = true

	tests := []struct {
		name     string
		stored   map[string]string
		prompted map[int]map[string]string
		want     string
		wantErr  bool
	}{
		{
			name:     "a prompted value fills an input that was never stored",
			stored:   map[string]string{"api_url": "https://api.example.test"},
			prompted: map[int]map[string]string{18: {"api_token": "typed-at-launch"}},
			want:     "typed-at-launch",
		},
		{
			name:     "a prompted value overrides a stored one",
			stored:   map[string]string{"api_token": "stored-token", "api_url": "https://api.example.test"},
			prompted: map[int]map[string]string{18: {"api_token": "typed-at-launch"}},
			want:     "typed-at-launch",
		},
		{
			name:     "an empty prompt leaves the stored value alone",
			stored:   map[string]string{"api_token": "stored-token", "api_url": "https://api.example.test"},
			prompted: map[int]map[string]string{18: {"api_token": ""}},
			want:     "stored-token",
		},
		{
			name:     "a prompt for a different credential does not apply",
			stored:   map[string]string{"api_url": "https://api.example.test"},
			prompted: map[int]map[string]string{99: {"api_token": "someone-elses"}},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in, _ := newTestInjector(t)
			art, err := in.Inject([]credtype.Credential{{
				ID: 18, Name: "prod api", Type: ct, Inputs: tt.stored,
			}}, tt.prompted)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Inject() succeeded with no value for a required input")
				}
				return
			}
			if err != nil {
				t.Fatalf("Inject() error = %v", err)
			}
			if art.Env["REST_API_TOKEN"] != tt.want {
				t.Errorf("Env[REST_API_TOKEN] = %q, want %q", art.Env["REST_API_TOKEN"], tt.want)
			}
		})
	}
}

// TestInjectErrorsNeverQuoteAValue is the rule every message in this
// package follows, and it is worth a test because these errors reach a job
// record an API caller reads.
func TestInjectErrorsNeverQuoteAValue(t *testing.T) {
	t.Parallel()

	const secret = "a-real-bearer-token"

	tests := []struct {
		name   string
		mutate func(*credtype.CredentialType)
		inputs map[string]string
	}{
		{
			name:   "a required input was not supplied",
			mutate: func(*credtype.CredentialType) {},
			inputs: map[string]string{"api_token": secret},
		},
		{
			name: "a template names something the type does not declare",
			mutate: func(ct *credtype.CredentialType) {
				ct.Injectors.Env["BROKEN"] = "{{ nonexistent }}"
			},
			inputs: map[string]string{"api_token": secret, "api_url": "https://api.example.test"},
		},
		{
			name: "a value is not among its input's choices",
			mutate: func(ct *credtype.CredentialType) {
				ct.Inputs.Fields[1].Choices = []string{"https://only.this.test"}
			},
			inputs: map[string]string{"api_token": secret, "api_url": "https://api.example.test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in, _ := newTestInjector(t)
			ct := restAPIType()
			tt.mutate(&ct)

			_, err := in.Inject([]credtype.Credential{{
				ID: 18, Name: "prod api", Type: ct, Inputs: tt.inputs,
			}}, nil)
			if err == nil {
				t.Fatal("Inject() succeeded where it should have failed")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the error quotes the secret value: %v", err)
			}
			if !strings.Contains(err.Error(), "prod api") {
				t.Errorf("the error does not name the credential: %v", err)
			}
		})
	}
}

// TestInjectRefusesAReservedEnvironmentVariableAtRunTime covers the
// backstop, not the save-time check.
//
// The document here never passed Validate: it stands in for a row written
// straight to the database, which PLAN.md Section 29.3's own residual note
// says is possible. Injection is what actually executes, so it checks too.
func TestInjectRefusesAReservedEnvironmentVariableAtRunTime(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	ct := restAPIType()
	ct.Injectors.Env["LD_PRELOAD"] = "{{ api_url }}"

	_, err := in.Inject([]credtype.Credential{{
		ID:     18,
		Name:   "tampered",
		Type:   ct,
		Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "/tmp/evil.so"},
	}}, nil)
	if err == nil {
		t.Fatal("Inject() honoured an LD_PRELOAD injector written past validation")
	}
	if !errors.Is(err, credtype.ErrInjection) {
		t.Errorf("error = %v, want one matching ErrInjection", err)
	}
	if !strings.Contains(err.Error(), "LD_PRELOAD") {
		t.Errorf("the error does not name the refused variable: %v", err)
	}
}

// TestInjectNothing covers the ordinary case of a template binding no
// credentials at all, which must be an empty artifact rather than an error.
func TestInjectNothing(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	art, err := in.Inject(nil, nil)
	if err != nil {
		t.Fatalf("Inject() with no credentials error = %v", err)
	}
	if !art.Empty() {
		t.Errorf("Inject() with no credentials = %+v, want an empty artifact", art)
	}
}

// TestNewInjectorRequiresARenderEngine pins the refusal that makes the
// one-renderer rule structural rather than conventional.
func TestNewInjectorRequiresARenderEngine(t *testing.T) {
	t.Parallel()

	if _, err := credtype.NewInjector(nil); err == nil {
		t.Fatal("NewInjector(nil) succeeded, so an injector can render with something other than the one engine")
	}
}

// TestInjectorHandlesNestedAndLiteralExtraVariables covers the two leaf
// shapes an extra-variable document can hold besides a template.
//
// A number or a boolean written literally into an injector document is
// legal in AWX and needs no rendering, so it passes through untouched.
// Rendering it would turn it into a string, and a playbook comparing
// `when: max_retries > 3` against the string "5" behaves differently from
// one comparing against the number.
func TestInjectorHandlesNestedAndLiteralExtraVariables(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	ct := restAPIType()
	ct.Injectors = credtype.Injectors{ExtraVars: map[string]any{
		"rendered": "{{ api_url }}",
		"literal":  true,
		"number":   float64(5),
		"nested":   map[string]any{"deep": map[string]any{"leaf": "{{ api_token }}"}},
	}}

	art, err := in.Inject([]credtype.Credential{{
		ID: 18, Name: "prod api", Type: ct,
		Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "https://api.example.test"},
	}}, nil)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	if art.ExtraVars["rendered"] != "https://api.example.test" {
		t.Errorf("ExtraVars[rendered] = %v, want the rendered url", art.ExtraVars["rendered"])
	}
	if art.ExtraVars["literal"] != true {
		t.Errorf("ExtraVars[literal] = %#v, want the boolean untouched rather than a string", art.ExtraVars["literal"])
	}
	if art.ExtraVars["number"] != float64(5) {
		t.Errorf("ExtraVars[number] = %#v, want the number untouched", art.ExtraVars["number"])
	}

	nested, ok := art.ExtraVars["nested"].(map[string]any)
	if !ok {
		t.Fatalf("ExtraVars[nested] = %#v, want a nested map", art.ExtraVars["nested"])
	}
	deep, ok := nested["deep"].(map[string]any)
	if !ok {
		t.Fatalf("ExtraVars[nested][deep] = %#v, want a nested map", nested["deep"])
	}
	if deep["leaf"] != "a-real-bearer-token" {
		t.Errorf("the deep leaf = %v, want the rendered token", deep["leaf"])
	}
}

// TestInjectorReportsAnUnrenderableNestedLeaf covers the error path through
// the nested walk, which is where a failure is easiest to lose: a walk that
// swallowed an error would produce an artifact missing one variable and no
// indication of it.
func TestInjectorReportsAnUnrenderableNestedLeaf(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	ct := restAPIType()
	ct.Injectors = credtype.Injectors{ExtraVars: map[string]any{
		"outer": map[string]any{"inner": "{{ api_token | nonexistent_filter }}"},
	}}

	_, err := in.Inject([]credtype.Credential{{
		ID: 18, Name: "prod api", Type: ct,
		Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "https://api.example.test"},
	}}, nil)
	if err == nil {
		t.Fatal("Inject() succeeded with an unrenderable nested leaf")
	}
	// The dotted path, so an author knows which leaf to fix.
	if !strings.Contains(err.Error(), "outer.inner") {
		t.Errorf("the error does not name the failing leaf: %v", err)
	}
}

// TestInjectorRefusesAnExtraVariableItCannotRender covers the leaf shape
// Injectors.Validate accepts nowhere, reached here through a document
// written past validation.
func TestInjectorRefusesAnExtraVariableItCannotRender(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	ct := restAPIType()
	ct.Injectors = credtype.Injectors{ExtraVars: map[string]any{
		"a_list": []any{"{{ api_token }}"},
	}}

	_, err := in.Inject([]credtype.Credential{{
		ID: 18, Name: "prod api", Type: ct,
		Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "https://api.example.test"},
	}}, nil)
	if err == nil {
		t.Fatal("Inject() accepted an extra variable that is neither a template, a map, nor a scalar")
	}
	if !errors.Is(err, credtype.ErrInjection) {
		t.Errorf("error = %v, want one matching ErrInjection", err)
	}
}

// TestInjectorReportsAnUnrenderableFileTemplate covers the file target's
// own error path, which fails before the reserved namespace exists and so
// takes a different route out than an env failure does.
func TestInjectorReportsAnUnrenderableFileTemplate(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	ct := restAPIType()
	ct.Injectors = credtype.Injectors{File: map[string]string{
		"template": "{{ api_token | nonexistent_filter }}",
	}}

	_, err := in.Inject([]credtype.Credential{{
		ID: 18, Name: "prod api", Type: ct,
		Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "https://api.example.test"},
	}}, nil)
	if err == nil {
		t.Fatal("Inject() succeeded with an unrenderable file template")
	}
	if !strings.Contains(err.Error(), "file template") {
		t.Errorf("the error does not name the failing target: %v", err)
	}
}

// TestSecretValuesOrdersLongestFirst covers what feeds the masking scrub,
// and the ordering is the load-bearing part: a password that is a prefix of
// a passphrase must not carve the passphrase in half and leave its tail
// exposed.
func TestSecretValuesOrdersLongestFirst(t *testing.T) {
	t.Parallel()

	cred := credtype.Credential{
		Type: credtype.CredentialType{Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "short", Label: "Short", Secret: true},
			{ID: "long", Label: "Long", Secret: true},
			{ID: "same_a", Label: "Same A", Secret: true},
			{ID: "same_b", Label: "Same B", Secret: true},
		}}},
		Inputs: map[string]string{
			"short":  "abc",
			"long":   "abcdefghij",
			"same_a": "bbbb",
			"same_b": "aaaa",
		},
	}

	got := cred.SecretValues()
	if len(got) != 4 {
		t.Fatalf("SecretValues() = %v, want four", got)
	}
	for i := 1; i < len(got); i++ {
		if len(got[i-1]) < len(got[i]) {
			t.Fatalf("SecretValues() is not longest-first: %v", got)
		}
	}
	// Ties break on the value itself, so the result does not depend on map
	// iteration order.
	if got[1] != "aaaa" || got[2] != "bbbb" {
		t.Errorf("SecretValues() = %v, want equal-length values sorted by value", got)
	}
}

// TestTheArtifactCarriesOnlyItsSecretsAcrossTheWire is the fix for a
// defect Phase 22b's own release gate caught, and the negative half is the
// whole point.
//
// The legacy adapter runs in a different process from the injector, so it
// cannot consult the Controller's masking set, and it cannot work out
// which of the values it was handed are secret: a token and a URL look
// identical by then. Its first version registered EVERY injected value,
// which masked the non-secret ones too, and the gate's byte-identity run
// failed with an environment of nothing but placeholders.
//
// So the artifact says which values are secret, and this test holds both
// directions of that: the secret and everything derived from it are in the
// list, and the ordinary values are not.
func TestTheArtifactCarriesOnlyItsSecretsAcrossTheWire(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	ct := restAPIType()
	ct.Injectors = credtype.Injectors{
		Env: map[string]string{
			"REST_API_TOKEN":  "{{ api_token }}",
			"REST_API_URL":    "{{ api_url }}",
			"REST_API_CONFIG": "{{ tower.filename }}",
			"REST_API_AUTH":   "Bearer {{ api_token }}",
		},
		File: map[string]string{"template": "token={{ api_token }}\nurl={{ api_url }}\n"},
	}

	art, err := in.Inject([]credtype.Credential{{
		ID: 18, Name: "prod api", Type: ct,
		Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "https://api.example.test"},
	}}, nil)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	tracked := map[string]bool{}
	for _, v := range art.SecretValues() {
		tracked[v] = true
	}

	// The secret, and both values derived from it.
	for _, want := range []string{
		"a-real-bearer-token",
		"Bearer a-real-bearer-token",
		"token=a-real-bearer-token\nurl=https://api.example.test\n",
	} {
		if !tracked[want] {
			t.Errorf("the artifact does not declare %q secret; it declares %v", want, art.SecretValues())
		}
	}

	// The ordinary values, which must NOT be there. Masking a URL or a
	// generated path would corrupt every later line of output in the
	// process that receives this without protecting anything.
	for _, unwanted := range []string{"https://api.example.test", credtype.FilePath(18, "")} {
		if tracked[unwanted] {
			t.Errorf("the artifact declares the non-secret value %q secret", unwanted)
		}
	}

	// Longest first, because the scrub depends on it: a password that is a
	// prefix of a passphrase must not carve the passphrase in half.
	values := art.SecretValues()
	for i := 1; i < len(values); i++ {
		if len(values[i-1]) < len(values[i]) {
			t.Fatalf("SecretValues() is not longest-first: %v", values)
		}
	}
}

// TestCombineMergesEverySourcesSecrets covers the merge, since a combined
// artifact crossing the wire has to carry every credential's secrets and
// not merely the last one's.
func TestCombineMergesEverySourcesSecrets(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	first := cloudCredential(1, "first", credtype.Injectors{
		Env: map[string]string{"FIRST_TOKEN": "{{ api_token }}"}})
	second := cloudCredential(2, "second", credtype.Injectors{
		Env: map[string]string{"SECOND_TOKEN": "{{ api_token }}"}})

	art, err := in.Inject([]credtype.Credential{first, second}, nil)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	tracked := map[string]bool{}
	for _, v := range art.SecretValues() {
		tracked[v] = true
	}
	for _, want := range []string{"token-first", "token-second"} {
		if !tracked[want] {
			t.Errorf("the combined artifact does not declare %q secret; it declares %v", want, art.SecretValues())
		}
	}
}

// TestAValueTooShortToMaskIsNotTracked covers the length bound, which
// exists for the same reason redact.Literals.Add refuses one: scrubbing a
// short or common value removes that substring from every unrelated later
// line.
func TestAValueTooShortToMaskIsNotTracked(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	ct := restAPIType()
	ct.Injectors = credtype.Injectors{Env: map[string]string{"SHORT": "{{ api_token }}"}}

	art, err := in.Inject([]credtype.Credential{{
		ID: 18, Name: "prod api", Type: ct,
		Inputs: map[string]string{"api_token": "abc", "api_url": "https://api.example.test"},
	}}, nil)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	for _, v := range art.SecretValues() {
		if v == "abc" {
			t.Errorf("a three-character value was declared secret: %v", art.SecretValues())
		}
	}
}
