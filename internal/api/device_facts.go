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
	// Held to the shape a stored key has, never to what a new fact may be
	// named (facts.ValidKey), so a key stored before that rule grew is
	// still read back (LESSONS 415).
	if err != nil || !facts.ReadableKey(key) {
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
	dto := factsDTO{Device: item.Name(), Key: shownKey(key, entries), Facts: entries}
	Respond(w, r, http.StatusOK, &dto)
}

// shownKey is the key a history answer names: the caller's key when the
// store holds it (its values carry it as stored) or when it is one a new
// fact may be named (facts.ValidKey), which shows as itself; otherwise none.
// The reader's rule is frozen and reads any key of valid UTF-8 with no
// control character, U+0020 or slash, so a caller can ask for one holding a
// direction override, an invisible character or a line separator, and naming
// back a key nothing holds would repeat the caller's text where it reads as
// something else (FAILURE_PATTERNS.md #72). No ValidKey under any Go release
// accepts a format character or a line separator, so a stored key holding
// one comes only from a hand-edited store; the keys a writer of another
// toolchain legitimately stores that this one refuses hold code points this
// one has not assigned. An empty key is the current facts' answer, which
// names none.
func shownKey(key string, entries []facts.Entry) string {
	if key == "" || len(entries) > 0 || facts.ValidKey(key) {
		return key
	}
	return ""
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
