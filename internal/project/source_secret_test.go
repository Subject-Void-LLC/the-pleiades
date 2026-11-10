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

// The lookalikes of "?" and "#" (FULLWIDTH QUESTION MARK, SMALL QUESTION
// MARK, FULLWIDTH NUMBER SIGN, SMALL NUMBER SIGN), built from code points for
// the same reason.
var (
	fullwidthQuestion = string(rune(0xFF1F))
	smallQuestion     = string(rune(0xFE56))
	fullwidthHash     = string(rune(0xFF03))
	smallHash         = string(rune(0xFE5F))
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

// TestStore_AStoredCredentialInTheURLIsReadableAndKeptByAnEditThatLeavesIt
// covers a row an older release stored, written here through the ent client
// as it would have been: reading it never refuses, an edit that leaves the URL
// as it is saves (only creation refuses a stored value, LESSONS 415 and 422),
// and an edit that changes the URL is held to the rule: another URL carrying a
// credential is refused and a clean one is accepted. It used to refuse every
// save of the row until the URL changed, so a rename was refused for a URL it
// did not touch.
func TestStore_AStoredCredentialInTheURLIsReadableAndKeptByAnEditThatLeavesIt(t *testing.T) {
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
	if err := store.Update(ctx, edited); err != nil {
		t.Fatalf("Update changing only the description = %v, want it saved", err)
	}
	if got, _ := store.Get(ctx, created.ID); got.SCMURL != secretSourceURL || got.Description != "edited" {
		t.Errorf("after an edit leaving the URL: %+v, want the URL kept and the description saved", got)
	}

	edited.SCMURL = "https://svc:other@git.example.invalid/team/repo.git"
	assertSecretRefusal(t, "Update changing the URL to another carrying a credential", store.Update(ctx, edited))

	edited.SCMURL = "https://git.example.invalid/team/repo.git"
	if err := store.Update(ctx, edited); err != nil {
		t.Fatalf("Update changing the URL to a clean one = %v, want it accepted", err)
	}
	if got, _ := store.Get(ctx, created.ID); got.SCMURL != edited.SCMURL {
		t.Errorf("after the accepted Update: %+v", got)
	}
}

// TestStore_ASourceThePolicyNowRefusesIsKeptByAnEditThatLeavesIt is the same
// rule for the deployment's source policy: a git project stored with a source
// the policy now refuses (plain http) can still be renamed, while changing
// the URL, or turning a manual project into a git one over such a URL, is
// checked against the policy.
func TestStore_ASourceThePolicyNowRefusesIsKeptByAnEditThatLeavesIt(t *testing.T) {
	client := newStoreClient(t)
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	store := project.NewEntStore(client, project.SourcePolicy{})
	const plainHTTP = "http://git.example.invalid/team/repo.git"

	for _, scmType := range []project.SCMType{project.SCMGit, project.SCMManual} {
		created, err := store.Create(ctx, project.Project{
			Name: "legacy-" + string(scmType), SCMType: scmType,
			SCMURL: "https://git.example.invalid/team/repo.git", OrganizationID: org.ID,
		})
		if err != nil {
			t.Fatalf("Create = %v", err)
		}
		client.Project.UpdateOneID(created.ID).SetScmURL(plainHTTP).ExecX(ctx)
		stored, err := store.Get(ctx, created.ID)
		if err != nil {
			t.Fatalf("Get = %v", err)
		}

		renamed := stored
		renamed.Name = "renamed-" + string(scmType)
		if err := store.Update(ctx, renamed); err != nil {
			t.Errorf("%s: a rename leaving a stored plain-http URL = %v, want it saved", scmType, err)
		}
		if scmType == project.SCMManual {
			// Becoming git is the first time the URL is ever fetched, so the
			// policy is asked even though the URL did not change.
			renamed.SCMType = project.SCMGit
			if err := store.Update(ctx, renamed); !errors.Is(err, project.ErrSourceRefused) {
				t.Errorf("turning a manual project git over a refused URL = %v, want ErrSourceRefused", err)
			}
		}
		moved := renamed
		moved.SCMType = scmType
		moved.SCMURL = "http://other.example.invalid/team/repo.git"
		err = store.Update(ctx, moved)
		switch {
		case scmType == project.SCMGit && !errors.Is(err, project.ErrSourceRefused):
			t.Errorf("git: changing to another plain-http URL = %v, want ErrSourceRefused", err)
		case scmType == project.SCMManual && err != nil:
			// Nothing dials a manual project, so the policy does not apply.
			t.Errorf("manual: changing to another plain-http URL = %v, want it saved", err)
		}
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

	// burn10's shapes: a query or fragment written percent-encoded or with a
	// lookalike, which some reader along the way (a URL decoder, an NFKC
	// mapping) can turn into a real one. They used to be stored and shown raw.
	"an encoded query":                 "https://git.example.invalid/r.git%3Fsig=hunter2",
	"a lowercase encoded query":        "https://git.example.invalid/r.git%3fsig=hunter2",
	"an encoded fragment":              "https://git.example.invalid/r.git%23token=hunter2",
	"a password and an encoded query":  "https://svc:pw@git.example.invalid/r.git%3Fsig=hunter2",
	"a fullwidth question mark":        "https://git.example.invalid/r.git" + fullwidthQuestion + "sig=hunter2",
	"a small question mark":            "https://git.example.invalid/r.git" + smallQuestion + "sig=hunter2",
	"a fullwidth number sign":          "https://git.example.invalid/r.git" + fullwidthHash + "token=hunter2",
	"a small number sign":              "https://git.example.invalid/r.git" + smallHash + "token=hunter2",
	"an encoded fullwidth question":    "https://git.example.invalid/r.git%EF%BC%9Fsig=hunter2",
	"an encoded small question":        "https://git.example.invalid/r.git%ef%b9%96sig=hunter2",
	"an encoded fullwidth number sign": "https://git.example.invalid/r.git%EF%BC%83token=hunter2",
	"an encoded small number sign":     "https://git.example.invalid/r.git%ef%b9%9ftoken=hunter2",

	// The reviews' shapes, which the lists above missed and revealed reads:
	// four more NFKC sources of "?", double encodings, and a fullwidth or
	// small percent sign standing for "%".
	"a vertical question mark":           "https://git.example.invalid/r.git" + string(rune(0xFE16)) + "sig=hunter2",
	"a double question mark":             "https://git.example.invalid/r.git" + string(rune(0x2047)) + "sig=hunter2",
	"a question exclamation mark":        "https://git.example.invalid/r.git" + string(rune(0x2048)) + "sig=hunter2",
	"an exclamation question mark":       "https://git.example.invalid/r.git" + string(rune(0x2049)) + "sig=hunter2",
	"an encoded vertical question mark":  "https://git.example.invalid/r.git%ef%b8%96sig=hunter2",
	"a password and a vertical question": "https://svc:pw@git.example.invalid/r.git" + string(rune(0xFE16)) + "sig=hunter2",
	"a double-encoded query":             "https://git.example.invalid/r.git%253Fsig=hunter2",
	"a double-encoded fragment":          "https://git.example.invalid/r.git%2523token=hunter2",
	"a double-encoded at":                "https://hunter2%2540git.example.invalid/r",
	"a fullwidth percent before 3F":      "https://git.example.invalid/r.git" + string(rune(0xFF05)) + "3Fsig=hunter2",
	"a small percent before 23":          "https://git.example.invalid/r.git" + string(rune(0xFE6A)) + "23token=hunter2",
	"encoding nested past every round":   "https://git.example.invalid/r.git%" + strings.Repeat("25", 9) + "3Fsig=hunter2",
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

// TestStore_TurningAProjectGitHoldsItsStoredURLToEveryRule is the type change
// checkSourceChange treats as a change of URL: a manual project an older
// release stored with a password in its URL keeps saving as manual, but
// becoming git, the first time the URL would be dialed, is refused, so the
// password is never sent.
func TestStore_TurningAProjectGitHoldsItsStoredURLToEveryRule(t *testing.T) {
	client := newStoreClient(t)
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	store := project.NewEntStore(client, project.SourcePolicy{})

	for _, from := range []project.SCMType{project.SCMManual, project.SCMArchive} {
		created, err := store.Create(ctx, project.Project{
			Name: "legacy-" + string(from), SCMType: from,
			SCMURL: "https://git.example.invalid/team/repo.git", OrganizationID: org.ID,
		})
		if err != nil {
			t.Fatalf("Create = %v", err)
		}
		client.Project.UpdateOneID(created.ID).SetScmURL(secretSourceURL).ExecX(ctx)
		stored, err := store.Get(ctx, created.ID)
		if err != nil {
			t.Fatalf("Get = %v", err)
		}

		flipped := stored
		flipped.SCMType = project.SCMGit
		assertSecretRefusal(t, "Update turning a "+string(from)+" project git over a password URL", store.Update(ctx, flipped))
		if got, _ := store.Get(ctx, created.ID); got.SCMType != from || got.SCMURL != secretSourceURL {
			t.Errorf("a refused flip changed the project: %+v", got)
		}
	}
}

// TestStore_RefusesTheWithheldPlaceholderAsAURL covers a stale edit form: it
// shows a withheld URL as the placeholder, another edit then changes the URL,
// and the stale form's placeholder no longer echoes what is stored, so it
// reaches the store as a new URL. It used to be saved as a manual project's
// source.
func TestStore_RefusesTheWithheldPlaceholderAsAURL(t *testing.T) {
	client := newStoreClient(t)
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	store := project.NewEntStore(client, project.SourcePolicy{})
	placeholder := project.RedactedSourceURL("https://git.example.invalid/r.git?sig=hunter2")

	_, err := store.Create(ctx, project.Project{Name: "p", SCMType: project.SCMManual, SCMURL: placeholder, OrganizationID: org.ID})
	assertSecretRefusal(t, "Create with the placeholder", err)

	created, err := store.Create(ctx, project.Project{
		Name: "clean", SCMType: project.SCMManual, SCMURL: "https://git.example.invalid/r.git", OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("Create = %v", err)
	}
	stale := created
	stale.SCMURL = project.KeepEchoedSourceURL(created.SCMURL, placeholder)
	assertSecretRefusal(t, "Update with a stale placeholder", store.Update(ctx, stale))
}
