package credtype_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// TestMachineKeysAreTheWireFormatTheyHaveAlwaysBeen pins the flattened
// credential keys to their literal values.
//
// It used to do something else, and the change is worth explaining rather
// than leaving as an unexplained rewrite. Until Phase 78d these key names
// were written out twice, once in internal/credtype and once in
// internal/credential, because credtype cannot import credential:
// internal/ent imports credtype for its own field.JSON column types, and
// credential's dependency closure reaches internal/ent through
// internal/crypto, so that import would close a cycle. This external test
// package was the escape hatch that held the two copies against each other.
//
// Both now alias pkg/wire, which is in neither loop, so comparing them
// would be comparing a constant to itself and would pass whatever anyone
// did to it. The drift this file existed to catch is gone by construction.
//
// What has NOT gone is the reason the names matter, so that is what it
// asserts now. These strings are a wire format: they key
// wire.DispatchPayload.Secrets, which crosses a NATS stream that can hold
// messages for as long as the deployment's retention window, and
// wire.ChildRequest.Secrets, which crosses the Section 17.5 subprocess
// boundary. Editing one is not a rename, it is a compatibility break
// against every Runner and every queued message that already exists, and
// the failure shape is the same one the old test named: a credential
// injected under a key the transport does not read, which looks like an
// authentication failure against the device rather than a bug here.
func TestMachineKeysAreTheWireFormatTheyHaveAlwaysBeen(t *testing.T) {
	t.Parallel()

	// Written as literals on purpose. Referring to the constant here would
	// make this test agree with any edit, which is precisely what it exists
	// to refuse.
	pins := []struct {
		name, got, want string
	}{
		{"username", credtype.MachineUsername, "username"},
		{"password", credtype.MachinePassword, "password"},
		{"private key", credtype.MachinePrivateKey, "private_key_pem"},
		{"passphrase", credtype.MachinePassphrase, "passphrase"},
		{"certificate", credtype.MachineCertificate, "certificate_pem"},
		{"pfx bundle", credtype.MachinePFX, "pfx_base64"},
	}

	for _, p := range pins {
		if p.got != p.want {
			t.Errorf("the %s key is %q, and changing it breaks every Runner and queued message that already exists (want %q)",
				p.name, p.got, p.want)
		}
	}

	// The three packages must still resolve to one value each. This is
	// cheap, and it is what fails if somebody re-literalizes one of the
	// aliases rather than editing pkg/wire.
	if credtype.MachineUsername != credential.SecretUsername {
		t.Errorf("credtype and credential disagree about the username key: %q and %q",
			credtype.MachineUsername, credential.SecretUsername)
	}
	if credtype.MachineCertificate != credential.SecretCertificatePEM {
		t.Errorf("credtype and credential disagree about the certificate key: %q and %q",
			credtype.MachineCertificate, credential.SecretCertificatePEM)
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
