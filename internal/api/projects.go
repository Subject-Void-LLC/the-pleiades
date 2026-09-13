// This file serves the project surface: the source repositories a
// deployment runs automation out of.
//
// It holds a project.Store and a project.Syncer and nothing else. In
// particular it holds no credential resolver, so a handler here cannot read
// a decrypted secret even by accident, which is the same boundary the
// credential handlers are held to.
//
// A project's sync error is returned verbatim, and that is safe because
// internal/project scrubs credential material out of it before it is ever
// stored. The scrubbing belongs there rather than here: this is one of
// several readers, and a guarantee enforced at the point of writing cannot
// be forgotten by a reader added later.
package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// ProjectHandler serves projects.
type ProjectHandler struct {
	projects project.Store
	syncer   project.Syncer
	logger   *slog.Logger
}

// NewProjectHandler returns a handler over the given store and syncer.
func NewProjectHandler(store project.Store, syncer project.Syncer, logger *slog.Logger) *ProjectHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProjectHandler{projects: store, syncer: syncer, logger: logger}
}

// projectDTO is one project on the wire.
type projectDTO struct {
	LinkSet

	ID           int    `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Organization int    `json:"organization"`
	SCMType      string `json:"scm_type"`
	SCMURL       string `json:"scm_url,omitempty"`
	SCMBranch    string `json:"scm_branch,omitempty"`
	Credential   int    `json:"credential,omitempty"`
	Revision     string `json:"revision,omitempty"`
	SyncStatus   string `json:"sync_status"`

	// SyncError is already scrubbed of credential material by
	// internal/project before it is stored. See this file's own comment.
	SyncError    string     `json:"sync_error,omitempty"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
}

// projectListDTO is a page of projects.
type projectListDTO struct {
	LinkSet

	Projects []projectDTO `json:"projects"`
}

// projectWriteDTO is a create or update body.
type projectWriteDTO struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Organization int    `json:"organization"`
	SCMType      string `json:"scm_type"`
	SCMURL       string `json:"scm_url"`
	SCMBranch    string `json:"scm_branch"`
	Credential   int    `json:"credential"`
}

// toProjectDTO projects a record onto the wire shape.
func toProjectDTO(p project.Project) projectDTO {
	return projectDTO{
		ID:           p.ID,
		Name:         p.Name,
		Description:  p.Description,
		Organization: p.OrganizationID,
		SCMType:      string(p.SCMType),
		SCMURL:       p.SCMURL,
		SCMBranch:    p.SCMBranch,
		Credential:   p.CredentialID,
		Revision:     p.Revision,
		SyncStatus:   string(p.SyncStatus),
		SyncError:    p.SyncError,
		LastSyncedAt: p.LastSyncedAt,
	}
}

// List serves the projects.
func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	q := project.Query{}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			RespondError(w, r, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		q.Limit = limit
	}

	found, err := h.projects.List(r.Context(), q)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to list projects", slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	dto := projectListDTO{Projects: make([]projectDTO, 0, len(found))}
	for _, p := range found {
		dto.Projects = append(dto.Projects, toProjectDTO(p))
	}
	Respond(w, r, http.StatusOK, &dto)
}

// Get serves one project.
func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r)
	if !ok {
		return
	}
	p, err := h.projects.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", err)
		return
	}
	dto := toProjectDTO(p)
	Respond(w, r, http.StatusOK, &dto)
}

// Create registers a source repository.
func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body projectWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	p, ok := h.fromBody(w, r, body)
	if !ok {
		return
	}
	if p.OrganizationID < 1 {
		RespondError(w, r, http.StatusBadRequest, "organization is required")
		return
	}

	created, err := h.projects.Create(r.Context(), p)
	if err != nil {
		h.respondStoreError(w, r, "create", err)
		return
	}
	dto := toProjectDTO(created)
	Respond(w, r, http.StatusCreated, &dto)
}

// Update changes a project's own fields, never its sync state.
func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r)
	if !ok {
		return
	}
	var body projectWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	p, ok := h.fromBody(w, r, body)
	if !ok {
		return
	}
	p.ID = id

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

// Delete removes a project, leaving its working tree on disk.
func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r)
	if !ok {
		return
	}
	if err := h.projects.Delete(r.Context(), id); err != nil {
		h.respondStoreError(w, r, "delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Sync fetches the project's source and records where it got to.
//
// It answers 200 for a sync that ran and failed, because the request
// succeeded: the outcome is in sync_status and sync_error, which is the
// same shape a caller polling a project already reads. A 5xx here would say
// the controller broke, when what actually happened is that somebody's
// repository was unreachable.
func (h *ProjectHandler) Sync(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r)
	if !ok {
		return
	}
	p, err := h.projects.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", err)
		return
	}

	// Public repositories only: nothing resolves a credential into an Auth
	// yet. See internal/ui/resources/projects/sync.go, which carries the
	// same limitation and the reasoning for where that resolution belongs.
	result, err := h.syncer.Sync(r.Context(), p, project.Auth{})
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.projects.RecordSync(r.Context(), id, result); err != nil {
		h.respondStoreError(w, r, "record a sync for", err)
		return
	}

	synced, err := h.projects.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", err)
		return
	}
	dto := toProjectDTO(synced)
	Respond(w, r, http.StatusOK, &dto)
}

// fromBody validates a write body into a domain project.
func (h *ProjectHandler) fromBody(w http.ResponseWriter, r *http.Request, body projectWriteDTO) (project.Project, bool) {
	p := project.Project{
		Name:           strings.TrimSpace(body.Name),
		Description:    strings.TrimSpace(body.Description),
		OrganizationID: body.Organization,
		SCMType:        project.SCMType(strings.TrimSpace(body.SCMType)),
		SCMURL:         strings.TrimSpace(body.SCMURL),
		SCMBranch:      strings.TrimSpace(body.SCMBranch),
		CredentialID:   body.Credential,
	}
	if p.Name == "" {
		RespondError(w, r, http.StatusBadRequest, "name is required")
		return project.Project{}, false
	}
	if p.SCMType == "" {
		p.SCMType = project.SCMGit
	}
	switch p.SCMType {
	case project.SCMGit, project.SCMArchive, project.SCMManual:
	default:
		RespondError(w, r, http.StatusBadRequest, "scm_type must be one of git, archive or manual")
		return project.Project{}, false
	}
	// Refused here rather than at sync time, for the reason the UI's own
	// binder gives: a git project with no URL can never do the one thing it
	// exists for, and finding that out by running a sync is a worse way to
	// be told.
	if p.SCMType == project.SCMGit && p.SCMURL == "" {
		RespondError(w, r, http.StatusBadRequest, "a git project needs scm_url")
		return project.Project{}, false
	}
	return p, true
}

// parseID reads the path parameter.
func (h *ProjectHandler) parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 1 {
		RespondError(w, r, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

// respondStoreError maps a store failure onto a status.
func (h *ProjectHandler) respondStoreError(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case strings.Contains(err.Error(), project.ErrNotFound.Error()):
		RespondError(w, r, http.StatusNotFound, "no project with that id")
	case strings.Contains(err.Error(), project.ErrExists.Error()):
		RespondError(w, r, http.StatusConflict, project.ErrExists.Error())
	default:
		h.logger.ErrorContext(r.Context(), "failed to "+what+" project", slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
	}
}
