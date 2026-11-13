// Tests for unbound credentials: the mark, the listing, clearing it, and the
// run-time notice.
package credential_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// TestUnbound_MarkListAndClear: an entry saved unbound is listed so, with
// its kinds and never a value; saved again bound, the mark is gone; and
// BindFileStore clears a mark once, reporting whether there was one.
func TestUnbound_MarkListAndClear(t *testing.T) {
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStoreWith(dir, key, "later", credential.Credential{Username: "ops", Password: "p", EnablePassword: "e"}, credential.SaveOptions{Unbound: true}); err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Token: "t"}); err != nil {
		t.Fatal(err)
	}
	listed, err := credential.ListCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].Name != "later" || !listed[0].Unbound || listed[1].Unbound ||
		!slices.Equal(listed[0].Kinds, []string{"password", "enable_password"}) || !slices.Equal(listed[1].Kinds, []string{"token"}) {
		t.Fatalf("listed %+v", listed)
	}
	if cleared, err := credential.BindFileStore(dir, "later"); err != nil || !cleared {
		t.Fatalf("BindFileStore = %v, %v; want cleared", cleared, err)
	}
	if cleared, err := credential.BindFileStore(dir, "later"); err != nil || cleared {
		t.Errorf("a second BindFileStore = %v, %v; want nothing to clear", cleared, err)
	}
	if err := credential.SaveFileStoreWith(dir, key, "web01", credential.Credential{Token: "t"}, credential.SaveOptions{Unbound: true}); err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if listed, _ := credential.ListCredentials(dir); listed[1].Unbound {
		t.Error("saving an entry again bound left its old mark")
	}
}

// failingLookup is a store whose every lookup fails.
type failingLookup struct{}

// Lookup implements credential.Store.
func (failingLookup) Lookup(context.Context, string) (credential.Credential, error) {
	return credential.Credential{}, credential.ErrNotFound
}

// TestNoticeUnbound_SaysSoOnceAndClears: the first lookup of an unbound
// entry calls the notice and clears the mark; a second lookup, and a lookup
// of a bound entry, do not; a failed lookup says nothing and is returned.
func TestNoticeUnbound_SaysSoOnceAndClears(t *testing.T) {
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStoreWith(dir, key, "later", credential.Credential{Username: "ops", Password: "p"}, credential.SaveOptions{Unbound: true}); err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Username: "ops", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	var said []string
	store := credential.NoticeUnbound(credential.NewLazyFileStore(dir), dir, func(name string) { said = append(said, name) })
	for _, name := range []string{"later", "later", "web01"} {
		if cred, err := store.Lookup(context.Background(), name); err != nil || cred.Username != "ops" {
			t.Fatalf("Lookup(%s) = %+v, %v", name, cred, err)
		}
	}
	if !slices.Equal(said, []string{"later"}) {
		t.Errorf("said %v, want later once", said)
	}
	if listed, _ := credential.ListCredentials(dir); listed[0].Unbound {
		t.Error("the mark was not cleared")
	}
	failing := credential.NoticeUnbound(failingLookup{}, dir, func(string) { t.Error("a failed lookup said so") })
	if _, err := failing.Lookup(context.Background(), "later"); !errors.Is(err, credential.ErrNotFound) {
		t.Errorf("a failed lookup returned %v", err)
	}
}
