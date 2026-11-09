// Package project_test's sync error coverage: what a failed sync records
// never carries the credential a stored URL may hold.
package project_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// TestSync_NeverRecordsAStoredCredentialInItsError syncs a git row an older
// release stored with a URL carrying a credential, and records the outcome
// through the real store. Every refusal and transport error a sync reports
// is stored in sync_error and shown on the project's page, and the parsers'
// own errors quote the URL they were given: go-git's for a URL it cannot
// read, net/http's for one it dials. Neither may carry the credential.
func TestSync_NeverRecordsAStoredCredentialInItsError(t *testing.T) {
	client := newStoreClient(t)
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	store := project.NewEntStore(client, project.SourcePolicy{})
	syncer := project.NewGitSyncer(t.TempDir(), nil, project.SourcePolicy{})

	for shape, raw := range map[string]string{
		"a parse error quoting the URL": "https://svc:pa/hunter2@git.example.invalid/r",
		"a bad port":                    "https://svc:hunter2@git.example.invalid:badport/x",
		"a space in the host":           "https://svc:hunter2@git.example invalid/x",
		// net/url reads 127.0.0.1:1 as the host and "/hunter2@..." as the
		// path, so the clone dials a closed port and the transport error
		// quotes the whole URL.
		"a dial error quoting the URL": "https://127.0.0.1:1/hunter2@git.example.invalid/r",
		"a token in the query":         "https://127.0.0.1:1/r.git?access_token=hunter2",
	} {
		t.Run(shape, func(t *testing.T) {
			created, err := store.Create(ctx, project.Project{
				Name: "legacy " + shape, SCMType: project.SCMGit,
				SCMURL: "https://git.example.invalid/clean.git", OrganizationID: org.ID,
			})
			if err != nil {
				t.Fatalf("Create = %v", err)
			}
			client.Project.UpdateOneID(created.ID).SetScmURL(raw).ExecX(ctx)
			stored, err := store.Get(ctx, created.ID)
			if err != nil {
				t.Fatalf("Get = %v", err)
			}

			var progress strings.Builder
			result, err := syncer.Sync(ctx, stored, &progress)
			if err != nil {
				t.Fatalf("Sync = %v", err)
			}
			if result.Status != project.SyncFailed {
				t.Fatalf("Sync status = %q, want failed", result.Status)
			}
			if err := store.RecordSync(ctx, created.ID, result); err != nil {
				t.Fatalf("RecordSync = %v", err)
			}
			recorded, err := store.Get(ctx, created.ID)
			if err != nil {
				t.Fatalf("Get after the sync = %v", err)
			}
			if recorded.SyncError == "" {
				t.Error("the failed sync recorded no error")
			}
			for _, text := range []string{recorded.SyncError, progress.String()} {
				if strings.Contains(text, "hunter2") {
					t.Errorf("the sync's output carries the stored credential: %q", text)
				}
			}
		})
	}
}

// TestSync_WithholdsATransportErrorQuotingALookalikeAt syncs a stored git row
// whose host holds a fullwidth "@" (built from its code point), with the
// context already cancelled so nothing is dialed. The HTTP client's error
// still quotes the URL it was about to request, lookalike, token and all,
// and that error used to reach sync_error whole, since no "@" was in it.
func TestSync_WithholdsATransportErrorQuotingALookalikeAt(t *testing.T) {
	client := newStoreClient(t)
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	store := project.NewEntStore(client, project.SourcePolicy{})
	syncer := project.NewGitSyncer(t.TempDir(), nil, project.SourcePolicy{})

	created, err := store.Create(ctx, project.Project{
		Name: "legacy lookalike", SCMType: project.SCMGit,
		SCMURL: "https://git.example.invalid/clean.git", OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("Create = %v", err)
	}
	raw := "https://hunter2" + fullwidthAt + "git.example.invalid/org/repo.git"
	client.Project.UpdateOneID(created.ID).SetScmURL(raw).ExecX(ctx)
	stored, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	result, err := syncer.Sync(cancelled, stored, nil)
	if err != nil {
		t.Fatalf("Sync = %v", err)
	}
	if result.Status != project.SyncFailed || result.Err == "" {
		t.Fatalf("Sync = %+v, want a failed sync with an error", result)
	}
	if strings.Contains(result.Err, "hunter2") {
		t.Errorf("the sync error carries the stored token: %q", result.Err)
	}
}
