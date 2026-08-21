package filters_test

import (
	"bytes"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// buildSelfSignedCertPEM mints a real, freshly generated self-signed
// ECDSA certificate, rather than a hardcoded PEM blob: hand-written
// fixture certificates rot (an expiry date eventually lands in the
// past), and a freshly generated one exercises the exact crypto/x509
// code path ParseX509Certificate reads, matching this project's own
// preference for a representative real artifact over a pre-baked one
// wherever generating one is cheap. Takes no *testing.T so
// pki_bench_test.go's own benchmarks can call it directly rather than
// constructing a throwaway one.
func buildSelfSignedCertPEM() (pemStr, serial string, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(0x1234abcd),
		Subject: pkix.Name{
			CommonName:   "test.example.com",
			Organization: []string{"Example Org"},
		},
		NotBefore:             time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2034, 1, 1, 0, 0, 0, 0, time.UTC),
		DNSNames:              []string{"test.example.com", "alt.example.com"},
		IPAddresses:           []net.IP{net.ParseIP("10.0.0.1").To4()},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return "", "", err
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return string(encoded), template.SerialNumber.String(), nil
}

// generateTestCertPEM wraps buildSelfSignedCertPEM for a test, failing
// it directly rather than returning an error a caller might forget to
// check.
func generateTestCertPEM(t *testing.T) (pemStr string, wantSerial string) {
	t.Helper()
	pemStr, wantSerial, err := buildSelfSignedCertPEM()
	if err != nil {
		t.Fatalf("building self-signed test certificate: %v", err)
	}
	return pemStr, wantSerial
}

func TestParseX509Certificate(t *testing.T) {
	certPEM, wantSerial := generateTestCertPEM(t)

	got := filters.ParseX509Certificate(certPEM)
	if got == nil {
		t.Fatal("ParseX509Certificate: got nil, want a real result")
	}
	subject, _ := got["subject"].(string)
	if !strings.Contains(subject, "CN=test.example.com") || !strings.Contains(subject, "O=Example Org") {
		t.Errorf("subject = %q, want it to contain CN=test.example.com and O=Example Org", subject)
	}
	if got["not_before"] != "2024-01-01T00:00:00Z" {
		t.Errorf("not_before = %v, want 2024-01-01T00:00:00Z", got["not_before"])
	}
	if got["not_after"] != "2034-01-01T00:00:00Z" {
		t.Errorf("not_after = %v, want 2034-01-01T00:00:00Z", got["not_after"])
	}
	if got["serial_number"] != wantSerial {
		t.Errorf("serial_number = %v, want %v", got["serial_number"], wantSerial)
	}
	dnsNames, _ := got["dns_names"].([]any)
	if len(dnsNames) != 2 || dnsNames[0] != "test.example.com" || dnsNames[1] != "alt.example.com" {
		t.Errorf("dns_names = %v, want [test.example.com alt.example.com]", dnsNames)
	}
	ips, _ := got["ip_addresses"].([]any)
	if len(ips) != 1 || ips[0] != "10.0.0.1" {
		t.Errorf("ip_addresses = %v, want [10.0.0.1]", ips)
	}

	t.Run("not_pem", func(t *testing.T) {
		if got := filters.ParseX509Certificate("not a pem block"); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("pem_but_not_a_certificate", func(t *testing.T) {
		notACert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not real DER")})
		if got := filters.ParseX509Certificate(string(notACert)); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := filters.ParseX509Certificate(""); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("over_cap", func(t *testing.T) {
		huge := strings.Repeat("a", filters.MaxStructuredInputBytes+1)
		if got := filters.ParseX509Certificate(huge); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestPEMToDER_DERToPEM_RoundTrip(t *testing.T) {
	certPEM, _ := generateTestCertPEM(t)
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("test fixture itself failed to PEM-decode")
	}

	der := filters.PEMToDER(certPEM)
	if der == "" {
		t.Fatal("PEMToDER: got \"\", want a real base64 string")
	}
	rawDER, err := base64.StdEncoding.DecodeString(der)
	if err != nil {
		t.Fatalf("PEMToDER's own output is not valid base64: %v", err)
	}
	if !bytes.Equal(rawDER, block.Bytes) {
		t.Error("PEMToDER's decoded bytes do not match the original PEM block's own bytes")
	}
	if _, err := x509.ParseCertificate(rawDER); err != nil {
		t.Errorf("PEMToDER's output does not parse back as the original certificate: %v", err)
	}

	reconstructed := filters.DERToPEM(der, "CERTIFICATE")
	rBlock, _ := pem.Decode([]byte(reconstructed))
	if rBlock == nil {
		t.Fatal("DERToPEM's own output does not PEM-decode")
	}
	if rBlock.Type != "CERTIFICATE" || !bytes.Equal(rBlock.Bytes, block.Bytes) {
		t.Errorf("DERToPEM round trip mismatch: type=%q, want CERTIFICATE with matching bytes", rBlock.Type)
	}

	t.Run("pem_to_der_not_pem", func(t *testing.T) {
		if got := filters.PEMToDER("not a pem block"); got != "" {
			t.Errorf("got %q, want \"\"", got)
		}
	})
	t.Run("der_to_pem_not_base64", func(t *testing.T) {
		if got := filters.DERToPEM("not base64!!", "CERTIFICATE"); got != "" {
			t.Errorf("got %q, want \"\"", got)
		}
	})
	// pem.Encode itself does NOT refuse a newline in Type (verified
	// directly against encoding/pem's own source: it only rejects a
	// colon in a Headers key), so this is DERToPEM's own validation being
	// proven, not the stdlib's. A newline in blockType is a real
	// PEM-injection vector: without this check, DERToPEM would emit a
	// second "-----BEGIN ...-----" block hidden inside what looks like
	// one clean block's type line.
	t.Run("der_to_pem_block_type_with_newline_rejected", func(t *testing.T) {
		if got := filters.DERToPEM(der, "CERT\nIFICATE"); got != "" {
			t.Errorf("got %q, want \"\"", got)
		}
	})
	t.Run("der_to_pem_block_type_injection_blocked", func(t *testing.T) {
		injected := "CERTIFICATE-----\n\n-----BEGIN EVIL"
		got := filters.DERToPEM(der, injected)
		if got != "" {
			t.Errorf("DERToPEM(der, %q) = %q, want \"\" (a smuggled second PEM block)", injected, got)
		}
	})
	t.Run("der_to_pem_block_type_lowercase_rejected", func(t *testing.T) {
		if got := filters.DERToPEM(der, "certificate"); got != "" {
			t.Errorf("got %q, want \"\" (only uppercase PEM labels are accepted)", got)
		}
	})
	t.Run("der_to_pem_block_type_valid_with_space", func(t *testing.T) {
		if got := filters.DERToPEM(der, "RSA PRIVATE KEY"); got == "" {
			t.Error("got \"\", want a real PEM block for a valid multi-word label")
		}
	})
	t.Run("pem_to_der_over_cap", func(t *testing.T) {
		huge := strings.Repeat("a", filters.MaxStructuredInputBytes+1)
		if got := filters.PEMToDER(huge); got != "" {
			t.Errorf("got %q, want \"\"", got)
		}
	})
	t.Run("der_to_pem_der_over_cap", func(t *testing.T) {
		huge := strings.Repeat("a", filters.MaxStructuredInputBytes+1)
		if got := filters.DERToPEM(huge, "CERTIFICATE"); got != "" {
			t.Errorf("got %q, want \"\"", got)
		}
	})
	t.Run("der_to_pem_block_type_over_cap", func(t *testing.T) {
		if got := filters.DERToPEM(der, strings.Repeat("A", filters.MaxInputBytes+1)); got != "" {
			t.Errorf("got %q, want \"\"", got)
		}
	})
}

func TestSSHPublicKeyToPEM_PEMToSSHPublicKey_RoundTrip(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("building ssh.PublicKey: %v", err)
	}
	authorizedKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))

	pemStr := filters.SSHPublicKeyToPEM(authorizedKey)
	if pemStr == "" {
		t.Fatal("SSHPublicKeyToPEM: got \"\", want a real PEM block")
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil || block.Type != "PUBLIC KEY" {
		t.Fatalf("SSHPublicKeyToPEM's output is not a PEM PUBLIC KEY block: %q", pemStr)
	}

	roundTripped := filters.PEMToSSHPublicKey(pemStr)
	if roundTripped != authorizedKey {
		t.Errorf("round trip: PEMToSSHPublicKey(SSHPublicKeyToPEM(%q)) = %q, want %q", authorizedKey, roundTripped, authorizedKey)
	}

	t.Run("ssh_to_pem_malformed", func(t *testing.T) {
		if got := filters.SSHPublicKeyToPEM("not an ssh key"); got != "" {
			t.Errorf("got %q, want \"\"", got)
		}
	})
	t.Run("ssh_to_pem_over_cap", func(t *testing.T) {
		huge := strings.Repeat("a", filters.MaxInputBytes+1)
		if got := filters.SSHPublicKeyToPEM(huge); got != "" {
			t.Errorf("got %q, want \"\"", got)
		}
	})
	t.Run("pem_to_ssh_not_pem", func(t *testing.T) {
		if got := filters.PEMToSSHPublicKey("not a pem block"); got != "" {
			t.Errorf("got %q, want \"\"", got)
		}
	})
	t.Run("pem_to_ssh_over_cap", func(t *testing.T) {
		huge := strings.Repeat("a", filters.MaxInputBytes+1)
		if got := filters.PEMToSSHPublicKey(huge); got != "" {
			t.Errorf("got %q, want \"\"", got)
		}
	})
	t.Run("pem_to_ssh_not_a_public_key", func(t *testing.T) {
		certPEM, _ := generateTestCertPEM(t)
		block, _ := pem.Decode([]byte(certPEM))
		notAKey := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: block.Bytes})
		if got := filters.PEMToSSHPublicKey(string(notAKey)); got != "" {
			t.Errorf("got %q, want \"\" (a certificate's DER is not a PKIX public key)", got)
		}
	})

	// An SSH certificate (not a bare key) parses fine via
	// ssh.ParseAuthorizedKey, since ssh.Certificate itself implements
	// ssh.PublicKey, but it does not implement ssh.CryptoPublicKey the
	// way every bare key type does -- proving SSHPublicKeyToPEM's own
	// type-assertion guard is real, not defensive dead code.
	t.Run("ssh_certificate_is_not_a_bare_key", func(t *testing.T) {
		hostPub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generating host key: %v", err)
		}
		hostSSHPub, err := ssh.NewPublicKey(hostPub)
		if err != nil {
			t.Fatalf("building host ssh.PublicKey: %v", err)
		}
		_, caPriv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generating CA key: %v", err)
		}
		caSigner, err := ssh.NewSignerFromKey(caPriv)
		if err != nil {
			t.Fatalf("building CA signer: %v", err)
		}
		cert := &ssh.Certificate{
			Key:             hostSSHPub,
			Serial:          1,
			CertType:        ssh.HostCert,
			ValidPrincipals: []string{"host.example.com"},
			ValidAfter:      0,
			ValidBefore:     ssh.CertTimeInfinity,
		}
		if err := cert.SignCert(rand.Reader, caSigner); err != nil {
			t.Fatalf("signing certificate: %v", err)
		}
		certLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(cert)))
		if got := filters.SSHPublicKeyToPEM(certLine); got != "" {
			t.Errorf("got %q, want \"\" (an SSH certificate is not a bare public key)", got)
		}
	})

	// A DSA key parses through ssh.ParseAuthorizedKey and satisfies
	// ssh.CryptoPublicKey (golang.org/x/crypto/ssh's own dsaPublicKey
	// implements it), but crypto/x509.MarshalPKIXPublicKey does not
	// support *dsa.PublicKey at all -- proving SSHPublicKeyToPEM's own
	// MarshalPKIXPublicKey error check is reachable, not defensive dead
	// code either.
	t.Run("dsa_key_unsupported_by_x509_pkix", func(t *testing.T) {
		var params dsa.Parameters
		if err := dsa.GenerateParameters(&params, rand.Reader, dsa.L1024N160); err != nil {
			t.Fatalf("generating DSA parameters: %v", err)
		}
		var priv dsa.PrivateKey
		priv.Parameters = params
		if err := dsa.GenerateKey(&priv, rand.Reader); err != nil {
			t.Fatalf("generating DSA key: %v", err)
		}
		sshPub, err := ssh.NewPublicKey(&priv.PublicKey)
		if err != nil {
			t.Fatalf("building ssh.PublicKey from DSA key: %v", err)
		}
		authorizedKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
		if got := filters.SSHPublicKeyToPEM(authorizedKey); got != "" {
			t.Errorf("got %q, want \"\" (x509.MarshalPKIXPublicKey does not support DSA)", got)
		}
	})

	// ssh.NewPublicKey only accepts ECDSA keys on the P-256, P-384 or
	// P-521 curves; a PKIX-encoded P-224 key parses fine via
	// x509.ParsePKIXPublicKey but ssh.NewPublicKey itself refuses it.
	t.Run("ecdsa_p224_unsupported_curve", func(t *testing.T) {
		priv, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
		if err != nil {
			t.Fatalf("generating P-224 key: %v", err)
		}
		der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		if err != nil {
			t.Fatalf("marshaling P-224 public key: %v", err)
		}
		pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
		if got := filters.PEMToSSHPublicKey(pemStr); got != "" {
			t.Errorf("got %q, want \"\" (ssh.NewPublicKey only accepts P-256/P-384/P-521)", got)
		}
	})
}

func signTestJWT(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("test-signing-secret"))
	if err != nil {
		t.Fatalf("signing test JWT: %v", err)
	}
	return signed
}

func TestParseJWTPayloadUnverified(t *testing.T) {
	signed := signTestJWT(t, jwt.MapClaims{"sub": "user-123", "role": "admin"})

	got := filters.ParseJWTPayloadUnverified(signed)
	if got == nil {
		t.Fatal("ParseJWTPayloadUnverified: got nil, want the decoded claims")
	}
	if got["sub"] != "user-123" || got["role"] != "admin" {
		t.Errorf("claims = %v, want sub=user-123 role=admin", got)
	}

	// The entire point of "unverified": a token whose signature has been
	// tampered with still yields its claims, exactly as readably as the
	// real one. Proving this is Phase 56's own Adversarial Pattern
	// Justification requirement for this function -- not that it rejects
	// a bad signature (it deliberately never checks one at all), but that
	// its own construction really does skip verification rather than
	// silently validating and only pretending not to.
	t.Run("tampered_signature_still_decodes", func(t *testing.T) {
		parts := strings.Split(signed, ".")
		if len(parts) != 3 {
			t.Fatalf("test fixture itself is not a 3-part JWT: %q", signed)
		}
		tampered := parts[0] + "." + parts[1] + ".not-a-real-signature"
		got := filters.ParseJWTPayloadUnverified(tampered)
		if got == nil {
			t.Fatal("a tampered signature should not prevent reading the claims; got nil")
		}
		if got["sub"] != "user-123" {
			t.Errorf("claims = %v, want sub=user-123 despite the tampered signature", got)
		}
	})

	t.Run("structurally_invalid_token", func(t *testing.T) {
		if got := filters.ParseJWTPayloadUnverified("not-a-jwt-at-all"); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := filters.ParseJWTPayloadUnverified(""); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("over_cap", func(t *testing.T) {
		huge := strings.Repeat("a", filters.MaxStructuredInputBytes+1)
		if got := filters.ParseJWTPayloadUnverified(huge); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestParseDistinguishedName(t *testing.T) {
	cases := []struct {
		name string
		dn   string
		want map[string]any
	}{
		{
			"simple", "CN=John Doe,OU=Sales,DC=example,DC=com",
			map[string]any{
				"CN": []any{"John Doe"},
				"OU": []any{"Sales"},
				"DC": []any{"example", "com"},
			},
		},
		{
			"escaped_comma_in_value", `CN=Doe\, John,OU=Sales`,
			map[string]any{
				"CN": []any{"Doe, John"},
				"OU": []any{"Sales"},
			},
		},
		{
			"escaped_equals_in_value", `CN=Weird\=Name,OU=Sales`,
			map[string]any{
				"CN": []any{"Weird=Name"},
				"OU": []any{"Sales"},
			},
		},
		{
			"whitespace_trimmed", "CN = John Doe , OU = Sales",
			map[string]any{
				"CN": []any{"John Doe"},
				"OU": []any{"Sales"},
			},
		},
		{
			// An escaped "=" (and, by construction, an escaped backslash
			// immediately before it) appearing before the real delimiting
			// "=" exercises splitDNComponent's own escape handling in its
			// delimiter-scanning loop, not just unescapeDN's later pass
			// over the value. Not a realistic attribute type in practice,
			// but the scanner has to handle it correctly regardless of
			// which side of "=" a backslash lands on.
			"escaped_char_before_the_real_delimiter", `A\=B=value`,
			map[string]any{`A\=B`: []any{"value"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.ParseDistinguishedName(tc.dn)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseDistinguishedName(%q) = %#v, want %#v", tc.dn, got, tc.want)
			}
		})
	}

	t.Run("multi_valued_rdn_rejected", func(t *testing.T) {
		if got := filters.ParseDistinguishedName("OU=Sales+L=NYC"); got != nil {
			t.Errorf("got %#v, want nil (multi-valued RDN is out of scope)", got)
		}
	})
	t.Run("hex_encoded_value_rejected", func(t *testing.T) {
		if got := filters.ParseDistinguishedName("CN=#04024869"); got != nil {
			t.Errorf("got %#v, want nil (raw hex-encoded value is out of scope)", got)
		}
	})
	t.Run("no_equals_sign_rejected", func(t *testing.T) {
		if got := filters.ParseDistinguishedName("NotAKeyValuePair"); got != nil {
			t.Errorf("got %#v, want nil", got)
		}
	})
	t.Run("empty_key_rejected", func(t *testing.T) {
		if got := filters.ParseDistinguishedName("=value"); got != nil {
			t.Errorf("got %#v, want nil", got)
		}
	})
	t.Run("trailing_unescaped_backslash_rejected", func(t *testing.T) {
		if got := filters.ParseDistinguishedName(`CN=John\`); got != nil {
			t.Errorf("got %#v, want nil", got)
		}
	})
	t.Run("empty_dn_rejected", func(t *testing.T) {
		if got := filters.ParseDistinguishedName(""); got != nil {
			t.Errorf("got %#v, want nil", got)
		}
	})
	t.Run("over_cap", func(t *testing.T) {
		huge := "CN=" + strings.Repeat("a", filters.MaxInputBytes+1)
		if got := filters.ParseDistinguishedName(huge); got != nil {
			t.Errorf("got %#v, want nil", got)
		}
	})
}
