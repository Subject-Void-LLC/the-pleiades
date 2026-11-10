// This file holds the credential readers to one snapshot: a reader that
// redacts a credential's values by its type's schema has to read both as
// they stood at one moment, or a type edit committed between its two reads
// shows a value written as secret in plain text.
package locktest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// betweenKey marks a context whose next credential type read runs a function
// first.
type betweenKey struct{}

// between is that function, run at most once.
type between struct {
	once sync.Once
	run  func()
}

// InterceptTypeReads registers on client an interceptor that, for a
// credential type query made under a context ReadsTakeOneSnapshot marked,
// runs the marked function once before the query reads. A credential read
// with its type eager-loaded reads the type in a second statement, after the
// credential's, so the function lands between the two.
func InterceptTypeReads(client *ent.Client) {
	client.CredentialType.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			if b, ok := ctx.Value(betweenKey{}).(*between); ok {
				b.once.Do(b.run)
			}
			return next.Query(ctx, q)
		})
	}))
}

// A Reader reads one credential the way a caller of the store or of the
// resolver does, and reports whether it showed a value written as secret in
// plain text, or would hand one to a run unmasked.
type Reader func(ctx context.Context, credID int) (exposed bool, err error)

// Resolver is the method of internal/credstore/resolve's Resolver these
// tests call. It is declared here, and the resolver handed in by a test,
// because only a designated consumer may import the package that returns
// plaintext values (internal/archtest), and this package is not a test file.
type Resolver interface {
	Resolve(ctx context.Context, ids []int) ([]credtype.Credential, error)
}

// Readers returns every credential reader that judges a value by its type's
// schema: the store's four, which redact by it, and the resolver, whose
// masking is built from it. client is the store's, used to give the
// credential a template for TemplateCredentials to read it through.
func Readers(client *ent.Client, store credstore.Store, orgID int, resolver Resolver) map[string]Reader {
	shown := func(c credstore.Credential) bool { return strings.HasPrefix(c.Inputs["note"], writtenSecret) }
	find := func(creds []credstore.Credential, credID int) (bool, error) {
		for _, c := range creds {
			if c.ID == credID {
				return shown(c), nil
			}
		}
		return false, errors.New("the list does not hold the credential")
	}
	return map[string]Reader{
		"GetCredential": func(ctx context.Context, credID int) (bool, error) {
			got, err := store.GetCredential(ctx, credID)
			return shown(got), err
		},
		"ListCredentials": func(ctx context.Context, credID int) (bool, error) {
			got, err := store.ListCredentials(ctx, orgID)
			if err != nil {
				return false, err
			}
			return find(got, credID)
		},
		"ListAllCredentials": func(ctx context.Context, credID int) (bool, error) {
			got, err := store.ListAllCredentials(ctx)
			if err != nil {
				return false, err
			}
			return find(got, credID)
		},
		"TemplateCredentials": func(ctx context.Context, credID int) (bool, error) {
			// Set up outside ctx, whose first type read is the one the edit
			// lands before: binding the credential reads its type too.
			setup := context.Background()
			inv := client.Inventory.Create().SetName(fmt.Sprintf("locktest %d", names.Add(1))).SetOrganizationID(orgID).SaveX(setup)
			tmpl := client.Template.Create().SetName(inv.Name).SetKind("runbook").SetDefinition("locktest.yml").
				SetOrganizationID(orgID).SetInventoryID(inv.ID).SaveX(setup)
			if err := store.SetTemplateCredentials(setup, tmpl.ID, []int{credID}); err != nil {
				return false, err
			}
			got, err := store.TemplateCredentials(ctx, tmpl.ID)
			if err != nil {
				return false, err
			}
			return find(got, credID)
		},
		"Resolve": func(ctx context.Context, credID int) (bool, error) {
			got, err := resolver.Resolve(ctx, []int{credID})
			if err != nil || len(got) != 1 {
				return false, errors.Join(err, errors.New("the resolver did not return the credential"))
			}
			value := got[0].Inputs["note"]
			return strings.HasPrefix(value, writtenSecret) && !slices.Contains(got[0].SecretValues(), value), nil
		},
	}
}

// ReadsTakeOneSnapshot lands a type edit, removing the secret note and adding
// it back plain, between each reader's read of a credential and its read of
// the credential's type, and fails when the reader shows the value the
// credential held for the secret note. The store's client must have
// InterceptTypeReads registered.
func ReadsTakeOneSnapshot(t *testing.T, store credstore.Store, orgID int, readers map[string]Reader) {
	t.Helper()
	for name, read := range readers {
		t.Run(name, func(t *testing.T) {
			namespace, typeID, credID := newNotedCredential(t, store, orgID)
			edited := make(chan error, 1)
			ctx := context.WithValue(context.Background(), betweenKey{}, &between{run: func() {
				_, removeErr := store.UpdateType(context.Background(), typeID, typeWith(namespace, nil))
				_, addErr := store.UpdateType(context.Background(), typeID, typeWith(namespace, plainNote))
				edited <- errors.Join(removeErr, addErr)
			}})

			exposed, err := read(ctx, credID)
			if err != nil {
				t.Fatalf("the read failed: %v", err)
			}
			select {
			case err := <-edited:
				if err != nil {
					t.Fatalf("the type edit failed: %v", err)
				}
			default:
				t.Fatal("no type read ran under the reader's context, so nothing landed between its reads")
			}
			if exposed {
				t.Error("a value written as secret was shown as plain: the credential and its type were read at two moments")
			}
		})
	}
}
