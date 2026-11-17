// The pin confirmation route: a person's confirmation that the pin
// onboarding recorded on first use is the device's own certificate.
package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// pinConfirmer is the one writer of a confirmation a stored device has
// (record.Base.ConfirmFirstUsePin).
type pinConfirmer interface {
	ConfirmFirstUsePin(pin string) error
}

// tlsPinRequest is the body of POST
// /inventory/devices/{name}/tls-pin/confirm.
type tlsPinRequest struct {
	// Fingerprint is the pin the person checked with the device's owner.
	Fingerprint string `json:"fingerprint"`
}

// tlsPinDTO is a device's pins after a confirmation.
type tlsPinDTO struct {
	LinkSet
	Device string   `json:"device"`
	Pins   []string `json:"pins"`
}

// ConfirmTLSPin confirms the named device's pin recorded on first use. The
// body names the pin the person checked, which must be the one recorded and
// the one the device trusts now: a first-use pin the record's own trust
// overrides is refused rather than added beside a person's. Who confirmed it
// is logged, and the device's history carries the move as revisions.
func (h *DeviceHandler) ConfirmTLSPin(w http.ResponseWriter, r *http.Request) {
	name, ok := h.deviceName(w, r)
	if !ok {
		return
	}
	var req tlsPinRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	pin, err := devicetls.NormalizePin(req.Fingerprint)
	if err != nil {
		RespondError(w, r, http.StatusUnprocessableEntity, "fingerprint must be the pin onboarding recorded, sha256:<64 hex>")
		return
	}
	item, err := h.repo.GetByName(r.Context(), name)
	if err != nil {
		h.writeRepositoryError(w, r, "read", name, err)
		return
	}
	settings := devicetls.For(item)
	recorded := settings.FirstUsePin()
	switch {
	case recorded == "" && settings.UnusedFirstUsePin() != "":
		RespondError(w, r, http.StatusConflict, "the device's own tls_fingerprint or tls_ca_pem overrides its pin recorded on first use, which onboarding it again removes")
		return
	case recorded == "":
		RespondError(w, r, http.StatusNotFound, "the device holds no pin recorded on first use")
		return
	case pin != recorded:
		// The recorded pin is no secret, and naming it lets the person
		// compare it with what they checked.
		RespondError(w, r, http.StatusUnprocessableEntity, "fingerprint is not the pin recorded on first use, "+recorded+": compare the recorded one with the device's owner before confirming it")
		return
	}
	// Every device the factory builds can confirm (record.Base), and the
	// checks above leave the move nothing to refuse, so either failure is
	// the store's own and is answered as one.
	err = errors.New("the stored device cannot confirm a pin")
	if c, ok := item.(pinConfirmer); ok {
		err = c.ConfirmFirstUsePin(recorded)
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "confirming a pin failed", slog.String("device", name), slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.repo.Save(r.Context(), item); err != nil {
		h.writeRepositoryError(w, r, "confirm", name, err)
		return
	}
	h.logger.InfoContext(r.Context(), "device TLS pin confirmed",
		slog.String("device", name), slog.String("pin", recorded), slog.String("actor", callerSubject(r)))
	Respond(w, r, http.StatusOK, &tlsPinDTO{Device: name, Pins: []string{recorded}})
}
