// The onboarding route: probe a generic device from the Controller and
// record what it proved.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
)

// onboardTimeout bounds one onboarding, which connects to a device from
// the Controller.
const onboardTimeout = 60 * time.Second

// Onboarder onboards the named device, recording the certificate a TLS
// device presents on first use when firstUse is set (the route's
// tls_first_use=true, onboard.WithFirstUsePins). cmd/controller binds it to
// the device repository and the credential store (onboard.Onboard); a
// handler without one answers 501.
type Onboarder func(ctx context.Context, name string, firstUse bool) (onboard.Result, error)

// firstUseParam reads the onboard route's tls_first_use query parameter:
// absent, true or false, given once. Anything else is refused rather than
// read as false, since a caller who wrote it meant something.
func firstUseParam(r *http.Request) (bool, bool) {
	values, present := r.URL.Query()["tls_first_use"]
	switch {
	case !present:
		return false, true
	case len(values) != 1:
		return false, false
	case values[0] == "true":
		return true, true
	case values[0] == "false":
		return false, true
	}
	return false, false
}

// WithOnboarder enables the onboarding route.
func (h *DeviceHandler) WithOnboarder(o Onboarder) *DeviceHandler {
	h.onboarder = o
	return h
}

// onboardDTO is onboard.Result on the wire.
type onboardDTO struct {
	LinkSet
	onboard.Result
}

// Onboard probes a generic device over its protocol and records what it
// proved. The caller and the outcome are logged, and the device's own
// history carries the discovery and every state change as revisions, so
// an onboarding is audited twice over: who asked, and what it changed.
func (h *DeviceHandler) Onboard(w http.ResponseWriter, r *http.Request) {
	name, ok := h.deviceName(w, r)
	if !ok {
		return
	}
	if h.onboarder == nil {
		RespondError(w, r, http.StatusNotImplemented, "device onboarding is not enabled")
		return
	}
	firstUse, ok := firstUseParam(r)
	if !ok {
		RespondError(w, r, http.StatusBadRequest, "tls_first_use must be given once, as true or false")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), onboardTimeout)
	defer cancel()
	res, err := h.onboarder(ctx, name, firstUse)

	actor := ""
	if id, ok := IdentityFromContext(r.Context()); ok && id != nil {
		actor = id.Subject
	}
	recorded := ""
	if res.FirstUse != nil {
		recorded = res.FirstUse.Fingerprint
	}
	h.logger.InfoContext(r.Context(), "device onboarding",
		slog.String("device", name),
		slog.String("actor", actor),
		slog.String("type", res.Type),
		slog.String("from", res.PreviousState),
		slog.String("to", res.State),
		slog.Bool("changed", res.Changed),
		slog.Bool("tls_first_use", firstUse),
		slog.String("tls_first_use_pin", recorded),
		slog.Bool("succeeded", err == nil))

	switch {
	case err == nil:
		Respond(w, r, http.StatusOK, &onboardDTO{Result: res})
	case errors.Is(err, onboard.ErrProbe):
		Respond(w, r, http.StatusBadGateway, &onboardDTO{Result: res})
	case errors.Is(err, onboard.ErrNotOnboarded):
		RespondError(w, r, http.StatusUnprocessableEntity, "only a device type with a prober is onboarded: "+strings.Join(onboard.Types(), ", "))
	case errors.Is(err, onboard.ErrAdministratorState):
		RespondError(w, r, http.StatusConflict, "the device is in a state an administrator set")
	default:
		// Not found, a concurrent edit, a read-only inventory, or a broken
		// store: the repository's own mapping, which never echoes a
		// driver's message.
		h.writeRepositoryError(w, r, "onboard", name, err)
	}
}
