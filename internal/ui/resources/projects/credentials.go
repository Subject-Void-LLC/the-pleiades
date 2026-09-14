// Package projects' credential chooser: how a private repository is
// authenticated.
//
// The chooser offers credentials by name and stores an id. No value is read
// here and none could be: this package holds credstore's redacted
// projection, which has no field a plaintext secret could occupy, and
// internal/archtest fails the build if the UI side ever reaches the package
// that decrypts. The resolution happens inside the syncer, from the id this
// form saved.
package projects

import (
	"context"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// credentialLister is the slice of credstore.Store this view needs.
type credentialLister interface {
	ListAllCredentials(ctx context.Context) ([]credstore.Credential, error)
}

// credentialOptions offers the credentials a project can authenticate as.
//
// Every credential rather than only the scm ones. A Source Control type is
// the natural fit and is what an AWX export carries, but an ssh type
// declares the same ssh_key_data and username, and a deployment may well
// have written its own. Filtering by kind here would be this chooser
// inventing a rule the credential system does not have, and the failure
// mode is somebody unable to select the credential they already made.
func credentialOptions(creds credentialLister) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		// The empty option first, because a public repository is the
		// common case and "none" has to be expressible: without it the
		// control would have no way to say what most projects need.
		out := []view.Option{{Label: "None (public repository)", Value: ""}}
		found, err := creds.ListAllCredentials(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range found {
			label := c.Name
			if c.TypeName != "" {
				label += " (" + c.TypeName + ")"
			}
			out = append(out, view.Option{Label: label, Value: strconv.Itoa(c.ID)})
		}
		return out, nil
	}
}

// credentialCell renders the credential column, which says whether a clone
// authenticates rather than as what.
func credentialCell(p project.Project) string {
	if p.CredentialID == 0 {
		return "none"
	}
	return strconv.Itoa(p.CredentialID)
}

// credentialValue prefills the chooser, empty for a public repository.
func credentialValue(p project.Project) string {
	if p.CredentialID == 0 {
		return ""
	}
	return strconv.Itoa(p.CredentialID)
}

// optionalID reads a chooser value that may legitimately be empty.
func optionalID(raw string) int {
	id, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || id < 1 {
		return 0
	}
	return id
}
