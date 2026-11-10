// This file holds the credential PATCH routes to the rule that a PATCH is
// folded over the record as it stands under the type's lock, never over a
// copy read before it.
//
// The handlers used to read the stored record, fold the body over it, and
// hand the result to the store. A type edit landing between that read and
// the write was undone by the write: a reference the edit's purge deleted
// came back, and an input the edit added was dropped. Each test here wraps
// the real store so a type edit lands at exactly that point.
package api_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// interleavingStore is the real store with edit run once, right after the
// first read of a record a handler makes, and right before a fold-form write
// takes the lock: either way, before the write reads the record it folds.
type interleavingStore struct {
	credstore.Store
	once sync.Once
	edit func()
}

// GetCredential reads, then runs the edit.
func (s *interleavingStore) GetCredential(ctx context.Context, id int) (credstore.Credential, error) {
	c, err := s.Store.GetCredential(ctx, id)
	s.once.Do(s.edit)
	return c, err
}

// GetType reads, then runs the edit.
func (s *interleavingStore) GetType(ctx context.Context, id int) (credstore.CredentialType, error) {
	ct, err := s.Store.GetType(ctx, id)
	s.once.Do(s.edit)
	return ct, err
}

// UpdateTypeWith runs the edit, then writes: the fold reads the type after
// the edit, under the lock, which is the point of the fold form.
func (s *interleavingStore) UpdateTypeWith(ctx context.Context, id int, fold func(credstore.CredentialType) (credtype.CredentialType, error)) (credstore.CredentialType, error) {
	s.once.Do(s.edit)
	return s.Store.UpdateTypeWith(ctx, id, fold)
}

// UpdateCredentialWith runs the edit, then writes, as UpdateTypeWith does.
func (s *interleavingStore) UpdateCredentialWith(ctx context.Context, id int, fold func(credstore.Credential) (credstore.Credential, error), opts ...credstore.CredentialOption) (credstore.Credential, error) {
	s.once.Do(s.edit)
	return s.Store.UpdateCredentialWith(ctx, id, fold, opts...)
}

// interleavingRouter serves the two PATCH routes over store.
func interleavingRouter(t *testing.T, store credstore.Store) http.Handler {
	t.Helper()
	handler := api.NewCredentialHandler(store, render.New())
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.UpdateCredential.Route(handler.UpdateCredential),
			apispec.UpdateCredentialType.Route(handler.UpdateCredentialType),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

// withBackup is the patch type's input schema with backup as given, or
// without it when backup is nil.
func withBackup(backup *credtype.InputField) credtype.InputSchema {
	fields := []credtype.InputField{{ID: "token", Label: "Token", Secret: true}}
	if backup != nil {
		fields = append(fields, *backup)
	}
	fields = append(fields, credtype.InputField{ID: "url", Label: "URL"},
		credtype.InputField{ID: "region", Label: "Region", Choices: []string{"east", "west"}})
	return credtype.InputSchema{Fields: fields, Required: []string{"token"}}
}

// TestCredentialPatch_NeverRestoresAReferenceAPurgeDeleted is the leak: the
// backup input reads from an external reference, an edit removes backup and
// adds it back as non-secret, and a PATCH naming only the name, read before
// the edit, used to write the deleted reference back, so the next run would
// resolve the secret into an input nothing masks.
func TestCredentialPatch_NeverRestoresAReferenceAPurgeDeleted(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	ctx := context.Background()
	typeID := f.createPatchType(t, f.orgA, "patch_api")
	id := f.createPatchCredential(t, map[string]any{
		"name": "referenced", "organization": f.orgA, "credential_type": typeID,
		"inputs":   map[string]string{"token": patchTokenSecret},
		"external": map[string]string{"backup": "file:edge_backup"},
	})
	base, err := f.store.GetType(ctx, typeID)
	if err != nil {
		t.Fatal(err)
	}

	store := &interleavingStore{Store: f.store, edit: func() {
		for _, inputs := range []credtype.InputSchema{withBackup(nil), withBackup(&credtype.InputField{ID: "backup", Label: "Backup token"})} {
			ct := base.CredentialType
			ct.Inputs = inputs
			if _, err := f.store.UpdateType(ctx, typeID, ct); err != nil {
				t.Errorf("the interleaved type edit: %v", err)
			}
		}
	}}
	rec := patchThrough(t, interleavingRouter(t, store), fmt.Sprintf("/api/v1/credentials/%d", id), `{"name":"renamed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d: %s", rec.Code, rec.Body)
	}
	if ref, restored := f.stored(t, id).External["backup"]; restored {
		t.Errorf("the PATCH wrote back the reference the purge deleted: %q", ref)
	}
}

// TestCredentialTypePatch_NeverDropsAnInputAddedMeanwhile is the lost update
// on the type: an input added between a name-only PATCH's read and its write
// used to be dropped by the write, and with it every value stored for it.
func TestCredentialTypePatch_NeverDropsAnInputAddedMeanwhile(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	ctx := context.Background()
	typeID := f.createPatchType(t, f.orgA, "patch_api")
	base, err := f.store.GetType(ctx, typeID)
	if err != nil {
		t.Fatal(err)
	}

	store := &interleavingStore{Store: f.store, edit: func() {
		ct := base.CredentialType
		ct.Inputs.Fields = append(append([]credtype.InputField(nil), ct.Inputs.Fields...),
			credtype.InputField{ID: "zone", Label: "Zone"})
		if _, err := f.store.UpdateType(ctx, typeID, ct); err != nil {
			t.Errorf("the interleaved type edit: %v", err)
		}
	}}
	rec := patchThrough(t, interleavingRouter(t, store), fmt.Sprintf("/api/v1/credential-types/%d", typeID), `{"description":"renamed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d: %s", rec.Code, rec.Body)
	}
	after, err := f.store.GetType(ctx, typeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := after.Inputs.Field("zone"); !kept || after.Description != "renamed" {
		t.Errorf("after the PATCH the type has description %q and zone declared %v, want both", after.Description, kept)
	}
}

// patchThrough sends one PATCH through router.
func patchThrough(t *testing.T, router http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}
