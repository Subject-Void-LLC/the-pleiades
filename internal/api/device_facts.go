// Package api: a device's facts (Phase 41), read through the store the
// Controller keeps them in. The facts are what real runs observed, stored as
// they changed; this only reads them.
package api

import (
	"context"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// FactReader is what the device resource reads facts through.
type FactReader interface {
	Current(ctx context.Context, device string) ([]facts.Entry, error)
	History(ctx context.Context, device, key string) ([]facts.Entry, error)
	Verify(ctx context.Context, device string) (facts.Verdict, error)
}

// WithFacts serves a device's facts from reader; without it the facts routes
// answer 501.
func (h *DeviceHandler) WithFacts(reader FactReader) {
	h.facts = reader
}

// factsDTO is a device's facts on the wire.
type factsDTO struct {
	Device string        `json:"device"`
	Key    string        `json:"key,omitempty"`
	Facts  []facts.Entry `json:"facts"`
	LinkSet
}

// Facts serves GET /inventory/devices/{name}/facts.
func (h *DeviceHandler) Facts(w http.ResponseWriter, r *http.Request) {
	h.serveFacts(w, r, "")
}

// FactHistory serves GET /inventory/devices/{name}/facts/{key}.
func (h *DeviceHandler) FactHistory(w http.ResponseWriter, r *http.Request) {
	key, err := PathParam(r, "key")
	if err != nil || !facts.ValidKey(key) {
		// Not echoed: it is the caller's text (FAILURE_PATTERNS.md #72).
		RespondError(w, r, http.StatusBadRequest, "invalid fact name")
		return
	}
	h.serveFacts(w, r, key)
}

// serveFacts answers with the device's current facts, or key's history.
func (h *DeviceHandler) serveFacts(w http.ResponseWriter, r *http.Request, key string) {
	if h.facts == nil {
		RespondError(w, r, http.StatusNotImplemented, "facts are not served by this controller")
		return
	}
	name, ok := h.deviceName(w, r)
	if !ok {
		return
	}
	item, err := h.repo.GetByName(r.Context(), name)
	if err != nil {
		h.writeRepositoryError(w, r, "read", name, err)
		return
	}
	var entries []facts.Entry
	if key == "" {
		entries, err = h.facts.Current(r.Context(), string(item.ID()))
	} else {
		entries, err = h.facts.History(r.Context(), string(item.ID()), key)
	}
	if err != nil {
		h.logger.Error("reading a device's facts failed", "device", name, "error", err)
		RespondError(w, r, http.StatusInternalServerError, "reading the device's facts failed")
		return
	}
	if entries == nil {
		entries = []facts.Entry{}
	}
	dto := factsDTO{Device: item.Name(), Key: key, Facts: entries}
	Respond(w, r, http.StatusOK, &dto)
}

// factChainDTO is what checking a device's fact chain found, on the wire.
type factChainDTO struct {
	Device string `json:"device"`
	facts.Verdict
	LinkSet
}

// FactChain serves GET /inventory/devices/{name}/fact-chain.
func (h *DeviceHandler) FactChain(w http.ResponseWriter, r *http.Request) {
	if h.facts == nil {
		RespondError(w, r, http.StatusNotImplemented, "facts are not served by this controller")
		return
	}
	name, ok := h.deviceName(w, r)
	if !ok {
		return
	}
	item, err := h.repo.GetByName(r.Context(), name)
	if err != nil {
		h.writeRepositoryError(w, r, "read", name, err)
		return
	}
	verdict, err := h.facts.Verify(r.Context(), string(item.ID()))
	if err != nil {
		h.logger.Error("checking a device's fact chain failed", "device", name, "error", err)
		RespondError(w, r, http.StatusInternalServerError, "checking the device's fact chain failed")
		return
	}
	dto := factChainDTO{Device: item.Name(), Verdict: verdict}
	Respond(w, r, http.StatusOK, &dto)
}
