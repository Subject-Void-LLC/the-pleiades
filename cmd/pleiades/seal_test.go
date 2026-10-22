// Tests for seal-key, seal and add-credential --password-file through the
// real binary: a password sealed to the project's key, or to a passphrase,
// becomes the stored credential without ever being readable in the file or
// on the command line.
package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

func sealKeyOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := runPleiades(t, dir, "seal-key", "--json")
	if err != nil {
		t.Fatalf("seal-key: %v\n%s", err, out)
	}
	var view struct {
		PublicKey   string `json:"public_key"`
		Fingerprint string `json:"fingerprint"`
		Created     bool   `json:"created"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("seal-key --json: %v\n%s", err, out)
	}
	return view.PublicKey
}

func TestCLI_PasswordFileSealedToTheProjectBecomesTheCredential(t *testing.T) {
	const password = "sealed-Passw0rd"
	project := t.TempDir()
	if out, err := runPleiades(t, project, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	pub := sealKeyOf(t, project)
	if again := sealKeyOf(t, project); again != pub {
		t.Fatal("seal-key printed a different key the second time")
	}

	// The person seals somewhere else entirely, knowing only the public key.
	elsewhere := t.TempDir()
	keyFile := filepath.Join(elsewhere, "project.sealkey")
	if err := os.WriteFile(keyFile, []byte(pub+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sealedFile := filepath.Join(elsewhere, "web01.sealed")
	out, err := runPleiadesWithStdin(t, elsewhere, password+"\n", "seal", "--to", "@"+keyFile, "--secret-stdin", "--out", sealedFile)
	if err != nil {
		t.Fatalf("seal: %v\n%s", err, out)
	}
	data, err := os.ReadFile(sealedFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), password) || strings.Contains(out, password) {
		t.Fatal("the sealed file or seal's output shows the password")
	}
	if info, _ := os.Stat(sealedFile); info.Mode().Perm() != 0o600 {
		t.Errorf("sealed file mode %v, want 0600", info.Mode().Perm())
	}

	out, err = runPleiades(t, project, "add-credential", "web01", "--username", "root", "--password-file", sealedFile)
	if err != nil {
		t.Fatalf("add-credential --password-file: %v\n%s", err, out)
	}
	if strings.Contains(out, password) {
		t.Errorf("add-credential shows the password:\n%s", out)
	}
	stored, err := credential.NewLazyFileStore(project).Lookup(context.Background(), "web01")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Username != "root" || stored.Password != password {
		t.Errorf("stored %s, want root with the sealed password", stored)
	}

	// Another project cannot open it.
	other := t.TempDir()
	if out, err := runPleiades(t, other, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	out, err = runPleiades(t, other, "add-credential", "web01", "--username", "root", "--password-file", sealedFile)
	if err == nil || !strings.Contains(out, "another project") {
		t.Errorf("another project: err = %v\n%s", err, out)
	}

	// Sealing never overwrites.
	out, err = runPleiadesWithStdin(t, elsewhere, password+"\n", "seal", "--to", pub, "--secret-stdin", "--out", sealedFile)
	if err == nil || !strings.Contains(out, "already exists") {
		t.Errorf("sealing over a file: err = %v\n%s", err, out)
	}
}

func TestCLI_PasswordFileSealedToAPassphraseOpensWithIt(t *testing.T) {
	const password = "passphrase-sealed-Pw"
	const passphrase = "correct horse battery staple"
	project := t.TempDir()
	if out, err := runPleiades(t, project, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	// seal reads the secret and a passphrase from two places, and a test
	// has no terminal for the prompt, so this file is sealed in process.
	data, err := sealed.SealWithPassphrase(passphrase, sealed.PurposePassword, []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	sealedFile := filepath.Join(t.TempDir(), "web01.sealed")
	if err := os.WriteFile(sealedFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runPleiadesWithStdin(t, project, passphrase+"\n", "add-credential", "web01", "--username", "root", "--password-file", sealedFile, "--passphrase-stdin")
	if err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	stored, err := credential.NewLazyFileStore(project).Lookup(context.Background(), "web01")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Password != password {
		t.Errorf("stored %s, want the sealed password", stored)
	}

	out, err = runPleiadesWithStdin(t, project, "the wrong one entirely\n", "add-credential", "web02", "--username", "root", "--password-file", sealedFile, "--passphrase-stdin")
	if err == nil || !strings.Contains(out, "passphrase is wrong") {
		t.Errorf("wrong passphrase: err = %v\n%s", err, out)
	}
	out, err = runPleiades(t, project, "add-credential", "web02", "--username", "root", "--password-file", sealedFile)
	if err == nil || !strings.Contains(out, "--passphrase") {
		t.Errorf("no passphrase: err = %v\n%s", err, out)
	}
}

func TestCLI_SealAndPasswordFileRefusals(t *testing.T) {
	project := t.TempDir()
	if out, err := runPleiades(t, project, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	pub := sealKeyOf(t, project)
	byKey := filepath.Join(t.TempDir(), "byKey.sealed")
	if out, err := runPleiadesWithStdin(t, project, "pw-value\n", "seal", "--to", pub, "--secret-stdin", "--out", byKey); err != nil {
		t.Fatalf("seal: %v\n%s", err, out)
	}
	plaintext := filepath.Join(t.TempDir(), "plain.password")
	if err := os.WriteFile(plaintext, []byte("pw-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "new.sealed")

	for name, c := range map[string]struct {
		stdin string
		args  []string
		want  string
	}{
		"a plaintext password file":   {"", []string{"add-credential", "web01", "--username", "root", "--password-file", plaintext}, "not a sealed file"},
		"a passphrase for a key seal": {"x\n", []string{"add-credential", "web01", "--username", "root", "--password-file", byKey, "--passphrase-stdin"}, "does not apply"},
		"two password forms":          {"x\n", []string{"add-credential", "web01", "--username", "root", "--password-file", byKey, "--password-stdin"}, "mutually exclusive"},
		"a password file and a key":   {"", []string{"add-credential", "web01", "--username", "root", "--password-file", byKey, "--key", "/dev/null"}, "mutually exclusive"},
		"with generate":               {"", []string{"add-credential", "web01", "--username", "root", "--password-file", byKey, "--generate"}, "cannot be combined"},
		"no way to seal":              {"x\n", []string{"seal", "--secret-stdin", "--out", out}, "choose one way"},
		"two ways to seal":            {"x\n", []string{"seal", "--to", pub, "--passphrase", "--out", out}, "choose one way"},
		"both on standard input":      {"x\n", []string{"seal", "--passphrase-stdin", "--secret-stdin", "--out", out}, "cannot both"},
		"no out":                      {"x\n", []string{"seal", "--to", pub, "--secret-stdin"}, "--out is required"},
		"not a seal key":              {"x\n", []string{"seal", "--to", "ssh-ed25519 AAAA", "--secret-stdin", "--out", out}, "not a pleiades seal key"},
		"a stray argument":            {"", []string{"seal-key", "extra"}, "usage"},
	} {
		got, err := runPleiadesWithStdin(t, project, c.stdin, c.args...)
		if err == nil || !strings.Contains(got, c.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q:\n%s", name, err, c.want, got)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused seal wrote its file")
	}
	if _, err := credential.NewLazyFileStore(project).Lookup(context.Background(), "web01"); err == nil {
		t.Error("a refused add-credential stored a credential")
	}
}
