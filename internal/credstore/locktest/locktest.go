// Package locktest holds the credential store's type lock to what it is for,
// on whichever database a test opens: a type edit and a credential write of
// that type never interleave, so a value written as secret is never shown as
// plain however the two race.
//
// It is a package rather than a test file so that internal/credstore's own
// tests run these against SQLite, container-free, and this package's
// PostgreSQL test runs the same functions against a real server, the dialect
// where the lock is a row lock and read committed reads each statement
// afresh. Only tests import it.
package locktest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// writtenSecret is the prefix of every value the racing writers store. A
// value with it is only ever accepted while note is secret, because the
// non-secret note allows the one choice "plain", so one seen in plain text
// is a secret shown as plain.
const writtenSecret = "SECRET-"

// storedCanary is the value every credential here stores for its api_token
// input. Named without the words gosec's G101 reads as a credential, as is
// the injected variable below, since this is a fixture value.
const storedCanary = "sk-live-LOCKTEST-CANARY-6a5b4c3d2e1f"

// names numbers every organization and type this package makes, so a test
// run twice against one database (go test -count=2) never collides.
var names atomic.Int64

// The note input in each shape the racing editor gives it.
var (
	secretNote = &credtype.InputField{ID: "note", Label: "Note", Secret: true}
	plainNote  = &credtype.InputField{ID: "note", Label: "Note", Choices: []string{"plain"}}
)

// HookedStore registers the encryption hooks on client, as the composition
// root does, and returns a store over it with one organization of its own.
func HookedStore(t testing.TB, client *ent.Client) (credstore.Store, int) {
	t.Helper()
	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))
	org := client.Organization.Create().SetName(fmt.Sprintf("locktest %d", names.Add(1))).SaveX(context.Background())
	return credstore.NewEntStore(client, render.New()), org.ID
}

// OpenSQLite opens a file database the way the Controller opens its
// embedded one: WAL, a busy timeout and a connection pool, not the
// one-connection client other tests serialize through.
func OpenSQLite(t testing.TB) *ent.Client {
	t.Helper()
	client, err := ent.OpenEmbedded(context.Background(), filepath.Join(t.TempDir(), "locktest.db"))
	if err != nil {
		t.Fatalf("OpenEmbedded() = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// typeWith is a custom type whose note input is as given, or absent when
// note is nil.
func typeWith(namespace string, note *credtype.InputField) credtype.CredentialType {
	fields := []credtype.InputField{{ID: "api_token", Label: "Token", Secret: true}}
	if note != nil {
		fields = append(fields, *note)
	}
	return credtype.CredentialType{
		Name:      "Locked " + namespace,
		Kind:      credtype.KindCloud,
		Namespace: namespace,
		Inputs:    credtype.InputSchema{Fields: fields, Required: []string{"api_token"}},
		Injectors: credtype.Injectors{Env: map[string]string{"LOCKTEST_LOGIN": "{{ api_token }}"}},
	}
}

// newNotedCredential makes a type with a secret note and one credential of
// it, and returns the type's namespace and both ids.
func newNotedCredential(t *testing.T, store credstore.Store, orgID int) (string, int, int) {
	t.Helper()
	ctx := context.Background()
	namespace := fmt.Sprintf("locked_%d", names.Add(1))
	ct, err := store.CreateType(ctx, orgID, typeWith(namespace, secretNote))
	if err != nil {
		t.Fatal(err)
	}
	cred, err := store.CreateCredential(ctx, orgID, ct.ID, namespace, "",
		map[string]string{"api_token": storedCanary, "note": writtenSecret + "0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return namespace, ct.ID, cred.ID
}

// RaceTypeEditsAgainstWrites races, for d, a type editor (removing a secret
// input, adding it back non-secret, removing it, adding it back secret)
// against two credential writers and a reader. It fails on any secret shown
// as plain, any unexpected error, and any goroutine still running at the
// deadline, which is a deadlock.
func RaceTypeEditsAgainstWrites(t *testing.T, store credstore.Store, orgID int, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d+30*time.Second)
	defer cancel()
	namespace, typeID, credID := newNotedCredential(t, store, orgID)

	var (
		stop                  atomic.Bool
		written, reads, edits atomic.Int64
		problems              = make(chan string, 64)
		wg                    sync.WaitGroup
	)
	report := func(format string, args ...any) {
		select {
		case problems <- fmt.Sprintf(format, args...):
		default:
		}
	}
	wg.Add(4)
	go func() { // the type editor
		defer wg.Done()
		for !stop.Load() {
			for _, note := range []*credtype.InputField{nil, plainNote, nil, secretNote} {
				if _, err := store.UpdateType(ctx, typeID, typeWith(namespace, note)); err != nil {
					report("a type edit failed: %v", err)
					return
				}
				edits.Add(1)
			}
		}
	}()
	for w := 0; w < 2; w++ {
		go func(w int) { // a credential writer
			defer wg.Done()
			for n := 1; !stop.Load(); n++ {
				value := fmt.Sprintf("%s%d-%d", writtenSecret, w, n)
				_, err := store.UpdateCredentialWith(ctx, credID, func(stored credstore.Credential) (credstore.Credential, error) {
					stored.Inputs = map[string]string{"note": value}
					return stored, nil
				})
				switch {
				case err == nil:
					written.Add(1)
				case !errors.Is(err, credtype.ErrInvalidCredential):
					// Refused while note is undeclared or plain; anything
					// else is the race going wrong.
					report("a credential write failed: %v", err)
					return
				}
			}
		}(w)
	}
	go func() { // a reader
		defer wg.Done()
		for !stop.Load() {
			got, err := store.GetCredential(ctx, credID)
			if err != nil {
				report("a read failed: %v", err)
				return
			}
			if strings.HasPrefix(got.Inputs["note"], writtenSecret) {
				report("a value written as secret was shown as plain")
				return
			}
			reads.Add(1)
		}
	}()

	time.Sleep(d)
	stop.Store(true)
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("the race did not finish by its deadline: a type edit and a credential write deadlocked")
	}
	close(problems)
	for p := range problems {
		t.Error(p)
	}
	if written.Load() == 0 || edits.Load() < 8 || reads.Load() == 0 {
		t.Errorf("the race did too little to prove anything: %d writes, %d edits, %d reads", written.Load(), edits.Load(), reads.Load())
	}
	t.Logf("%d credential writes, %d type edits and %d reads raced for %s", written.Load(), edits.Load(), reads.Load(), d)
}

// HoldTypeEditsOff lands a type edit (removing note, then adding it back
// plain) inside a credential write. The write's fold is where it lands:
// fold runs inside the write's transaction, after the write has taken the
// type's lock and read the schema it checks against, and before the check.
// The edit has to wait for the write, and the write's value is then purged
// by the edit, so it is never shown as plain. Without the lock the edit
// lands, and the write stores a value checked as secret under a note that is
// not.
func HoldTypeEditsOff(t *testing.T, store credstore.Store, orgID int) {
	t.Helper()
	ctx := context.Background()
	namespace, typeID, credID := newNotedCredential(t, store, orgID)

	editDone := make(chan error, 1)
	landedInside := false
	_, writeErr := store.UpdateCredentialWith(ctx, credID, func(stored credstore.Credential) (credstore.Credential, error) {
		go func() {
			_, removeErr := store.UpdateType(ctx, typeID, typeWith(namespace, nil))
			_, addErr := store.UpdateType(ctx, typeID, typeWith(namespace, plainNote))
			editDone <- errors.Join(removeErr, addErr)
		}()
		select {
		case err := <-editDone:
			landedInside = true
			editDone <- err
		case <-time.After(time.Second):
		}
		stored.Inputs = map[string]string{"note": writtenSecret + "held"}
		return stored, nil
	})
	if err := <-editDone; err != nil {
		t.Fatalf("the type edit failed: %v", err)
	}

	if landedInside {
		t.Error("a type edit landed inside a credential write: the type lock did not hold it off")
	}
	if writeErr != nil {
		t.Errorf("the credential write failed: %v", writeErr)
	}
	got, err := store.GetCredential(ctx, credID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(got.Inputs["note"], writtenSecret) {
		t.Error("a value written as secret is shown as plain")
	}
}
