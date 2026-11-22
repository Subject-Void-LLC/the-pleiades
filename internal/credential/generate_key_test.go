// Tests of the generated key types: each is made in OpenSSH's form, reads
// back through every parse that uses it, and logs in by key alone; names
// outside the five are refused; and only ed25519 and the password draw from
// the entropy hook.
package credential

import (
	"context"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// keyBits is the size DescribeKey reports for each generated type.
var keyBits = map[KeyType]int{
	KeyTypeEd25519: 256, KeyTypeRSA3072: 3072, KeyTypeRSA4096: 4096, KeyTypeECDSAP256: 256, KeyTypeECDSAP384: 384,
}

// TestGenerateWith_EachTypeLogsInByKey: every type is stored in OpenSSH's
// own form, parses with ssh.ParsePrivateKey, is described as its own type
// and size, gives an authorized_keys line of the same fingerprint, and logs
// in through remoteexec.AuthFrom to a server that refuses passwords, so the
// key and nothing else is what got in.
func TestGenerateWith_EachTypeLogsInByKey(t *testing.T) {
	need.POSIXShell(t)
	if len(keyBits) != len(GenerateKeyTypes) {
		t.Fatalf("keyBits lists %d types, GenerateKeyTypes %d", len(keyBits), len(GenerateKeyTypes))
	}
	for _, keyType := range GenerateKeyTypes {
		cred, err := GenerateWith("root", "root@lab", GenerateOptions{KeyType: keyType})
		if err != nil {
			t.Fatalf("%s: %v", keyType, err)
		}
		if block, _ := pem.Decode(cred.PrivateKeyPEM); block == nil || block.Type != "OPENSSH PRIVATE KEY" {
			t.Fatalf("%s: the key is not in OpenSSH form", keyType)
		}
		if len(cred.Password) != GeneratedPasswordLength {
			t.Errorf("%s: the password is %d characters", keyType, len(cred.Password))
		}
		signer, err := ssh.ParsePrivateKey(cred.PrivateKeyPEM)
		if err != nil {
			t.Fatalf("%s: ssh.ParsePrivateKey: %v", keyType, err)
		}
		info, err := DescribeKey(cred)
		if err != nil || info.Type != keyType || info.Bits != keyBits[keyType] || info.Fingerprint != ssh.FingerprintSHA256(signer.PublicKey()) {
			t.Errorf("%s: described as %+v, %v", keyType, info, err)
		}
		line, err := PublicKey(cred, "root@lab")
		if err != nil {
			t.Fatalf("%s: PublicKey: %v", keyType, err)
		}
		pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil || comment != "root@lab" || ssh.FingerprintSHA256(pub) != info.Fingerprint {
			t.Errorf("%s: the public key line %q is not the key's: %v", keyType, line, err)
		}
		if keyType.IsRSA() != strings.HasPrefix(line, "ssh-rsa ") {
			t.Errorf("%s: IsRSA is %v for the line %q", keyType, keyType.IsRSA(), line)
		}

		srv, err := remoteexectest.Start(remoteexectest.Options{Username: "root", AuthorizedKey: pub, RefusePasswords: true})
		if err != nil {
			t.Fatal(err)
		}
		auth, err := remoteexec.AuthFrom(cred.Username, "", cred.PrivateKeyPEM, "")
		if err != nil {
			t.Fatalf("%s: AuthFrom: %v", keyType, err)
		}
		result, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).Run(context.Background(), nil, remoteexec.Target{Host: srv.Host, Port: srv.Port}, auth, "echo in")
		if err != nil || result.Stdout != "in\n" || srv.KeyLogins() != 1 {
			t.Errorf("%s: %+v, %v, %d logins by key", keyType, result, err, srv.KeyLogins())
		}
		srv.Close()
	}
}

// TestGenerateWith_NoPasswordStoresTheKeyAlone: --no-password's credential
// has a key and an empty password.
func TestGenerateWith_NoPasswordStoresTheKeyAlone(t *testing.T) {
	cred, err := GenerateWith("root", "", GenerateOptions{KeyType: KeyTypeECDSAP256, NoPassword: true})
	if err != nil {
		t.Fatal(err)
	}
	if cred.Password != "" || len(cred.PrivateKeyPEM) == 0 || cred.Username != "root" {
		t.Errorf("generated %s", cred)
	}
	if _, err := GenerateWith("", "", GenerateOptions{NoPassword: true}); err == nil {
		t.Error("a credential with no username was generated")
	}
	if _, err := GenerateKey("rsa-2048", ""); err == nil {
		t.Error("GenerateKey made a type it does not list")
	}
}

// TestParseKeyType_TakesTheFiveNamesExactly: each name parses as itself;
// a near miss, another case, a type left out and an empty value are refused,
// listing the five, and rsa-2048 names rsa-3072 and --key instead.
func TestParseKeyType_TakesTheFiveNamesExactly(t *testing.T) {
	for _, keyType := range GenerateKeyTypes {
		if got, err := ParseKeyType(string(keyType)); err != nil || got != keyType {
			t.Errorf("%s parsed as %q, %v", keyType, got, err)
		}
	}
	for _, s := range []string{"rsa", "ED25519", "Ed25519", "dsa", "ecdsa-p521", "ecdsa", "rsa-1024", "", " ed25519"} {
		_, err := ParseKeyType(s)
		if err == nil || !strings.Contains(err.Error(), "ed25519, rsa-3072, rsa-4096, ecdsa-p256, ecdsa-p384") {
			t.Errorf("%q: %v, want a refusal listing the five types", s, err)
		}
	}
	for _, s := range []string{"rsa-2048", "RSA-2048"} {
		if _, err := ParseKeyType(s); err == nil || !strings.Contains(err.Error(), "rsa-3072") || !strings.Contains(err.Error(), "--key") {
			t.Errorf("%q: %v, want rsa-3072 and --key named", s, err)
		}
	}
}

// TestGenerateWith_TheEntropyHookReachesEd25519AndThePasswordOnly pins what
// GenerateKey's comment says of Go 1.26 and later: an ed25519 key fails with
// a failing entropy source, while RSA and ECDSA keys are drawn from Go's own
// source and still made, so with a password asked for only the password's
// draw can fail. A Go that honored the reader again would fail this test,
// and the comment would need rewriting.
func TestGenerateWith_TheEntropyHookReachesEd25519AndThePasswordOnly(t *testing.T) {
	old := entropy
	t.Cleanup(func() { entropy = old })
	entropy = &failAfter{n: 0}
	if _, err := GenerateWith("root", "", GenerateOptions{KeyType: KeyTypeEd25519, NoPassword: true}); err == nil || !strings.Contains(err.Error(), "entropy source failed") {
		t.Errorf("ed25519 with failing entropy: %v", err)
	}
	for _, keyType := range []KeyType{KeyTypeRSA3072, KeyTypeECDSAP256} {
		entropy = &failAfter{n: 0}
		if _, err := GenerateWith("root", "", GenerateOptions{KeyType: keyType, NoPassword: true}); err != nil {
			t.Errorf("%s drew from the entropy hook: %v", keyType, err)
		}
		entropy = &failAfter{n: 0}
		if _, err := GenerateWith("root", "", GenerateOptions{KeyType: keyType}); err == nil || !strings.Contains(err.Error(), "entropy source failed") {
			t.Errorf("%s with a password and failing entropy: %v", keyType, err)
		}
	}
}
