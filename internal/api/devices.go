// This file exposes inventory devices over the API.
//
// It exists because Phase 13's Release Gate ("a read-only API user
// receives a device JSON object, but the _links array actively omits the
// delete URL") had no handler behind it: before this, the entire versioned
// API was two job routes, and no phase in the roadmap owned exposing a
// device. Satisfying that gate against a route registered only inside a
// test is precisely what IMPLEMENTATION.md's checkbox discipline calls not
// passing, so the routes are real, mounted by the real composition root,
// and backed by the same inventory.Repository port the CLI uses.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/go-chi/chi/v5"
)

// maxDeviceNameLength bounds the {name} URL parameter. It is the DNS name
// limit, which is the longest thing a device name plausibly is.
//
// It is deliberately a bound and not a character allowlist. Phase 39's own
// SQL audit round-tripped a device literally named "'; DROP TABLE devices;
// --" through the real repository, so odd characters in device names are a
// real, supported condition rather than an attack signature, and an
// allowlist here would break existing inventory to defend against nothing.
// The name is safe in a response because it is never echoed back in an
// error, and safe in an href because hrefFor escapes it (links.go).
const maxDeviceNameLength = 253

// DeviceRepository is the narrow slice of inventory.Repository the device
// handlers need. Depending on the two methods actually called, rather than
// the whole port, is the same Interface Segregation shape TokenValidator
// and Admitter already use in this package, and it is what lets a test
// supply a small double instead of a full repository.
type DeviceRepository interface {
	GetByName(ctx context.Context, name string) (pkginventory.InventoryItem, error)
	Retire(ctx context.Context, name string) error
}

// DeviceHandler serves the inventory device resource.
type DeviceHandler struct {
	repo   DeviceRepository
	logger *slog.Logger
}

// NewDeviceHandler builds the device resource's handlers over repo.
// A nil logger falls back to the process default.
func NewDeviceHandler(repo DeviceRepository, logger *slog.Logger) *DeviceHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &DeviceHandler{repo: repo, logger: logger}
}

// deviceDTO is the wire projection of an InventoryItem.
//
// It deliberately omits the property bag, and that omission is a hardening
// decision rather than an oversight. cmd/controller installs
// crypto.DeviceEnvelopePropertiesInterceptor, so Properties() returns
// decrypted values on every read; emitting them would ship whatever an
// operator stored there, including enable secrets and API keys, to any
// caller holding inventory:read. PLAN.md Section 25 assigns the secret
// masking ruleset to Phase 22 and none exists today, so the honest choice
// is to ship identity, lifecycle, and classification now and let Phase 22
// decide how a property is safely rendered.
type deviceDTO struct {
	LinkSet

	ID           pkginventory.DeviceID `json:"id"`
	Name         string                `json:"name"`
	State        string                `json:"state"`
	Version      uint64                `json:"version"`
	Tags         []pkginventory.Tag    `json:"tags"`
	Capabilities []string              `json:"capabilities"`
	Executable   bool                  `json:"executable"`
}

// AllowsRel implements LinkFilter: it suppresses affordances this
// device's own lifecycle state makes inapplicable, regardless of what the
// caller is authorized for.
//
// A device that is already archived offers no delete relation. Retirement
// is idempotent at the repository, so following such a link would succeed
// and change nothing, which is worse than it failing: a client would show
// an action, the user would take it, and nothing would happen with no
// error to explain why. The affordance is withdrawn instead, which is what
// makes the _links array describe available next steps rather than a
// static permission table rendered server-side.
func (d deviceDTO) AllowsRel(rel auth.LinkRel) bool {
	if rel == auth.RelDelete {
		return d.State != pkginventory.StateArchived.String()
	}
	return true
}

// toDeviceDTO projects a hydrated item onto the wire shape.
//
// Every slice is initialized rather than left nil, so the JSON carries []
// instead of null for a device with no tags. A client distinguishing
// "none" from "unknown" should not have to guess which one null meant.
func toDeviceDTO(item pkginventory.InventoryItem) deviceDTO {
	tags := item.Tags()
	if tags == nil {
		tags = []pkginventory.Tag{}
	}

	caps := make([]string, 0, len(item.Capabilities()))
	for _, c := range item.Capabilities() {
		caps = append(caps, string(c))
	}

	return deviceDTO{
		ID:           item.ID(),
		Name:         item.Name(),
		State:        item.State().String(),
		Version:      item.Version(),
		Tags:         tags,
		Capabilities: caps,
		// Surfaced explicitly rather than left for a client to re-derive
		// from State: LifecycleState.CanExecute() is the platform's own
		// rule about which states accept work, and re-implementing it in
		// every client is exactly the hardcoded permission table
		// hypermedia exists to remove.
		Executable: item.State().CanExecute(),
	}
}

// Get serves a single device by name.
func (h *DeviceHandler) Get(w http.ResponseWriter, r *http.Request) {
	name, ok := h.deviceName(w, r)
	if !ok {
		return
	}

	item, err := h.repo.GetByName(r.Context(), name)
	if err != nil {
		h.writeRepositoryError(w, r, "read", name, err)
		return
	}

	dto := toDeviceDTO(item)
	Respond(w, r, http.StatusOK, &dto)
}

// Delete retires a device, transitioning it to the archived lifecycle
// state rather than removing its row.
//
// The verb is DELETE and the effect is retirement, which is a deliberate,
// documented choice rather than a euphemism: every Revision is immutable
// by schema and the revisions edge carries no cascade, so removing a
// device would mean destroying the audit trail the schema exists to
// protect. See inventory.Repository.Retire for the full argument.
//
// Afterwards the resource still answers GET, reporting state "archived"
// and no longer offering the delete affordance in its _links. That is not
// a loose end; it is the clearest demonstration this API has that
// affordances track application state rather than only caller identity,
// which is what the "engine of application state" in HATEOAS names.
func (h *DeviceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name, ok := h.deviceName(w, r)
	if !ok {
		return
	}

	if err := h.repo.Retire(r.Context(), name); err != nil {
		h.writeRepositoryError(w, r, "retire", name, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// deviceName extracts and bounds the {name} URL parameter, answering the
// caller itself and reporting false when the value is unusable.
func (h *DeviceHandler) deviceName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := chi.URLParam(r, "name")
	if name == "" || len(name) > maxDeviceNameLength || strings.ContainsAny(name, "\x00\r\n") {
		// The rejected value is never echoed back. It is caller-controlled
		// and reflecting it is the shape FAILURE_PATTERNS.md #72 records.
		RespondError(w, r, http.StatusBadRequest, "invalid device name")
		return "", false
	}
	return name, true
}

// writeRepositoryError maps a repository failure onto a status code,
// logging the real error and telling the client only the category.
//
// The sentinels are what make this possible, and the Retire conformance
// suite asserts both adapters return them: without ErrItemNotFound, a
// missing device would be indistinguishable from a broken database and
// both would have to be 500.
func (h *DeviceHandler) writeRepositoryError(w http.ResponseWriter, r *http.Request, op, name string, err error) {
	switch {
	case errors.Is(err, inventory.ErrItemNotFound):
		RespondError(w, r, http.StatusNotFound, "device not found")
	case errors.Is(err, inventory.ErrInventoryReadOnly):
		// Not 403. RequireScope owns authorization failures, and
		// conflating the two would make a deliberately non-mutating run
		// look like a permissions problem to whoever is debugging it.
		RespondError(w, r, http.StatusConflict, "inventory is read-only")
	case errors.Is(err, inventory.ErrVersionConflict):
		RespondError(w, r, http.StatusConflict, "device was modified concurrently, reload and retry")
	default:
		// The driver's own message never reaches the client: it can name
		// tables, columns, and hosts. The same reasoning /readyz already
		// applies to dependency errors on an unauthenticated endpoint.
		h.logger.ErrorContext(r.Context(), "device repository call failed",
			slog.String("op", op),
			slog.String("device", name),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
	}
}
