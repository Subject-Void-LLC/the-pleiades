package credtype_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// TestMachineKeysAgreeWithTheFlattenedCredential is what keeps the two
// halves of a restated contract honest.
//
// internal/credtype cannot import internal/credential: internal/ent imports
// credtype for its own field.JSON column types, and credential's dependency
// closure reaches internal/ent through internal/crypto, so the import would
// close a cycle and nothing in the module would build. The four key names
// are therefore written out twice.
//
// This file is an EXTERNAL test package, which is the escape hatch: it is
// compiled after both packages and may import either, so it can hold them
// against each other. Without it the two definitions drift silently, and
// the failure shape is the worst available: a machine credential injected
// under a key the transport does not read, which presents as an
// authentication failure against the device rather than as a bug here.
func TestMachineKeysAgreeWithTheFlattenedCredential(t *testing.T) {
	t.Parallel()

	pairs := []struct {
		name             string
		injector, walker string
	}{
		{"username", credtype.MachineUsername, credential.SecretUsername},
		{"password", credtype.MachinePassword, credential.SecretPassword},
		{"private key", credtype.MachinePrivateKey, credential.SecretPrivateKeyPEM},
		{"passphrase", credtype.MachinePassphrase, credential.SecretPassphrase},
	}

	for _, p := range pairs {
		if p.injector != p.walker {
			t.Errorf("the %s key is %q in credtype and %q in credential", p.name, p.injector, p.walker)
		}
	}
}

// TestTheFlattenedMachineCredentialRoundTrips proves the agreement is
// useful rather than merely true: an injected machine identity, handed to
// the same Unflatten every dispatch path already uses, produces the
// credential the transport dials with.
func TestTheFlattenedMachineCredentialRoundTrips(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	art, err := in.Inject([]credtype.Credential{{
		ID:   3,
		Name: "lab machine",
		Type: credtype.CredentialType{
			Name: "Machine", Kind: credtype.KindSSH, Namespace: "ssh", Managed: true,
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{
				{ID: "username", Label: "Username"},
				{ID: "password", Label: "Password", Secret: true},
				{ID: "ssh_key_data", Label: "SSH Private Key", Secret: true},
				{ID: "ssh_key_unlock", Label: "Passphrase", Secret: true},
			}},
		},
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

	got := credential.Unflatten(machine)
	if got.Username != "operator" {
		t.Errorf("Username = %q, want %q", got.Username, "operator")
	}
	if got.Password != "a-real-password" {
		t.Errorf("Password = %q, want the injected password", got.Password)
	}
	if string(got.PrivateKeyPEM) != "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n" {
		t.Errorf("PrivateKeyPEM = %q, want the injected key", got.PrivateKeyPEM)
	}
	if got.Passphrase != "a-real-passphrase" {
		t.Errorf("Passphrase = %q, want the injected passphrase", got.Passphrase)
	}

	// Flatten is the inverse and every dispatch path uses it, so the two
	// directions must agree on the same four keys.
	reflattened := credential.Flatten(got)
	if len(reflattened) != len(machine) {
		t.Fatalf("Flatten(Unflatten(machine)) = %v, want the same four keys as %v", reflattened, machine)
	}
	for k, v := range machine {
		if reflattened[k] != v {
			t.Errorf("the round trip changed %s: %q became %q", k, v, reflattened[k])
		}
	}
}
