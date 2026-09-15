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

// credentialOptions offers the credentials a project can actually
// authenticate as.
//
// Only Source Control credentials that carry a password, token or SSH key.
//
// This has been narrowed twice. It first offered every credential, then
// every credential CARRYING usable material, and neither was right: the
// second still passed any custom type that happened to declare an input
// called "password", whatever that type was for. A project sync must not be
// reachable with a credential issued for something else.
//
// project.AuthenticatesGit is the predicate, shared with the check that
// refuses such a credential on submission, so the chooser and the validator
// cannot disagree about what is usable.
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
			if !project.AuthenticatesGit(c.Kind, c.Inputs, c.External) {
				continue
			}
			label := c.Name
			if c.TypeName != "" {
				label += " (" + c.TypeName + ")"
			}
			out = append(out, view.Option{Label: label, Value: strconv.Itoa(c.ID)})
		}
		return out, nil
	}
}

// usableForGit refuses a credential that cannot authenticate a clone.
//
// The chooser already hides these, so reaching this needs a submission that
// did not come from the form. That is not a reason to skip it: the id is an
// ordinary form value, the API accepts the same field, and a rule enforced
// only by what a page happens to render is not enforced.
//
// The message names the two things that would fix it rather than only
// stating the refusal, because "this credential cannot authenticate a git
// clone" leaves somebody looking at a credential that seems fine to them.
func usableForGit(ctx context.Context, creds credentialLister, id int) error {
	if id == 0 {
		return nil
	}
	found, err := creds.ListAllCredentials(ctx)
	if err != nil {
		return err
	}
	for _, c := range found {
		if c.ID != id {
			continue
		}
		if !project.AuthenticatesGit(c.Kind, c.Inputs, c.External) {
			return view.FieldFault{
				Field: "credential",
				Message: "That is not a Source Control credential carrying a password, token or SSH key, " +
					"so it cannot authenticate a clone. Choose one that is, or leave this unset for a public repository.",
			}
		}
		return nil
	}
	return view.FieldFault{Field: "credential", Message: "That credential no longer exists."}
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
