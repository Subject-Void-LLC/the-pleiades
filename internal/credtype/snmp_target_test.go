// Tests of the SNMP credential kind and of the certificate check every
// client certificate input is held to, through the real injector, the
// shipped pleiades_snmp type and the store's write-time check.
package credtype_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// testKeyPEM is a private key block as it lands beside a certificate.
const testKeyPEM = "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49\n-----END PRIVATE KEY-----\n"

// TestPleiadesSNMP_InjectsTheWireKeysAndNothingElse: a v3 user's inputs
// become the artifact's SNMP credential under the wire's own fields, its
// passphrases join the masking set and its names do not, and nothing reaches
// an environment, an extra variable, a file or the machine identity, which
// is what leaves the Ansible path with nothing from it.
func TestPleiadesSNMP_InjectsTheWireKeysAndNothingElse(t *testing.T) {
	art, err := injectAll(t, kubernetesCredential(t, 9, "pleiades_snmp", map[string]string{
		"username": "monitor", "auth_protocol": "sha384", "auth_passphrase": "auth-pass-phrase",
		"priv_protocol": "aes256-reeder", "priv_passphrase": "priv-pass-phrase",
	}))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := art.SNMP()
	want := wire.SNMPCredential{User: "monitor", AuthProtocol: wire.SNMPAuthSHA384, AuthPassphrase: "auth-pass-phrase", PrivProtocol: wire.SNMPPrivAES256Reeder, PrivPassphrase: "priv-pass-phrase"}
	if !ok || got != want {
		t.Fatalf("the artifact's SNMP credential = %v (%v), want %v", got, ok, want)
	}
	if _, machine := art.Machine(); machine || len(art.Env) != 0 || len(art.ExtraVars) != 0 || len(art.Files) != 0 {
		t.Errorf("an SNMP credential injected env %v, vars %v, files %v or a machine identity", art.Env, art.ExtraVars, art.Files)
	}
	masked := art.SecretValues()
	if !slices.Contains(masked, "auth-pass-phrase") || !slices.Contains(masked, "priv-pass-phrase") || slices.Contains(masked, "monitor") || slices.Contains(masked, "aes256-reeder") {
		t.Errorf("masked %q, want the passphrases and not the user or the protocol", masked)
	}
}

// TestPleiadesSNMP_RefusesWhatTheWireRefuses: every rule of
// wire.SNMPCredential.Check refuses at injection naming the input, never a
// value; a short community is accepted only with allow_short_community; and
// a credential holding nothing is refused.
func TestPleiadesSNMP_RefusesWhatTheWireRefuses(t *testing.T) {
	if art, err := injectAll(t, kubernetesCredential(t, 1, "pleiades_snmp", map[string]string{"community": "public", "allow_short_community": "true"})); err != nil {
		t.Errorf("an allowed short community was refused: %v", err)
	} else if s, _ := art.SNMP(); s.Community != "public" || slices.Contains(art.SecretValues(), "public") {
		t.Errorf("an allowed short community injected %v, masked %v; want it carried and left out of the masking set", s, art.SecretValues())
	}
	for name, tc := range map[string]struct {
		inputs map[string]string
		input  string
	}{
		"a short community":            {map[string]string{"community": "public"}, "allow_short_community"},
		"a community with a user":      {map[string]string{"community": "c0mmunity-s3cret", "username": "u"}, `"community"`},
		"privacy without auth":         {map[string]string{"username": "u", "priv_protocol": "des", "priv_passphrase": "priv-pass-phrase"}, `"priv_protocol"`},
		"a protocol without a user":    {map[string]string{"auth_protocol": "sha", "auth_passphrase": "auth-pass-phrase"}, `"username"`},
		"a short passphrase":           {map[string]string{"username": "u", "auth_protocol": "md5", "auth_passphrase": "q7x2"}, `"auth_passphrase"`},
		"an allowance it cannot need":  {map[string]string{"community": "c0mmunity-s3cret", "allow_short_community": "true"}, `"community"`},
		"nothing at all":               {map[string]string{}, "needs a community or a username"},
		"an unknown protocol, unsaved": {map[string]string{"username": "u", "auth_protocol": "sha1", "auth_passphrase": "auth-pass-phrase"}, `"auth_protocol"`},
		// Refused, not trimmed, as add-credential refuses --snmp-user with one.
		"a user with a space at either end": {map[string]string{"username": " monitor ", "auth_protocol": "sha", "auth_passphrase": "auth-pass-phrase"}, `input "username" begins or ends with a space`},
	} {
		// An unknown protocol meets the schema's own choices first, which
		// the injector reports as an injection failure; every other rule is
		// SNMPCredentialOf's.
		_, err := injectAll(t, kubernetesCredential(t, 1, "pleiades_snmp", tc.inputs))
		if err == nil || !(errors.Is(err, credtype.ErrInvalidCredential) || errors.Is(err, credtype.ErrInjection)) || !strings.Contains(err.Error(), tc.input) {
			t.Errorf("%s: %v, want a refusal naming %s", name, err, tc.input)
			continue
		}
		for _, v := range tc.inputs {
			if len(v) >= 6 && strings.Contains(err.Error(), v) && v != "true" {
				t.Errorf("%s: the refusal repeats a value: %v", name, err)
			}
		}
	}
}

// TestPleiadesSNMP_BindsBesideANetworkLoginAndOnceAlone: its own kind lets a
// template bind a network credential and an SNMP one together, while two
// SNMP credentials are refused at the binding and again at Combine.
func TestPleiadesSNMP_BindsBesideANetworkLoginAndOnceAlone(t *testing.T) {
	snmp := func(name string) credtype.Credential {
		c := kubernetesCredential(t, 2, "pleiades_snmp", map[string]string{"community": "c0mmunity-s3cret"})
		c.Name = name
		return c
	}
	login := kubernetesCredential(t, 3, "net", map[string]string{"username": "admin", "password": "login-pass-1"})
	if err := credtype.CheckBinding([]credtype.Bound{login.Bound(), snmp("a").Bound()}); err != nil {
		t.Errorf("a network login and an SNMP credential were refused together: %v", err)
	}
	if err := credtype.CheckBinding([]credtype.Bound{snmp("a").Bound(), snmp("b").Bound()}); !errors.Is(err, credtype.ErrBindingConflict) {
		t.Errorf("two SNMP credentials bound together: %v", err)
	}
	if _, err := injectAll(t, snmp("a"), snmp("b")); !errors.Is(err, credtype.ErrInjectorConflict) {
		t.Errorf("two SNMP credentials injected together: %v", err)
	}
	if art, err := injectAll(t, login, snmp("a")); err != nil {
		t.Errorf("a login and an SNMP credential would not inject together: %v", err)
	} else if _, ok := art.SNMP(); !ok {
		t.Error("the SNMP credential was lost beside the login")
	}
}

// TestPleiadesSNMP_ChoicesAreTheWiresVocabulary: the shipped type offers
// exactly the protocols pkg/wire accepts, so a form cannot offer one the
// check refuses or leave one out.
func TestPleiadesSNMP_ChoicesAreTheWiresVocabulary(t *testing.T) {
	ct, ok := managed.Type("pleiades_snmp")
	if !ok {
		t.Fatal("pleiades_snmp is not shipped")
	}
	if ct.Kind != credtype.KindSNMP || len(ct.Injectors.Env)+len(ct.Injectors.ExtraVars)+len(ct.Injectors.File) != 0 {
		t.Errorf("pleiades_snmp is kind %q with injectors %+v, want snmp with none", ct.Kind, ct.Injectors)
	}
	auth, _ := ct.Inputs.Field(credtype.SNMPInputAuthProtocol)
	priv, _ := ct.Inputs.Field(credtype.SNMPInputPrivProtocol)
	var wantAuth, wantPriv []string
	for _, p := range wire.SNMPAuthProtocols() {
		wantAuth = append(wantAuth, string(p))
	}
	for _, p := range wire.SNMPPrivProtocols() {
		wantPriv = append(wantPriv, string(p))
	}
	if !slices.Equal(auth.Choices, wantAuth) || !slices.Equal(priv.Choices, wantPriv) {
		t.Errorf("choices %v and %v, want %v and %v", auth.Choices, priv.Choices, wantAuth, wantPriv)
	}
	for _, id := range []string{credtype.SNMPInputCommunity, credtype.SNMPInputAuthPassphrase, credtype.SNMPInputPrivPassphrase} {
		if f, _ := ct.Inputs.Field(id); !f.Secret {
			t.Errorf("input %q is not secret", id)
		}
	}
}

// TestCheckStoredValues_RefusesWhatNoRunCouldUse: the store's write-time
// check refuses an SNMP credential the wire refuses, leaves one reading an
// unresolved input to injection, and refuses a key pasted beside a client
// certificate in a Kubernetes or a cryptography credential, naming the key's
// input; another kind's values are not its question.
func TestCheckStoredValues_RefusesWhatNoRunCouldUse(t *testing.T) {
	const cert = "-----BEGIN CERTIFICATE-----\nMIIBclient\n-----END CERTIFICATE-----\n"
	if err := credtype.CheckStoredValues(credtype.KindSNMP, "s", map[string]string{"community": "public"}); !errors.Is(err, credtype.ErrInvalidCredential) {
		t.Errorf("a short community was stored: %v", err)
	}
	if err := credtype.CheckStoredValues(credtype.KindSNMP, "s", map[string]string{"username": "u", "auth_protocol": "sha"}, "auth_passphrase"); err != nil {
		t.Errorf("a passphrase from an external source was refused at the write: %v", err)
	}
	if err := credtype.CheckStoredValues(credtype.KindSNMP, "s", map[string]string{"username": "u", "auth_protocol": "sha", "auth_passphrase": "auth-pass-phrase"}); err != nil {
		t.Errorf("a whole v3 user was refused: %v", err)
	}
	if err := credtype.CheckStoredValues(credtype.KindSNMP, "s", map[string]string{"username": "monitor ", "auth_protocol": "sha", "auth_passphrase": "auth-pass-phrase"}); !errors.Is(err, credtype.ErrInvalidCredential) {
		t.Errorf("a user with a trailing space was stored: %v", err)
	}
	for kind, inputs := range map[credtype.Kind][2]string{
		credtype.KindKubernetes:   {credtype.KubernetesInputClientCert, credtype.KubernetesInputClientKey},
		credtype.KindCryptography: {credtype.CertificateInputCertificate, credtype.CertificateInputPrivateKey},
	} {
		err := credtype.CheckStoredValues(kind, "c", map[string]string{inputs[0]: cert + testKeyPEM})
		if !errors.Is(err, credtype.ErrInvalidCredential) || !strings.Contains(err.Error(), inputs[1]) || strings.Contains(err.Error(), "MIGHAgEAMBMGByqGSM49") {
			t.Errorf("%s: a key beside the certificate = %v, want refused naming %s", kind, err, inputs[1])
		}
		if err := credtype.CheckStoredValues(kind, "c", map[string]string{inputs[0]: cert}); err != nil {
			t.Errorf("%s: a certificate alone was refused: %v", kind, err)
		}
	}
	if err := credtype.CheckStoredValues(credtype.KindSSH, "m", map[string]string{"certificate": testKeyPEM}); err != nil {
		t.Errorf("an ssh credential's values were judged: %v", err)
	}
}

// TestClientCertificateInputs_RefuseAKeyAtInjection: a key pasted beside a
// Kubernetes client certificate, or a cryptography credential's certificate,
// is refused at every use too, as a value stored before the write-time check
// or arriving from a source would be.
func TestClientCertificateInputs_RefuseAKeyAtInjection(t *testing.T) {
	const cert = "-----BEGIN CERTIFICATE-----\nMIIBclient\n-----END CERTIFICATE-----\n"
	_, err := injectAll(t, kubernetesCredential(t, 4, "pleiades_kubernetes_client_certificate", map[string]string{
		"host": "https://k8s.example.com:6443", "client_certificate": cert + testKeyPEM, "client_key": testKeyPEM,
	}))
	if !errors.Is(err, credtype.ErrInjection) || !strings.Contains(err.Error(), "client_key") {
		t.Errorf("a Kubernetes certificate holding its key injected: %v", err)
	}
	crypto := credtype.Credential{ID: 5, Name: "winrm-cert", Type: credtype.CredentialType{Name: "cert", Kind: credtype.KindCryptography, Namespace: "cert",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: credtype.CertificateInputCertificate, Label: "c", Type: credtype.InputString},
			{ID: credtype.CertificateInputPrivateKey, Label: "k", Type: credtype.InputString, Secret: true},
		}}}, Inputs: map[string]string{credtype.CertificateInputCertificate: cert + testKeyPEM, credtype.CertificateInputPrivateKey: testKeyPEM}}
	if _, err := injectAll(t, crypto); !errors.Is(err, credtype.ErrInvalidCredential) || !strings.Contains(err.Error(), credtype.CertificateInputPrivateKey) {
		t.Errorf("a cryptography certificate holding its key injected: %v", err)
	}
}
