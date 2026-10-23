// The grant routes: an operator's assertion that a device holds a
// capability an external program defines, and taking one back.
package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// grantWriter is the one write path a grant has on a stored device
// (record.Base.RecordGrant and RevokeGrant).
type grantWriter interface {
	RecordGrant(pkginventory.Grant) error
	RevokeGrant(capability.Name) (bool, error)
}

// grantRequest is the body of POST /inventory/devices/{name}/capabilities.
type grantRequest struct {
	Capability string         `json:"capability"`
	Properties map[string]any `json:"properties"`
	Reason     string         `json:"reason"`
}

// grantDTO is one grant on the wire.
type grantDTO struct {
	Capability string         `json:"capability"`
	Source     string         `json:"source"`
	Program    string         `json:"program,omitempty"`
	Properties map[string]any `json:"properties"`
	By         string         `json:"by,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	At         time.Time      `json:"at"`
}

// grantsDTO is a device's grants after a change.
type grantsDTO struct {
	LinkSet
	Device string     `json:"device"`
	Grants []grantDTO `json:"grants"`
}

// GrantCapability records an operator's assertion that a device holds a
// capability an external program defines. The Controller loads no
// program, so it holds the grant to its shape here; a Runner holds it to
// the capability's definition before a method requiring it runs.
func (h *DeviceHandler) GrantCapability(w http.ResponseWriter, r *http.Request) {
	name, ok := h.deviceName(w, r)
	if !ok {
		return
	}
	var req grantRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	capName := capability.Name(req.Capability)
	switch {
	case !capability.IsDataName(capName):
		if _, compiled := capability.Lookup(capName); compiled {
			RespondError(w, r, http.StatusUnprocessableEntity, "a compiled capability is granted only by onboarding")
			return
		}
		RespondError(w, r, http.StatusUnprocessableEntity, "capability must be the name of one an external program defines, <namespace>.<name>")
		return
	}
	if err := pkginventory.ValidateGrantReason(req.Reason); err != nil {
		RespondError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if req.Properties == nil {
		req.Properties = map[string]any{}
	}
	if err := capability.ValidateGrantShape(req.Properties); err != nil {
		RespondError(w, r, http.StatusUnprocessableEntity, "malformed grant properties")
		return
	}
	// The Controller holds no definition to refuse a secret-named property
	// by, as the loader does, and a grant rides every dispatch to the
	// device, so a credential put here would travel the message stream
	// (the branch review's F4).
	for k := range req.Properties {
		if redact.SecretName(k) {
			RespondError(w, r, http.StatusUnprocessableEntity, "a grant's property never holds a secret; the device's credential reaches a method on its own")
			return
		}
	}

	item, err := h.repo.GetByName(r.Context(), name)
	if err != nil {
		h.writeRepositoryError(w, r, "read", name, err)
		return
	}
	g, ok := item.(grantWriter)
	if !ok {
		RespondError(w, r, http.StatusUnprocessableEntity, "this device cannot hold a granted capability")
		return
	}
	actor := callerSubject(r)
	if err := g.RecordGrant(pkginventory.Grant{
		Capability: capName,
		Source:     pkginventory.GrantAsserted,
		Properties: req.Properties,
		By:         actor,
		Reason:     req.Reason,
		At:         time.Now().UTC(),
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "recording a grant failed", slog.String("device", name), slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.repo.Save(r.Context(), item); err != nil {
		h.writeRepositoryError(w, r, "grant", name, err)
		return
	}
	h.logger.InfoContext(r.Context(), "device capability granted",
		slog.String("device", name), slog.String("capability", string(capName)), slog.String("actor", actor))
	Respond(w, r, http.StatusOK, toGrantsDTO(name, item))
}

// RevokeCapability removes a device's grant of a capability, however it
// was made.
func (h *DeviceHandler) RevokeCapability(w http.ResponseWriter, r *http.Request) {
	name, ok := h.deviceName(w, r)
	if !ok {
		return
	}
	capName := capability.Name(chi.URLParam(r, "capability"))
	if !capability.IsDataName(capName) {
		RespondError(w, r, http.StatusBadRequest, "invalid capability")
		return
	}
	item, err := h.repo.GetByName(r.Context(), name)
	if err != nil {
		h.writeRepositoryError(w, r, "read", name, err)
		return
	}
	g, ok := item.(grantWriter)
	if !ok {
		RespondError(w, r, http.StatusNotFound, "the device has no such grant")
		return
	}
	removed, err := g.RevokeGrant(capName)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoking a grant failed", slog.String("device", name), slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}
	if !removed {
		RespondError(w, r, http.StatusNotFound, "the device has no such grant")
		return
	}
	if err := h.repo.Save(r.Context(), item); err != nil {
		h.writeRepositoryError(w, r, "revoke", name, err)
		return
	}
	h.logger.InfoContext(r.Context(), "device capability revoked",
		slog.String("device", name), slog.String("capability", string(capName)), slog.String("actor", callerSubject(r)))
	Respond(w, r, http.StatusOK, toGrantsDTO(name, item))
}

// callerSubject is the authenticated caller's subject, or the empty string.
func callerSubject(r *http.Request) string {
	if id, ok := IdentityFromContext(r.Context()); ok && id != nil {
		return id.Subject
	}
	return ""
}

// toGrantsDTO is item's grants on the wire. A record whose grants cannot
// be read answers with none, since the write that preceded it succeeded.
func toGrantsDTO(name string, item pkginventory.InventoryItem) *grantsDTO {
	out := &grantsDTO{Device: name, Grants: []grantDTO{}}
	grants, _, err := pkginventory.GrantsFrom(item.Properties())
	if err != nil {
		return out
	}
	for _, g := range grants {
		out.Grants = append(out.Grants, grantDTO{
			Capability: string(g.Capability), Source: string(g.Source), Program: g.Program,
			Properties: g.Properties, By: g.By, Reason: strings.TrimSpace(g.Reason), At: g.At,
		})
	}
	return out
}
