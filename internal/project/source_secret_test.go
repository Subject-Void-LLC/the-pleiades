// Package project_test's credential-in-URL coverage: the store refuses a
// source URL carrying a credential for a project of every type, on create
// and on update, while a row that already holds one stays readable.
//
// It used to refuse one only for a git project, because the refusal sat
// behind checkSource's git-only return, so a manual or archive project
// stored a password in the plain scm_url column.
package project_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// fullwidthAt and smallAt are the two lookalikes of "@" (FULLWIDTH
// COMMERCIAL AT and SMALL COMMERCIAL AT), built from their code points so
// this source holds no invisible or confusable character (FAILURE_PATTERNS
// 256, 834).
var (
	fullwidthAt = string(rune(0xFF20))
	smallAt     = string(rune(0xFE6B))
)

// secretSourceURL carries a password in its userinfo.
const secretSourceURL = "https://svc:hunter2@git.example.invalid/team/repo.git"

// assertSecretRefusal checks err is the credential-in-URL refusal and that
// its text repeats neither the password nor the URL.
func assertSecretRefusal(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, project.ErrSourceSecretInURL) {
		t.Fatalf("%s = %v, want ErrSourceSecretInURL", what, err)
	}
	if msg := err.Error(); strings.Contains(msg, "hunter2") || strings.Contains(msg, "git.example.invalid") {
		t.Errorf("%s: the refusal %q repeats the URL or its password", what, msg)
	}
	if !strings.Contains(err.Error(), "credential") {
		t.Errorf("%s: the refusal %q does not say to use a credential", what, err)
	}
}

// TestStore_RefusesACredentialInTheURLForEveryType holds the refusal on
// create and on update for git, manual and archive alike.
func TestStore_RefusesACredentialInTheURLForEveryType(t *testing.T) {
	client := newStoreClient(t)
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	store := project.NewEntStore(client, project.SourcePolicy{})

	for _, scmType := range []project.SCMType{project.SCMGit, project.SCMManual, project.SCMArchive} {
		t.Run(string(scmType), func(t *testing.T) {
			_, err := store.Create(ctx, project.Project{
				Name: "secret-" + string(scmType), SCMType: scmType, SCMURL: secretSourceURL, OrganizationID: org.ID,
			})
			assertSecretRefusal(t, "Create", err)

			clean, err := store.Create(ctx, project.Project{
				Name: "clean-" + string(scmType), SCMType: scmType,
				SCMURL: "https://git.example.invalid/team/repo.git", OrganizationID: org.ID,
			})
			if err != nil {
				t.Fatalf("Create with a clean URL = %v", err)
			}
			repointed := clean
			repointed.SCMURL = secretSourceURL
			assertSecretRefusal(t, "Update", store.Update(ctx, repointed))
			if got, _ := store.Get(ctx, clean.ID); got.SCMURL != clean.SCMURL {
				t.Errorf("a refused Update stored scm_url %q", got.SCMURL)
			}
		})
	}
}

// TestStore_AStoredCredentialInTheURLIsReadableAndMustBeChanged covers a row
// an older release stored, written here through the ent client as it would
// have been: reading it never refuses, its next save is refused until the URL
// is changed, and a clean URL is accepted.
func TestStore_AStoredCredentialInTheURLIsReadableAndMustBeChanged(t *testing.T) {
	client := newStoreClient(t)
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	store := project.NewEntStore(client, project.SourcePolicy{})

	created, err := store.Create(ctx, project.Project{
		Name: "legacy", SCMType: project.SCMManual,
		SCMURL: "https://git.example.invalid/team/repo.git", OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("Create = %v", err)
	}
	client.Project.UpdateOneID(created.ID).SetScmURL(secretSourceURL).ExecX(ctx)

	stored, err := store.Get(ctx, created.ID)
	if err != nil || stored.SCMURL != secretSourceURL {
		t.Fatalf("Get of the legacy row = %+v, %v; want it read as stored", stored, err)
	}
	if listed, err := store.List(ctx, project.Query{}); err != nil || len(listed) != 1 {
		t.Fatalf("List with the legacy row = %d rows, %v; want it listed", len(listed), err)
	}

	edited := stored
	edited.Description = "edited"
	assertSecretRefusal(t, "Update changing only the description", store.Update(ctx, edited))

	edited.SCMURL = "https://git.example.invalid/team/repo.git"
	if err := store.Update(ctx, edited); err != nil {
		t.Fatalf("Update changing the URL to a clean one = %v, want it accepted", err)
	}
	if got, _ := store.Get(ctx, created.ID); got.SCMURL != edited.SCMURL || got.Description != "edited" {
		t.Errorf("after the accepted Update: %+v", got)
	}
}

// misreadSecretURLs are URLs carrying a credential that a parser misreads or
// refuses to read, each of which the store used to accept for a manual or
// archive project and every reader then showed raw.
var misreadSecretURLs = map[string]string{
	"a bad port":                  "https://svc:hunter2@git.example.invalid:badport/x",
	"a space in the host":         "https://svc:hunter2@git.example invalid/x",
	"an unclosed IPv6 host":       "https://svc:hunter2@[::1:bad/x",
	"git+https with a token":      "git+https://hunter2@git.example.invalid/org/repo.git",
	"a scheme-less authority":     "//bot:hunter2@git.example.invalid/org/repo",
	"scp-like with a host space":  "git:hunter2@git.example invalid:team/repo",
	"a token over the git daemon": "git://hunter2@git.example.invalid/org/repo.git",

	// The second review's shapes, refused by the allowlist without having
	// been listed in it.
	"a password holding a slash":   "https://svc:pa/hunter2@git.example.invalid/r",
	"a password read as a port":    "https://svc:1234/hunter2@git.example.invalid/r",
	"a compound https scheme":      "git+ssh+https://hunter2@git.example.invalid/r",
	"https under ssh":              "https+ssh://hunter2@git.example.invalid/r",
	"one slash":                    "https:/svc:hunter2@git.example.invalid/r",
	"three slashes":                "https:///svc:hunter2@git.example.invalid/r",
	"a backslash":                  `https:\/hunter2@git.example.invalid/r`,
	"a token in the query":         "https://git.example.invalid/r.git?access_token=hunter2",
	"a GitLab private token":       "https://git.example.invalid/r.git?private_token=hunter2",
	"an ssh user with an encoding": "ssh://git%3Ahunter2@git.example.invalid/r",

	// The claim-refuter's shapes: an encoded or lookalike "@", and a
	// credential in a query or fragment no list of secret names covers.
	"an encoded at":                 "https://hunter2%40git.example.invalid/r",
	"an encoded at in the password": "https://svc:hunter2%40git.example.invalid/r",
	"a fullwidth at":                "https://hunter2" + fullwidthAt + "git.example.invalid/org/repo.git",
	"a small at":                    "https://hunter2" + smallAt + "git.example.invalid/org/repo.git",
	"an Azure SAS signature":        "https://git.example.invalid/c/repo.tar?sv=2024&sig=hunter2",
	"an S3 signature":               "https://git.example.invalid/repo.tar?X-Amz-Signature=hunter2",
	"an apikey":                     "https://git.example.invalid/r.git?apikey=hunter2",
	"a token in the fragment":       "https://git.example.invalid/r.git#token=hunter2",
}

// TestStore_RefusesACredentialInAURLAParserMisreads holds the refusal on
// every misread shape, for every type, on create: the detector fails closed
// rather than reading "could not parse" as "carries nothing".
func TestStore_RefusesACredentialInAURLAParserMisreads(t *testing.T) {
	client := newStoreClient(t)
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	store := project.NewEntStore(client, project.SourcePolicy{})

	for shape, raw := range misreadSecretURLs {
		for _, scmType := range []project.SCMType{project.SCMGit, project.SCMManual, project.SCMArchive} {
			t.Run(shape+", "+string(scmType), func(t *testing.T) {
				_, err := store.Create(ctx, project.Project{
					Name: "misread-" + string(scmType) + "-" + shape, SCMType: scmType, SCMURL: raw, OrganizationID: org.ID,
				})
				assertSecretRefusal(t, "Create", err)
				if shown := project.RedactedSourceURL(raw); strings.Contains(shown, "hunter2") {
					t.Errorf("RedactedSourceURL shows the credential: %q", shown)
				}
			})
		}
	}
}
