package credential

import (
	"context"
	"errors"
	"testing"
)

func TestStaticStore_LookupReturnsTheSameCredentialRegardlessOfName(t *testing.T) {
	secrets := map[string]string{SecretUsername: "admin", SecretPassword: "hunter2"}
	store := NewStaticStore(secrets)

	for _, name := range []string{"router1", "switch2", ""} {
		cred, err := store.Lookup(context.Background(), name)
		if err != nil {
			t.Fatalf("Lookup(%q) returned unexpected error: %v", name, err)
		}
		if cred.Username != "admin" || cred.Password != "hunter2" {
			t.Errorf("Lookup(%q) = %+v, want Username=admin Password=hunter2", name, cred)
		}
	}
}

func TestStaticStore_EmptySecretsReturnsErrNotFound(t *testing.T) {
	store := NewStaticStore(nil)
	_, err := store.Lookup(context.Background(), "router1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup() error = %v, want ErrNotFound", err)
	}
}
