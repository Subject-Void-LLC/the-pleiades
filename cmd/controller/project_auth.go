// This file is the bridge that turns a project's credential into the values
// one clone needs, and the point where that credential is authorized for use.
package main

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// errProjectCredentialUnreadable is the sync error for a credential that
// could not be read. It is a fixed phrase: the resolver's own error can name
// the credential and its inputs, and this string is on its way to a stored
// sync_error that an operator reads.
var errProjectCredentialUnreadable = errors.New("the project's credential could not be read")

// errProjectCredentialOfAnotherOrganization is the sync error for a project
// bound to a credential its organization does not own. It names neither the
// credential nor its organization, for the same reason.
var errProjectCredentialOfAnotherOrganization = errors.New(
	"the project's credential belongs to another organization, so this sync does not use it; " +
		"bind a credential of the project's own organization")

// credentialOwners is the slice of credstore.Store projectAuth reads: the
// redacted projection, which says which organization owns a credential and
// holds no secret.
type credentialOwners interface {
	GetCredential(ctx context.Context, id int) (credstore.Credential, error)
}

// projectAuth resolves a project's credential into the values one clone
// needs, after checking that the project may use it.
//
// A credential belongs to exactly one organization (the schema requires the
// edge) and authenticates only that organization's projects, as an AWX
// credential is owned by an organization and used by its projects. The write
// paths refuse binding another organization's (the API's checkCredential and
// the UI's usableForGit), and this refuses it again at the moment of use, so
// a binding stored before those checks existed is never acted on: the sync
// fails with errProjectCredentialOfAnotherOrganization and the project stays
// readable, since reading it is not using the credential. Without this a
// project of one organization bound to another's source control credential
// would send that credential to whatever host the project names.
//
// It keeps a project and its credential in one organization; it does not keep
// a caller to theirs. Project writes are authorized by token scope across the
// deployment (role bindings are not enforced, FAILURE_PATTERNS 98), so a
// caller holding project:write can create a project in another organization
// with that organization's credential, and this then resolves it. That waits
// on per-organization authorization.
//
// It maps AWX's Source Control input names, which is the vocabulary
// internal/credtype/managed/types/scm.json declares and an AWX export
// already uses. A credential of some other type is not rejected here: an ssh
// type carries the same ssh_key_data and username, and refusing it would be
// this bridge inventing a type rule the credential system does not have.
type projectAuth struct {
	// resolver reads a credential's real values.
	resolver resolve.Resolver
	// owners says which organization owns a credential. Nil refuses every
	// credential, since without it the owner cannot be checked.
	owners credentialOwners
}

// ResolveAuth reads the real values of p's credential, refusing one p's
// organization does not own.
func (a projectAuth) ResolveAuth(ctx context.Context, p project.Project) (project.Auth, error) {
	if p.CredentialID == 0 {
		return project.Auth{}, nil
	}
	if a.owners == nil {
		return project.Auth{}, errProjectCredentialUnreadable
	}
	owned, err := a.owners.GetCredential(ctx, p.CredentialID)
	if err != nil {
		return project.Auth{}, errProjectCredentialUnreadable
	}
	// Checked before the resolver runs, so another organization's secret
	// is never decrypted on this project's behalf.
	if owned.OrganizationID != p.OrganizationID {
		return project.Auth{}, errProjectCredentialOfAnotherOrganization
	}

	found, err := a.resolver.Resolve(ctx, []int{p.CredentialID})
	if err != nil || len(found) != 1 {
		return project.Auth{}, errProjectCredentialUnreadable
	}

	in := found[0].Inputs
	auth := project.Auth{
		Username:   in[project.InputUsername],
		Password:   in[project.InputPassword],
		Passphrase: in[project.InputPassphrase],
	}
	if key := in[project.InputPrivateKey]; key != "" {
		auth.PrivateKey = []byte(key)
	}
	return auth, nil
}
