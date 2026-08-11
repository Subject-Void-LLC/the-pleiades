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
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
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
	GetGroup(ctx context.Context, sel pkginventory.Selector) (inventory.Iterator, error)
	GetByName(ctx context.Context, name string) (pkginventory.InventoryItem, error)
	Create(ctx context.Context, item pkginventory.InventoryItem) error
	Save(ctx context.Context, item pkginventory.InventoryItem) error
	Retire(ctx context.Context, name string) error
}

// DeviceFactory hydrates a storage-agnostic Record into a typed
// InventoryItem. Create needs one because a device arriving over HTTP has
// no stored row to be rebuilt from: the factory is what turns a submitted
// type name into the concrete Go type whose interfaces decide the device's
// capabilities, and it is what refuses a type this build does not know.
type DeviceFactory interface {
	Build(rec record.Record) (pkginventory.InventoryItem, error)
}

// defaultDeviceListLimit and maxDeviceListLimit bound a list page.
//
// A list endpoint over a fleet inventory has to be bounded somewhere, and
// the cap exists so the bound is not the caller's to remove: ?limit=100000
// is a request for the server to hold a hundred thousand hydrated devices
// in memory at once, which is a denial of service written as a query
// parameter.
const (
	defaultDeviceListLimit = 50
	maxDeviceListLimit     = 200
)

// DeviceHandler serves the inventory device resource.
type DeviceHandler struct {
	repo    DeviceRepository
	factory DeviceFactory
	logger  *slog.Logger
}

// NewDeviceHandler builds the device resource's handlers over repo.
// A nil logger falls back to the process default; a nil factory disables
// Create, which refuses with 501 rather than panicking, so a composition
// root that wires only the read path stays a supported configuration
// rather than a latent crash.
func NewDeviceHandler(repo DeviceRepository, factory DeviceFactory, logger *slog.Logger) *DeviceHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &DeviceHandler{repo: repo, factory: factory, logger: logger}
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

// deviceListDTO is one page of devices.
//
// next_cursor is the last device's ID rather than a page number, so a
// client resuming a walk asks "what comes after this device" instead of
// "give me rows 100 through 150" -- the second question has a different
// answer every time the inventory is written to.
type deviceListDTO struct {
	LinkSet

	Devices    []deviceDTO `json:"devices"`
	NextCursor string      `json:"next_cursor"`
}

// deviceWriteRequest is the JSON body create and update accept. Absent
// fields are distinguishable from empty ones through the pointer, which is
// what lets update be a genuine PATCH: omitting tags leaves the stored
// tags alone, while sending an empty array clears them.
type deviceWriteRequest struct {
	Name  *string   `json:"name"`
	Type  *string   `json:"type"`
	Tags  *[]string `json:"tags"`
	State *string   `json:"state"`
}

// List serves a bounded, keyset-paginated page of devices.
func (h *DeviceHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := defaultDeviceListLimit
	if raw := q.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			RespondError(w, r, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(parsed, maxDeviceListLimit)
	}

	group := q.Get("group")
	after := q.Get("after")
	// Both reach a storage query, so both get the same control-character
	// guard the {name} parameter already gets, for the same reason.
	if strings.ContainsAny(group, "\x00\r\n") || strings.ContainsAny(after, "\x00\r\n") {
		RespondError(w, r, http.StatusBadRequest, "invalid list parameter")
		return
	}

	// One more than asked for, so the presence of a next page is observed
	// rather than guessed. Returning a cursor whenever the page came back
	// full would advertise a next page that is empty exactly when the
	// total is a multiple of the limit.
	sel := pkginventory.Selector{
		GroupName: group,
		After:     pkginventory.DeviceID(after),
		Limit:     limit + 1,
	}

	it, err := h.repo.GetGroup(r.Context(), sel)
	if err != nil {
		h.writeRepositoryError(w, r, "list", "", err)
		return
	}
	defer func() { _ = it.Close() }()

	dto := deviceListDTO{Devices: make([]deviceDTO, 0, limit)}
	for it.Next(r.Context()) {
		if len(dto.Devices) == limit {
			dto.NextCursor = string(dto.Devices[limit-1].ID)
			break
		}
		dto.Devices = append(dto.Devices, toDeviceDTO(it.Item()))
	}
	if err := it.Error(); err != nil {
		h.writeRepositoryError(w, r, "list", "", err)
		return
	}

	Respond(w, r, http.StatusOK, &dto)
}

// Create onboards a device the platform did not discover for itself.
//
// It is deliberately not an upsert. inventory.Repository.Create's own doc
// comment records why: folding create and update together means a caller
// can no longer tell a first-time onboard from a re-sync, which is exactly
// the distinction a sync plugin's reconciliation report is made of.
func (h *DeviceHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.factory == nil {
		RespondError(w, r, http.StatusNotImplemented, "device creation is not enabled")
		return
	}

	var req deviceWriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	name := deref(req.Name)
	if name == "" || len(name) > maxDeviceNameLength || strings.ContainsAny(name, "\x00\r\n") {
		RespondError(w, r, http.StatusBadRequest, "invalid device name")
		return
	}
	deviceType := deref(req.Type)
	if deviceType == "" || strings.ContainsAny(deviceType, "\x00\r\n") {
		RespondError(w, r, http.StatusBadRequest, "invalid device type")
		return
	}

	state := pkginventory.StateActive
	if raw := deref(req.State); raw != "" {
		parsed, err := pkginventory.ParseLifecycleState(raw)
		if err != nil {
			// 422 rather than 400: the body parsed fine, the value is just
			// not one this platform knows.
			RespondError(w, r, http.StatusUnprocessableEntity, "unknown lifecycle state")
			return
		}
		state = parsed
	}

	item, err := h.factory.Build(record.Record{
		ID:    newDeviceID(),
		Name:  name,
		Type:  deviceType,
		Tags:  toTagSlice(req.Tags),
		State: state,
		// Provenance is recorded honestly: this device came from an API
		// caller, not from a sync plugin that could reconcile it later.
		Source: pkginventory.SourceAuthority{Plugin: "api"},
	})
	if err != nil {
		// The factory refuses a type no builtin registers, which is a
		// caller mistake rather than a server fault.
		RespondError(w, r, http.StatusUnprocessableEntity, "unknown device type")
		return
	}

	if err := h.repo.Create(r.Context(), item); err != nil {
		h.writeRepositoryError(w, r, "create", name, err)
		return
	}

	// Read back rather than projecting the submitted item: the stored row
	// carries the ID and version the repository assigned, and echoing the
	// request would report a state the database may not agree with.
	stored, err := h.repo.GetByName(r.Context(), name)
	if err != nil {
		h.writeRepositoryError(w, r, "read", name, err)
		return
	}

	dto := toDeviceDTO(stored)
	Respond(w, r, http.StatusCreated, &dto)
}

// Update applies tag and lifecycle changes to an existing device.
//
// The write goes through Save, which is guarded by the stored version
// token, so a concurrent edit is refused with 409 rather than silently
// overwriting whatever the other writer did. That refusal is the whole
// reason this is not a blind PUT.
func (h *DeviceHandler) Update(w http.ResponseWriter, r *http.Request) {
	name, ok := h.deviceName(w, r)
	if !ok {
		return
	}

	var req deviceWriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	item, err := h.repo.GetByName(r.Context(), name)
	if err != nil {
		h.writeRepositoryError(w, r, "read", name, err)
		return
	}

	if h.factory == nil {
		RespondError(w, r, http.StatusNotImplemented, "device updates are not enabled")
		return
	}

	// pkg/inventory.InventoryItem is read-only about tags and lifecycle
	// state: AddInfo and RemoveInfo are its only mutators, and they cover
	// properties alone. Changing either of the other two means rebuilding
	// the item through the factory from a modified Record, carrying ID,
	// version and history forward -- which is exactly what the sync
	// plugin's own reconcile path does (syncplugin/reconcile.go). Doing it
	// the same way here rather than adding setters to the domain interface
	// keeps classification the factory's business, where it already lives.
	typed, ok := item.(deviceTyped)
	if !ok {
		h.logger.ErrorContext(r.Context(), "stored device reports no device type",
			slog.String("device", name))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	rec := record.Record{
		ID:         item.ID(),
		Name:       item.Name(),
		Type:       typed.DeviceType(),
		Properties: item.Properties().Raw(),
		Tags:       item.Tags(),
		State:      item.State(),
		Source:     item.Source(),
		// Carrying version and history forward is what keeps the write an
		// optimistic-concurrency update rather than a blind overwrite that
		// resets the audit trail.
		Version: item.Version(),
		History: item.History(),
	}

	if req.State != nil {
		state, err := pkginventory.ParseLifecycleState(*req.State)
		if err != nil {
			RespondError(w, r, http.StatusUnprocessableEntity, "unknown lifecycle state")
			return
		}
		rec.State = state
	}
	if req.Tags != nil {
		rec.Tags = toTagSlice(req.Tags)
	}

	updated, err := h.factory.Build(rec)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rebuilding device for update failed",
			slog.String("device", name), slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.repo.Save(r.Context(), updated); err != nil {
		h.writeRepositoryError(w, r, "update", name, err)
		return
	}

	stored, err := h.repo.GetByName(r.Context(), name)
	if err != nil {
		h.writeRepositoryError(w, r, "read", name, err)
		return
	}

	dto := toDeviceDTO(stored)
	Respond(w, r, http.StatusOK, &dto)
}

// deviceTyped is the structural check for an item that can report its own
// classification. Every item the factory builds embeds record.Base, which
// carries DeviceType(), but pkg/inventory.InventoryItem deliberately does
// not: classification is the factory's business, and the assertion is the
// same structural-matching idiom pkg/capability uses rather than a nominal
// method added to the domain interface. internal/inventory has its own
// unexported copy of this for the same reason.
type deviceTyped interface{ DeviceType() string }

// newDeviceID mints the stable opaque identifier a created device is
// stored under, matching internal/ent/schema/device.go's own default: a
// UUIDv7, so identifiers sort in creation order and the keyset paging
// GetGroup does is chronological rather than arbitrary. The caller must
// supply one because Repository.Create writes item.ID() through verbatim.
func newDeviceID() pkginventory.DeviceID {
	if id, err := uuid.NewV7(); err == nil {
		return pkginventory.DeviceID(id.String())
	}
	// V7 needs a clock reading and can fail; a v4 is still unique, and
	// losing time-ordering for one device is better than refusing to
	// onboard it.
	return pkginventory.DeviceID(uuid.New().String())
}

// maxRequestBodyBytes bounds a JSON request body. Every endpoint that
// reads one goes through decodeJSON, so the bound is not something a
// handler author has to remember: an unbounded io.Reader from the network
// is a memory-exhaustion primitive, and http.MaxBytesReader is the one
// place to say so.
const maxRequestBodyBytes = 64 << 10

// decodeJSON reads a JSON request body into dst, answering the caller
// itself and reporting false when the body is unusable.
//
// This is the first request body in this API -- every endpoint before it
// read its input from path and query parameters -- so the rules it sets
// are the ones every later endpoint inherits. Unknown fields are refused
// rather than ignored, because silently dropping a field a client believed
// it sent is how a caller ends up convinced it disabled something it did
// not. A single JSON value is required, so trailing garbage after a valid
// object is an error rather than data nobody reads.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mediaType, _, err := mime.ParseMediaType(ct); err != nil || mediaType != "application/json" {
			RespondError(w, r, http.StatusUnsupportedMediaType, "expected application/json")
			return false
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			RespondError(w, r, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		// The decoder's message can quote the caller's own bytes back at
		// them, which is the reflection shape FAILURE_PATTERNS.md #72
		// records, so only the category is reported.
		RespondError(w, r, http.StatusBadRequest, "malformed JSON body")
		return false
	}
	if dec.More() {
		RespondError(w, r, http.StatusBadRequest, "body must contain a single JSON object")
		return false
	}
	return true
}

// deref reads an optional string field, treating absent as empty.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// toTagSlice converts the wire's optional string array into domain Tags.
// A nil pointer means the field was absent and yields nil; a present but
// empty array yields an empty slice, which is what makes clearing tags
// expressible.
func toTagSlice(tags *[]string) []pkginventory.Tag {
	if tags == nil {
		return nil
	}
	out := make([]pkginventory.Tag, 0, len(*tags))
	for _, t := range *tags {
		out = append(out, pkginventory.Tag(t))
	}
	return out
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
	case errors.Is(err, inventory.ErrItemExists):
		// Create never overwrites, so a name already in the inventory is a
		// conflict the caller must resolve rather than a server fault. The
		// sentinel is what makes it distinguishable at all: without it a
		// duplicate would arrive as a driver-specific constraint-violation
		// string and have to be reported as 500.
		RespondError(w, r, http.StatusConflict, "device already exists")
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
