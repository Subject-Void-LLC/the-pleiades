// Package credtype_test's certificate half: a KindCryptography credential
// becoming the machine identity a run authenticates as.
//
// Kept beside machine_test.go rather than inside it because the two answer
// different questions. That file pins the flattened key names as a wire
// format; this one is about a credential type reaching those keys at all.
package credtype_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// certificateType is a user-defined credential type that presents a client
// certificate.
//
// It is built here rather than shipped in internal/credtype/managed,
// because AWX has no such type and shipping one would be the first managed
// type this platform ships that AWX does not have. Nothing forces that
// decision yet: a type created through the credential API is validated
// against the namespace pattern alone, so this works as an ordinary
// user-defined type and the reserved-prefix question stays deferred.
func certificateType() credtype.CredentialType {
	return credtype.CredentialType{
		Name:      "Client Certificate",
		Kind:      credtype.KindCryptography,
		Namespace: "client_certificate",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: credtype.CertificateInputCertificate, Label: "Certificate", Secret: true, Multiline: true},
			{ID: credtype.CertificateInputPrivateKey, Label: "Private Key", Secret: true, Multiline: true},
			{ID: credtype.CertificateInputKeyUnlock, Label: "Private Key Passphrase", Secret: true},
			{ID: credtype.CertificateInputPFX, Label: "PKCS#12 Bundle", Secret: true, Multiline: true},
		}},
	}
}

// TestACertificateCredentialBecomesTheMachineIdentity is the whole point of
// routing KindCryptography through the machine target: the values come out
// under the keys pkg/winrmexec reads, with no injector document involved.
func TestACertificateCredentialBecomesTheMachineIdentity(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	art, err := in.Inject([]credtype.Credential{{
		ID:   7,
		Name: "lab certificate",
		Type: certificateType(),
		Inputs: map[string]string{
			credtype.CertificateInputCertificate: "-----BEGIN CERTIFICATE-----\nbody\n-----END CERTIFICATE-----\n",
			credtype.CertificateInputPrivateKey:  "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n",
		},
	}}, nil)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	machine, ok := art.Machine()
	if !ok {
		t.Fatal("a certificate credential produced no machine identity")
	}
	if machine[credtype.MachineCertificate] == "" {
		t.Errorf("the certificate is absent from the machine identity: %v", keysOf(machine))
	}
	if machine[credtype.MachinePrivateKey] == "" {
		t.Errorf("the private key is absent from the machine identity: %v", keysOf(machine))
	}

	// The round trip the transport actually makes. If these keys ever stop
	// agreeing with internal/credential, this is where it shows up as a
	// credential the transport cannot read rather than as a failed login.
	got := credential.Unflatten(machine)
	if len(got.CertificatePEM) == 0 || len(got.PrivateKeyPEM) == 0 {
		t.Errorf("Unflatten lost the certificate or the key: %+v", got)
	}
}

// TestABundleAlsoBecomesTheMachineIdentity covers the other supply form, so
// the PKCS#12 path has a caller before the decoder exists.
func TestABundleAlsoBecomesTheMachineIdentity(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	art, err := in.Inject([]credtype.Credential{{
		ID:   8,
		Name: "lab bundle",
		Type: certificateType(),
		Inputs: map[string]string{
			credtype.CertificateInputPFX:       "MIIKzQIBAzCCCoc=",
			credtype.CertificateInputKeyUnlock: "a-real-passphrase",
		},
	}}, nil)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	machine, ok := art.Machine()
	if !ok {
		t.Fatal("a bundle credential produced no machine identity")
	}
	if machine[credtype.MachinePFX] == "" {
		t.Errorf("the bundle is absent from the machine identity: %v", keysOf(machine))
	}
	if machine[credtype.MachinePassphrase] != "a-real-passphrase" {
		t.Error("the passphrase that unlocks the bundle did not travel with it")
	}
}

// TestIncompleteCertificateMaterialIsRefusedAtInjection proves each broken
// shape is refused where the credential still has a name.
func TestIncompleteCertificateMaterialIsRefusedAtInjection(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		inputs map[string]string
		want   string
	}{
		{
			name: "a bundle and a loose pair at once",
			inputs: map[string]string{
				credtype.CertificateInputPFX:         "MIIKzQIBAzCCCoc=",
				credtype.CertificateInputCertificate: "-----BEGIN CERTIFICATE-----\nbody\n-----END CERTIFICATE-----\n",
				credtype.CertificateInputPrivateKey:  "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n",
			},
			want: "two ways to supply one identity",
		},
		{
			name: "a certificate with no key",
			inputs: map[string]string{
				credtype.CertificateInputCertificate: "-----BEGIN CERTIFICATE-----\nbody\n-----END CERTIFICATE-----\n",
			},
			want: "cannot prove possession",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in, _ := newTestInjector(t)

			_, err := in.Inject([]credtype.Credential{{
				ID: 9, Name: "broken certificate", Type: certificateType(), Inputs: tt.inputs,
			}}, nil)
			if err == nil {
				t.Fatal("broken certificate material was injected")
			}
			if !errors.Is(err, credtype.ErrInvalidCredential) {
				t.Errorf("error = %v, want it to wrap ErrInvalidCredential", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "broken certificate") {
				t.Errorf("error = %v, want it to name the credential to go and edit", err)
			}
		})
	}
}

// TestACertificateAndAMachineCredentialCannotBothBeBound is the refusal
// this seam was chosen to inherit rather than to write.
//
// Combine already refuses two machine identities on the grounds that a run
// authenticates as exactly one thing. Routing a certificate through the
// same slot means that rule covers it with no new code, and this test is
// what proves the inheritance is real rather than assumed.
func TestACertificateAndAMachineCredentialCannotBothBeBound(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)

	_, err := in.Inject([]credtype.Credential{
		{
			ID:   10,
			Name: "lab machine",
			Type: credtype.CredentialType{
				Name: "Machine", Kind: credtype.KindSSH, Namespace: "ssh", Managed: true,
				Inputs: credtype.InputSchema{Fields: []credtype.InputField{
					{ID: "username", Label: "Username"},
					{ID: "password", Label: "Password", Secret: true},
				}},
			},
			Inputs: map[string]string{"username": "operator", "password": "a-real-password"},
		},
		{
			ID:   11,
			Name: "lab certificate",
			Type: certificateType(),
			Inputs: map[string]string{
				credtype.CertificateInputCertificate: "-----BEGIN CERTIFICATE-----\nbody\n-----END CERTIFICATE-----\n",
				credtype.CertificateInputPrivateKey:  "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n",
			},
		},
	}, nil)

	if err == nil {
		t.Fatal("a template bound a machine credential and a certificate credential at once")
	}
	if !errors.Is(err, credtype.ErrInjectorConflict) {
		t.Errorf("error = %v, want it to wrap ErrInjectorConflict", err)
	}
	if !strings.Contains(err.Error(), "exactly one identity") {
		t.Errorf("error = %v, want it to say a run authenticates as one identity", err)
	}
}

// keysOf names the keys present in a machine identity, for an error message
// that says what WAS there rather than only what was not.
func keysOf(machine map[string]string) []string {
	keys := make([]string, 0, len(machine))
	for k := range machine {
		keys = append(keys, k)
	}
	return keys
}

// TestACryptographyCredentialThatIsNotACertificateIsLeftAlone is the
// negative control for the whole KindCryptography branch, and it guards a
// regression that an earlier draft of this work actually had.
//
// The kind means "a signing or verification key" and covers far more than
// client certificates: AWX's GPG Public Key type is registered under it,
// and any operator may pick it for a custom type. So the machine slot has
// to be claimed on the evidence of a CERTIFICATE or a BUNDLE, never on the
// kind and never on an input that happens to be called private_key.
//
// An earlier version refused this credential outright, which would have
// turned every launch binding an existing code-signing credential into an
// injection failure complaining about a certificate it was never meant to
// carry.
func TestACryptographyCredentialThatIsNotACertificateIsLeftAlone(t *testing.T) {
	t.Parallel()

	signing := credtype.CredentialType{
		Name:      "Code Signing Key",
		Kind:      credtype.KindCryptography,
		Namespace: "code_signing",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: credtype.CertificateInputPrivateKey, Label: "Signing Key", Secret: true, Multiline: true},
			{ID: credtype.CertificateInputKeyUnlock, Label: "Passphrase", Secret: true},
		}},
		Injectors: credtype.Injectors{Env: map[string]string{"SIGNING_KEY": "{{ private_key }}"}},
	}

	in, _ := newTestInjector(t)
	art, err := in.Inject([]credtype.Credential{{
		ID: 12, Name: "prod signing key", Type: signing,
		Inputs: map[string]string{
			credtype.CertificateInputPrivateKey: "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n",
			credtype.CertificateInputKeyUnlock:  "a-real-passphrase",
		},
	}}, nil)
	if err != nil {
		t.Fatalf("a signing credential was refused: %v", err)
	}

	// It must NOT claim the single machine-identity slot, or binding it
	// beside a real machine credential would be refused as a conflict with
	// something that authenticates as nobody.
	if _, ok := art.Machine(); ok {
		t.Error("a signing credential claimed the machine identity slot")
	}
	// Its own injector still works, which is the half that proves nothing
	// was taken away from it.
	if art.Env["SIGNING_KEY"] == "" {
		t.Errorf("the signing credential's own injector did not run: %v", art.Env)
	}
}

// TestASigningCredentialAndAMachineCredentialCoexist is the consequence of
// the test above, stated where somebody would look for it: the two occupy
// different slots, so binding both is ordinary rather than a conflict.
func TestASigningCredentialAndAMachineCredentialCoexist(t *testing.T) {
	t.Parallel()

	in, _ := newTestInjector(t)
	art, err := in.Inject([]credtype.Credential{
		{
			ID: 13, Name: "lab machine",
			Type: credtype.CredentialType{
				Name: "Machine", Kind: credtype.KindSSH, Namespace: "ssh", Managed: true,
				Inputs: credtype.InputSchema{Fields: []credtype.InputField{
					{ID: "username", Label: "Username"},
					{ID: "password", Label: "Password", Secret: true},
				}},
			},
			Inputs: map[string]string{"username": "operator", "password": "a-real-password"},
		},
		{
			ID: 14, Name: "prod signing key",
			Type: credtype.CredentialType{
				Name: "Code Signing Key", Kind: credtype.KindCryptography, Namespace: "code_signing",
				Inputs: credtype.InputSchema{Fields: []credtype.InputField{
					{ID: credtype.CertificateInputPrivateKey, Label: "Signing Key", Secret: true},
				}},
				Injectors: credtype.Injectors{Env: map[string]string{"SIGNING_KEY": "{{ private_key }}"}},
			},
			Inputs: map[string]string{credtype.CertificateInputPrivateKey: "-----BEGIN PRIVATE KEY-----\nk\n-----END PRIVATE KEY-----\n"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("a machine credential and a signing credential could not coexist: %v", err)
	}

	machine, ok := art.Machine()
	if !ok {
		t.Fatal("the machine credential lost its slot")
	}
	if machine[credtype.MachineUsername] != "operator" {
		t.Errorf("the machine identity is not the machine credential's: %v", machine)
	}
	if art.Env["SIGNING_KEY"] == "" {
		t.Error("the signing credential's injector did not run")
	}
}

// TestBindingAMachineAndACertificateIsRefusedAtWriteTime closes the gap
// between what the documentation promised and where the refusal happened.
//
// Combine refuses this pair at fan-out, which is correct but late: the
// failure lands on whoever launched the template, against a device, naming
// two credentials they may not have bound. docs/10-running-in-production.md
// states the rule as "a template cannot bind both", and the same document
// makes the write-time/run-time distinction load bearing elsewhere, so that
// sentence has to be true at the write.
func TestBindingAMachineAndACertificateIsRefusedAtWriteTime(t *testing.T) {
	t.Parallel()

	err := credtype.CheckBinding([]credtype.Bound{
		{CredentialID: 1, CredentialName: "lab machine", Kind: credtype.KindSSH},
		{CredentialID: 2, CredentialName: "lab certificate", Kind: credtype.KindCryptography, PresentsCertificate: true},
	})
	if err == nil {
		t.Fatal("a machine credential and a certificate credential were bound together")
	}
	if !errors.Is(err, credtype.ErrBindingConflict) {
		t.Errorf("error = %v, want ErrBindingConflict", err)
	}
	if !strings.Contains(err.Error(), "exactly one identity") {
		t.Errorf("error = %v, want it to say a run authenticates as one identity", err)
	}
	for _, name := range []string{"lab machine", "lab certificate"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error = %v, want it to name %q", err, name)
		}
	}
}

// TestBindingAMachineAndASigningKeyIsAccepted is the negative control, and
// it is the reason PresentsCertificate exists rather than a test on Kind.
//
// A cryptography credential that carries no certificate competes for
// nothing, so refusing it would break an ordinary arrangement that has
// never had anything to do with this feature.
func TestBindingAMachineAndASigningKeyIsAccepted(t *testing.T) {
	t.Parallel()

	if err := credtype.CheckBinding([]credtype.Bound{
		{CredentialID: 1, CredentialName: "lab machine", Kind: credtype.KindSSH},
		{CredentialID: 2, CredentialName: "prod signing key", Kind: credtype.KindCryptography},
	}); err != nil {
		t.Errorf("a signing credential was refused beside a machine credential: %v", err)
	}
}

// TestPresentsCertificateForReadsTheValues pins the evidence test itself,
// since two callers now depend on it agreeing with the injector.
func TestPresentsCertificateForReadsTheValues(t *testing.T) {
	t.Parallel()

	crypto := func(inputs map[string]string) credtype.Credential {
		return credtype.Credential{
			Type:   credtype.CredentialType{Kind: credtype.KindCryptography},
			Inputs: inputs,
		}
	}
	for _, tt := range []struct {
		name string
		cred credtype.Credential
		want bool
	}{
		{"a certificate", crypto(map[string]string{credtype.CertificateInputCertificate: "c"}), true},
		{"a bundle", crypto(map[string]string{credtype.CertificateInputPFX: "b"}), true},
		{"a signing key", crypto(map[string]string{credtype.CertificateInputPrivateKey: "k"}), false},
		{"nothing at all", crypto(nil), false},
		{
			"another kind entirely",
			credtype.Credential{
				Type:   credtype.CredentialType{Kind: credtype.KindSSH},
				Inputs: map[string]string{credtype.CertificateInputCertificate: "c"},
			},
			false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := credtype.PresentsCertificateFor(tt.cred); got != tt.want {
				t.Errorf("PresentsCertificateFor() = %v, want %v", got, tt.want)
			}
		})
	}
}
