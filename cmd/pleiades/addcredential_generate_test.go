// Tests for add-credential --generate: what it stores, what it prints,
// and what it refuses, through the command itself and the real vault.
package main

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

func TestAddCredential_Generate(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	var runErr error
	out := captureStdout(t, func() {
		runErr = runAddCredential([]string{"ubuntu-lab", "--username", "root", "--generate", "--dir", dir})
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	stored := lookupStored(t, dir, "ubuntu-lab")
	if stored.Username != "root" || len(stored.Password) != credential.GeneratedPasswordLength || len(stored.PrivateKeyPEM) == 0 {
		t.Fatalf("stored %s", stored)
	}
	if strings.Contains(out, stored.Password) || strings.Contains(out, "PRIVATE KEY") {
		t.Fatalf("the output shows a secret:\n%s", out)
	}
	line := strings.TrimSpace(out[strings.Index(out, "public key: ")+len("public key: "):])
	key, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || comment != "root@ubuntu-lab" {
		t.Fatalf("printed public key %q: %v", line, err)
	}
	signer, err := ssh.ParsePrivateKey(stored.PrivateKeyPEM)
	if err != nil || string(signer.PublicKey().Marshal()) != string(key.Marshal()) {
		t.Fatalf("the printed key is not the stored key's: %v", err)
	}

	// A second generate would lock whatever the first one reaches out.
	err = runAddCredential([]string{"ubuntu-lab", "--username", "root", "--generate", "--dir", dir})
	if err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("a second generate: %v", err)
	}
	if again := lookupStored(t, dir, "ubuntu-lab"); again.Password != stored.Password {
		t.Fatal("a refused generate changed the stored credential")
	}
	captureStdout(t, func() {
		runErr = runAddCredential([]string{"ubuntu-lab", "--username", "root", "--generate", "--replace", "--dir", dir})
	})
	if runErr != nil || lookupStored(t, dir, "ubuntu-lab").Password == stored.Password {
		t.Fatalf("--replace: %v", runErr)
	}
}

func TestAddCredential_GenerateRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	for name, tt := range map[string]struct {
		args []string
		want string
	}{
		"no username":              {[]string{"--generate"}, "--username is required"},
		"with a password":          {[]string{"--username", "root", "--generate", "--password", "p"}, "cannot be combined"},
		"with a key":               {[]string{"--username", "root", "--generate", "--key", "id"}, "cannot be combined"},
		"with a passphrase":        {[]string{"--username", "root", "--generate", "--passphrase"}, "cannot be combined"},
		"replace without generate": {[]string{"--username", "root", "--password", "p", "--replace"}, "only applies to --generate"},
	} {
		err := runAddCredential(append([]string{"ubuntu-lab", "--dir", dir}, tt.args...))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want one mentioning %q", name, err, tt.want)
		}
	}
}

// lookupStored reads device's credential back out of dir's vault.
func lookupStored(t *testing.T, dir, device string) credential.Credential {
	t.Helper()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := store.Lookup(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	return cred
}
