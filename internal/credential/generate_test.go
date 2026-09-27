// Tests for Generate and PublicKey: that what is generated logs in as a
// real key would, is random, and that its public half is readable.
package credential

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerate(t *testing.T) {
	a, err := Generate("root", "ubuntu-lab")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate("root", "ubuntu-lab")
	if err != nil {
		t.Fatal(err)
	}
	if a.Username != "root" || a.Passphrase != "" || a.CertificatePEM != nil || a.PFXBase64 != "" {
		t.Errorf("generated %s", a)
	}
	if len(a.Password) != GeneratedPasswordLength || strings.Trim(a.Password, passwordAlphabet) != "" {
		t.Errorf("password is %d characters, or holds one outside the alphabet", len(a.Password))
	}
	if a.Password == b.Password || string(a.PrivateKeyPEM) == string(b.PrivateKeyPEM) {
		t.Error("two generated credentials share a secret")
	}
	block, _ := pem.Decode(a.PrivateKeyPEM)
	if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
		t.Fatalf("the key is not in OpenSSH form: %v", block)
	}
	signer, err := ssh.ParsePrivateKey(a.PrivateKeyPEM)
	if err != nil || signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("the key does not parse as ed25519: %v", err)
	}
	if _, err := Generate("", "x"); err == nil {
		t.Error("a credential with no username was generated")
	}
}

func TestPublicKey(t *testing.T) {
	cred, err := Generate("root", "")
	if err != nil {
		t.Fatal(err)
	}
	line, err := PublicKey(cred, "root@ubuntu-lab")
	if err != nil {
		t.Fatal(err)
	}
	key, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || comment != "root@ubuntu-lab" || key.Type() != ssh.KeyAlgoED25519 {
		t.Errorf("line %q: %v, comment %q", line, err, comment)
	}
	if strings.Contains(line, cred.Password) {
		t.Error("the public key line holds the password")
	}
	if bare, _ := PublicKey(cred, ""); strings.Count(bare, " ") != 1 {
		t.Errorf("a line with no comment is %q", bare)
	}

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(private, "", []byte("correct-horse"))
	if err != nil {
		t.Fatal(err)
	}
	locked := Credential{PrivateKeyPEM: pem.EncodeToMemory(block), Passphrase: "correct-horse"}
	if _, err := PublicKey(locked, ""); err != nil {
		t.Errorf("a passphrase-protected key: %v", err)
	}
	locked.Passphrase = "wrong"
	if _, err := PublicKey(locked, ""); err == nil {
		t.Error("a wrong passphrase read the key")
	}
	if _, err := PublicKey(Credential{Password: "p"}, ""); err == nil {
		t.Error("a credential with no key gave a public key")
	}
}

// TestRandomPasswordHoldsEveryKindOfCharacter is the rule Windows' default
// password policy applies: three kinds of character. Drawn often enough
// that a generator skipping the redraw would fail it (about one draw in 38
// has no digit).
func TestRandomPasswordHoldsEveryKindOfCharacter(t *testing.T) {
	for range 2000 {
		p, err := randomPassword(GeneratedPasswordLength)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != GeneratedPasswordLength || !strings.ContainsAny(p, upper) || !strings.ContainsAny(p, lower) || !strings.ContainsAny(p, digits) {
			t.Fatalf("%q lacks a kind of character", p)
		}
	}
	if p, err := randomPassword(2); err != nil || len(p) != 2 {
		t.Errorf("a password too short to hold every kind: %q, %v", p, err)
	}
}

// failAfter reads n bytes of real randomness and then fails.
type failAfter struct{ n int }

func (f *failAfter) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("the entropy source failed")
	}
	k := min(len(p), f.n)
	f.n -= k
	return rand.Read(p[:k])
}

func TestGenerateReportsAFailedEntropySource(t *testing.T) {
	old := entropy
	t.Cleanup(func() { entropy = old })
	for _, n := range []int{0, 36} {
		entropy = &failAfter{n: n}
		if _, err := Generate("Administrator", ""); err == nil || !strings.Contains(err.Error(), "entropy source failed") {
			t.Errorf("failing after %d bytes: %v", n, err)
		}
	}
}
