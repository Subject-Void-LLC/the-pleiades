// Tests of SaveOptions.RefuseStoredLogin: decided under the credential file
// lock, so of several saves started together exactly one stores a login, and
// an entry holding SNMP fields alone has no login to protect.
package credential_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// TestSaveFileStoreWith_RefuseStoredLoginLetsOneOfManyIn: eight saves of a
// login for one device start together, each refusing a stored login. One
// stores; every other is refused with ErrLoginStored, and the stored login is
// the one that won.
func TestSaveFileStoreWith_RefuseStoredLoginLetsOneOfManyIn(t *testing.T) {
	dir := t.TempDir()
	key := testKey('r')
	const n = 8
	errs := make([]error, n)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range n {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			cred := credential.Credential{Username: "root", Password: fmt.Sprintf("s3cret-pw-%d", i)}
			errs[i] = credential.SaveFileStoreWith(dir, key, "web01", cred, credential.SaveOptions{RefuseStoredLogin: true})
		}()
	}
	start.Done()
	done.Wait()
	winner := -1
	for i, err := range errs {
		switch {
		case err == nil && winner >= 0:
			t.Fatalf("saves %d and %d both stored a login", winner, i)
		case err == nil:
			winner = i
		case !errors.Is(err, credential.ErrLoginStored):
			t.Errorf("save %d: %v, want ErrLoginStored", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("no save stored a login")
	}
	stored, err := lookup(t, dir, key, "web01")
	if err != nil || stored.Password != fmt.Sprintf("s3cret-pw-%d", winner) {
		t.Errorf("the stored login is not the winner's: %v", err)
	}
}

// TestSaveFileStoreWith_RefuseStoredLoginIgnoresSNMPAlone: an entry holding
// only SNMP fields has no login to lose, so a login is stored beside them.
func TestSaveFileStoreWith_RefuseStoredLoginIgnoresSNMPAlone(t *testing.T) {
	dir := t.TempDir()
	key := testKey('s')
	if err := credential.SaveFileStore(dir, key, "sw1", v3User()); err != nil {
		t.Fatal(err)
	}
	login := credential.Credential{Username: "admin", Password: "s3cret-pw"}
	if err := credential.SaveFileStoreWith(dir, key, "sw1", login, credential.SaveOptions{Families: credential.LoginFamily, RefuseStoredLogin: true}); err != nil {
		t.Fatalf("a login beside SNMP fields alone: %v", err)
	}
	if err := credential.SaveFileStoreWith(dir, key, "sw1", login, credential.SaveOptions{Families: credential.LoginFamily, RefuseStoredLogin: true}); !errors.Is(err, credential.ErrLoginStored) {
		t.Errorf("a second login: %v, want ErrLoginStored", err)
	}
}
