package credtype_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// TestCheckBinding covers PLAN.md Section 29.3's binding rule: at most one
// credential per type on a definition, except vault credentials, which may
// repeat when each carries a distinct vault identifier.
func TestCheckBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		bound   []credtype.Bound
		wantErr bool
	}{
		{
			name:  "nothing bound",
			bound: nil,
		},
		{
			name:  "one credential",
			bound: []credtype.Bound{{CredentialID: 1, CredentialName: "ssh", Kind: credtype.KindSSH}},
		},
		{
			name: "one of each kind, which is the corpus template's own shape",
			bound: []credtype.Bound{
				{CredentialID: 1, CredentialName: "machine", Kind: credtype.KindSSH},
				{CredentialID: 2, CredentialName: "aws", Kind: credtype.KindCloud},
				{CredentialID: 3, CredentialName: "vault", Kind: credtype.KindVault},
			},
		},
		{
			name: "two of the same kind",
			bound: []credtype.Bound{
				{CredentialID: 1, CredentialName: "prod aws", Kind: credtype.KindCloud},
				{CredentialID: 2, CredentialName: "dev aws", Kind: credtype.KindCloud},
			},
			wantErr: true,
		},
		{
			name: "two machine credentials",
			bound: []credtype.Bound{
				{CredentialID: 1, CredentialName: "root", Kind: credtype.KindSSH},
				{CredentialID: 2, CredentialName: "deploy", Kind: credtype.KindSSH},
			},
			wantErr: true,
		},
		{
			name: "two vault credentials with distinct identifiers, which is the exemption",
			bound: []credtype.Bound{
				{CredentialID: 1, CredentialName: "vault a", Kind: credtype.KindVault, VaultIdentifier: "prod"},
				{CredentialID: 2, CredentialName: "vault b", Kind: credtype.KindVault, VaultIdentifier: "staging"},
			},
		},
		{
			name: "three vault credentials with distinct identifiers",
			bound: []credtype.Bound{
				{CredentialID: 1, CredentialName: "a", Kind: credtype.KindVault, VaultIdentifier: "one"},
				{CredentialID: 2, CredentialName: "b", Kind: credtype.KindVault, VaultIdentifier: "two"},
				{CredentialID: 3, CredentialName: "c", Kind: credtype.KindVault, VaultIdentifier: "three"},
			},
		},
		{
			name: "two vault credentials sharing an identifier",
			// The exemption is per identifier, not per kind. Two vault
			// credentials with the same id would produce two --vault-id
			// arguments ansible-playbook cannot tell apart.
			bound: []credtype.Bound{
				{CredentialID: 1, CredentialName: "vault a", Kind: credtype.KindVault, VaultIdentifier: "prod"},
				{CredentialID: 2, CredentialName: "vault b", Kind: credtype.KindVault, VaultIdentifier: "prod"},
			},
			wantErr: true,
		},
		{
			name: "two vault credentials both unnamed",
			// An empty identifier is Ansible's default vault identity, a
			// real value rather than an absent one, so a definition can
			// have at most one of those exactly as it can of any other.
			bound: []credtype.Bound{
				{CredentialID: 1, CredentialName: "vault a", Kind: credtype.KindVault},
				{CredentialID: 2, CredentialName: "vault b", Kind: credtype.KindVault},
			},
			wantErr: true,
		},
		{
			name: "one named and one unnamed vault credential",
			bound: []credtype.Bound{
				{CredentialID: 1, CredentialName: "default vault", Kind: credtype.KindVault},
				{CredentialID: 2, CredentialName: "prod vault", Kind: credtype.KindVault, VaultIdentifier: "prod"},
			},
		},
		{
			name: "a vault identifier on a non-vault credential is ignored",
			// The field is only meaningful for vault, so two cloud
			// credentials still conflict regardless of what it holds.
			bound: []credtype.Bound{
				{CredentialID: 1, CredentialName: "a", Kind: credtype.KindCloud, VaultIdentifier: "one"},
				{CredentialID: 2, CredentialName: "b", Kind: credtype.KindCloud, VaultIdentifier: "two"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := credtype.CheckBinding(tt.bound)
			if tt.wantErr {
				if err == nil {
					t.Fatal("CheckBinding() accepted a conflicting set")
				}
				if !errors.Is(err, credtype.ErrBindingConflict) {
					t.Errorf("error = %v, want one matching ErrBindingConflict", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckBinding() refused a valid set: %v", err)
			}
		})
	}
}

// TestBindingConflictNamesBothCredentials is what makes the error usable.
//
// The operator seeing it has to choose which credential to remove, and
// "conflict" alone does not help them choose. Naming one is not enough
// either: they need to know what it collides with.
func TestBindingConflictNamesBothCredentials(t *testing.T) {
	t.Parallel()

	err := credtype.CheckBinding([]credtype.Bound{
		{CredentialID: 1, CredentialName: "prod aws", Kind: credtype.KindCloud},
		{CredentialID: 2, CredentialName: "dev aws", Kind: credtype.KindCloud},
	})
	if err == nil {
		t.Fatal("CheckBinding() accepted a conflicting set")
	}
	for _, want := range []string{"prod aws", "dev aws", "cloud"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// TestVaultConflictDescribesTheUnnamedIdentity covers the message for two
// vault credentials that both left the identifier blank, where a naive
// message would read "with the identifier " and leave the operator to
// guess what went wrong.
func TestVaultConflictDescribesTheUnnamedIdentity(t *testing.T) {
	t.Parallel()

	err := credtype.CheckBinding([]credtype.Bound{
		{CredentialID: 1, CredentialName: "vault a", Kind: credtype.KindVault},
		{CredentialID: 2, CredentialName: "vault b", Kind: credtype.KindVault},
	})
	if err == nil {
		t.Fatal("CheckBinding() accepted two unnamed vault credentials")
	}
	if !strings.Contains(err.Error(), "default") {
		t.Errorf("the error does not describe the unnamed vault identity: %v", err)
	}
}

// TestVaultIdentifierOf covers the extraction, including the case that
// makes it worth having a function at all: a non-vault credential whose
// inputs happen to contain a vault_id key.
func TestVaultIdentifierOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		kind   credtype.Kind
		inputs map[string]string
		want   string
	}{
		{
			name:   "a vault credential with an identifier",
			kind:   credtype.KindVault,
			inputs: map[string]string{"vault_id": "prod"},
			want:   "prod",
		},
		{
			name:   "a vault credential with none",
			kind:   credtype.KindVault,
			inputs: map[string]string{"vault_password": "x"},
			want:   "",
		},
		{
			name: "surrounding whitespace is not part of the identity",
			// A pasted identifier with a trailing space would otherwise
			// count as a distinct identity, letting two credentials bind
			// that ansible-playbook then cannot tell apart.
			kind:   credtype.KindVault,
			inputs: map[string]string{"vault_id": "  prod\n"},
			want:   "prod",
		},
		{
			name:   "a non-vault credential is always the empty identity",
			kind:   credtype.KindCloud,
			inputs: map[string]string{"vault_id": "prod"},
			want:   "",
		},
		{
			name:   "no inputs at all",
			kind:   credtype.KindVault,
			inputs: nil,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := credtype.VaultIdentifierOf(tt.kind, tt.inputs); got != tt.want {
				t.Errorf("VaultIdentifierOf() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSecretValuesFeedsTheMaskingRuleset covers the accessor that decides
// which of a credential's values get registered for scrubbing.
func TestSecretValuesFeedsTheMaskingRuleset(t *testing.T) {
	t.Parallel()

	cred := credtype.Credential{
		Name: "prod api",
		Type: credtype.CredentialType{
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{
				{ID: "api_token", Label: "Token", Secret: true},
				{ID: "api_secret", Label: "Secret", Secret: true},
				{ID: "region", Label: "Region"},
			}},
		},
		Inputs: map[string]string{
			"api_token":  "short-one",
			"api_secret": "a-much-longer-secret-value",
			"region":     "us-east-1",
		},
	}

	got := cred.SecretValues()
	if len(got) != 2 {
		t.Fatalf("SecretValues() = %v, want the two secret values only", got)
	}
	// Longest first, because the scrub depends on it: a value that is a
	// prefix of another must not carve the longer one in half.
	if len(got[0]) < len(got[1]) {
		t.Errorf("SecretValues() = %v, want longest first", got)
	}
	for _, v := range got {
		if v == "us-east-1" {
			t.Error("a non-secret value was included, which would scrub an ordinary region name out of every later log line")
		}
	}
}

// TestWithDefaultsDoesNotMutateTheCaller guards a real hazard: the inputs
// map may be the one the resolver handed out, and filling defaults into it
// would make a stored credential appear to hold values nobody set.
func TestWithDefaultsDoesNotMutateTheCaller(t *testing.T) {
	t.Parallel()

	original := map[string]string{"api_token": "supplied"}
	cred := credtype.Credential{
		Type: credtype.CredentialType{
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{
				{ID: "api_token", Label: "Token", Secret: true},
				{ID: "region", Label: "Region", Default: "us-east-1"},
			}},
		},
		Inputs: original,
	}

	filled := cred.WithDefaults()
	if filled.Inputs["region"] != "us-east-1" {
		t.Errorf("WithDefaults() did not fill the default: %v", filled.Inputs)
	}
	if filled.Inputs["api_token"] != "supplied" {
		t.Errorf("WithDefaults() overwrote a supplied value: %v", filled.Inputs)
	}
	if _, leaked := original["region"]; leaked {
		t.Error("WithDefaults() mutated the caller's map")
	}
}
