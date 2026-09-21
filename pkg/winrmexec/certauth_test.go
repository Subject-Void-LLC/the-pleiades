// Unit tests for client certificate authentication: the credential
// vocabulary, the one-complete-credential rule, the HTTPS selection and the
// cleartext-port refusal.
//
// An internal test package, unlike the Release Gate beside it, because
// these reach unexported behaviour (Options.resolve, checkCertificatePort)
// that has no exported surface of its own and should not grow one merely to
// be testable.
package winrmexec

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// testKeyPair returns a self-signed certificate and its private key, both
// PEM encoded.
//
// It generates rather than embedding a fixture because an embedded
// certificate expires, and a test that starts failing on a date nobody
// chose is worse than one that costs a few milliseconds of key generation.
// P-256 rather than RSA for the same reason it is fast enough to do per
// test.
func testKeyPair(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "pleiades-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling key: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// TestAuthFromSecretsReadsBothCredentialForms proves the one place the key
// vocabulary is read understands both mechanisms, and reads them from
// pkg/wire's constants rather than from literals of its own.
func TestAuthFromSecretsReadsBothCredentialForms(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := testKeyPair(t)

	t.Run("password", func(t *testing.T) {
		auth, err := AuthFromSecrets(map[string]string{
			wire.SecretUsername: "administrator",
			wire.SecretPassword: "hunter2",
		})
		if err != nil {
			t.Fatalf("AuthFromSecrets: %v", err)
		}
		if auth.Username != "administrator" || auth.Password != "hunter2" {
			t.Errorf("auth = %+v, want the username and password back", auth)
		}
		if auth.usesCertificate() {
			t.Error("a password credential reports that it uses a certificate")
		}
		if err := auth.Validate(); err != nil {
			t.Errorf("Validate rejected a complete password credential: %v", err)
		}
	})

	t.Run("certificate", func(t *testing.T) {
		auth, err := AuthFromSecrets(map[string]string{
			wire.SecretCertificatePEM: string(certPEM),
			wire.SecretPrivateKeyPEM:  string(keyPEM),
		})
		if err != nil {
			t.Fatalf("AuthFromSecrets: %v", err)
		}
		if !bytes.Equal(auth.CertificatePEM, certPEM) || !bytes.Equal(auth.PrivateKeyPEM, keyPEM) {
			t.Error("the certificate or key did not survive AuthFromSecrets intact")
		}
		if !auth.usesCertificate() {
			t.Error("a certificate credential reports that it does not use one")
		}
		if err := auth.Validate(); err != nil {
			t.Errorf("Validate rejected a complete certificate credential: %v", err)
		}
	})

	t.Run("empty map is not an error here", func(t *testing.T) {
		auth, err := AuthFromSecrets(map[string]string{})
		if err != nil {
			t.Fatalf("AuthFromSecrets on an empty map: %v", err)
		}
		if auth.CertificatePEM != nil || auth.PrivateKeyPEM != nil {
			t.Errorf("absent keys produced non-nil slices: %+v", auth)
		}
		// Refused at the point of use instead, which is what Validate is for.
		if err := auth.Validate(); err == nil {
			t.Error("Validate accepted an empty credential")
		}
	})
}

// TestCertificateAuthenticationSelectsHTTPSItself proves the caller is not
// asked to set a flag whose only correct value is true.
func TestCertificateAuthenticationSelectsHTTPSItself(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := testKeyPair(t)
	certAuth := Auth{CertificatePEM: certPEM, PrivateKeyPEM: keyPEM}

	if got := (Options{}).resolve(certAuth); !got.HTTPS {
		t.Error("a certificate credential did not select HTTPS")
	}

	// The negative control: password authentication must be left alone, or
	// every existing WinRM call would silently move to a port nothing is
	// listening on.
	passwordAuth := Auth{Username: "administrator", Password: "hunter2"}
	if got := (Options{}).resolve(passwordAuth); got.HTTPS {
		t.Error("a password credential was moved to HTTPS, which would break every existing call")
	}

	// resolve returns a copy. A retry must see what the first attempt saw.
	original := Options{}
	_ = original.resolve(certAuth)
	if original.HTTPS {
		t.Error("resolve mutated the caller's Options")
	}
}

// TestCertificateAuthenticationRefusesTheCleartextPort covers the case an
// operator actually hits: a Windows device whose port nobody changed still
// reports 5985, and a TLS handshake against the plain HTTP listener fails
// with a transport error that reads like a broken certificate.
func TestCertificateAuthenticationRefusesTheCleartextPort(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := testKeyPair(t)
	auth := Auth{CertificatePEM: certPEM, PrivateKeyPEM: keyPEM}

	_, err := Run(context.Background(), Target{Host: "192.0.2.1", Port: DefaultPort}, auth,
		ShellPowerShell, "hostname", Options{})
	if err == nil {
		t.Fatal("certificate authentication against the cleartext port was accepted")
	}
	if !strings.Contains(err.Error(), "cleartext") || !strings.Contains(err.Error(), "5986") {
		t.Errorf("error = %v, want it to name the cleartext listener and the port to set instead", err)
	}

	// The negative control. Without it this test would pass against an
	// implementation that refused certificate authentication outright.
	if err := checkCertificatePort(DefaultPortHTTPS); err != nil {
		t.Errorf("the HTTPS listener was refused: %v", err)
	}
	// A deliberately non-default HTTPS port is the operator's business.
	if err := checkCertificatePort(5443); err != nil {
		t.Errorf("a non-default port was refused: %v", err)
	}
}

// TestCertificateErrorsDoNotLeakThePrivateKey is the certificate half of
// TestRun_ErrorsDoNotLeakThePassword, and matters for the same reason:
// these errors land on a job record.
func TestCertificateErrorsDoNotLeakThePrivateKey(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := testKeyPair(t)

	// Port 1 is reserved, so the dial fails immediately and the error is a
	// transport error rather than a validation one, which is the shape most
	// likely to carry material it should not.
	_, err := Run(context.Background(), Target{Host: "127.0.0.1", Port: 1},
		Auth{CertificatePEM: certPEM, PrivateKeyPEM: keyPEM},
		ShellPowerShell, "hostname", Options{})
	if err == nil {
		t.Fatal("a connection to a reserved port succeeded, which cannot be right")
	}
	if strings.Contains(err.Error(), string(keyPEM)) {
		t.Error("the error carried the private key")
	}
	// The PEM body is base64, so check a distinctive interior run of it too
	// rather than only the whole block, which a wrapper could have split.
	if body := strings.TrimSpace(strings.Split(string(keyPEM), "\n")[1]); len(body) > 16 {
		if strings.Contains(err.Error(), body) {
			t.Error("the error carried a line of the private key")
		}
	}
}

// TestAnUnusableKeypairIsReportedBeforeTheDial proves the library's own
// keypair validation surfaces where an operator can act on it.
func TestAnUnusableKeypairIsReportedBeforeTheDial(t *testing.T) {
	t.Parallel()

	certPEM, _ := testKeyPair(t)

	// A syntactically valid PEM block whose contents are not a key: the
	// certificate parses, the key does not, so no pair can be formed.
	unusableKey := []byte("-----BEGIN EC PRIVATE KEY-----\nbm90LWEta2V5\n-----END EC PRIVATE KEY-----\n")

	_, err := Run(context.Background(), Target{Host: "192.0.2.1", Port: DefaultPortHTTPS},
		Auth{CertificatePEM: certPEM, PrivateKeyPEM: unusableKey},
		ShellPowerShell, "hostname", Options{})
	if err == nil {
		t.Fatal("an unusable keypair was accepted")
	}
	if !strings.Contains(err.Error(), "building client") {
		t.Errorf("error = %v, want it reported while building the client rather than at the handshake", err)
	}
}

// TestABundleAlongsideAnotherCredentialIsRefused covers the conflicts
// AuthFromSecrets refuses rather than resolves.
//
// Refusing matters more than it looks. A credential carrying two
// identities has no predictable behaviour, and picking one would mean a run
// authenticating as something the operator did not choose, which is a
// failure nothing in the record would explain.
func TestABundleAlongsideAnotherCredentialIsRefused(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := testKeyPair(t)
	const bundle = "MIIKzQIBAzCCCoc="

	for _, tt := range []struct {
		name    string
		secrets map[string]string
		want    string
	}{
		{
			name: "a bundle and a loose certificate",
			secrets: map[string]string{
				wire.SecretPFXBase64:      bundle,
				wire.SecretCertificatePEM: string(certPEM),
			},
			want: "two ways to supply one identity",
		},
		{
			name: "a bundle and a loose key",
			secrets: map[string]string{
				wire.SecretPFXBase64:     bundle,
				wire.SecretPrivateKeyPEM: string(keyPEM),
			},
			want: "two ways to supply one identity",
		},
		{
			name: "a bundle and a password",
			secrets: map[string]string{
				wire.SecretPFXBase64: bundle,
				wire.SecretUsername:  "administrator",
				wire.SecretPassword:  "hunter2",
			},
			want: "different authentication mechanisms",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := AuthFromSecrets(tt.secrets)
			if err == nil {
				t.Fatal("a credential carrying two identities was accepted")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}

	// An unopenable bundle on its own is reported as a bundle problem, not
	// as one of the conflicts above.
	if _, err := AuthFromSecrets(map[string]string{wire.SecretPFXBase64: "not base64 at all !!!"}); err == nil {
		t.Error("an unopenable bundle was accepted")
	} else if !strings.Contains(err.Error(), "bundle could not be opened") {
		t.Errorf("error = %v, want it to report the bundle as unopenable", err)
	}
}

// TestAPassphraseProtectedKeyIsRefusedRatherThanIgnored covers the shape
// that used to be accepted, carried across the broker and then silently
// dropped.
//
// crypto/tls cannot decrypt a private key, so the passphrase on the loose
// certificate path had no consumer. The run failed at client construction
// with the library's "failed to find any PEM data in key input", naming
// neither the credential nor the passphrase that was ignored, while three
// separate places in this codebase advertised the combination as working.
func TestAPassphraseProtectedKeyIsRefusedRatherThanIgnored(t *testing.T) {
	t.Parallel()

	certPEM, _ := testKeyPair(t)

	for _, tt := range []struct {
		name string
		key  string
	}{
		{
			name: "PKCS#8, which says so in the block type",
			key:  "-----BEGIN ENCRYPTED PRIVATE KEY-----\nbm90LWEta2V5\n-----END ENCRYPTED PRIVATE KEY-----\n",
		},
		{
			// The older OpenSSL form is labelled exactly like an
			// unencrypted key, so only the header distinguishes it. A check
			// on the block type alone would miss this one entirely.
			name: "legacy OpenSSL, which says so only in a header",
			key: "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,0123\n\n" +
				"bm90LWEta2V5\n-----END RSA PRIVATE KEY-----\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := Auth{CertificatePEM: certPEM, PrivateKeyPEM: []byte(tt.key)}.Validate()
			if err == nil {
				t.Fatal("a passphrase-protected key was accepted on the certificate path")
			}
			if !strings.Contains(err.Error(), "passphrase protected") {
				t.Errorf("error = %v, want it to name the problem", err)
			}
			if !strings.Contains(err.Error(), "PKCS#12") {
				t.Errorf("error = %v, want it to name the path that DOES unlock a key", err)
			}
		})
	}

	// The negative control: an ordinary unencrypted key must still pass, or
	// this check would have broken the feature it is protecting.
	unencryptedCert, unencryptedKey := testKeyPair(t)
	if err := (Auth{CertificatePEM: unencryptedCert, PrivateKeyPEM: unencryptedKey}).Validate(); err != nil {
		t.Errorf("an unencrypted key was refused: %v", err)
	}
}
