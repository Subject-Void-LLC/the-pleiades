// Package api_test's project coverage: the six endpoints a source
// repository is managed through, and the one rule that decides whether a
// credential may authenticate a clone.
//
// Driven through a real chi router with the real middleware chain and a
// real ent-backed store, because the interesting failures here are not in
// the handler bodies. They are in what the router does with a malformed
// path parameter, and in whether a rule the UI enforces is also enforced on
// the path that has no form to validate against. A test calling the handler
// methods directly would prove neither.
//
// The syncer is a fake, and deliberately: a real one clones a repository
// over a network. What a fake cannot weaken is the property this file most
// wants to check, which is that the handler never holds a credential at
// all.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// projectFixtureSeq keeps each fixture's in-memory database distinct.
//
// The same reason the credential fixture has one: these databases are named
// and shared by name, so two tests reusing a name would see each other's
// rows, and `go test -count=3` would see the previous iteration's.
var projectFixtureSeq atomic.Int64

// errSyncerRefused is what the fake syncer returns for a project it will
// not attempt at all, which is how internal/project reports an unsyncable
// project as opposed to a sync that ran and failed.
var errSyncerRefused = errors.New("project: this project has no fetchable source")

// fakeSyncer stands in for the go-git syncer.
//
// It records the projects it was asked about so a test can assert the
// handler passed the record it read rather than one it rebuilt from the
// request, which is the difference between syncing the project somebody
// named and syncing whatever they posted.
type fakeSyncer struct {
	result project.Result
	err    error

	// progress is written to the runner's stream before the outcome is
	// reported, standing in for a real clone's own chatter.
	progress string

	// started is closed when a clone begins and block holds it open, so a
	// test can catch a sync in flight the way a slow repository would.
	started chan struct{}
	block   chan struct{}

	seen []project.Project
}

// Sync records the request and returns the configured outcome.
func (f *fakeSyncer) Sync(ctx context.Context, p project.Project, progress io.Writer) (project.Result, error) {
	f.seen = append(f.seen, p)
	if f.progress != "" && progress != nil {
		_, _ = io.WriteString(progress, f.progress)
	}
	if f.started != nil {
		close(f.started)
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return project.Result{Status: project.SyncFailed, Err: "stopped"}, nil
		}
	}
	if f.err != nil {
		return project.Result{}, f.err
	}
	return f.result, nil
}

// Playbooks is unused here; the playbook surface has its own tests.
func (f *fakeSyncer) Playbooks(_ context.Context, _ project.Project) ([]string, error) {
	return nil, nil
}

// fakeCredentials is the redacted credential projection the handler reads.
//
// It returns credstore.Credential values built by hand rather than through
// a real store, because what the handler asks of them is exactly the shape
// credstore guarantees: a secret input present as a marker rather than as a
// dropped key, and an externally sourced one named in External instead.
// Both halves matter and a real store would only exercise whichever one the
// fixture happened to create.
type fakeCredentials struct {
	creds []credstore.Credential
	err   error
}

// ListAllCredentials returns the configured projection.
func (f *fakeCredentials) ListAllCredentials(_ context.Context) ([]credstore.Credential, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.creds, nil
}

// projectFixture is one controller's worth of the project surface.
type projectFixture struct {
	client *ent.Client
	store  project.Store
	syncer *fakeSyncer
	runner *project.Runner
	creds  *fakeCredentials
	router http.Handler

	orgID int
}

// newProjectFixture wires the real router over a real store.
func newProjectFixture(t *testing.T) *projectFixture {
	t.Helper()

	client := newSerializedSQLiteClient(t, fmt.Sprintf("projects-%d", projectFixtureSeq.Add(1)))
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)

	store := project.NewEntStore(client, project.SourcePolicy{})
	syncer := &fakeSyncer{}
	// The real runner over the fake syncer, so the sync endpoint exercises
	// the genuine async path: the handler enqueues, the runner claims and
	// clones in the background, and a test waits on runner.Wait() for the
	// outcome the way a caller waits on a poll.
	runner := project.NewRunner(store, syncer, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	creds := &fakeCredentials{}
	handler := api.NewProjectHandler(store, runner, creds, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListProjects.Route(handler.List),
			apispec.GetProject.Route(handler.Get),
			apispec.CreateProject.Route(handler.Create),
			apispec.UpdateProject.Route(handler.Update),
			apispec.DeleteProject.Route(handler.Delete),
			apispec.SyncProject.Route(handler.Sync),
			apispec.StreamProjectSyncLogs.Route(handler.StreamSyncLogs),
			apispec.CancelProjectSync.Route(handler.CancelSync),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	return &projectFixture{
		client: client, store: store, syncer: syncer, runner: runner,
		creds: creds, router: router, orgID: org.ID,
	}
}

// do issues a request and returns the status and the raw body.
func (f *projectFixture) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshalling request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	return rec.Code, rec.Body.Bytes()
}

// createProject makes one through the real endpoint and returns its id.
func (f *projectFixture) createProject(t *testing.T, name string) int {
	t.Helper()

	status, body := f.do(t, http.MethodPost, "/api/v1/projects", map[string]any{
		"name":         name,
		"organization": f.orgID,
		"scm_type":     "git",
		"scm_url":      "https://example.invalid/automation.git",
	})
	if status != http.StatusCreated {
		t.Fatalf("creating %q: status %d, body %s", name, status, body)
	}

	var dto struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &dto); err != nil {
		t.Fatalf("decoding the created project: %v", err)
	}
	return dto.ID
}

// realCredential inserts a credential row and returns its id.
//
// The projection the handler READS is a fake, but the row has to exist:
// Project.credential is a real foreign key, so a create naming an absent
// credential fails inside the store rather than at the check under test,
// and reports itself as a duplicate name (entStore.Create maps every
// constraint error onto ErrExists). Creating the row keeps each test
// failing for its own reason.
func realCredential(t *testing.T, client *ent.Client, orgID int, name string) int {
	t.Helper()
	ctx := context.Background()
	ct := client.CredentialType.Create().
		SetName(name + " type").
		SetKind(string(credtype.KindSCM)).
		SetNamespace("test_" + name).
		SaveX(ctx)
	return client.Credential.Create().
		SetName(name).
		SetCredentialTypeID(ct.ID).
		SetOrganizationID(orgID).
		SaveX(ctx).ID
}

// scmCredential is a source control credential carrying a stored password.
func scmCredential(id int) credstore.Credential {
	return credstore.Credential{
		ID:     id,
		Name:   "forge",
		Kind:   credtype.KindSCM,
		Inputs: map[string]string{"username": "svc", "password": "$encrypted$"},
	}
}

// TestProjects_CreateAndReadRoundTrip is the ordinary path.
func TestProjects_CreateAndReadRoundTrip(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "automation")

	status, body := f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%d", id), nil)
	if status != http.StatusOK {
		t.Fatalf("GET: status %d, body %s", status, body)
	}

	var dto struct {
		Name       string `json:"name"`
		SCMType    string `json:"scm_type"`
		SCMURL     string `json:"scm_url"`
		SyncStatus string `json:"sync_status"`
	}
	if err := json.Unmarshal(body, &dto); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if dto.Name != "automation" || dto.SCMType != "git" {
		t.Errorf("round trip lost fields: %+v", dto)
	}
	// A project that has never been synced must not read as one whose sync
	// failed. The two call for opposite actions.
	if dto.SyncStatus != string(project.SyncNever) {
		t.Errorf("sync_status = %q, want %q for a project that has never synced", dto.SyncStatus, project.SyncNever)
	}

	status, body = f.do(t, http.MethodGet, "/api/v1/projects", nil)
	if status != http.StatusOK {
		t.Fatalf("LIST: status %d, body %s", status, body)
	}
	var list struct {
		Projects []struct {
			ID int `json:"id"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decoding the list: %v", err)
	}
	if len(list.Projects) != 1 || list.Projects[0].ID != id {
		t.Errorf("list returned %+v, want exactly the created project", list.Projects)
	}
}

// TestProjects_MalformedWritesAreRefused covers every branch that turns a
// body into a domain project, because each one is a rule an operator finds
// out about either here or at three in the morning.
func TestProjects_MalformedWritesAreRefused(t *testing.T) {
	f := newProjectFixture(t)

	for _, tc := range []struct {
		name string
		body map[string]any
		want int
	}{
		{
			name: "no name",
			body: map[string]any{"organization": f.orgID, "scm_url": "https://example.invalid/a.git"},
			want: http.StatusBadRequest,
		},
		{
			name: "a name of only spaces is no name",
			body: map[string]any{"name": "   ", "organization": f.orgID, "scm_url": "https://example.invalid/a.git"},
			want: http.StatusBadRequest,
		},
		{
			name: "no organization",
			body: map[string]any{"name": "orphan", "scm_url": "https://example.invalid/a.git"},
			want: http.StatusBadRequest,
		},
		{
			name: "an unknown scm type",
			body: map[string]any{"name": "weird", "organization": f.orgID, "scm_type": "subversion"},
			want: http.StatusBadRequest,
		},
		{
			name: "a git project with no url can never do the one thing it exists for",
			body: map[string]any{"name": "urlless", "organization": f.orgID, "scm_type": "git"},
			want: http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := f.do(t, http.MethodPost, "/api/v1/projects", tc.body)
			if status != tc.want {
				t.Errorf("status %d, want %d; body %s", status, tc.want, body)
			}
		})
	}
}

// TestProjects_ScmTypeDefaultsToGit records that an omitted scm_type is
// git rather than an error, which is what makes the common case a two-field
// create.
func TestProjects_ScmTypeDefaultsToGit(t *testing.T) {
	f := newProjectFixture(t)

	status, body := f.do(t, http.MethodPost, "/api/v1/projects", map[string]any{
		"name":         "implicit",
		"organization": f.orgID,
		"scm_url":      "https://example.invalid/a.git",
	})
	if status != http.StatusCreated {
		t.Fatalf("status %d, body %s", status, body)
	}
	var dto struct {
		SCMType string `json:"scm_type"`
	}
	if err := json.Unmarshal(body, &dto); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if dto.SCMType != "git" {
		t.Errorf("scm_type = %q, want git when the body omits it", dto.SCMType)
	}
}

// TestProjects_AManualProjectNeedsNoURL is the other side of the git rule:
// the URL requirement is a property of git, not of projects.
func TestProjects_AManualProjectNeedsNoURL(t *testing.T) {
	f := newProjectFixture(t)

	status, body := f.do(t, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "hand written", "organization": f.orgID, "scm_type": "manual",
	})
	if status != http.StatusCreated {
		t.Errorf("status %d, want a manual project to be accepted with no url; body %s", status, body)
	}
}

// TestProjects_OnlyASourceControlCredentialMayAuthenticateAClone is the
// rule this surface exists to hold, on the path that has no form.
//
// The UI narrows its chooser to credentials that can work, but a chooser is
// not enforcement: the same field is accepted here, where there are no
// options to validate against. A Machine credential is the case that makes
// this more than a type check, because it really does carry an SSH key that
// a clone could use. It is refused on what it IS rather than on what it
// carries, because it was issued to open shells on managed devices.
func TestProjects_OnlyASourceControlCredentialMayAuthenticateAClone(t *testing.T) {
	f := newProjectFixture(t)

	// Real rows, so a project may reference them, paired with the redacted
	// projection the handler actually reads.
	withPassword := realCredential(t, f.client, f.orgID, "forge")
	machine := realCredential(t, f.client, f.orgID, "device-access")
	usernameOnly := realCredential(t, f.client, f.orgID, "half-filled")
	external := realCredential(t, f.client, f.orgID, "vault-backed")

	f.creds.creds = []credstore.Credential{
		scmCredential(withPassword),
		{
			ID:     machine,
			Name:   "device access",
			Kind:   credtype.KindSSH,
			Inputs: map[string]string{"username": "root", "ssh_key_data": "$encrypted$"},
		},
		{
			ID:     usernameOnly,
			Name:   "scm with only a username",
			Kind:   credtype.KindSCM,
			Inputs: map[string]string{"username": "svc"},
		},
		{
			ID:     external,
			Name:   "scm from a secret manager",
			Kind:   credtype.KindSCM,
			Inputs: map[string]string{"username": "svc"},
			// The secret is absent from Inputs entirely, which is how
			// credstore reports an externally sourced value. A check
			// consulting only Inputs would refuse every credential whose
			// password lives in a secret manager.
			External: map[string]string{"password": "vault:/forge#token"},
		},
	}

	for _, tc := range []struct {
		name       string
		credential int
		want       int
	}{
		{"a source control credential with a password", withPassword, http.StatusCreated},
		{"a machine credential carrying an ssh key is still refused", machine, http.StatusBadRequest},
		{"a source control credential supplying no secret", usernameOnly, http.StatusBadRequest},
		{"a secret living in an external manager still counts as supplied", external, http.StatusCreated},
		{"a credential that does not exist", 4242, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := f.do(t, http.MethodPost, "/api/v1/projects", map[string]any{
				"name":         "repo " + tc.name,
				"organization": f.orgID,
				"scm_url":      "https://example.invalid/a.git",
				"credential":   tc.credential,
			})
			if status != tc.want {
				t.Errorf("status %d, want %d; body %s", status, tc.want, body)
			}
		})
	}
}

// TestProjects_NoCredentialIsThePublicRepositoryCase proves the check is
// skipped rather than failed when no credential is named.
func TestProjects_NoCredentialIsThePublicRepositoryCase(t *testing.T) {
	f := newProjectFixture(t)
	f.creds.err = errors.New("the credential store is unreachable")

	// Credential zero, so the store is never consulted and its failure
	// cannot matter. A public clone must not depend on the credential
	// store being up.
	status, body := f.do(t, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "public", "organization": f.orgID, "scm_url": "https://example.invalid/a.git",
	})
	if status != http.StatusCreated {
		t.Errorf("status %d, want a public project to need no credential store; body %s", status, body)
	}
}

// TestProjects_AnUnreadableCredentialStoreIsAServerError separates "your
// credential is wrong" from "we could not find out", which are different
// facts and call for different actions.
func TestProjects_AnUnreadableCredentialStoreIsAServerError(t *testing.T) {
	f := newProjectFixture(t)
	f.creds.err = errors.New("the credential store is unreachable")

	status, _ := f.do(t, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "private", "organization": f.orgID,
		"scm_url": "https://example.invalid/a.git", "credential": 1,
	})
	if status != http.StatusInternalServerError {
		t.Errorf("status %d, want 500 when the credential store cannot be read", status)
	}
}

// TestProjects_UpdateChangesFieldsAndRechecksTheCredential proves the
// credential rule is not create-only. An update is the obvious way around a
// create-time check.
func TestProjects_UpdateChangesFieldsAndRechecksTheCredential(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "renameable")
	f.creds.creds = []credstore.Credential{{
		ID:     7,
		Name:   "cloud money",
		Kind:   credtype.KindCloud,
		Inputs: map[string]string{"password": "$encrypted$"},
	}}

	status, body := f.do(t, http.MethodPatch, fmt.Sprintf("/api/v1/projects/%d", id), map[string]any{
		"name": "renamed", "organization": f.orgID,
		"scm_url": "https://example.invalid/a.git", "credential": 7,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status %d, want an update to recheck the credential; body %s", status, body)
	}

	status, body = f.do(t, http.MethodPatch, fmt.Sprintf("/api/v1/projects/%d", id), map[string]any{
		"name": "renamed", "organization": f.orgID, "scm_url": "https://example.invalid/b.git",
	})
	if status != http.StatusOK {
		t.Fatalf("status %d, body %s", status, body)
	}
	var dto struct {
		Name   string `json:"name"`
		SCMURL string `json:"scm_url"`
	}
	if err := json.Unmarshal(body, &dto); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if dto.Name != "renamed" || dto.SCMURL != "https://example.invalid/b.git" {
		t.Errorf("update did not take: %+v", dto)
	}
}

// TestProjects_DuplicateNameInAnOrganizationIsAConflict covers the store
// error the handler has to translate rather than report as a 500.
func TestProjects_DuplicateNameInAnOrganizationIsAConflict(t *testing.T) {
	f := newProjectFixture(t)
	f.createProject(t, "taken")

	status, body := f.do(t, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "taken", "organization": f.orgID, "scm_url": "https://example.invalid/a.git",
	})
	if status != http.StatusConflict {
		t.Errorf("status %d, want 409 for a duplicate name; body %s", status, body)
	}
}

// TestProjects_MissingRecordsAnswerNotFound covers every id-taking endpoint
// at once, because a 500 here would send somebody looking for a broken
// controller rather than a mistyped id.
func TestProjects_MissingRecordsAnswerNotFound(t *testing.T) {
	f := newProjectFixture(t)

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/projects/4242", nil},
		{http.MethodDelete, "/api/v1/projects/4242", nil},
		{http.MethodPost, "/api/v1/projects/4242/sync", nil},
		{http.MethodPatch, "/api/v1/projects/4242", map[string]any{
			"name": "ghost", "organization": f.orgID, "scm_url": "https://example.invalid/a.git",
		}},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			status, body := f.do(t, tc.method, tc.path, tc.body)
			if status != http.StatusNotFound {
				t.Errorf("status %d, want 404; body %s", status, body)
			}
		})
	}
}

// TestProjects_MalformedIdsAndQueriesAnswerBadRequest keeps the path
// parameter and the listing bound from reaching the store as something
// unintended.
func TestProjects_MalformedIdsAndQueriesAnswerBadRequest(t *testing.T) {
	f := newProjectFixture(t)

	for _, path := range []string{
		"/api/v1/projects/not-a-number",
		"/api/v1/projects/0",
		"/api/v1/projects/-1",
		"/api/v1/projects?limit=0",
		"/api/v1/projects?limit=banana",
		"/api/v1/projects?limit=-3",
	} {
		t.Run(path, func(t *testing.T) {
			status, body := f.do(t, http.MethodGet, path, nil)
			if status != http.StatusBadRequest {
				t.Errorf("status %d, want 400; body %s", status, body)
			}
		})
	}
}

// TestProjects_MalformedBodiesAnswerBadRequest covers the decode path on
// both writing endpoints.
func TestProjects_MalformedBodiesAnswerBadRequest(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "decodable")

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/projects"},
		{http.MethodPatch, fmt.Sprintf("/api/v1/projects/%d", id)},
	} {
		t.Run(tc.method, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte("{not json")))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			f.router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400 for a malformed body", rec.Code)
			}
		})
	}
}

// TestProjects_DeleteRemovesTheProject proves the delete is real rather
// than a 204 over nothing.
func TestProjects_DeleteRemovesTheProject(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "temporary")

	status, body := f.do(t, http.MethodDelete, fmt.Sprintf("/api/v1/projects/%d", id), nil)
	if status != http.StatusNoContent {
		t.Fatalf("status %d, body %s", status, body)
	}
	if status, _ = f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%d", id), nil); status != http.StatusNotFound {
		t.Errorf("the project is still readable after a delete: status %d", status)
	}
}

// TestProjects_SyncEnqueuesAndRecordsItsOutcome covers the async happy path:
// the request is accepted at once, the clone runs in the background over the
// STORED project, and the outcome a caller polls for is persisted.
func TestProjects_SyncEnqueuesAndRecordsItsOutcome(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "syncable")
	f.syncer.result = project.Result{
		Status:    project.SyncSucceeded,
		Revision:  "3b9f1c2d4e5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c",
		LocalPath: "/var/lib/pleiades/projects/1",
	}

	status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/sync", id), nil)
	if status != http.StatusAccepted {
		t.Fatalf("status %d, want 202 Accepted; body %s", status, body)
	}

	// The clone runs off the request; wait for it the way a caller polls.
	f.runner.Wait()

	if len(f.syncer.seen) != 1 {
		t.Fatalf("the syncer was asked %d times, want once", len(f.syncer.seen))
	}
	// The record the runner read, not one rebuilt from the request. A sync
	// must fetch the URL that was saved, whatever the caller posted.
	if got := f.syncer.seen[0]; got.ID != id || got.SCMURL != "https://example.invalid/automation.git" {
		t.Errorf("the syncer was handed %+v, want the stored project", got)
	}

	_, polled := f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%d", id), nil)
	var dto struct {
		Revision   string `json:"revision"`
		SyncStatus string `json:"sync_status"`
	}
	if err := json.Unmarshal(polled, &dto); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if dto.SyncStatus != string(project.SyncSucceeded) || dto.Revision != f.syncer.result.Revision {
		t.Errorf("the sync outcome was not persisted: %+v", dto)
	}
}

// TestProjects_AFailedSyncBecomesAFailedStatus is the distinction that used
// to be "a failed sync is still a 200": under the async path the request is
// accepted regardless, and the failure lands in the status a caller polls,
// which is worth a test because getting it wrong sends an operator to look
// at the controller when the actual problem is somebody's repository.
func TestProjects_AFailedSyncBecomesAFailedStatus(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "unreachable")
	f.syncer.result = project.Result{
		Status: project.SyncFailed,
		Err:    "repository not found",
	}

	status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/sync", id), nil)
	if status != http.StatusAccepted {
		t.Fatalf("status %d, want 202 for an accepted sync; body %s", status, body)
	}
	f.runner.Wait()

	_, polled := f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%d", id), nil)
	var dto struct {
		SyncStatus string `json:"sync_status"`
		SyncError  string `json:"sync_error"`
	}
	if err := json.Unmarshal(polled, &dto); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if dto.SyncStatus != string(project.SyncFailed) {
		t.Errorf("sync_status = %q, want failed", dto.SyncStatus)
	}
	if dto.SyncError != "repository not found" {
		t.Errorf("sync_error = %q, want the recorded reason", dto.SyncError)
	}
}

// TestProjects_AnUnsyncableProjectIsRefused covers the synchronous refusal:
// a project with no fetchable source is a 400 at claim time rather than a
// background failure nobody is watching.
func TestProjects_AnUnsyncableProjectIsRefused(t *testing.T) {
	f := newProjectFixture(t)
	// A manual project has no source to fetch, so BeginSync refuses it.
	p, err := f.store.Create(context.Background(), project.Project{
		Name: "manual-only", SCMType: project.SCMManual, OrganizationID: f.orgID,
	})
	if err != nil {
		t.Fatalf("creating an unsyncable project: %v", err)
	}

	status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/sync", p.ID), nil)
	if status != http.StatusBadRequest {
		t.Errorf("status %d, want 400 for a project with no fetchable source; body %s", status, body)
	}
}

// TestProjects_ASyncAlreadyRunningIsRefused covers the 409: a second sync of
// a project whose first is still running is refused rather than started, so
// two clones never race on the one working tree.
func TestProjects_ASyncAlreadyRunningIsRefused(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "busy")

	// Claim it directly, so it is running when the request arrives and is
	// never completed for the duration of the test.
	if _, err := f.store.BeginSync(context.Background(), id, "tester"); err != nil {
		t.Fatalf("claiming the project: %v", err)
	}

	status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/sync", id), nil)
	if status != http.StatusConflict {
		t.Errorf("status %d, want 409 when a sync is already running; body %s", status, body)
	}
}

// TestNewProjectHandler_DefaultsItsLogger records that a nil logger is a
// usable handler rather than a panic on the first request.
func TestNewProjectHandler_DefaultsItsLogger(t *testing.T) {
	if h := api.NewProjectHandler(nil, nil, nil, nil); h == nil {
		t.Fatal("NewProjectHandler returned nil")
	}
}

// TestProjects_ANilCredentialListerAcceptsAnyCredential is the documented
// behaviour for a deployment that has wired no credential store, and it is
// tested because the alternative reading of the same code, refusing
// everything, would make such a deployment unable to create a project at
// all.
func TestProjects_ANilCredentialListerAcceptsAnyCredential(t *testing.T) {
	client := newSerializedSQLiteClient(t, fmt.Sprintf("projects-nilcreds-%d", projectFixtureSeq.Add(1)))
	org := client.Organization.Create().SetName("network").SaveX(context.Background())
	store := project.NewEntStore(client, project.SourcePolicy{})
	handler := api.NewProjectHandler(store, project.NewRunner(store, &fakeSyncer{}, nil), nil, nil)

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.CreateProject.Route(handler.Create)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	// A real row, because the point under test is that the LISTER is never
	// consulted, not that a dangling foreign key is tolerated.
	credentialID := realCredential(t, client, org.ID, "unconsulted")

	encoded, err := json.Marshal(map[string]any{
		"name": "unchecked", "organization": org.ID,
		"scm_url": "https://example.invalid/a.git", "credential": credentialID,
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status %d, want a deployment with no credential store to still create projects; body %s",
			rec.Code, rec.Body.String())
	}
}

// errStoreBroken is a store failure that is neither a missing record nor a
// name collision, so it must reach the caller as a 500 and the operator as
// a log line.
var errStoreBroken = errors.New("the database is on fire")

// failingStore fails whichever operation a test is about.
//
// A fake rather than a real store made to fail, because the paths under
// test are the ones a real SQLite store will not take on demand: a listing
// that errors, a delete that errors, a sync whose outcome cannot be
// recorded. Those are the branches that decide whether an operator is sent
// to look at the controller or at their own request, which makes them worth
// covering even though the failure has to be manufactured.
type failingStore struct {
	project.Store

	err error

	// getOK lets Get succeed while a later call fails, which is how the
	// sync path reaches RecordSync at all.
	getOK bool
}

// List fails.
func (f *failingStore) List(context.Context, project.Query) ([]project.Project, error) {
	return nil, f.err
}

// Get fails unless the test needs it to succeed first.
func (f *failingStore) Get(_ context.Context, id int) (project.Project, error) {
	if f.getOK {
		return project.Project{ID: id, Name: "readable", SCMType: project.SCMGit, SCMURL: "https://example.invalid/a.git"}, nil
	}
	return project.Project{}, f.err
}

// Create fails.
func (f *failingStore) Create(context.Context, project.Project) (project.Project, error) {
	return project.Project{}, f.err
}

// Update fails.
func (f *failingStore) Update(context.Context, project.Project) error { return f.err }

// Delete fails.
func (f *failingStore) Delete(context.Context, int) error { return f.err }

// RecordSync fails.
func (f *failingStore) RecordSync(context.Context, int, project.Result) error { return f.err }

// BeginSync fails, which is how a broken store surfaces on the sync path now
// that the clone and its recording happen in the background: the one thing a
// request still owns is whether the sync could be claimed at all.
func (f *failingStore) BeginSync(context.Context, int, string) (project.Claim, error) {
	return project.Claim{}, f.err
}

// ResetInterruptedSyncs fails.
func (f *failingStore) ResetInterruptedSyncs(context.Context) (int, error) { return 0, f.err }

// routerOverStore mounts every project route over the given store.
func routerOverStore(t *testing.T, store project.Store, syncer project.Syncer) http.Handler {
	t.Helper()

	runner := project.NewRunner(store, syncer, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	handler := api.NewProjectHandler(store, runner, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListProjects.Route(handler.List),
			apispec.GetProject.Route(handler.Get),
			apispec.CreateProject.Route(handler.Create),
			apispec.UpdateProject.Route(handler.Update),
			apispec.DeleteProject.Route(handler.Delete),
			apispec.SyncProject.Route(handler.Sync),
			apispec.StreamProjectSyncLogs.Route(handler.StreamSyncLogs),
			apispec.CancelProjectSync.Route(handler.CancelSync),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

// TestProjects_AnUnexpectedStoreFailureIsAServerError covers the branch
// every id-taking endpoint shares.
//
// The distinction being held is the one respondStoreError exists for: a
// missing record and a taken name are the caller's problem and are
// answered as such, and everything else is this deployment's problem and
// must not be dressed up as either.
func TestProjects_AnUnexpectedStoreFailureIsAServerError(t *testing.T) {
	for _, tc := range []struct {
		name         string
		method, path string
		body         any
		getOK        bool
	}{
		{name: "list", method: http.MethodGet, path: "/api/v1/projects"},
		{name: "get", method: http.MethodGet, path: "/api/v1/projects/1"},
		{name: "delete", method: http.MethodDelete, path: "/api/v1/projects/1"},
		{
			name: "create", method: http.MethodPost, path: "/api/v1/projects",
			body: map[string]any{"name": "x", "organization": 1, "scm_url": "https://example.invalid/a.git"},
		},
		{
			name: "update", method: http.MethodPatch, path: "/api/v1/projects/1",
			body:  map[string]any{"name": "x", "organization": 1, "scm_url": "https://example.invalid/a.git"},
			getOK: true,
		},
		// Claiming the sync is what failed. The clone and its recording
		// happen off the request now, so the one sync outcome a request
		// still owns is whether it could be started, and a broken store
		// there is a 500 rather than a repository problem.
		{name: "claiming a sync", method: http.MethodPost, path: "/api/v1/projects/1/sync"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &failingStore{err: errStoreBroken, getOK: tc.getOK}
			router := routerOverStore(t, store, &fakeSyncer{result: project.Result{Status: project.SyncSucceeded}})

			var reader io.Reader
			if tc.body != nil {
				encoded, err := json.Marshal(tc.body)
				if err != nil {
					t.Fatalf("marshalling: %v", err)
				}
				reader = bytes.NewReader(encoded)
			}
			req := httptest.NewRequest(tc.method, tc.path, reader)
			if tc.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status %d, want 500; body %s", rec.Code, rec.Body.String())
			}
			// The underlying failure must not be echoed back. It is this
			// deployment's internal state, and an error string is a
			// reliable way to leak a DSN or a table name.
			if body := rec.Body.String(); strings.Contains(body, errStoreBroken.Error()) {
				t.Errorf("the response repeats the store's own error: %s", body)
			}
		})
	}
}

// TestProjects_AnUpdateThatCannotBeReadBackIsAServerError covers the
// second Get inside Update, which a store that writes but cannot read
// would otherwise reach with nobody watching.
func TestProjects_AnUpdateThatCannotBeReadBackIsAServerError(t *testing.T) {
	store := &writeThenBlindStore{}
	router := routerOverStore(t, store, &fakeSyncer{})

	encoded, err := json.Marshal(map[string]any{
		"name": "written", "organization": 1, "scm_url": "https://example.invalid/a.git",
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/1", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500 when the written record cannot be read back", rec.Code)
	}
}

// writeThenBlindStore accepts an update and then cannot read it back.
type writeThenBlindStore struct {
	project.Store
}

// Get always fails.
func (w *writeThenBlindStore) Get(context.Context, int) (project.Project, error) {
	return project.Project{}, errStoreBroken
}

// Update succeeds.
func (w *writeThenBlindStore) Update(context.Context, project.Project) error { return nil }

// TestProjects_EveryIdTakingEndpointRefusesAMalformedId keeps a path
// parameter from reaching the store as something unintended, on the three
// verbs the earlier listing test does not cover.
func TestProjects_EveryIdTakingEndpointRefusesAMalformedId(t *testing.T) {
	f := newProjectFixture(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPatch, "/api/v1/projects/not-a-number"},
		{http.MethodPatch, "/api/v1/projects/0"},
		{http.MethodDelete, "/api/v1/projects/not-a-number"},
		{http.MethodDelete, "/api/v1/projects/-4"},
		{http.MethodPost, "/api/v1/projects/not-a-number/sync"},
		{http.MethodPost, "/api/v1/projects/0/sync"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			status, body := f.do(t, tc.method, tc.path, map[string]any{
				"name": "x", "organization": f.orgID, "scm_url": "https://example.invalid/a.git",
			})
			if status != http.StatusBadRequest {
				t.Errorf("status %d, want 400; body %s", status, body)
			}
		})
	}
}

// TestProjects_AnUpdateIsValidatedLikeACreate proves the body rules are not
// create-only, which is the obvious way around them.
func TestProjects_AnUpdateIsValidatedLikeACreate(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "editable")

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"no name", map[string]any{"organization": f.orgID, "scm_url": "https://example.invalid/a.git"}},
		{"an unknown scm type", map[string]any{"name": "x", "organization": f.orgID, "scm_type": "subversion"}},
		{"a git project stripped of its url", map[string]any{"name": "x", "organization": f.orgID, "scm_type": "git"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := f.do(t, http.MethodPatch, fmt.Sprintf("/api/v1/projects/%d", id), tc.body)
			if status != http.StatusBadRequest {
				t.Errorf("status %d, want 400; body %s", status, body)
			}
		})
	}
}

// TestProjects_ListHonoursItsLimit covers the bound actually being applied
// rather than only rejected when malformed.
func TestProjects_ListHonoursItsLimit(t *testing.T) {
	f := newProjectFixture(t)
	f.createProject(t, "first")
	f.createProject(t, "second")
	f.createProject(t, "third")

	status, body := f.do(t, http.MethodGet, "/api/v1/projects?limit=2", nil)
	if status != http.StatusOK {
		t.Fatalf("status %d, body %s", status, body)
	}
	var list struct {
		Projects []struct {
			ID int `json:"id"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(list.Projects) != 2 {
		t.Errorf("limit=2 returned %d projects, want 2", len(list.Projects))
	}
}

// syncThenBlindStore claims a sync successfully and then cannot read the
// project back, which is the one place the asynchronous sync handler still
// owes a caller a 500: the read after the claim.
type syncThenBlindStore struct {
	project.Store
}

// BeginSync claims the project, so the handler reaches the read-back.
func (s *syncThenBlindStore) BeginSync(_ context.Context, id int, _ string) (project.Claim, error) {
	return project.Claim{
		Project: project.Project{
			ID: id, Name: "readable", SCMType: project.SCMGit,
			SCMURL: "https://example.invalid/a.git", SyncStatus: project.SyncRunning,
		},
		RunID:     id,
		StartedAt: time.Now(),
	}, nil
}

// Get always fails, standing in for a store that claimed the sync but cannot
// read the project the handler returns.
func (s *syncThenBlindStore) Get(context.Context, int) (project.Project, error) {
	return project.Project{}, errStoreBroken
}

// RecordSync succeeds, so the background clone's own write is not the failure
// under test.
func (s *syncThenBlindStore) RecordSync(context.Context, int, project.Result) error { return nil }

// TestProjects_ASyncThatCannotBeReadBackIsAServerError covers the read
// after the write, which is the one place a sync can succeed and still owe
// the caller an error.
func TestProjects_ASyncThatCannotBeReadBackIsAServerError(t *testing.T) {
	router := routerOverStore(t, &syncThenBlindStore{}, &fakeSyncer{result: project.Result{Status: project.SyncSucceeded}})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/1/sync", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500 when a recorded sync cannot be read back", rec.Code)
	}
}

// TestProjects_SyncLogStreamEndsWhenThereIsNothingRunning covers the reader
// who opens the page when no clone is in flight: they must be told the
// stream is over rather than left holding a connection that will never
// deliver anything.
func TestProjects_SyncLogStreamEndsWhenThereIsNothingRunning(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "quiet")

	status, body := f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/sync/logs", id), nil)
	if status != http.StatusOK {
		t.Fatalf("status %d, want 200 for an SSE stream; body %s", status, body)
	}
	got := string(body)
	if !strings.Contains(got, "event: init") {
		t.Errorf("the stream never opened: %s", got)
	}
	if !strings.Contains(got, "event: done") {
		t.Errorf("the stream did not say it had finished, so a reader waits forever: %s", got)
	}
}

// TestProjects_SyncLogStreamCarriesTheClonesOutput proves the fetch's own
// lines reach a reader, which is the whole point of the endpoint.
func TestProjects_SyncLogStreamCarriesTheClonesOutput(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "chatty")
	f.syncer.result = project.Result{Status: project.SyncSucceeded, Revision: "abc123"}
	f.syncer.progress = "Counting objects: 12, done.\n"

	if status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/sync", id), nil); status != http.StatusAccepted {
		t.Fatalf("starting the sync = %d: %s", status, body)
	}
	f.runner.Wait()

	// The clone has finished, so this reader gets the replayed tail and an
	// immediate close, which is the late-arrival path.
	status, body := f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/sync/logs", id), nil)
	if status != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", status, body)
	}
	if got := string(body); !strings.Contains(got, "Counting objects: 12, done.") {
		t.Errorf("the stream did not carry the clone's output:\n%s", got)
	}
}

// TestProjects_SyncLogStreamRefusesAMalformedId keeps the guard every
// id-taking endpoint shares on the streaming one too.
func TestProjects_SyncLogStreamRefusesAMalformedId(t *testing.T) {
	f := newProjectFixture(t)

	if status, _ := f.do(t, http.MethodGet, "/api/v1/projects/not-a-number/sync/logs", nil); status != http.StatusBadRequest {
		t.Errorf("a malformed id on the log stream = %d, want 400", status)
	}
}

// nonFlushingWriter is an http.ResponseWriter that deliberately does NOT
// implement http.Flusher, which is the one thing an SSE handler cannot work
// without.
type nonFlushingWriter struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (w *nonFlushingWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *nonFlushingWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *nonFlushingWriter) WriteHeader(code int)        { w.code = code }

// TestProjects_SyncLogStreamNeedsAFlushingWriter proves the handler refuses
// rather than streaming into a writer that cannot flush. Without a flush,
// every frame would sit in a buffer until the response ended, which for a
// stream that ends when the clone does is the same as sending nothing.
func TestProjects_SyncLogStreamNeedsAFlushingWriter(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "unflushable")

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/sync/logs", id), nil)
	w := &nonFlushingWriter{}
	f.router.ServeHTTP(w, req)

	if w.code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500 when the writer cannot flush; body %s", w.code, w.body.String())
	}
}

// TestProjects_CancelStopsARunningSync covers the control an operator needs
// when a fetch is taking too long: the clone is stopped and the project
// settles to a failed status naming the cancellation.
func TestProjects_CancelStopsARunningSync(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "slow")
	f.syncer.started = make(chan struct{})
	f.syncer.block = make(chan struct{})

	if status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/sync", id), nil); status != http.StatusAccepted {
		t.Fatalf("starting the sync = %d: %s", status, body)
	}
	<-f.syncer.started // the clone is in flight and will not finish on its own

	status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/sync/cancel", id), nil)
	if status != http.StatusAccepted {
		t.Fatalf("cancelling = %d, want 202; body %s", status, body)
	}
	f.runner.Wait()

	_, polled := f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%d", id), nil)
	var dto struct {
		SyncStatus string `json:"sync_status"`
		SyncError  string `json:"sync_error"`
	}
	if err := json.Unmarshal(polled, &dto); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if dto.SyncStatus != string(project.SyncFailed) {
		t.Errorf("sync_status = %q, want failed after a cancel", dto.SyncStatus)
	}
	if !strings.Contains(dto.SyncError, "cancelled") {
		t.Errorf("sync_error = %q, want it to name the cancellation", dto.SyncError)
	}
}

// TestProjects_CancelWithNothingRunningIsAConflict keeps the endpoint honest:
// reporting success for having stopped nothing would tell a caller it had
// done something it had not.
func TestProjects_CancelWithNothingRunningIsAConflict(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "idle")

	if status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/sync/cancel", id), nil); status != http.StatusConflict {
		t.Errorf("cancelling an idle project = %d, want 409; body %s", status, body)
	}
}

// TestProjects_CancelAnUnknownProjectIsNotFound proves an unknown id is a 404
// rather than a 409 that would read as "nothing is running" about a project
// that does not exist.
func TestProjects_CancelAnUnknownProjectIsNotFound(t *testing.T) {
	f := newProjectFixture(t)

	if status, _ := f.do(t, http.MethodPost, "/api/v1/projects/999999/sync/cancel", nil); status != http.StatusNotFound {
		t.Errorf("cancelling an unknown project = %d, want 404", status)
	}
}

// TestProjects_ASyncRecordsWhoAskedForIt proves the attempt is attributable
// afterward: the history row names the request's own identity, so a person's
// sync and a schedule's sync can be told apart weeks later.
//
// The actor is read back through the real store rather than asserted on the
// response, because the response does not carry it and the question this
// covers is what was durably recorded.
func TestProjects_ASyncRecordsWhoAskedForIt(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "attributed")
	// The outcome the background clone reports. Without one the fake syncer
	// reports an empty status, which is not an outcome the store can record.
	f.syncer.result = project.Result{
		Status:    project.SyncSucceeded,
		Revision:  "abc123",
		LocalPath: t.TempDir(),
	}

	status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/sync", id), nil)
	if status != http.StatusAccepted {
		t.Fatalf("status %d, want 202; body %s", status, body)
	}
	f.runner.Wait()

	runs, err := f.store.ListSyncRuns(context.Background(), id, 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("recorded %d attempts, want exactly 1", len(runs))
	}
	// alwaysAuthenticated's identity, which is what the request carried.
	if runs[0].Actor != "test-user" {
		t.Errorf("attempt actor = %q, want the request's own subject", runs[0].Actor)
	}
	if runs[0].Running() {
		t.Errorf("the attempt still reads as running after the runner finished it: %+v", runs[0])
	}
}

// TestProjects_ASyncWithNoIdentityIsRefused proves an unattributable sync is
// refused rather than recorded against nobody. It is a 401 rather than a 500,
// because a request with no identity is an authentication failure, and rather
// than a silent default, because a default actor is a name in an audit trail
// that nothing answers to.
func TestProjects_ASyncWithNoIdentityIsRefused(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "anonymous")

	// The same handler behind a middleware that authenticates nobody, which
	// is the shape a broken or misordered chain produces.
	handler := api.NewProjectHandler(f.store, f.runner, f.creds, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      func(next http.Handler) http.Handler { return next },
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.SyncProject.Route(handler.Sync)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/sync", id), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401; body %s", rec.Code, rec.Body.String())
	}

	// Nothing was claimed, so the project is still syncable and its history
	// is empty.
	runs, err := f.store.ListSyncRuns(context.Background(), id, 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() = %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("a refused sync recorded %d attempts, want none", len(runs))
	}
}
