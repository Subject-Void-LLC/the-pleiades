// This file is the body PATCH /projects/{id} accepts, and how it is folded
// over the stored project.
//
// PATCH is a partial update, as the template and access PATCHes are
// (templates_patch.go, access_patch.go): a field the body leaves out keeps
// its stored value and a field it names is applied. An empty value clears
// description and scm_branch (and scm_url, which a git project may not do);
// an empty scm_type is git, as on create; and "credential": 0 unbinds the
// credential, as it binds none on create. The route used to decode its create
// body instead, so a field the body left out was written back empty: a
// PATCH that did not name the credential unbound it, and one that did not
// name scm_type turned a manual or archive project into git.
//
// The rules are the shared ones in patch.go. A null on any field is refused,
// naming it; an unknown key, a read-only one such as sync_status among them,
// and a body that is not an object are malformed. The organization is fixed:
// a different one is refused, naming it, where it used to be ignored with a
// 200, and the stored one is accepted as an echo.
//
// The handler reads the stored project, folds the body over it and holds the
// merged project to every rule a create is held to (validProject, then
// checkCredential, so a kept credential is checked again on every PATCH),
// before the unchanged store Update, which checks the source again. Sync
// state is never written here: the store's Update leaves it to RecordSync.
//
// Concurrent PATCHes are last-writer-wins, as they were: each reads the
// stored project, folds its fields over it and writes the result, with no
// version check, so of two at once the second's write covers the first's for
// every field.
package api

import (
	"net/http"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// projectNoun names a project in the shared PATCH refusals (patch.go).
const projectNoun = "a project"

// projectPatchDTO is the body PATCH /projects/{id} accepts: every field of
// the create body, each a pointer so an absent key keeps its stored value.
type projectPatchDTO struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`

	// Organization is fixed: moving a project between tenants would hand its
	// source and its credential to another tenant's templates. It is
	// accepted only as the stored value; see fixedFieldRefusal.
	Organization *int `json:"organization"`

	SCMType   *string `json:"scm_type"`
	SCMURL    *string `json:"scm_url"`
	SCMBranch *string `json:"scm_branch"`

	// Credential, when named, rebinds the project; 0 unbinds it.
	Credential *int `json:"credential"`

	// nulls names every field the body set to JSON null, sorted.
	// Unexported, so decoding never fills it.
	nulls []string
}

// UnmarshalJSON decodes the body strictly and records which fields were null.
func (d *projectPatchDTO) UnmarshalJSON(data []byte) error {
	// fields is the same struct without this method, so decoding into it
	// does not recurse back here.
	type fields projectPatchDTO
	decoded, nulls, err := decodePatch[fields](data, projectNoun)
	if err != nil {
		return err
	}
	*d = projectPatchDTO(decoded)
	d.nulls = nulls
	return nil
}

// fixedFieldRefusal is the 400 message for a body that asks to move the
// project to another organization, or "" when it does not.
func (d projectPatchDTO) fixedFieldRefusal(stored project.Project) string {
	if d.Organization != nil && *d.Organization != stored.OrganizationID {
		return fixedFieldMessage("organization", projectNoun, "create the project in the other organization instead")
	}
	return ""
}

// apply folds the fields the body named over stored, trimmed as a create
// body is, leaving every other field (the sync state among them) as it was.
func (d projectPatchDTO) apply(stored project.Project) project.Project {
	patchTrimmed(&stored.Name, d.Name)
	patchTrimmed(&stored.Description, d.Description)
	patchTrimmed(&stored.SCMBranch, d.SCMBranch)
	// GET shows scm_url redacted, so a body sending back what GET showed is
	// an echo and keeps the stored URL rather than saving the redacted form.
	if d.SCMURL != nil {
		stored.SCMURL = project.KeepEchoedSourceURL(stored.SCMURL, strings.TrimSpace(*d.SCMURL))
	}
	if d.SCMType != nil {
		stored.SCMType = project.SCMType(strings.TrimSpace(*d.SCMType))
	}
	patchField(&stored.CredentialID, d.Credential)
	return stored
}

// patchTrimmed is patchField for a text field the store keeps trimmed.
func patchTrimmed(dst *string, value *string) {
	if value != nil {
		*dst = strings.TrimSpace(*value)
	}
}

// Update serves PATCH /projects/{id}: a partial update of a project's own
// fields, never its sync state. See projectPatchDTO and this file's comment
// for the rules.
func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r)
	if !ok {
		return
	}
	var body projectPatchDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.nulls) > 0 {
		RespondError(w, r, http.StatusBadRequest, patchNullRefusal(projectNoun, body.nulls))
		return
	}

	// Read first: a missing project is a 404, and the stored project is what
	// the body is folded over.
	stored, err := h.projects.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", err)
		return
	}
	if refusal := body.fixedFieldRefusal(stored); refusal != "" {
		RespondError(w, r, http.StatusBadRequest, refusal)
		return
	}

	// The merged project is held to every rule a create is, so a PATCH
	// naming one field is refused for a kept value a full write would be
	// refused for: setting scm_type to git on a project with no URL, say.
	p := body.apply(stored)
	if !h.validProject(w, r, &p) || !h.checkCredential(w, r, p) {
		return
	}

	if err := h.projects.Update(r.Context(), p); err != nil {
		h.respondStoreError(w, r, "update", err)
		return
	}
	updated, err := h.projects.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", err)
		return
	}
	dto := toProjectDTO(updated)
	Respond(w, r, http.StatusOK, &dto)
}
