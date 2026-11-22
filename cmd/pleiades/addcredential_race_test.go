// Tests that add-credential --generate without --replace never discards a
// login another command stored after this one looked: the check is made
// again under the credential file lock, at the save.
package main

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// TestAddCredential_GenerateRefusesALoginStoredWhileItRan: the first check
// finds no login; while the key is made, another command stores one, as a
// second add-credential run started at the same moment would. The save
// refuses rather than replacing it, and that login is what the vault holds.
func TestAddCredential_GenerateRefusesALoginStoredWhileItRan(t *testing.T) {
	dir := generateProject(t)
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := generateKey
	t.Cleanup(func() { generateKey = original })
	generateKey = func(username, comment string, opts credential.GenerateOptions) (credential.Credential, error) {
		// The other command's save lands between the check and this one's.
		if err := credential.SaveFileStore(dir, key, "db1", credential.Credential{Username: "ops", Password: "the-other-commands-pw"}); err != nil {
			t.Fatal(err)
		}
		return original(username, comment, opts)
	}
	_, _, err = generateIn(t, dir, "db1", "--username", "root")
	if err == nil || !strings.Contains(err.Error(), "stored while this one was made") || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("a login stored while --generate ran: %v, want it refused", err)
	}
	if stored := lookupStored(t, dir, "db1"); stored.Username != "ops" || stored.Password != "the-other-commands-pw" || len(stored.PrivateKeyPEM) != 0 {
		t.Errorf("the other command's login was replaced: %s", stored)
	}

	// With --replace the same moment replaces it, as asked.
	generateKey = original
	if _, _, err := generateIn(t, dir, "db2", "--username", "root", "--replace"); err != nil {
		t.Fatal(err)
	}
	generateKey = func(username, comment string, opts credential.GenerateOptions) (credential.Credential, error) {
		if err := credential.SaveFileStore(dir, key, "db2", credential.Credential{Username: "ops", Password: "the-other-commands-pw"}); err != nil {
			t.Fatal(err)
		}
		return original(username, comment, opts)
	}
	if _, _, err := generateIn(t, dir, "db2", "--username", "root", "--replace"); err != nil {
		t.Fatalf("--replace: %v", err)
	}
	if stored := lookupStored(t, dir, "db2"); stored.Username != "root" || len(stored.PrivateKeyPEM) == 0 {
		t.Errorf("--replace kept %s", stored)
	}
}
