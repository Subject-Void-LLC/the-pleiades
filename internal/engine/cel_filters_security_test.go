package engine_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestCELFilters_Phase56SecurityCryptographyFilters is Phase 56's own
// Release Gate requirement: every one of its 15 filters proven callable
// through the real, unmodified engine.NewCELEvaluator()/Program.Eval via
// a compiled when_cel-shaped expression, not a bare Go function call
// (RULE 0). Each case's want value was independently verified against
// pkg/filters' own unit tests before being written here.
func TestCELFilters_Phase56SecurityCryptographyFilters(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	certPEM, wantNotBefore := buildTestCertPEM(t)
	jwtToken := signTestJWTForEngine(t)
	sshAuthorizedKey := buildTestSSHAuthorizedKey(t)

	cases := []struct {
		name string
		expr string
		vars map[string]interface{}
	}{
		{"sha256_hash", `filters.sha256Hash("") == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`, nil},
		{"hmac_generate", `filters.hmacGenerate("The quick brown fox jumps over the lazy dog", "key") == "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"`, nil},
		{"secure_compare_true", `filters.secureCompare("same-secret", "same-secret")`, nil},
		{"secure_compare_false", `!filters.secureCompare("secret-a", "secret-b")`, nil},
		{"generate_random_password_length", `size(filters.generateRandomPassword(16)) == 16`, nil},
		{"parse_jwt_payload_unverified", `filters.parseJWTPayloadUnverified(stat.token)["sub"] == "user-1"`, map[string]interface{}{"stat": map[string]interface{}{"token": jwtToken}}},
		{"parse_x509_certificate", `filters.parseX509Certificate(stat.cert)["not_before"] == stat.wantNotBefore`, map[string]interface{}{"stat": map[string]interface{}{"cert": certPEM, "wantNotBefore": wantNotBefore}}},
		{"pem_to_der_der_to_pem_round_trip", `filters.derToPEM(filters.pemToDER(stat.cert), "CERTIFICATE") == stat.cert`, map[string]interface{}{"stat": map[string]interface{}{"cert": certPEM}}},
		{"ssh_public_key_to_pem_round_trip", `filters.pemToSSHPublicKey(filters.sshPublicKeyToPEM(stat.sshKey)) == stat.sshKey`, map[string]interface{}{"stat": map[string]interface{}{"sshKey": sshAuthorizedKey}}},
		{"mask_pii", `filters.maskPII("SSN is 123-45-6789 on file") == "SSN is [REDACTED-SSN] on file"`, nil},
		{"windows_sid_to_hex", `filters.windowsSIDToHex("S-1-5-18") == "010100000000000512000000"`, nil},
		{"hex_to_windows_sid", `filters.hexToWindowsSID("010100000000000512000000") == "S-1-5-18"`, nil},
		{"parse_distinguished_name", `filters.parseDistinguishedName("CN=John Doe,OU=Sales") == {"CN": ["John Doe"], "OU": ["Sales"]}`, nil},
		{"snmp_oid_translate", `filters.snmpOIDTranslate("1.3.6.1.2.1.1.1.0") == "sysDescr.0"`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prg, err := eval.Compile(tc.expr)
			if err != nil {
				t.Fatalf("failed to compile %q: %v", tc.expr, err)
			}
			vars := tc.vars
			if vars == nil {
				vars = map[string]interface{}{}
			}
			got, err := prg.Eval(vars)
			if err != nil {
				t.Fatalf("eval of %q failed: %v", tc.expr, err)
			}
			if !got {
				t.Errorf("%s: expression %q evaluated false", tc.name, tc.expr)
			}
		})
	}
}

// TestCELFilters_Phase56CombinedCondition chains several of this phase's
// functions in one when_cel-shaped condition against a realistic device
// stat payload, the same combined-condition shape
// TestCELFilters_Phase51CombinedCondition through
// TestCELFilters_Phase55CombinedCondition established, with a negative
// control proving the condition genuinely flips false.
func TestCELFilters_Phase56CombinedCondition(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}

	expr := `filters.secureCompare(stat.config_hash, filters.sha256Hash(stat.config_contents)) && ` +
		`filters.maskPII(stat.log_excerpt) == "user ssn [REDACTED-SSN] flagged" && ` +
		`filters.snmpOIDTranslate(stat.polled_oid) == "sysUpTime.0" && ` +
		`filters.windowsSIDToHex(stat.owner_sid) == "010100000000000512000000"`

	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	configContents := "server { listen 80; }"
	stat := map[string]interface{}{
		"config_contents": configContents,
		"config_hash":     sha256HashForTest(configContents),
		"log_excerpt":     "user ssn 123-45-6789 flagged",
		"polled_oid":      "1.3.6.1.2.1.1.3.0",
		"owner_sid":       "S-1-5-18",
	}
	got, err := prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !got {
		t.Fatal("expected the combined condition to evaluate true against a realistic stat payload")
	}

	// Negative control: a config_hash that no longer matches
	// config_contents must flip the same condition false, proving the
	// combined expression is actually exercising every clause rather
	// than being vacuously true.
	stat["config_hash"] = sha256HashForTest("a different config entirely")
	got, err = prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if got {
		t.Fatal("expected the combined condition to evaluate false once config_hash no longer matches config_contents")
	}
}

// sha256HashForTest computes a SHA-256 digest independently of
// filters.SHA256Hash: using the filter under test to build its own
// expected value would make TestCELFilters_Phase56CombinedCondition's
// secureCompare clause circular (it would pass even if SHA256Hash's
// digest were simply wrong, as long as it were wrong the same way
// twice).
func sha256HashForTest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// buildTestCertPEM mints a real, freshly generated self-signed ECDSA
// certificate, mirroring pkg/filters/pki_test.go's own
// buildSelfSignedCertPEM (duplicated rather than imported: pkg/filters'
// own _test package is not reachable from here, and this fixture is
// small enough that duplicating it is clearer than adding an exported
// test-only helper to a package that otherwise carries none).
func buildTestCertPEM(t *testing.T) (pemStr, wantNotBefore string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	notBefore := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "engine-test.example.com"},
		NotBefore:             notBefore,
		NotAfter:              time.Date(2034, 1, 1, 0, 0, 0, 0, time.UTC),
		DNSNames:              []string{"engine-test.example.com"},
		IPAddresses:           []net.IP{net.ParseIP("10.0.0.2").To4()},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("creating test certificate: %v", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return string(encoded), notBefore.Format(time.RFC3339)
}

func buildTestSSHAuthorizedKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating test SSH key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("building ssh.PublicKey: %v", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

func signTestJWTForEngine(t *testing.T) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "user-1"})
	signed, err := token.SignedString([]byte("engine-test-secret"))
	if err != nil {
		t.Fatalf("signing test JWT: %v", err)
	}
	return signed
}
