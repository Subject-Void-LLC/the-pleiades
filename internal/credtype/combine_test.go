package credtype_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// The collision rules. AWX resolves each of these silently, last writer
// winning; this platform refuses every one of them. The assertion that
// carries the design is not that an error happens, it is that the message
// names BOTH credentials: an operator seeing it has to choose which one to
// remove, and "conflict" alone does not help them choose.

// injectTwo renders two credentials of the same shape and returns the
// combined result, so a collision test states only what differs.
func injectTwo(t *testing.T, a, b credtype.Credential) (credtype.Artifact, error) {
	t.Helper()

	in, _ := newTestInjector(t)
	return in.Inject([]credtype.Credential{a, b}, nil)
}

// cloudCredential builds a cloud credential with a given injector document.
func cloudCredential(id int, name string, inj credtype.Injectors) credtype.Credential {
	ct := restAPIType()
	ct.Injectors = inj
	return credtype.Credential{
		ID:     id,
		Name:   name,
		Type:   ct,
		Inputs: map[string]string{"api_token": "token-" + name, "api_url": "https://" + name + ".test"},
	}
}

func TestCombineRefusesEveryCollision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b credtype.Credential
		// want names the two credentials and the colliding key.
		want []string
	}{
		{
			name: "the same environment variable",
			a:    cloudCredential(1, "first", credtype.Injectors{Env: map[string]string{"API_TOKEN": "{{ api_token }}"}}),
			b:    cloudCredential(2, "second", credtype.Injectors{Env: map[string]string{"API_TOKEN": "{{ api_token }}"}}),
			want: []string{"first", "second", "API_TOKEN"},
		},
		{
			name: "the same extra variable",
			a:    cloudCredential(1, "first", credtype.Injectors{ExtraVars: map[string]any{"token": "{{ api_token }}"}}),
			b:    cloudCredential(2, "second", credtype.Injectors{ExtraVars: map[string]any{"token": "{{ api_token }}"}}),
			want: []string{"first", "second", "token"},
		},
		{
			name: "the same nested extra-variable leaf, reported by its dotted path",
			a: cloudCredential(1, "first", credtype.Injectors{
				ExtraVars: map[string]any{"outer": map[string]any{"inner": "{{ api_token }}"}}}),
			b: cloudCredential(2, "second", credtype.Injectors{
				ExtraVars: map[string]any{"outer": map[string]any{"inner": "{{ api_token }}"}}}),
			want: []string{"first", "second", "outer.inner"},
		},
		{
			name: "a leaf landing where a nested map already is",
			a: cloudCredential(1, "first", credtype.Injectors{
				ExtraVars: map[string]any{"outer": map[string]any{"inner": "{{ api_token }}"}}}),
			b:    cloudCredential(2, "second", credtype.Injectors{ExtraVars: map[string]any{"outer": "{{ api_token }}"}}),
			want: []string{"second", "outer"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := injectTwo(t, tt.a, tt.b)
			if err == nil {
				t.Fatal("Inject() silently picked a winner between two colliding credentials")
			}
			if !errors.Is(err, credtype.ErrInjectorConflict) {
				t.Fatalf("error = %v, want one matching ErrInjectorConflict", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the conflict does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestCombineDeepMergesDistinctLeaves is the other half of the nesting
// rule, and it matters as much as the refusal: refusing two credentials
// that each contribute a different leaf under one parent would make a
// nested injector document unusable alongside any other credential.
func TestCombineDeepMergesDistinctLeaves(t *testing.T) {
	t.Parallel()

	art, err := injectTwo(t,
		cloudCredential(1, "first", credtype.Injectors{
			ExtraVars: map[string]any{"outer": map[string]any{"a": "{{ api_url }}"}}}),
		cloudCredential(2, "second", credtype.Injectors{
			ExtraVars: map[string]any{"outer": map[string]any{"b": "{{ api_url }}"}}}),
	)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	outer, ok := art.ExtraVars["outer"].(map[string]any)
	if !ok {
		t.Fatalf("ExtraVars[outer] = %#v, want a nested map", art.ExtraVars["outer"])
	}
	if outer["a"] != "https://first.test" || outer["b"] != "https://second.test" {
		t.Errorf("ExtraVars[outer] = %v, want both leaves merged", outer)
	}
}

// TestCombineRefusesTwoMachineIdentities covers the rule that has nothing
// to do with an injector document: a run authenticates as exactly one
// thing, and two machine credentials is a question with no answer.
func TestCombineRefusesTwoMachineIdentities(t *testing.T) {
	t.Parallel()

	machine := func(id int, name string) credtype.Credential {
		return credtype.Credential{
			ID:   id,
			Name: name,
			Type: credtype.CredentialType{
				Name: "Machine", Kind: credtype.KindSSH, Namespace: "ssh", Managed: true,
				Inputs: credtype.InputSchema{Fields: []credtype.InputField{{ID: "username", Label: "Username"}}},
			},
			Inputs: map[string]string{"username": name},
		}
	}

	_, err := injectTwo(t, machine(1, "first"), machine(2, "second"))
	if err == nil {
		t.Fatal("Inject() accepted two machine credentials on one run")
	}
	if !errors.Is(err, credtype.ErrInjectorConflict) {
		t.Fatalf("error = %v, want one matching ErrInjectorConflict", err)
	}
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the conflict does not name %q: %v", want, err)
		}
	}
}

// TestCombineVaultIdentities covers the one kind the binding rule exempts:
// several vault credentials are legal, and only a shared identifier is not.
func TestCombineVaultIdentities(t *testing.T) {
	t.Parallel()

	vault := func(id int, name, identifier string) credtype.Credential {
		return credtype.Credential{
			ID: id, Name: name, Type: vaultType(),
			Inputs: map[string]string{"vault_password": "password-" + name, "vault_id": identifier},
		}
	}

	t.Run("distinct identifiers combine, ordered", func(t *testing.T) {
		t.Parallel()

		art, err := injectTwo(t, vault(2, "second", "staging"), vault(1, "first", "prod"))
		if err != nil {
			t.Fatalf("Inject() error = %v", err)
		}
		got := art.Vault()
		if len(got) != 2 {
			t.Fatalf("Vault() = %+v, want two identities", got)
		}
		if got[0].Identifier != "prod" || got[1].Identifier != "staging" {
			t.Errorf("Vault() = %+v, want the identities ordered", got)
		}
		if len(art.Files) != 2 || art.Files[0].Path == art.Files[1].Path {
			t.Errorf("Files = %+v, want two distinct password files", art.Files)
		}
	})

	t.Run("a shared identifier is refused", func(t *testing.T) {
		t.Parallel()

		_, err := injectTwo(t, vault(1, "first", "prod"), vault(2, "second", "prod"))
		if err == nil {
			t.Fatal("Inject() accepted two vault credentials sharing an identifier")
		}
		for _, want := range []string{"first", "second", "prod"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the conflict does not name %q: %v", want, err)
			}
		}
	})

	t.Run("two default identities are refused and the message says so", func(t *testing.T) {
		t.Parallel()

		_, err := injectTwo(t, vault(1, "first", ""), vault(2, "second", ""))
		if err == nil {
			t.Fatal("Inject() accepted two vault credentials both using the default identity")
		}
		// An operator who left both blank would otherwise read "with the
		// identifier " and have to guess what went wrong.
		if !strings.Contains(err.Error(), "default") {
			t.Errorf("the conflict does not describe the unnamed identity: %v", err)
		}
	})
}

// TestCombineOfNothing pins the empty case, since a template binding no
// credentials takes this path on every launch.
func TestCombineOfNothing(t *testing.T) {
	t.Parallel()

	art, err := credtype.Combine()
	if err != nil {
		t.Fatalf("Combine() error = %v", err)
	}
	if !art.Empty() {
		t.Errorf("Combine() = %+v, want an empty artifact", art)
	}
	if art.Env != nil || art.ExtraVars != nil {
		t.Errorf("Combine() = %+v, want nil maps rather than empty ones on the wire", art)
	}
}

// TestCombineSortsItsFiles pins the ordering, which is what makes a
// container spec built from an artifact byte-stable across runs rather than
// dependent on Go's map iteration order.
func TestCombineSortsItsFiles(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	multi := credtype.Injectors{File: map[string]string{
		"template.zulu":  "{{ api_url }}",
		"template.alpha": "{{ api_url }}",
		"template.mike":  "{{ api_url }}",
	}}
	art, err := in.Inject([]credtype.Credential{cloudCredential(1, "first", multi)}, nil)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if len(art.Files) != 3 {
		t.Fatalf("Files = %+v, want three", art.Files)
	}
	for i := 1; i < len(art.Files); i++ {
		if art.Files[i-1].Path >= art.Files[i].Path {
			t.Fatalf("Files are not sorted by path: %+v", art.Files)
		}
	}
}
